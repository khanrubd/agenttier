# Observability

AgentTier ships built-in observability across three signals — **traces** (OpenTelemetry/OTLP), **metrics** (Prometheus), and **structured logs** (slog JSON with trace correlation). All three are off-by-default with a single Helm flag to opt in.

## Quick start: turn on traces

The shortest path to a trace pipeline that works without any external infrastructure is to enable the in-cluster OTel Collector that ships with the chart:

```bash
helm upgrade agenttier ./helm/agenttier/ -n agenttier --reuse-values \
  --set observability.otelCollector.enabled=true
```

That gives you:

- An `agenttier-otel-collector` Deployment listening on OTLP gRPC `:4317` and OTLP HTTP `:4318`.
- The router and controller automatically pointing `OTEL_EXPORTER_OTLP_ENDPOINT` at the collector's ClusterIP service. No env-var wiring needed.
- A `debug` exporter on the collector that prints incoming spans to its container logs — handy for verifying the pipeline before you wire up a real backend.

Verify traces are flowing:

```bash
kubectl logs -n agenttier deployment/agenttier-otel-collector --tail=20
```

You should see one or more `Resource SchemaURL` blocks per span with attributes like `service.name=agenttier-router` and span names like `router.GET` or `agenttier.invoke`.

## Pointing at your own backend

Most production deployments want to ship spans to a real backend. Two paths:

### Path A — replace the bundled collector's exporter

Append a vendor-specific exporter via `observability.otelCollector.extraConfig`:

```yaml
observability:
  otelCollector:
    enabled: true
    extraConfig: |
      exporters:
        otlphttp/honeycomb:
          endpoint: https://api.honeycomb.io
          headers:
            x-honeycomb-team: ${HONEYCOMB_API_KEY}
      service:
        pipelines:
          traces:
            exporters: [debug, otlphttp/honeycomb]
```

The `debug` exporter stays attached so you can keep tailing collector logs while real traffic flows to Honeycomb.

### Path B — bypass the bundled collector entirely

If you already run a cluster-wide collector (or a vendor agent like the Datadog agent), point AgentTier at it directly and skip our collector:

```yaml
observability:
  otelCollector:
    enabled: false
  otlp:
    endpoint: my-collector.observability.svc.cluster.local:4317
    insecure: true
```

When `otlp.endpoint` is set explicitly it always wins over the bundled collector, so you can leave both knobs untouched and override at the namespace level later.

## Span shape

Every HTTP request to the router gets a server span named `router.<method>` (e.g. `router.GET`, `router.POST`). Health probes (`/healthz`, `/readyz`, `/metrics`) and WebSocket upgrades are excluded — they would otherwise dominate span volume without carrying any signal.

Agent-mode operations have their own bounded-cardinality spans:

- **`agenttier.invoke`** — one span per `/invoke` call. Attributes: `sandbox`, `template`, `actor_hash`, `outcome`, `bytes_stdout`, `bytes_stderr`.
- **`agenttier.configure`** — one span per `/configure` call. Attributes: `sandbox`, `template`, `actor_hash`, `outcome`, `install_command_hash`.

`actor_hash` is a stable, non-reversible 8-character SHA-256 prefix of the OIDC `sub` claim. The raw subject **never** leaves the process. This satisfies GDPR / SOC2 controls in third-party trace stores while still letting an operator group spans by user within an investigation window.

## Logs with trace correlation

The router and controller emit slog JSON to stdout. When a request is in flight under an active span, every log line picks up two extra fields: `trace_id` and `span_id`. That means a single trace ID — copied from the OTel UI or pulled from the response's `traceparent` header — pivots straight to the matching log lines.

Example:

```json
{
  "time": "2026-05-28T18:30:14.13Z",
  "level": "INFO",
  "msg": "request",
  "method": "POST",
  "path": "/api/v1/sandboxes/sb-1/invoke",
  "status": 200,
  "duration_ms": 1247,
  "trace_id": "9b3e1f2a8c4d5e6f7a8b9c0d1e2f3a4b",
  "span_id": "1a2b3c4d5e6f7a8b"
}
```

When the process boots without an OTLP exporter (the default), spans are still recorded but never flushed — so the `trace_id` field above is omitted unless you explicitly set `OTEL_EXPORTER_OTLP_ENDPOINT`.

## Metrics

The Prometheus `/metrics` endpoint is always on. The router exposes:

- **`agenttier_invoke_requests_total{template, outcome}`** — counter
- **`agenttier_invoke_duration_seconds{template, outcome}`** — histogram (0.5s..~34min)
- **`agenttier_invoke_throttled_total`** — counter
- **`agenttier_configure_requests_total{template, outcome}`** — counter
- **`agenttier_configure_duration_seconds{template, outcome}`** — histogram (0.1s..~7min)

Plus controller-runtime's standard reconciler metrics on the controller's `:8081/metrics`.

To register these with the Prometheus Operator (when installed), set `observability.prometheus.serviceMonitor=true`. The chart then renders ServiceMonitors for the router and (when enabled) the OTel collector's self-metrics endpoint.

## Recommended dashboards

A starter Grafana dashboard for the metrics above lives in [`hack/dashboards/agenttier.json`](https://github.com/agenttier/agenttier) (placeholder; not yet checked in). Until that lands, the per-template invoke duration histogram is usually the single most useful panel — it surfaces latency regressions before they hit error rates.

## Troubleshooting

- **No spans in the collector logs** — confirm `OTEL_EXPORTER_OTLP_ENDPOINT` is set on the router and controller pods (`kubectl describe pod -n agenttier <router-pod> | grep OTEL`). When the env var is empty, the SDK installs a `NeverSample` provider that drops spans cheaply.
- **Spans show up but aren't reaching my backend** — tail the collector logs (`kubectl logs -n agenttier deployment/agenttier-otel-collector`). The collector logs every export failure, and the `debug` exporter (left attached by default) prints what arrived in case the issue is upstream of the collector.
- **`Connection refused` errors at startup** — the router and controller fail closed if `OTEL_EXPORTER_OTLP_ENDPOINT` is set but the collector isn't reachable. Either bring the collector up first, leave the env var empty during initial install, or set `observability.otelCollector.enabled=true` so the chart's manifest order brings the collector up alongside the rest of the install.

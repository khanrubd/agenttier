# AgentTier Minimal Sandbox Image
# Minimal shell environment with bash, git, and curl.
#
# Build context: repository root (for cmd/sandbox-runtime).

# ---- Stage 1: in-pod runtime binary ----------------------------------------
FROM public.ecr.aws/docker/library/golang:1.22 AS runtime-build
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/sandbox-runtime ./cmd/sandbox-runtime
COPY pkg/sandboxruntime ./pkg/sandboxruntime
COPY pkg/version ./pkg/version
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags='-s -w' \
    -o /out/agenttier-sandbox-runtime ./cmd/sandbox-runtime

# ---- Stage 2: minimal sandbox ----------------------------------------------
FROM public.ecr.aws/docker/library/alpine:3.20

RUN apk add --no-cache \
    bash \
    git \
    curl \
    openssh-client \
    ca-certificates \
    && rm -rf /var/cache/apk/*

# In-pod runtime binary + entrypoint that backgrounds it.
COPY --from=runtime-build /out/agenttier-sandbox-runtime /usr/local/bin/agenttier-sandbox-runtime
COPY images/minimal/entrypoint.sh /usr/local/bin/agenttier-entrypoint
RUN chmod 0755 /usr/local/bin/agenttier-sandbox-runtime /usr/local/bin/agenttier-entrypoint \
    && mkdir -p /var/log \
    && chmod 1777 /var/log

# Create non-root user (UID 1000 matches SecurityContext)
RUN adduser -D -u 1000 -h /workspace sandbox

# Create workspace directory
RUN mkdir -p /workspace && chown sandbox:sandbox /workspace

USER sandbox
WORKDIR /workspace

# Keep container running for exec access
ENTRYPOINT ["/usr/local/bin/agenttier-entrypoint"]
CMD ["sleep", "infinity"]

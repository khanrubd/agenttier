{{/*
Copyright 2024 AgentTier Authors.
SPDX-License-Identifier: Apache-2.0
*/}}

{{/* Chart name */}}
{{- define "agenttier.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Fullname

Use the release name when it already contains the chart name (the documented
install path `helm install agenttier agenttier/agenttier`), otherwise
concatenate release name and chart name so multiple releases in the same
cluster don't collide. Mirrors the standard Bitnami helper. */}}
{{- define "agenttier.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/* Common labels */}}
{{- define "agenttier.labels" -}}
helm.sh/chart: {{ include "agenttier.chart" . }}
{{ include "agenttier.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/* Selector labels */}}
{{- define "agenttier.selectorLabels" -}}
app.kubernetes.io/name: {{ include "agenttier.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/* Chart label */}}
{{- define "agenttier.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Image tag

`.Chart.AppVersion` is the bare semver (e.g. "0.2.0") because semver itself
does not include a `v` prefix — but the release workflow tags images as
`vX.Y.Z`. When the user hasn't overridden the tag, prepend `v` to the
appVersion so pods pull the right image. Users can still override
`<component>.image.tag` explicitly (including overrides without a `v`
prefix) and we pass that through untouched. */}}
{{- define "agenttier.imageTag" -}}
{{- $explicit := . -}}
{{- if $explicit -}}
{{- $explicit -}}
{{- else -}}
v{{ $.Chart.AppVersion }}
{{- end -}}
{{- end }}

{{/* Resolve a repository name against global.registry.
     If the repository already contains a "/" it is treated as a fully-qualified
     registry/repo path and returned as-is (enables per-component registry
     overrides). Otherwise, it is a short name and is prefixed with the
     global.registry value.

     Usage: {{ include "agenttier.resolveRepo" (dict "repo" .Values.controller.image.repository "global" .Values.global) }}
*/}}
{{- define "agenttier.resolveRepo" -}}
{{- $repo := .repo -}}
{{- $registry := .global.registry | default "ghcr.io/agenttier" -}}
{{- if contains "/" $repo -}}
{{- $repo -}}
{{- else -}}
{{- printf "%s/%s" $registry $repo -}}
{{- end -}}
{{- end }}

{{/* Resolve a sandbox image (short name or full override) to registry/name:tag.
     A value that already contains ":" is treated as a full image ref and
     returned unchanged. A value that contains "/" but no ":" is a bare full
     path and gets the appVersion tag appended. A short name (no "/" or ":")
     gets the global registry prefix and the appVersion tag.

     Usage: {{ include "agenttier.resolveSandboxImage" (dict "image" .Values.defaults.sandbox.image "global" .Values.global "appVersion" .Chart.AppVersion) }}
*/}}
{{- define "agenttier.resolveSandboxImage" -}}
{{- $image := .image -}}
{{- $registry := .global.registry | default "ghcr.io/agenttier" -}}
{{- $tag := printf "v%s" .appVersion -}}
{{- if contains ":" $image -}}
{{- /* full image:tag override — pass through unchanged */ -}}
{{- $image -}}
{{- else if contains "/" $image -}}
{{- /* full path without tag — append appVersion tag */ -}}
{{- printf "%s:%s" $image $tag -}}
{{- else -}}
{{- /* short name — prepend registry and append appVersion tag */ -}}
{{- printf "%s/%s:%s" $registry $image $tag -}}
{{- end -}}
{{- end }}

{{/* Controller image */}}
{{- define "agenttier.controllerImage" -}}
{{- $repo := include "agenttier.resolveRepo" (dict "repo" .Values.controller.image.repository "global" .Values.global) }}
{{- $tag := .Values.controller.image.tag | default (printf "v%s" .Chart.AppVersion) }}
{{- printf "%s:%s" $repo $tag }}
{{- end }}

{{/* Router image */}}
{{- define "agenttier.routerImage" -}}
{{- $repo := include "agenttier.resolveRepo" (dict "repo" .Values.router.image.repository "global" .Values.global) }}
{{- $tag := .Values.router.image.tag | default (printf "v%s" .Chart.AppVersion) }}
{{- printf "%s:%s" $repo $tag }}
{{- end }}

{{/* Web UI image */}}
{{- define "agenttier.webuiImage" -}}
{{- $repo := include "agenttier.resolveRepo" (dict "repo" .Values.webui.image.repository "global" .Values.global) }}
{{- $tag := .Values.webui.image.tag | default (printf "v%s" .Chart.AppVersion) }}
{{- printf "%s:%s" $repo $tag }}
{{- end }}

{{/* Service account name

Shared by the controller and router deployments (both need the same
sandbox / pod / pvc / service / ingress / configmap permissions granted by
the cluster role). Historically named `<fullname>-controller` for backward
compat. */}}
{{- define "agenttier.serviceAccountName" -}}
{{- printf "%s-controller" (include "agenttier.fullname" .) }}
{{- end }}

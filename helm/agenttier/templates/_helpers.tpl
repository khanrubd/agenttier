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

{{/* Controller image */}}
{{- define "agenttier.controllerImage" -}}
{{- $tag := .Values.controller.image.tag | default (printf "v%s" .Chart.AppVersion) }}
{{- printf "%s:%s" .Values.controller.image.repository $tag }}
{{- end }}

{{/* Router image */}}
{{- define "agenttier.routerImage" -}}
{{- $tag := .Values.router.image.tag | default (printf "v%s" .Chart.AppVersion) }}
{{- printf "%s:%s" .Values.router.image.repository $tag }}
{{- end }}

{{/* Web UI image */}}
{{- define "agenttier.webuiImage" -}}
{{- $tag := .Values.webui.image.tag | default (printf "v%s" .Chart.AppVersion) }}
{{- printf "%s:%s" .Values.webui.image.repository $tag }}
{{- end }}

{{/* Service account name

Shared by the controller and router deployments (both need the same
sandbox / pod / pvc / service / ingress / configmap permissions granted by
the cluster role). Historically named `<fullname>-controller` for backward
compat. */}}
{{- define "agenttier.serviceAccountName" -}}
{{- printf "%s-controller" (include "agenttier.fullname" .) }}
{{- end }}

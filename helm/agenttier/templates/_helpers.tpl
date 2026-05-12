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

{{/* Controller image */}}
{{- define "agenttier.controllerImage" -}}
{{- printf "%s:%s" .Values.controller.image.repository (default .Chart.AppVersion .Values.controller.image.tag) }}
{{- end }}

{{/* Router image */}}
{{- define "agenttier.routerImage" -}}
{{- printf "%s:%s" .Values.router.image.repository (default .Chart.AppVersion .Values.router.image.tag) }}
{{- end }}

{{/* Web UI image */}}
{{- define "agenttier.webuiImage" -}}
{{- printf "%s:%s" .Values.webui.image.repository (default .Chart.AppVersion .Values.webui.image.tag) }}
{{- end }}

{{/* Service account name

Shared by the controller and router deployments (both need the same
sandbox / pod / pvc / service / ingress / configmap permissions granted by
the cluster role). Historically named `<fullname>-controller` for backward
compat. */}}
{{- define "agenttier.serviceAccountName" -}}
{{- printf "%s-controller" (include "agenttier.fullname" .) }}
{{- end }}

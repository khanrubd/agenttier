{{/*
Copyright 2024 AgentTier Authors.
SPDX-License-Identifier: Apache-2.0
*/}}

{{/* Chart name */}}
{{- define "agenttier.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Fullname */}}
{{- define "agenttier.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
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

{{/* MongoDB connection string */}}
{{- define "agenttier.mongodbURI" -}}
{{- if .Values.mongodb.enabled }}
{{- printf "mongodb://%s-mongodb:27017/agenttier" (include "agenttier.fullname" .) }}
{{- else }}
{{- .Values.mongodb.external.connectionString }}
{{- end }}
{{- end }}

{{/* Service account name */}}
{{- define "agenttier.serviceAccountName" -}}
{{- printf "%s-controller" (include "agenttier.fullname" .) }}
{{- end }}

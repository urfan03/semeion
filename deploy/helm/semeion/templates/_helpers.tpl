{{- define "semeion.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "semeion.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "semeion.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "semeion.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "semeion.selectorLabels" -}}
app.kubernetes.io/name: {{ include "semeion.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "semeion.modelPlaneFullname" -}}
{{- printf "%s-model" (include "semeion.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Model-plane URL handed to the engine via --model-url. */}}
{{- define "semeion.modelURL" -}}
{{- printf "http://%s:%d" (include "semeion.modelPlaneFullname" .) (int .Values.modelPlane.port) -}}
{{- end -}}

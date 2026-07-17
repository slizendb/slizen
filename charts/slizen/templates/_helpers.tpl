{{/* Chart name. */}}
{{- define "slizen.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Release-scoped resource name. */}}
{{- define "slizen.fullname" -}}
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

{{/* Chart label. */}}
{{- define "slizen.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Selector labels remain stable across upgrades. */}}
{{- define "slizen.selectorLabels" -}}
app.kubernetes.io/name: {{ include "slizen.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/* Common labels. */}}
{{- define "slizen.labels" -}}
helm.sh/chart: {{ include "slizen.chart" . }}
{{ include "slizen.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/* Image reference, optionally pinned by digest. */}}
{{- define "slizen.image" -}}
{{- if .Values.image.digest -}}
{{ printf "%s@%s" .Values.image.repository .Values.image.digest }}
{{- else -}}
{{ printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end -}}
{{- end }}

{{/* Convert the chart's whole-unit duration format to nanoseconds safely. */}}
{{- define "slizen.durationNanoseconds" -}}
{{- $value := index . 0 -}}
{{- $path := index . 1 -}}
{{- $amountText := regexFind "^[0-9]+" $value -}}
{{- $unit := regexFind "(ns|us|ms|s|m|h)$" $value -}}
{{- if or (eq $amountText "") (eq $unit "") -}}
{{- fail (printf "%s must be a positive whole-unit duration" $path) -}}
{{- end -}}
{{- $maxAmounts := dict "ns" "9223372036854775807" "us" "9223372036854775" "ms" "9223372036854" "s" "9223372036" "m" "153722867" "h" "2562047" -}}
{{- $multipliers := dict "ns" 1 "us" 1000 "ms" 1000000 "s" 1000000000 "m" 60000000000 "h" 3600000000000 -}}
{{- $maxAmountText := get $maxAmounts $unit -}}
{{- if or (gt (len $amountText) (len $maxAmountText)) (and (eq (len $amountText) (len $maxAmountText)) (gt $amountText $maxAmountText)) -}}
{{- fail (printf "%s exceeds the supported duration range" $path) -}}
{{- end -}}
{{- mul (int64 $amountText) (int64 (get $multipliers $unit)) -}}
{{- end }}

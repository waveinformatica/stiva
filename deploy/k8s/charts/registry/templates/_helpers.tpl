{{- define "registry.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "registry.fullname" -}}
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

{{- define "registry.labels" -}}
helm.sh/chart: {{ include "registry.name" . }}-{{ .Chart.Version | replace "+" "_" }}
{{ include "registry.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "registry.selectorLabels" -}}
app.kubernetes.io/name: {{ include "registry.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "registry.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "registry.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "registry.image" -}}
{{- $reg := .Values.image.registry -}}
{{- $repo := .Values.image.repository -}}
{{- $tag := .Values.image.tag -}}
{{- if $reg -}}{{ printf "%s/%s:%s" $reg $repo $tag }}{{- else -}}{{ printf "%s:%s" $repo $tag }}{{- end -}}
{{- end -}}

{{- define "registry.postgresUrl" -}}
{{- if .Values.postgresql.enabled -}}
{{- $port := .Values.postgresql.primary.service.ports.postgresql | default "5432" -}}
{{- $db := .Values.postgresql.auth.database -}}
{{- $user := .Values.postgresql.auth.username -}}
{{- $pass := .Values.postgresql.auth.password -}}
{{- $host := printf "%s-postgresql" (include "registry.fullname" .) -}}
{{- printf "postgres://%s:%s@%s:%s/%s?sslmode=disable" $user (urlquery $pass) $host (toString $port) $db -}}
{{- else -}}
{{- $e := .Values.postgresql.external -}}
{{- printf "postgres://%s:%s@%s:%s/%s?sslmode=%s" $e.username (urlquery $e.password) $e.host (toString $e.port) $e.database $e.sslmode -}}
{{- end -}}
{{- end -}}

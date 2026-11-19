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

{{/*
Declared service accounts (docs/scanning.md): the accounts the enabled
components use. Each has a Secret holding its API key, mounted both into the
registry, which provisions the account from it at startup, and into the
component that authenticates with it.
*/}}
{{- define "registry.serviceAccounts" -}}
{{- $accounts := list -}}
{{- if .Values.scanner.enabled -}}
{{- $accounts = append $accounts (dict "name" "stiva-scanner" "role" "scanner" "scope" "*") -}}
{{- end -}}
{{- if .Values.warmer.enabled -}}
{{- $accounts = append $accounts (dict "name" "stiva-warmer" "role" "warmer" "scope" (printf "oci:%s" .Values.warmer.registry)) -}}
{{- end -}}
{{- if .Values.cacheMirror.enabled -}}
{{- $accounts = append $accounts (dict "name" "stiva-nodes" "role" "reader" "scope" (printf "oci:%s" .Values.cacheMirror.registry)) -}}
{{- end -}}
{{- toJson $accounts -}}
{{- end }}

{{- define "registry.serviceAccountSecret" -}}
{{- printf "%s-%s" (include "registry.fullname" .root) .name -}}
{{- end }}

{{/*
The key of a service account: the one already in its Secret, or a new random
one when the Secret does not exist yet. Deleting the Secret and upgrading
rotates the key.
*/}}
{{- define "registry.serviceAccountKey" -}}
{{- $secret := lookup "v1" "Secret" .root.Release.Namespace (include "registry.serviceAccountSecret" .) -}}
{{- if $secret -}}
{{- index $secret.data "token" | b64dec -}}
{{- else -}}
{{- printf "rk_%s" (randAlphaNum 48 | lower) -}}
{{- end -}}
{{- end }}

{{- define "cp.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cp.fullname" -}}
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

{{- define "cp.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "cp.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: antifailure
{{- end -}}

{{- define "cp.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cp.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "cp.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "cp.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
The image reference. A digest wins over a tag, because a tag can be moved and a
digest cannot, and an operator who took the trouble to pin one meant it.
*/}}
{{- define "cp.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- if .Values.image.digest -}}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}
{{- end -}}

{{- define "cp.databaseSecretName" -}}
{{- default (printf "%s-database" (include "cp.fullname" .)) .Values.database.existingSecret -}}
{{- end -}}

{{- define "cp.githubSecretName" -}}
{{- default (printf "%s-github" (include "cp.fullname" .)) .Values.github.existingSecret -}}
{{- end -}}

{{/*
Refuse to render rather than install something that cannot work.

Every one of these is a failure that would otherwise appear minutes later as a
CrashLoopBackOff whose real cause is one missing value. `helm install` failing
in a second with the name of the value is a better morning than reading logs.
*/}}
{{- define "cp.validate" -}}
{{- if not .Values.database.existingSecret -}}
  {{- if not .Values.database.url -}}
    {{- fail "database.url is required (or set database.existingSecret to a Secret you created yourself)" -}}
  {{- end -}}
  {{- if and .Values.bootstrap.enabled (not .Values.database.migrationUrl) -}}
    {{- fail "database.migrationUrl is required when bootstrap.enabled is true: the bootstrap job applies the schema and needs a role that may run DDL. Set bootstrap.enabled=false if something else owns the schema." -}}
  {{- end -}}
{{- end -}}
{{- if not .Values.github.existingSecret -}}
  {{- if not .Values.github.clientId -}}{{- fail "github.clientId is required (or set github.existingSecret)" -}}{{- end -}}
  {{- if not .Values.github.clientSecret -}}{{- fail "github.clientSecret is required (or set github.existingSecret)" -}}{{- end -}}
  {{- if not .Values.github.redirectUri -}}{{- fail "github.redirectUri is required (or set github.existingSecret). It must match the OAuth App exactly." -}}{{- end -}}
{{- end -}}
{{- if not (has .Values.maintenance.mode (list "cronjob" "inProcess" "off")) -}}
  {{- fail (printf "maintenance.mode is %q; it has to be cronjob, inProcess or off" .Values.maintenance.mode) -}}
{{- end -}}
{{- if and (eq .Values.maintenance.mode "inProcess") (not .Values.database.migrationUrl) (not .Values.database.existingSecret) -}}
  {{- fail "maintenance.mode=inProcess needs database.migrationUrl: keeping event partitions ahead is DDL." -}}
{{- end -}}
{{- if and (eq .Values.maintenance.mode "cronjob") (not .Values.database.migrationUrl) (not .Values.database.existingSecret) -}}
  {{- fail "maintenance.mode=cronjob needs database.migrationUrl: keeping event partitions ahead is DDL. Set maintenance.mode=off only if something outside this chart does it." -}}
{{- end -}}
{{- if and .Values.ingress.enabled .Values.config.insecureCookies -}}
  {{- fail "config.insecureCookies must not be true with an ingress: session cookies would be sent without the Secure attribute over a public route." -}}
{{- end -}}
{{- end -}}

{{/*
The application's environment. Shared by the Deployment and the bootstrap Job
so the two cannot drift into disagreeing about how the application is
configured.
*/}}
{{- define "cp.appEnv" -}}
- name: AF_DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: {{ include "cp.databaseSecretName" . }}
      key: {{ .Values.database.secretKeys.url }}
{{- end -}}

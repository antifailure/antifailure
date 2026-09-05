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

{{/*
The Secret the bootstrap hook reads. An operator-supplied Secret already exists
before the install starts, so the hook can use it directly; otherwise the hook
gets its own, created at a lower hook weight than the Job that needs it.
*/}}
{{- define "cp.bootstrapSecretName" -}}
{{- if .Values.database.existingSecret -}}
{{- .Values.database.existingSecret -}}
{{- else -}}
{{- printf "%s-bootstrap" (include "cp.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "cp.githubSecretName" -}}
{{- default (printf "%s-github" (include "cp.fullname" .)) .Values.github.existingSecret -}}
{{- end -}}

{{- define "cp.providerKeysSecretName" -}}
{{- default (printf "%s-provider-keys" (include "cp.fullname" .)) .Values.providerKeys.existingSecret -}}
{{- end -}}

{{- define "cp.mailSecretName" -}}
{{- default (printf "%s-mail" (include "cp.fullname" .)) .Values.mail.existingSecret -}}
{{- end -}}

{{- define "cp.stripeSecretName" -}}
{{- default (printf "%s-stripe" (include "cp.fullname" .)) .Values.stripe.existingSecret -}}
{{- end -}}

{{- define "cp.analyticsSecretName" -}}
{{- default (printf "%s-analytics" (include "cp.fullname" .)) .Values.analytics.existingSecret -}}
{{- end -}}

{{/*
Whether a secret-valued setting is configured at all, either as a literal in
values or as a Secret the operator made themselves.

It gates a secretKeyRef, and that is the whole reason it exists. A container
naming a Secret that is not there does not start: kubelet reports
CreateContainerConfigError and the release times out, which is a worse way to
learn that Stripe is not configured than Stripe simply being off. So every
optional credential below renders only when there is something to reference.
*/}}
{{- define "cp.hasSecret" -}}
{{- if or .value .existing -}}yes{{- end -}}
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
{{/*
The GitHub App is three values or none. The application stops at startup on a
partial set; failing here names the VALUE instead of the variable, in a second,
before anything is installed.

github.appId is the switch, and existingSecret is deliberately not treated as
evidence of anything. A chart cannot read a Secret an operator made themselves,
so it cannot know whether that Secret carries an App key, and guessing that it
does refuses an installation that is using existingSecret for the OAuth App
alone, which is the ordinary case. So: with appId set, the key and the secret
must be reachable, from values or from that Secret. With appId unset, a LITERAL
key or webhook secret in values is refused, because it is a value somebody wrote
that nothing will read.
*/}}
{{- if .Values.github.appId -}}
  {{- $missing := list -}}
  {{- if not (or .Values.github.privateKey .Values.github.existingSecret) -}}{{- $missing = append $missing "github.privateKey" -}}{{- end -}}
  {{- if not (or .Values.github.webhookSecret .Values.github.existingSecret) -}}{{- $missing = append $missing "github.webhookSecret" -}}{{- end -}}
  {{- if gt (len $missing) 0 -}}
    {{- fail (printf "github.appId is set and %s is not. A half configured App accepts installations and drops every delivery, and the application stops at startup rather than running that way. Set it, or put it in the Secret named by github.existingSecret." (join " and " $missing)) -}}
  {{- end -}}
{{- else -}}
  {{- $orphan := list -}}
  {{- if .Values.github.privateKey -}}{{- $orphan = append $orphan "github.privateKey" -}}{{- end -}}
  {{- if .Values.github.webhookSecret -}}{{- $orphan = append $orphan "github.webhookSecret" -}}{{- end -}}
  {{- if gt (len $orphan) 0 -}}
    {{- fail (printf "%s is set and github.appId is not, so nothing reads it: without the App id there is no App, and /webhooks/github answers 503. Set github.appId, or remove the value." (join " and " $orphan)) -}}
  {{- end -}}
{{- end -}}
{{/*
Billing is credentials plus a price or neither, for the same reason and one
sharper: the value an operator most often misses is the webhook secret, and a
missing webhook secret fails for the first time when a real customer pays.

existingSecret DOES count here, and the asymmetry with the App above is the
point rather than an inconsistency. A Secret named by stripe.existingSecret
exists for Stripe and nothing else, so its presence is a statement that billing
is meant to be on. github.existingSecret carries the OAuth App, which every
installation has, so its presence says nothing about the App at all.
*/}}
{{- $stripeCreds := or (and .Values.stripe.secretKey .Values.stripe.webhookSecret) .Values.stripe.existingSecret -}}
{{- if and (not .Values.stripe.existingSecret) (or .Values.stripe.secretKey .Values.stripe.webhookSecret) (not (and .Values.stripe.secretKey .Values.stripe.webhookSecret)) -}}
  {{- fail "stripe.secretKey and stripe.webhookSecret go together. One without the other leaves billing off, and the one usually missing is the webhook secret, which fails only when somebody pays." -}}
{{- end -}}
{{- if and $stripeCreds (not .Values.stripe.priceTeam) -}}
  {{- fail "Stripe credentials are set and stripe.priceTeam is not, so billing stays off and nothing is sold. A subscription for a price this installation does not name is recorded and does not change the plan." -}}
{{- end -}}
{{- if and .Values.stripe.priceTeam (not $stripeCreds) -}}
  {{- fail "stripe.priceTeam is set and the Stripe credentials are not, so billing is off and the price is read by nothing. Set stripe.secretKey and stripe.webhookSecret, or stripe.existingSecret." -}}
{{- end -}}
{{/*
An operator pool with no operator credential is a setting that does nothing.
*/}}
{{- if and .Values.config.adminPoolMax (not (or .Values.database.adminUrl .Values.database.existingSecret)) -}}
  {{- fail "config.adminPoolMax is set and database.adminUrl is not, so there is no operator pool for it to size. Set database.adminUrl, or leave both alone: an installation with no operator portal is the right default for a single team." -}}
{{- end -}}
{{- if and .Values.config.hostedRequiredPlan (not $stripeCreds) -}}
  {{- fail "config.hostedRequiredPlan is set and billing is off, so no organization could ever satisfy it and every customer would be refused. The application stops at startup on this; leave it empty when self-hosting." -}}
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

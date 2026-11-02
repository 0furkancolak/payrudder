{{/* Image of a service: <registry>payrudder/<name>:<tag>. */}}
{{- define "payrudder.image" -}}
{{- $root := .root -}}
{{- $svc := .svc -}}
{{- printf "%s%s:%s" $root.Values.global.imageRegistry (default (printf "payrudder/%s" .name) $svc.image) $root.Values.global.imageTag -}}
{{- end -}}

{{/* Common labels; selector labels are only app.kubernetes.io/name and instance. */}}
{{- define "payrudder.selector" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
{{- end -}}

{{- define "payrudder.labels" -}}
{{ include "payrudder.selector" . }}
app.kubernetes.io/part-of: payrudder
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .root.Chart.Name .root.Chart.Version }}
{{- end -}}

{{/* Password of a Postgres role. */}}
{{- define "payrudder.pgPassword" -}}
{{- $pw := get .root.Values.postgres.passwords .role -}}
{{- default (printf "%s_local" .role) $pw -}}
{{- end -}}

{{/* postgres://<role>_<kind>:<password>@host:port/<db> */}}
{{- define "payrudder.dsn" -}}
{{- $role := printf "%s_%s" .db.role .kind -}}
{{- printf "postgres://%s:%s@%s:%v/%s?sslmode=%s" $role (include "payrudder.pgPassword" (dict "root" .root "role" $role) | urlquery) .root.Values.postgres.host .root.Values.postgres.port .db.name .root.Values.postgres.sslmode -}}
{{- end -}}

{{/* Services that are deployed (a service can be switched off with enabled: false). */}}
{{- define "payrudder.enabled" -}}
{{- if or (not (hasKey . "enabled")) .enabled -}}true{{- end -}}
{{- end -}}

{{/* Secret-backed environment of a service, as a dict: the DSNs of its runtime roles, broker and S3 credentials, the operator
token. With .migrate the migrator role's DSN is added; that is the environment of the migration Job, so the running pods never
see the schema owner's password while the Job still loads the service's complete configuration. */}}
{{- define "payrudder.secretEnv" -}}
{{- $root := .root -}}
{{- $svc := .svc -}}
{{- $out := dict -}}
{{- if $svc.db -}}
  {{- if $svc.db.app -}}{{- $_ := set $out "PAYRUDDER_PG_APP_DSN" (include "payrudder.dsn" (dict "root" $root "db" $svc.db "kind" "app")) -}}{{- end -}}
  {{- if $svc.db.system -}}{{- $_ := set $out "PAYRUDDER_PG_SYSTEM_DSN" (include "payrudder.dsn" (dict "root" $root "db" $svc.db "kind" "system")) -}}{{- end -}}
  {{- if .migrate -}}{{- $_ := set $out "PAYRUDDER_PG_MIGRATOR_DSN" (include "payrudder.dsn" (dict "root" $root "db" $svc.db "kind" "migrator")) -}}{{- end -}}
{{- end -}}
{{- range $svc.uses -}}
  {{- if eq . "rabbitmq" -}}
    {{- $_ := set $out "PAYRUDDER_RABBITMQ_URL" (printf "amqp://%s:%s@%s:%v/" $root.Values.rabbitmq.user ($root.Values.rabbitmq.password | urlquery) $root.Values.rabbitmq.host $root.Values.rabbitmq.port) -}}
  {{- else if eq . "s3" -}}
    {{- $_ := set $out "S3_ACCESS_KEY" $root.Values.s3.accessKey -}}
    {{- $_ := set $out "S3_SECRET_KEY" $root.Values.s3.secretKey -}}
  {{- else if eq . "clickhouse" -}}
    {{- $_ := set $out "CLICKHOUSE_PASSWORD" $root.Values.clickhouse.password -}}
  {{- else if eq . "operatorToken" -}}
    {{- $_ := set $out "PAYRUDDER_OPERATOR_TOKEN" $root.Values.operatorToken -}}
  {{- else if eq . "otpPepper" -}}
    {{- $_ := set $out "PAYRUDDER_OTP_PEPPER" (include "payrudder.otpPepper" $root) -}}
  {{- else if eq . "google" -}}
    {{- if and $root.Values.auth.google.clientId $root.Values.auth.google.clientSecret -}}
      {{- $_ := set $out "PAYRUDDER_GOOGLE_CLIENT_ID" $root.Values.auth.google.clientId -}}
      {{- $_ := set $out "PAYRUDDER_GOOGLE_CLIENT_SECRET" $root.Values.auth.google.clientSecret -}}
    {{- end -}}
  {{- else if eq . "trustedProxySecret" -}}
    {{- if $root.Values.auth.trustedProxySecret -}}
      {{- $_ := set $out "PAYRUDDER_TRUSTED_PROXY_SECRET" $root.Values.auth.trustedProxySecret -}}
    {{- end -}}
  {{- else if eq . "mailEventsSecret" -}}
    {{- if $root.Values.mail.eventsSecret -}}
      {{- $_ := set $out "NOTIFIER_MAIL_EVENTS_SECRET" $root.Values.mail.eventsSecret -}}
    {{- end -}}
  {{- else if eq . "mailEventsClient" -}}
    {{- if $root.Values.mail.eventsSecret -}}
      {{- $_ := set $out "PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET" $root.Values.mail.eventsSecret -}}
    {{- end -}}
  {{- else if eq . "mailWebhook" -}}
    {{- if $root.Values.mail.webhookSecret -}}
      {{- if not $root.Values.mail.eventsSecret -}}{{- fail "mail.webhookSecret needs mail.eventsSecret: the gateway hands a delivery event to the notifier's listener, and refuses to start with the webhook secret and no listener" -}}{{- end -}}
      {{- $_ := set $out "PAYRUDDER_MAIL_WEBHOOK_SECRET" $root.Values.mail.webhookSecret -}}
    {{- end -}}
  {{- else if eq . "paymentsToken" -}}
    {{- $_ := set $out "PAYRUDDER_PAYMENTS_INTERNAL_TOKEN" $root.Values.payments.internalToken -}}
  {{- else if eq . "secretsKey" -}}
    {{- $_ := include "payrudder.secretsKeys" (dict "root" $root "out" $out) -}}
  {{- else if eq . "paymentsSecrets" -}}
    {{- $_ := set $out "PAYMENTS_INTERNAL_TOKEN" $root.Values.payments.internalToken -}}
    {{- $_ := include "payrudder.secretsKeys" (dict "root" $root "out" $out) -}}
  {{- end -}}
{{- end -}}
{{- /* A DSN holds a key, so it goes in the Secret of every service (not the migration Job's); empty renders nothing and error tracking stays off. */ -}}
{{- if and $root.Values.errorTracking.url (not .migrate) -}}{{- $_ := set $out "PAYRUDDER_ERROR_TRACKING_URL" $root.Values.errorTracking.url -}}{{- end -}}
{{- toJson $out -}}
{{- end -}}

{{/* The pepper that keys the hash of the sign-in and recovery codes. Set in the values, that is it. Empty in a development environment (global.environment local, ci or kind):
the public local value of the compose stack. Empty anywhere else: no chart without one (plans-svc would refuse to start), unless the Secrets are yours (secrets.existingSecrets). */}}
{{- define "payrudder.otpPepper" -}}
{{- if .Values.auth.otpPepper -}}
{{- .Values.auth.otpPepper -}}
{{- else if has .Values.global.environment (list "local" "ci" "kind") -}}
local-otp-pepper-change-me-0123456789
{{- else if not .Values.secrets.existingSecrets -}}
{{- required "auth.otpPepper is required when global.environment is not local, ci or kind: put it in your secrets file (`openssl rand -hex 32`, see values-secrets.example.yaml)" .Values.auth.otpPepper -}}
{{- end -}}
{{- end -}}

{{/* Where customers reach the gateway (global.publicURL): the hosted invoice links and the link in the mail that asks a customer to confirm a payment (3-D Secure) start with it,
and billing-svc sends that mail without a link when it is empty. Empty in a development environment (local, ci, kind): relative links. Empty anywhere else: no chart without one. */}}
{{- define "payrudder.publicURL" -}}
{{- if has .Values.global.environment (list "local" "ci" "kind") -}}
{{- .Values.global.publicURL -}}
{{- else -}}
{{- required "global.publicURL is required when global.environment is not local, ci or kind: the address customers reach the gateway at (https://api.example.com); without it the mail that asks a customer to confirm a payment has no link" .Values.global.publicURL -}}
{{- end -}}
{{- end -}}

{{/* The secrets key of payments-worker and plans-svc, and while it is being replaced (docs/runbooks/secrets-key-rotation.md) its id and the key it replaces with its id.
Adds to .out; the ids and the previous key are set only when the values hold them, so a chart that is not rotating renders what it always did. */}}
{{- define "payrudder.secretsKeys" -}}
{{- $v := .root.Values.payments -}}
{{- $_ := set .out "PAYRUDDER_SECRETS_KEY" $v.secretsKey -}}
{{- if $v.secretsKeyId -}}{{- $_ := set .out "PAYRUDDER_SECRETS_KEY_ID" (toString $v.secretsKeyId) -}}{{- end -}}
{{- if $v.secretsKeyPrevious -}}{{- $_ := set .out "PAYRUDDER_SECRETS_KEY_PREVIOUS" $v.secretsKeyPrevious -}}{{- end -}}
{{- if $v.secretsKeyPreviousId -}}{{- $_ := set .out "PAYRUDDER_SECRETS_KEY_PREVIOUS_ID" (toString $v.secretsKeyPreviousId) -}}{{- end -}}
{{- end -}}

{{/* Plain environment shared by every container of a service (also the migration Job). */}}
{{- define "payrudder.baseEnv" -}}
- { name: PAYRUDDER_ENV, value: {{ .root.Values.global.environment | quote }} }
- { name: PAYRUDDER_SERVICE_NAME, value: {{ .name | quote }} }
- { name: PAYRUDDER_VERSION, value: {{ .root.Values.global.imageTag | quote }} }
- { name: PAYRUDDER_METRICS_ADDR, value: {{ printf ":%v" .svc.metricsPort | quote }} }
{{- if .root.Values.global.otlpEndpoint }}
- { name: PAYRUDDER_OTLP_ENDPOINT, value: {{ .root.Values.global.otlpEndpoint | quote }} }
{{- end }}
{{- end -}}

{{/* Settings that identify the S3 and ClickHouse endpoints (not secret). */}}
{{- define "payrudder.usesEnv" -}}
{{- $root := .root -}}
{{- range .svc.uses }}
{{- if eq . "s3" }}
- { name: S3_ENDPOINT, value: {{ $root.Values.s3.endpoint | quote }} }
- { name: S3_PUBLIC_ENDPOINT, value: {{ $root.Values.s3.publicEndpoint | quote }} }
- { name: S3_REGION, value: {{ $root.Values.s3.region | quote }} }
- { name: S3_FORCE_PATH_STYLE, value: {{ $root.Values.s3.forcePathStyle | quote }} }
{{- else if eq . "clickhouse" }}
- { name: CLICKHOUSE_ADDR, value: {{ $root.Values.clickhouse.addr | quote }} }
- { name: CLICKHOUSE_DB, value: {{ $root.Values.clickhouse.database | quote }} }
- { name: CLICKHOUSE_USER, value: {{ $root.Values.clickhouse.user | quote }} }
{{- end }}
{{- end }}
{{- end -}}

{{/* Static env of a service: its env map with {{ }} expanded against the root context, sorted by name. */}}
{{- define "payrudder.staticEnv" -}}
{{- $root := .root -}}
{{- range $k := keys .svc.env | sortAlpha }}
- { name: {{ $k }}, value: {{ tpl (toString (get $.svc.env $k)) $root | quote }} }
{{- end }}
{{- end -}}

{{/* Image of a service: <registry>quotify/<name>:<tag>. */}}
{{- define "quotify.image" -}}
{{- $root := .root -}}
{{- $svc := .svc -}}
{{- printf "%s%s:%s" $root.Values.global.imageRegistry (default (printf "quotify/%s" .name) $svc.image) $root.Values.global.imageTag -}}
{{- end -}}

{{/* Common labels; selector labels are only app.kubernetes.io/name and instance. */}}
{{- define "quotify.selector" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
{{- end -}}

{{- define "quotify.labels" -}}
{{ include "quotify.selector" . }}
app.kubernetes.io/part-of: quotify
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .root.Chart.Name .root.Chart.Version }}
{{- end -}}

{{/* Password of a Postgres role. */}}
{{- define "quotify.pgPassword" -}}
{{- $pw := get .root.Values.postgres.passwords .role -}}
{{- default (printf "%s_local" .role) $pw -}}
{{- end -}}

{{/* postgres://<role>_<kind>:<password>@host:port/<db> */}}
{{- define "quotify.dsn" -}}
{{- $role := printf "%s_%s" .db.role .kind -}}
{{- printf "postgres://%s:%s@%s:%v/%s?sslmode=%s" $role (include "quotify.pgPassword" (dict "root" .root "role" $role) | urlquery) .root.Values.postgres.host .root.Values.postgres.port .db.name .root.Values.postgres.sslmode -}}
{{- end -}}

{{/* Services that are deployed (a service can be switched off with enabled: false). */}}
{{- define "quotify.enabled" -}}
{{- if or (not (hasKey . "enabled")) .enabled -}}true{{- end -}}
{{- end -}}

{{/* Secret-backed environment of a service, as a dict: the DSNs of its runtime roles, broker and S3 credentials, the operator
token. With .migrate the migrator role's DSN is added; that is the environment of the migration Job, so the running pods never
see the schema owner's password while the Job still loads the service's complete configuration. */}}
{{- define "quotify.secretEnv" -}}
{{- $root := .root -}}
{{- $svc := .svc -}}
{{- $out := dict -}}
{{- if $svc.db -}}
  {{- if $svc.db.app -}}{{- $_ := set $out "QUOTIFY_PG_APP_DSN" (include "quotify.dsn" (dict "root" $root "db" $svc.db "kind" "app")) -}}{{- end -}}
  {{- if $svc.db.system -}}{{- $_ := set $out "QUOTIFY_PG_SYSTEM_DSN" (include "quotify.dsn" (dict "root" $root "db" $svc.db "kind" "system")) -}}{{- end -}}
  {{- if .migrate -}}{{- $_ := set $out "QUOTIFY_PG_MIGRATOR_DSN" (include "quotify.dsn" (dict "root" $root "db" $svc.db "kind" "migrator")) -}}{{- end -}}
{{- end -}}
{{- range $svc.uses -}}
  {{- if eq . "rabbitmq" -}}
    {{- $_ := set $out "QUOTIFY_RABBITMQ_URL" (printf "amqp://%s:%s@%s:%v/" $root.Values.rabbitmq.user ($root.Values.rabbitmq.password | urlquery) $root.Values.rabbitmq.host $root.Values.rabbitmq.port) -}}
  {{- else if eq . "s3" -}}
    {{- $_ := set $out "S3_ACCESS_KEY" $root.Values.s3.accessKey -}}
    {{- $_ := set $out "S3_SECRET_KEY" $root.Values.s3.secretKey -}}
  {{- else if eq . "clickhouse" -}}
    {{- $_ := set $out "CLICKHOUSE_PASSWORD" $root.Values.clickhouse.password -}}
  {{- else if eq . "operatorToken" -}}
    {{- $_ := set $out "QUOTIFY_OPERATOR_TOKEN" $root.Values.operatorToken -}}
  {{- else if eq . "otpPepper" -}}
    {{- $_ := set $out "QUOTIFY_OTP_PEPPER" $root.Values.auth.otpPepper -}}
  {{- else if eq . "google" -}}
    {{- if and $root.Values.auth.google.clientId $root.Values.auth.google.clientSecret -}}
      {{- $_ := set $out "QUOTIFY_GOOGLE_CLIENT_ID" $root.Values.auth.google.clientId -}}
      {{- $_ := set $out "QUOTIFY_GOOGLE_CLIENT_SECRET" $root.Values.auth.google.clientSecret -}}
    {{- end -}}
  {{- else if eq . "trustedProxySecret" -}}
    {{- if $root.Values.auth.trustedProxySecret -}}
      {{- $_ := set $out "QUOTIFY_TRUSTED_PROXY_SECRET" $root.Values.auth.trustedProxySecret -}}
    {{- end -}}
  {{- else if eq . "paymentsToken" -}}
    {{- $_ := set $out "QUOTIFY_PAYMENTS_INTERNAL_TOKEN" $root.Values.payments.internalToken -}}
  {{- else if eq . "secretsKey" -}}
    {{- $_ := set $out "QUOTIFY_SECRETS_KEY" $root.Values.payments.secretsKey -}}
  {{- else if eq . "paymentsSecrets" -}}
    {{- $_ := set $out "PAYMENTS_INTERNAL_TOKEN" $root.Values.payments.internalToken -}}
    {{- $_ := set $out "QUOTIFY_SECRETS_KEY" $root.Values.payments.secretsKey -}}
  {{- end -}}
{{- end -}}
{{- toJson $out -}}
{{- end -}}

{{/* Plain environment shared by every container of a service (also the migration Job). */}}
{{- define "quotify.baseEnv" -}}
- { name: QUOTIFY_ENV, value: {{ .root.Values.global.environment | quote }} }
- { name: QUOTIFY_SERVICE_NAME, value: {{ .name | quote }} }
- { name: QUOTIFY_VERSION, value: {{ .root.Values.global.imageTag | quote }} }
- { name: QUOTIFY_METRICS_ADDR, value: {{ printf ":%v" .svc.metricsPort | quote }} }
{{- if .root.Values.global.otlpEndpoint }}
- { name: QUOTIFY_OTLP_ENDPOINT, value: {{ .root.Values.global.otlpEndpoint | quote }} }
{{- end }}
{{- end -}}

{{/* Settings that identify the S3 and ClickHouse endpoints (not secret). */}}
{{- define "quotify.usesEnv" -}}
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
{{- define "quotify.staticEnv" -}}
{{- $root := .root -}}
{{- range $k := keys .svc.env | sortAlpha }}
- { name: {{ $k }}, value: {{ tpl (toString (get $.svc.env $k)) $root | quote }} }
{{- end }}
{{- end -}}

{{- define "infra.labels" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/part-of: quotify-infra
{{- end -}}
{{- define "infra.selector" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
{{- end -}}
{{- define "infra.storageClass" -}}
{{- if .Values.storageClass }}storageClassName: {{ .Values.storageClass | quote }}{{- end -}}
{{- end -}}
{{- define "infra.pw" -}}
{{- default (printf "%s_local" .role) (get .root.Values.postgres.passwords .role) -}}
{{- end -}}

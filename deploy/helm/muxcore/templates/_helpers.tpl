{{- define "muxcore.labels" -}}
app.kubernetes.io/part-of: muxcore
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

{{- define "muxcore.tlsEnv" -}}
- name: MUXCORE_INSECURE_DISABLE_TLS
  value: {{ .Values.insecureDisableTLS | quote }}
{{- end -}}

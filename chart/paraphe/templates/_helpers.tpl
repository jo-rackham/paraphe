{{- define "paraphe.nom" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "paraphe.nomComplet" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $nom := default .Chart.Name .Values.nameOverride -}}
{{- if contains $nom .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $nom | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "paraphe.etiquettes" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "paraphe.selecteur" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "paraphe.selecteur" -}}
app.kubernetes.io/name: {{ include "paraphe.nom" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "paraphe.nomSecret" -}}
{{- if .Values.secrets.existingSecret -}}
{{- .Values.secrets.existingSecret -}}
{{- else -}}
{{- include "paraphe.nomComplet" . -}}
{{- end -}}
{{- end -}}

{{- define "paraphe.nomPostgres" -}}
{{- printf "%s-pg" (include "paraphe.nomComplet" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "paraphe.nomValkey" -}}
{{- printf "%s-valkey" (include "paraphe.nomComplet" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Its own selector: the Valkey pods must not be picked up by the
     application's Service, its PDB or its NetworkPolicy rules. */}}
{{- define "paraphe.selecteurValkey" -}}
app.kubernetes.io/name: {{ include "paraphe.nom" . }}-valkey
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* Secret et clé où lire le DSN PostgreSQL : celui que CloudNativePG
     génère pour le rôle applicatif, ou celui fourni par l'exploitant. */}}
{{- define "paraphe.secretBase" -}}
{{- if .Values.postgres.cnpg.enabled -}}
{{- printf "%s-app" (include "paraphe.nomPostgres" .) -}}
{{- else if .Values.postgres.external.existingSecret -}}
{{- .Values.postgres.external.existingSecret -}}
{{- else -}}
{{- fail "Aucune base configurée : activez postgres.cnpg.enabled ou renseignez postgres.external.existingSecret" -}}
{{- end -}}
{{- end -}}

{{- define "paraphe.cleBase" -}}
{{- if .Values.postgres.cnpg.enabled -}}uri{{- else -}}{{ .Values.postgres.external.secretKey }}{{- end -}}
{{- end -}}

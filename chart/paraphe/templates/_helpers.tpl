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

{{- define "paraphe.nomGarage" -}}
{{- printf "%s-garage" (include "paraphe.nomComplet" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Same reason as Valkey's: these pods answer on their own ports and must
     not be picked up by the application's Service or its PDB. */}}
{{- define "paraphe.selecteurGarage" -}}
app.kubernetes.io/name: {{ include "paraphe.nom" . }}-garage
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* L'endpoint S3 que l'APPLICATION appelle pour écrire : le Service du
     cluster quand le chart héberge Garage, l'adresse du fournisseur sinon.
     Deux adresses distinctes pour un même seau — l'autre, publique, est
     media.publicUrl, celle que le NAVIGATEUR appelle. */}}
{{- define "paraphe.endpointMedia" -}}
{{- if .Values.media.endpoint -}}
{{- .Values.media.endpoint -}}
{{- else if .Values.garage.enabled -}}
{{- printf "http://%s:3900" (include "paraphe.nomGarage" .) -}}
{{- else -}}
{{- fail "media.enabled sans garage.enabled : renseignez media.endpoint (l'object store du fournisseur), ou laissez le chart héberger Garage" -}}
{{- end -}}
{{- end -}}

{{/* L'origine PUBLIQUE d'où le navigateur télécharge les logos. Explicite
     ou déduite de media.host, jamais devinée à moitié : c'est elle que la
     Content-Security-Policy autorise, et une valeur fausse ne se voit que
     dans la console du navigateur. */}}
{{- define "paraphe.urlPubliqueMedia" -}}
{{- if .Values.media.publicUrl -}}
{{- .Values.media.publicUrl | trimSuffix "/" -}}
{{- else -}}
{{- printf "%s://%s" (ternary "https" "http" .Values.ingress.tls.enabled) .Values.media.host -}}
{{- end -}}
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

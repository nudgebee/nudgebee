{{- /*
Redis, rendered by this chart instead of the Bitnami subchart (redis 17.11.8)
it replaces. Every name, label and PVC field the Bitnami chart froze into the
StatefulSet is reproduced here exactly, so `helm upgrade` from a Bitnami-based
release only rolls the pod: same StatefulSet, same Services, same PVC and data.
See docs/security/redis-official-image-spec.md before changing anything that
feeds spec.selector, spec.serviceName or volumeClaimTemplates — the API server
rejects any change to those and the whole release upgrade fails.
*/ -}}

{{/* Bitnami common.names.name: the subchart name unless nameOverride is set. */}}
{{- define "nudgebee.redis.name" -}}
{{- default "redis" .Values.redis.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Bitnami common.names.fullname, against the redis subchart's values. */}}
{{- define "nudgebee.redis.fullname" -}}
{{- $name := include "nudgebee.redis.name" . -}}
{{- if .Values.redis.fullnameOverride -}}
{{- .Values.redis.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/* Bitnami common.labels.matchLabels. Frozen into spec.selector and the PVC template. */}}
{{- define "nudgebee.redis.matchLabels" -}}
app.kubernetes.io/name: {{ include "nudgebee.redis.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "nudgebee.redis.labels" -}}
{{ include "nudgebee.redis.matchLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Values.redis.image.tag | toString | trunc 63 | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- with .Values.redis.commonLabels }}
{{ tpl (toYaml .) $ }}
{{- end }}
{{- end -}}

{{/* Bitnami common.images.image: global.imageRegistry wins over redis.image.registry. */}}
{{- define "nudgebee.redis.image" -}}
{{- $img := .Values.redis.image -}}
{{- $registry := $img.registry -}}
{{- if .Values.global.imageRegistry -}}{{- $registry = .Values.global.imageRegistry -}}{{- end -}}
{{- $ref := ternary (printf "%s@%s" $img.repository ($img.digest | toString)) (printf "%s:%s" $img.repository ($img.tag | toString)) (not (empty $img.digest)) -}}
{{- if $registry -}}{{ printf "%s/%s" $registry $ref }}{{- else -}}{{ $ref }}{{- end -}}
{{- end -}}

{{/* Bitnami common.storage.class: global.storageClass wins; "-" renders "". */}}
{{- define "nudgebee.redis.storageClass" -}}
{{- $sc := .Values.redis.master.persistence.storageClass -}}
{{- if .Values.global.storageClass -}}{{- $sc = .Values.global.storageClass -}}{{- end -}}
{{- if $sc -}}
{{- if eq "-" $sc -}}storageClassName: ""{{- else -}}storageClassName: {{ $sc | quote }}{{- end -}}
{{- end -}}
{{- end -}}

{{- define "nudgebee.redis.secretName" -}}
{{- default "redis" (tpl (.Values.redis.auth.existingSecret | default "") .) -}}
{{- end -}}

{{- define "nudgebee.redis.secretKey" -}}
{{- default "redis-password" .Values.redis.auth.existingSecretPasswordKey -}}
{{- end -}}

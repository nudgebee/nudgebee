{{- define "postgresMigrationImageName" -}}
{{- $registry := (default .Values.global.image.registry .Values.postgres_migrations.image.registry) -}}
{{- if $registry -}}
{{- if contains $registry .Values.postgres_migrations.image.repository -}}
{{- printf "%s:%s" .Values.postgres_migrations.image.repository .Values.postgres_migrations.image.tag -}}
{{- else -}}
{{- printf "%s/%s:%s" $registry .Values.postgres_migrations.image.repository .Values.postgres_migrations.image.tag -}}
{{- end -}}
{{- else -}}
{{- printf "%s:%s" .Values.postgres_migrations.image.repository .Values.postgres_migrations.image.tag -}}
{{- end -}}
{{- end }}


{{- define "imagePullSecret" }}
{{- printf "{\"auths\": {\"%s\": {\"auth\": \"%s\"}}}" ((default .Values.global.image.registry .Values.nudgebee_registry_secret.registry )) (printf "%s:%s" .Values.nudgebee_registry_secret.username (default .Values.nudgebee_secret.NUDGEBEE_LICENSE .Values.nudgebee_registry_secret.password) | b64enc) | b64enc }}
{{- end }}

{{/*
Return the name of the main Nudgebee secret.
If .Values.global.existingNudgebeeSecretName is set, use that. Otherwise, use the default name "nudgebee".
*/}}
{{- define "nudgebee.secretName" -}}
{{- .Values.global.existingNudgebeeSecretName | default "nudgebee" -}}
{{- end -}}

{{/*
Return the name of the Nudgebee registry secret.
If nudgebee_registry_secret.existingSecretName is set, use that. Otherwise, use the default name "nudgebee-registry-secret".
*/}}
{{- define "nudgebee.registrySecretName" -}}
{{- .Values.nudgebee_registry_secret.existingSecretName | default "nudgebee-registry-secret" -}}
{{- end -}}

{{/*
Return the name of the ClickHouse secret.
If clickhouse.auth.existingSecret is set, use that. Otherwise, use the default name "clickhouse".
*/}}
{{- define "nudgebee.clickhouseSecretName" -}}
{{- .Values.clickhouse.auth.existingSecret | default "clickhouse" -}}
{{- end -}}

{{/*
Return the name of the PostgreSQL secret.
If postgresql.auth.existingSecret is set, use that. Otherwise, use the default name "postgresql".
*/}}
{{- define "nudgebee.postgresqlSecretName" -}}
{{- .Values.postgresql.auth.existingSecret | default "postgresql" -}}
{{- end -}}

{{/*
Return the name of the RabbitMQ secret (primarily for password).
If rabbitmq.auth.existingPasswordSecret is set, use that. Otherwise, use the default name "rabbitmq".
*/}}
{{- define "nudgebee.rabbitmqSecretName" -}}
{{- .Values.rabbitmq.auth.existingPasswordSecret | default "rabbitmq" -}}
{{- end -}}
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
{{/*
Fail an upgrade that renders against the previous chart's defaults.
`helm upgrade --reuse-values` replaces this chart's values.yaml with the last
release's computed values, so every default this version changes is lost:
the release workflow pins each app image tag there, and 1.12 still carried
the Bitnami PostgreSQL/RabbitMQ/Redis settings. The release workflow stamps
chartDefaultsVersion with the chart version; a reused copy carries the old
stamp (or none, from 1.12 and older).
*/}}
{{- define "nudgebee.failOnReusedValues" -}}
{{- if and .Release.IsUpgrade (ne (toString .Values.chartDefaultsVersion) .Chart.Version) -}}
  {{- fail (printf "\n\n[ERROR] This upgrade is rendering with the defaults of chart %s, not %s.\n\n`helm upgrade --reuse-values` keeps the previous chart's defaults, including its image tags, so the release would keep running the old images (and, from 1.12, the old Bitnami settings).\n\nUpgrade with your values file (-f values.yaml) or with --reset-then-reuse-values instead. If your values file sets chartDefaultsVersion, remove it.\n" (toString (default "1.12 or older" .Values.chartDefaultsVersion)) .Chart.Version) -}}
{{- end -}}
{{- end -}}

{{/*
Resolve the shared-password login flag once for Secret rendering and install
notes. An unset flag defaults off on installs and inherits the existing Secret
value on upgrades; upgrades without a readable value must provide an explicit
setting.
*/}}
{{- define "nudgebee.dummyCredsEnabled" -}}
{{- $configured := .Values.nudgebee_secret.NEXTAUTH_DUMMY_CREDS_ENABLED -}}
{{- $configuredString := "" -}}
{{- $secretName := .Values.nudgebee_secret.existingSecretName | default (include "nudgebee.secretName" .) -}}
{{- $secret := (lookup "v1" "Secret" .Release.Namespace $secretName) | default dict -}}
{{- $stored := "" -}}
{{- if and $secret.data (index $secret.data "NEXTAUTH_DUMMY_CREDS_ENABLED") -}}
  {{- $stored = index $secret.data "NEXTAUTH_DUMMY_CREDS_ENABLED" | b64dec | lower | trim -}}
{{- end -}}
{{- if kindIs "bool" $configured -}}
  {{- if $configured -}}
    {{- $configuredString = "true" -}}
  {{- else -}}
    {{- $configuredString = "false" -}}
  {{- end -}}
{{- else if kindIs "string" $configured -}}
  {{- $configuredString = lower (trim $configured) -}}
  {{- if and $configuredString (ne $configuredString "true") (ne $configuredString "false") -}}
    {{- fail "NEXTAUTH_DUMMY_CREDS_ENABLED must be true or false when set." -}}
  {{- end -}}
{{- else if not (eq $configured nil) -}}
  {{- fail "NEXTAUTH_DUMMY_CREDS_ENABLED must be a boolean or an unset value." -}}
{{- end -}}
{{- if $configuredString -}}
  {{- $configuredString -}}
{{- else if .Release.IsInstall -}}
false
{{- else if or (eq $stored "true") (eq $stored "false") -}}
  {{- $stored -}}
{{- else -}}
  {{- fail (printf "\n\n[ERROR] Cannot determine NEXTAUTH_DUMMY_CREDS_ENABLED for this upgrade. Secret %q is unavailable or does not contain a valid setting. For offline/no-secret upgrades, set it explicitly with --set nudgebee_secret.NEXTAUTH_DUMMY_CREDS_ENABLED=false (or true if intentionally enabling shared-password sign-in).\n" $secretName) -}}
{{- end -}}
{{- end -}}

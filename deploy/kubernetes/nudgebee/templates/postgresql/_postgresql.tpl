{{- /*
PostgreSQL, rendered by this chart instead of the Bitnami subchart
(postgresql 13.2.25) it replaces. The StatefulSet's frozen fields (name,
selector, serviceName, volumeClaimTemplates), PGDATA, uid/fsGroup, Secrets and
Services are reproduced exactly, so `helm upgrade` rolls the pod onto the
official image on the same data. 16.1 -> 16.15 is a minor upgrade: rollback
works on the same data. See docs/security/postgresql-official-image-spec.md.
*/ -}}

{{/*
Bitnami postgresql 13.2.25 defaults for every key these templates read, so an
install that set nothing renders what the subchart rendered. Values under
.Values.postgresql override them (nudgebee.postgresql.values).
*/}}
{{- define "nudgebee.postgresql.defaults" -}}
architecture: standalone
clusterDomain: cluster.local
commonLabels: {}
commonAnnotations: {}
containerPorts:
  postgresql: 5432
postgresqlDataDir: "/bitnami/postgresql/data"
postgresqlSharedPreloadLibraries: pgaudit
shmVolume:
  enabled: true
  sizeLimit: ''
image:
  pullPolicy: IfNotPresent
  pullSecrets: []
  digest: ''
auth:
  enablePostgresUser: true
  username: ''
  database: ''
  usePasswordFiles: false
  secretKeys:
    adminPasswordKey: postgres-password
    userPasswordKey: password
    replicationPasswordKey: replication-password
  existingSecret: ''
audit:
  logHostname: false
  logConnections: false
  logDisconnections: false
  pgAuditLog: ''
  pgAuditLogCatalog: 'off'
  clientMinMessages: error
  logLinePrefix: ''
  logTimezone: ''
primary:
  name: primary
  configuration: ''
  pgHbaConfiguration: ''
  existingConfigmap: ''
  extendedConfiguration: ''
  existingExtendedConfigmap: ''
  initdb:
    args: ''
    postgresqlWalDir: ''
    scripts: {}
    scriptsConfigMap: ''
    scriptsSecret: ''
    user: ''
    password: ''
  standby:
    enabled: false
    primaryHost: ''
    primaryPort: ''
  extraEnvVars: []
  extraEnvVarsCM: ''
  extraEnvVarsSecret: ''
  command: []
  args: []
  livenessProbe:
    enabled: true
    initialDelaySeconds: 30
    periodSeconds: 10
    timeoutSeconds: 5
    failureThreshold: 6
    successThreshold: 1
  readinessProbe:
    enabled: true
    initialDelaySeconds: 5
    periodSeconds: 10
    timeoutSeconds: 5
    failureThreshold: 6
    successThreshold: 1
  startupProbe:
    enabled: false
    initialDelaySeconds: 30
    periodSeconds: 10
    timeoutSeconds: 1
    failureThreshold: 15
    successThreshold: 1
  customLivenessProbe: {}
  customReadinessProbe: {}
  customStartupProbe: {}
  lifecycleHooks: {}
  resources:
    limits: {}
    requests:
      memory: 256Mi
      cpu: 250m
  podSecurityContext:
    enabled: true
    fsGroup: 1001
  containerSecurityContext:
    enabled: true
    runAsUser: 1001
    runAsNonRoot: true
    privileged: false
    readOnlyRootFilesystem: false
    allowPrivilegeEscalation: false
    capabilities:
      drop:
      - ALL
    seccompProfile:
      type: RuntimeDefault
  hostAliases: []
  hostNetwork: false
  hostIPC: false
  labels: {}
  annotations: {}
  podLabels: {}
  podAnnotations: {}
  podAffinityPreset: ''
  podAntiAffinityPreset: soft
  nodeAffinityPreset:
    type: ''
    key: ''
    values: []
  affinity: {}
  nodeSelector: {}
  tolerations: []
  topologySpreadConstraints: []
  priorityClassName: ''
  schedulerName: ''
  terminationGracePeriodSeconds: ''
  updateStrategy:
    type: RollingUpdate
    rollingUpdate: {}
  extraVolumeMounts: []
  extraVolumes: []
  sidecars: []
  initContainers: []
  extraPodSpec: {}
  service:
    type: ClusterIP
    ports:
      postgresql: 5432
    nodePorts:
      postgresql: ''
    clusterIP: ''
    annotations: {}
    loadBalancerIP: ''
    externalTrafficPolicy: Cluster
    loadBalancerSourceRanges: []
    extraPorts: []
    sessionAffinity: None
    sessionAffinityConfig: {}
    headless:
      annotations: {}
  persistentVolumeClaimRetentionPolicy:
    enabled: false
    whenScaled: Retain
    whenDeleted: Retain
  persistence:
    enabled: true
    existingClaim: ''
    mountPath: "/bitnami/postgresql"
    subPath: ''
    storageClass: ''
    accessModes:
    - ReadWriteOnce
    size: 8Gi
    annotations: {}
    labels: {}
    selector: {}
    dataSource: {}
metrics:
  enabled: false
  customMetrics: {}
  extraEnvVars: []
  containerSecurityContext:
    enabled: true
    runAsUser: 1001
    runAsNonRoot: true
    privileged: false
    readOnlyRootFilesystem: false
    allowPrivilegeEscalation: false
    capabilities:
      drop:
      - ALL
    seccompProfile:
      type: RuntimeDefault
  livenessProbe:
    enabled: true
    initialDelaySeconds: 5
    periodSeconds: 10
    timeoutSeconds: 5
    failureThreshold: 6
    successThreshold: 1
  readinessProbe:
    enabled: true
    initialDelaySeconds: 5
    periodSeconds: 10
    timeoutSeconds: 5
    failureThreshold: 6
    successThreshold: 1
  startupProbe:
    enabled: false
    initialDelaySeconds: 10
    periodSeconds: 10
    timeoutSeconds: 1
    failureThreshold: 15
    successThreshold: 1
  customLivenessProbe: {}
  customReadinessProbe: {}
  customStartupProbe: {}
  containerPorts:
    metrics: 9187
  resources:
    limits: {}
    requests: {}
  service:
    ports:
      metrics: 9187
    clusterIP: ''
    sessionAffinity: None
    annotations:
      prometheus.io/scrape: 'true'
      prometheus.io/port: "{{ "{{" }} .Values.metrics.service.ports.metrics {{ "}}" }}"
  serviceMonitor:
    enabled: false
    namespace: ''
    interval: ''
    scrapeTimeout: ''
    labels: {}
    selector: {}
    relabelings: []
    metricRelabelings: []
    honorLabels: false
    jobLabel: ''
  prometheusRule:
    enabled: false
    namespace: ''
    labels: {}
    rules: []
  collectors: {}
  image:
    pullPolicy: IfNotPresent
    pullSecrets: []
    digest: ''
serviceAccount:
  create: false
  name: ''
  automountServiceAccountToken: true
  annotations: {}
rbac:
  create: false
  rules: []
psp:
  create: false
{{- end -}}

{{/* .Values.postgresql merged over the Bitnami defaults. Use: $r := include "nudgebee.postgresql.values" . | fromYaml */}}
{{- define "nudgebee.postgresql.values" -}}
{{- $d := include "nudgebee.postgresql.defaults" . | fromYaml -}}
{{- toYaml (mergeOverwrite $d (deepCopy .Values.postgresql)) -}}
{{- end -}}

{{/*
tpl a value written for the Bitnami subchart, whose templates saw `.Values` as
the postgresql block. Use: (dict "value" <string|map|list> "root" $)
*/}}
{{- define "nudgebee.postgresql.tpl" -}}
{{- $r := include "nudgebee.postgresql.values" .root | fromYaml -}}
{{- $_ := set $r "global" (.root.Values.global | default (dict)) -}}
{{- $ctx := dict "Values" $r "Release" .root.Release "Chart" .root.Chart "Template" .root.Template "Capabilities" .root.Capabilities -}}
{{- if typeIs "string" .value -}}{{- tpl .value $ctx -}}{{- else -}}{{- tpl (toYaml .value) $ctx -}}{{- end -}}
{{- end -}}

{{/* Bitnami's global.postgresql block, empty when unset. */}}
{{- define "nudgebee.postgresql.global" -}}
{{- toYaml ((.Values.global | default (dict)).postgresql | default (dict)) -}}
{{- end -}}

{{- define "nudgebee.postgresql.name" -}}
{{- default "postgresql" .Values.postgresql.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Bitnami common.names.fullname. */}}
{{- define "nudgebee.postgresql.fullname" -}}
{{- $name := include "nudgebee.postgresql.name" . -}}
{{- if .Values.postgresql.fullnameOverride -}}
{{- .Values.postgresql.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/* Bitnami postgresql.v1.primary.fullname (standalone: the fullname; replication is rejected). */}}
{{- define "nudgebee.postgresql.primaryFullname" -}}
{{- include "nudgebee.postgresql.fullname" . -}}
{{- end -}}

{{- define "nudgebee.postgresql.headlessName" -}}
{{- printf "%s-hl" (include "nudgebee.postgresql.primaryFullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Bitnami common.labels.matchLabels with customLabels: name/instance, where a
custom label may override exactly those two keys. Frozen into spec.selector.
Use: (dict "custom" <map> "root" $)
*/}}
{{- define "nudgebee.postgresql.matchLabels" -}}
{{- $base := dict "app.kubernetes.io/name" (include "nudgebee.postgresql.name" .root) "app.kubernetes.io/instance" .root.Release.Name -}}
{{- $custom := pick (.custom | default (dict)) "app.kubernetes.io/name" "app.kubernetes.io/instance" -}}
{{- toYaml (merge $custom $base) -}}
{{- end -}}

{{/* Bitnami common.labels.standard. Use: (dict "custom" <map> "root" $) */}}
{{- define "nudgebee.postgresql.labels" -}}
{{- $std := dict "app.kubernetes.io/managed-by" .root.Release.Service "app.kubernetes.io/version" (toString .root.Values.postgresql.image.tag) "helm.sh/chart" (printf "%s-%s" .root.Chart.Name .root.Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-") -}}
{{- toYaml (merge (dict) (.custom | default (dict)) (include "nudgebee.postgresql.matchLabels" (dict "custom" (dict) "root" .root) | fromYaml) $std) -}}
{{- end -}}

{{/* Bitnami common.images.image. Use: (dict "image" <imageRoot> "root" $) */}}
{{- define "nudgebee.postgresql.image" -}}
{{- $img := .image -}}
{{- $registry := $img.registry -}}
{{- if .root.Values.global.imageRegistry -}}{{- $registry = .root.Values.global.imageRegistry -}}{{- end -}}
{{- $ref := ternary (printf "%s@%s" $img.repository ($img.digest | toString)) (printf "%s:%s" $img.repository ($img.tag | toString)) (not (empty $img.digest)) -}}
{{- if $registry -}}{{ printf "%s/%s" $registry $ref }}{{- else -}}{{ $ref }}{{- end -}}
{{- end -}}

{{- define "nudgebee.postgresql.storageClass" -}}
{{- $sc := .persistence.storageClass -}}
{{- if .global.storageClass -}}{{- $sc = .global.storageClass -}}{{- end -}}
{{- if $sc -}}
{{- if eq "-" $sc -}}storageClassName: ""{{- else -}}storageClassName: {{ $sc | quote }}{{- end -}}
{{- end -}}
{{- end -}}

{{/* Bitnami postgresql.v1.secretName: global.postgresql.auth.existingSecret wins. */}}
{{- define "nudgebee.postgresql.secretName" -}}
{{- $g := (include "nudgebee.postgresql.global" . | fromYaml).auth | default (dict) -}}
{{- if $g.existingSecret -}}{{- tpl $g.existingSecret . -}}
{{- else if .Values.postgresql.auth.existingSecret -}}{{- tpl .Values.postgresql.auth.existingSecret . -}}
{{- else -}}{{- include "nudgebee.postgresql.fullname" . -}}{{- end -}}
{{- end -}}

{{/* Bitnami postgresql.v1.adminPasswordKey. */}}
{{- define "nudgebee.postgresql.adminPasswordKey" -}}
{{- $r := include "nudgebee.postgresql.values" . | fromYaml -}}
{{- $g := (include "nudgebee.postgresql.global" . | fromYaml).auth | default (dict) -}}
{{- if or $g.existingSecret $r.auth.existingSecret -}}
{{- coalesce ((($g.secretKeys | default (dict)).adminPasswordKey)) $r.auth.secretKeys.adminPasswordKey "postgres-password" -}}
{{- else -}}postgres-password{{- end -}}
{{- end -}}

{{/* Bitnami postgresql.v1.database. */}}
{{- define "nudgebee.postgresql.database" -}}
{{- $g := (include "nudgebee.postgresql.global" . | fromYaml).auth | default (dict) -}}
{{- if $g.database -}}{{- tpl $g.database . -}}
{{- else if .Values.postgresql.auth.database -}}{{- tpl .Values.postgresql.auth.database . -}}{{- end -}}
{{- end -}}

{{/* Bitnami postgresql.v1.service.port. */}}
{{- define "nudgebee.postgresql.servicePort" -}}
{{- $g := include "nudgebee.postgresql.global" . | fromYaml -}}
{{- $r := include "nudgebee.postgresql.values" . | fromYaml -}}
{{- default $r.primary.service.ports.postgresql (($g.service | default (dict)).ports | default (dict)).postgresql -}}
{{- end -}}

{{- define "nudgebee.postgresql.serviceAccountName" -}}
{{- $sa := (include "nudgebee.postgresql.values" . | fromYaml).serviceAccount -}}
{{- if $sa.create -}}{{ default (include "nudgebee.postgresql.fullname" .) $sa.name }}{{- else -}}{{ default "default" $sa.name }}{{- end -}}
{{- end -}}

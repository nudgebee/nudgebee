{{- /*
RabbitMQ, rendered by this chart instead of the Bitnami subchart (rabbitmq
12.6.1) it replaces. The StatefulSet's frozen fields (name, selector,
serviceName, podManagementPolicy, volumeClaimTemplates), the node name and the
data path are reproduced exactly, so `helm upgrade` from a Bitnami-based
release rolls the pod onto the official image on the same data. Rollback to
the Bitnami 3.13 image is not possible (RabbitMQ 3.13 cannot read data a 4.x
node has run on). See docs/security/rabbitmq-official-image-spec.md.
*/ -}}

{{/*
Bitnami rabbitmq 12.6.1 defaults for every key these templates read, so an
install that set nothing renders what the subchart rendered. Values under
.Values.rabbitmq override them (nudgebee.rabbitmq.values).
*/}}
{{- define "nudgebee.rabbitmq.defaults" -}}
image:
  pullPolicy: IfNotPresent
  pullSecrets: []
  digest: ''
auth:
  username: user
  password: ''
  securePassword: true
  existingPasswordSecret: ''
  enableLoopbackUser: false
  erlangCookie: ''
  existingErlangSecret: ''
plugins: rabbitmq_management rabbitmq_peer_discovery_k8s
extraPlugins: rabbitmq_auth_backend_ldap
clustering:
  enabled: true
  name: ''
  addressType: hostname
  rebalance: false
  forceBoot: false
  partitionHandling: autoheal
clusterDomain: cluster.local
servicenameOverride: ''
podManagementPolicy: OrderedReady
replicaCount: 1
updateStrategy:
  type: RollingUpdate
persistence:
  enabled: true
  storageClass: ''
  selector: {}
  accessModes:
  - ReadWriteOnce
  existingClaim: ''
  mountPath: "/bitnami/rabbitmq/mnesia"
  subPath: ''
  size: 8Gi
  annotations: {}
  labels: {}
persistentVolumeClaimRetentionPolicy:
  enabled: false
  whenScaled: Retain
  whenDeleted: Retain
containerPorts:
  amqp: 5672
  amqpTls: 5671
  dist: 25672
  manager: 15672
  epmd: 4369
  metrics: 9419
service:
  type: ClusterIP
  portEnabled: true
  distPortEnabled: true
  managerPortEnabled: true
  epmdPortEnabled: true
  ports:
    amqp: 5672
    amqpTls: 5671
    dist: 25672
    manager: 15672
    metrics: 9419
    epmd: 4369
  portNames:
    amqp: amqp
    amqpTls: amqp-tls
    dist: dist
    manager: http-stats
    metrics: metrics
    epmd: epmd
  nodePorts:
    amqp: ''
    amqpTls: ''
    dist: ''
    manager: ''
    epmd: ''
    metrics: ''
  extraPorts: []
  loadBalancerSourceRanges: []
  allocateLoadBalancerNodePorts: true
  externalIPs: []
  externalTrafficPolicy: Cluster
  loadBalancerIP: ''
  clusterIP: ''
  labels: {}
  annotations: {}
  annotationsHeadless: {}
  headless:
    annotations: {}
  sessionAffinity: None
  sessionAffinityConfig: {}
podSecurityContext:
  enabled: true
  fsGroup: 1001
containerSecurityContext:
  enabled: true
  runAsUser: 1001
  runAsNonRoot: true
  allowPrivilegeEscalation: false
  capabilities:
    drop:
    - ALL
  seccompProfile:
    type: RuntimeDefault
livenessProbe:
  enabled: true
  initialDelaySeconds: 120
  timeoutSeconds: 20
  periodSeconds: 30
  failureThreshold: 6
  successThreshold: 1
readinessProbe:
  enabled: true
  initialDelaySeconds: 10
  timeoutSeconds: 20
  periodSeconds: 30
  failureThreshold: 3
  successThreshold: 1
startupProbe:
  enabled: false
  initialDelaySeconds: 10
  timeoutSeconds: 20
  periodSeconds: 30
  failureThreshold: 3
  successThreshold: 1
terminationGracePeriodSeconds: 120
enableServiceLinks: true
logs: "-"
memoryHighWatermark:
  enabled: false
  type: relative
  value: 0.4
tcpListenOptions:
  backlog: 128
  nodelay: true
  linger:
    lingerOn: true
    timeout: 0
  keepalive: false
rbac:
  create: true
serviceAccount:
  create: true
  name: ''
  automountServiceAccountToken: true
  annotations: {}
pdb:
  create: false
  minAvailable: 1
  maxUnavailable: ''
podAffinityPreset: ''
podAntiAffinityPreset: soft
nodeAffinityPreset:
  type: ''
  key: ''
  values: []
loadDefinition:
  enabled: false
  file: "/app/load_definition.json"
  existingSecret: ''
maxAvailableSchedulers: ''
onlineSchedulers: ''
metrics:
  enabled: false
  plugins: rabbitmq_prometheus
  podAnnotations:
    prometheus.io/scrape: 'true'
    prometheus.io/port: "{{ "{{" }} .Values.service.ports.metrics {{ "}}" }}"
  serviceMonitor:
    enabled: false
    namespace: ''
    interval: 30s
    scrapeTimeout: ''
    jobLabel: ''
    relabelings: []
    metricRelabelings: []
    honorLabels: false
    targetLabels: {}
    podTargetLabels: {}
    path: ''
    params: {}
    selector: {}
    labels: {}
    annotations: {}
  prometheusRule:
    enabled: false
    additionalLabels: {}
    namespace: ''
    rules: []
featureFlags: ''
configurationExistingSecret: ''
extraConfiguration: ''
extraConfigurationExistingSecret: ''
advancedConfiguration: ''
advancedConfigurationExistingSecret: ''
commonLabels: {}
commonAnnotations: {}
statefulsetLabels: {}
statefulsetAnnotations: {}
podLabels: {}
podAnnotations: {}
extraEnvVars: []
extraEnvVarsCM: ''
extraEnvVarsSecret: ''
extraContainerPorts: []
extraVolumes: []
extraVolumeMounts: []
initContainers: []
sidecars: []
hostAliases: []
nodeSelector: {}
tolerations: []
affinity: {}
topologySpreadConstraints: []
priorityClassName: ''
schedulerName: ''
dnsPolicy: ''
dnsConfig: {}
lifecycleHooks: {}
customLivenessProbe: {}
customReadinessProbe: {}
customStartupProbe: {}
command: []
args: []
resources: {}
{{- end -}}

{{/* .Values.rabbitmq merged over the Bitnami defaults. Use: $r := include "nudgebee.rabbitmq.values" . | fromYaml */}}
{{- define "nudgebee.rabbitmq.values" -}}
{{- $d := include "nudgebee.rabbitmq.defaults" . | fromYaml -}}
{{- toYaml (mergeOverwrite $d (deepCopy .Values.rabbitmq)) -}}
{{- end -}}


{{- define "nudgebee.rabbitmq.name" -}}
{{- default "rabbitmq" .Values.rabbitmq.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Bitnami common.names.fullname. */}}
{{- define "nudgebee.rabbitmq.fullname" -}}
{{- $name := include "nudgebee.rabbitmq.name" . -}}
{{- if .Values.rabbitmq.fullnameOverride -}}
{{- .Values.rabbitmq.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "nudgebee.rabbitmq.headlessName" -}}
{{- printf "%s-%s" (include "nudgebee.rabbitmq.fullname" .) (default "headless" .Values.rabbitmq.servicenameOverride) -}}
{{- end -}}

{{/*
Bitnami common.labels.matchLabels with customLabels: name/instance, where a
custom label may override exactly those two keys. Frozen into spec.selector
and the PVC template. Use: (dict "custom" <map> "root" $)
*/}}
{{- define "nudgebee.rabbitmq.matchLabels" -}}
{{- $base := dict "app.kubernetes.io/name" (include "nudgebee.rabbitmq.name" .root) "app.kubernetes.io/instance" .root.Release.Name -}}
{{- $custom := pick (.custom | default (dict)) "app.kubernetes.io/name" "app.kubernetes.io/instance" -}}
{{- toYaml (merge $custom $base) -}}
{{- end -}}

{{/* Bitnami common.labels.standard: matchLabels + chart labels + custom labels. Use: (dict "custom" <map> "root" $) */}}
{{- define "nudgebee.rabbitmq.labels" -}}
{{- $std := dict "app.kubernetes.io/managed-by" .root.Release.Service "app.kubernetes.io/version" (toString .root.Values.rabbitmq.image.tag) "helm.sh/chart" (printf "%s-%s" .root.Chart.Name .root.Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-") -}}
{{- toYaml (merge (dict) (.custom | default (dict)) (include "nudgebee.rabbitmq.matchLabels" (dict "custom" (dict) "root" .root) | fromYaml) $std) -}}
{{- end -}}

{{- define "nudgebee.rabbitmq.image" -}}
{{- $img := .Values.rabbitmq.image -}}
{{- $registry := $img.registry -}}
{{- if .Values.global.imageRegistry -}}{{- $registry = .Values.global.imageRegistry -}}{{- end -}}
{{- $ref := ternary (printf "%s@%s" $img.repository ($img.digest | toString)) (printf "%s:%s" $img.repository ($img.tag | toString)) (not (empty $img.digest)) -}}
{{- if $registry -}}{{ printf "%s/%s" $registry $ref }}{{- else -}}{{ $ref }}{{- end -}}
{{- end -}}

{{- define "nudgebee.rabbitmq.storageClass" -}}
{{- $sc := .persistence.storageClass -}}
{{- if .global.storageClass -}}{{- $sc = .global.storageClass -}}{{- end -}}
{{- if $sc -}}
{{- if eq "-" $sc -}}storageClassName: ""{{- else -}}storageClassName: {{ $sc | quote }}{{- end -}}
{{- end -}}
{{- end -}}

{{- define "nudgebee.rabbitmq.passwordSecret" -}}
{{- default (include "nudgebee.rabbitmq.fullname" .) (tpl (.Values.rabbitmq.auth.existingPasswordSecret | default "") .) -}}
{{- end -}}

{{- define "nudgebee.rabbitmq.erlangSecret" -}}
{{- default (include "nudgebee.rabbitmq.fullname" .) (tpl (.Values.rabbitmq.auth.existingErlangSecret | default "") .) -}}
{{- end -}}

{{- define "nudgebee.rabbitmq.serviceAccountName" -}}
{{- $sa := (include "nudgebee.rabbitmq.values" . | fromYaml).serviceAccount -}}
{{- if $sa.create -}}{{ default (include "nudgebee.rabbitmq.fullname" .) $sa.name }}{{- else -}}{{ default "default" $sa.name }}{{- end -}}
{{- end -}}

{{/* Bitnami rabbitmq.plugins, comma-separated for RABBITMQ_ENABLED_PLUGINS. */}}
{{- define "nudgebee.rabbitmq.plugins" -}}
{{- $r := include "nudgebee.rabbitmq.values" . | fromYaml -}}
{{- $p := splitList " " (printf "%s %s %s" $r.plugins $r.extraPlugins (ternary $r.metrics.plugins "" $r.metrics.enabled)) -}}
{{- join "," (compact $p | uniq) -}}
{{- end -}}

{{/*
tpl a value written for the Bitnami subchart, whose templates saw `.Values` as
the rabbitmq block (e.g. metrics.podAnnotations refers to
.Values.service.ports.metrics). Use: (dict "value" <string|map|list> "root" $)
*/}}
{{- define "nudgebee.rabbitmq.tpl" -}}
{{- $r := include "nudgebee.rabbitmq.values" .root | fromYaml -}}
{{- $ctx := dict "Values" $r "Release" .root.Release "Chart" .root.Chart "Template" .root.Template "Capabilities" .root.Capabilities -}}
{{- if typeIs "string" .value -}}{{- tpl .value $ctx -}}{{- else -}}{{- tpl (toYaml .value) $ctx -}}{{- end -}}
{{- end -}}
{{- define "nudgebee.rabbitmq.toBytes" -}}
{{- /* A plain number of bytes arrives as a float64 from YAML/--set. */ -}}
{{- $in := ternary . (printf "%d" (int64 .)) (kindIs "string" .) -}}
{{- $value := int (regexReplaceAll "([0-9]+).*" $in "${1}") }}
{{- $unit := regexReplaceAll "[0-9]+(.*)" $in "${1}" }}
{{- if eq $unit "Ki" }}
    {{- mul $value 1024 }}
{{- else if eq $unit "Mi" }}
    {{- mul $value 1024 1024 }}
{{- else if eq $unit "Gi" }}
    {{- mul $value 1024 1024 1024 }}
{{- else if eq $unit "Ti" }}
    {{- mul $value 1024 1024 1024 1024 }}
{{- else if eq $unit "Pi" }}
    {{- mul $value 1024 1024 1024 1024 1024 }}
{{- else if eq $unit "Ei" }}
    {{- mul $value 1024 1024 1024 1024 1024 1024 }}
{{- else if eq $unit "K" }}
    {{- mul $value 1000 }}
{{- else if eq $unit "M" }}
    {{- mul $value 1000 1000 }}
{{- else if eq $unit "G" }}
    {{- mul $value 1000 1000 1000 }}
{{- else if eq $unit "T" }}
    {{- mul $value 1000 1000 1000 1000 }}
{{- else if eq $unit "P" }}
    {{- mul $value 1000 1000 1000 1000 1000 }}
{{- else if eq $unit "E" }}
    {{- mul $value 1000 1000 1000 1000 1000 1000 }}
{{- else }}
    {{- $value }}
{{- end }}
{{- end -}}

{{/* Bitnami common.names.namespace: namespaceOverride places every object elsewhere. */}}
{{- define "nudgebee.rabbitmq.namespace" -}}
{{- default .Release.Namespace .Values.rabbitmq.namespaceOverride -}}
{{- end -}}

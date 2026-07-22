package integrations

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"nudgebee/services/common"
	"nudgebee/services/eventrule"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
	"time"

	"github.com/google/uuid"
)

func init() {
	core.RegisterIntegration(PrometheusAlertManagerWebhook{})
}

type PrometheusAlertManagerWebhook struct {
}

const IntegrationPrometheusAlertManagerWebhook = "prometheus_alertmanager_webhook"

func (m PrometheusAlertManagerWebhook) Name() string {
	return IntegrationPrometheusAlertManagerWebhook
}

func (m PrometheusAlertManagerWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m PrometheusAlertManagerWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of Prometheus Alertmanager Webhook",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         100,
			},
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			"token": {
				Type:             core.ToolSchemaTypeString,
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         70,
			},
		},
	}
}

func (m PrometheusAlertManagerWebhook) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m PrometheusAlertManagerWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

func (m PrometheusAlertManagerWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	payload := map[string]any{}
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &payload); err != nil {
		return []core.EventIncomingWebhook{}, err
	}

	alertsRaw, ok := payload["alerts"].([]any)
	if !ok || len(alertsRaw) == 0 {
		return []core.EventIncomingWebhook{}, fmt.Errorf("no alerts found in payload")
	}

	externalURL, _ := payload["externalURL"].(string)

	var results []core.EventIncomingWebhook

	for _, alertRaw := range alertsRaw {
		alert, ok := alertRaw.(map[string]any)
		if !ok {
			continue
		}

		labels := mapStringAnyToStringString(alert["labels"])
		if externalURL != "" {
			labels["externalURL"] = externalURL
		}
		annotations := mapStringAnyToStringString(alert["annotations"])

		// Alertmanager carries runbook_url as an annotation, not a label, but the
		// downstream alert-rule evidence card (buildAlertRuleEvidence) reads Labels.
		// Surface it there (non-clobber) so the runbook link renders — mirrors the
		// zenduty/pagerduty parser, which flattens annotations into labels.
		if rb := annotations["runbook_url"]; rb != "" && labels["runbook_url"] == "" {
			labels["runbook_url"] = rb
		}

		startsAtStr, _ := alert["startsAt"].(string)
		startsAt, err := time.Parse(time.RFC3339, startsAtStr)
		if err != nil && startsAtStr != "" {
			slog.Warn("prometheus alertmanager webhook: failed to parse 'startsAt'", "value", startsAtStr, "error", err)
		}

		endsAtStr, _ := alert["endsAt"].(string)
		endsAt, err := time.Parse(time.RFC3339, endsAtStr)
		if err != nil && endsAtStr != "" {
			slog.Warn("prometheus alertmanager webhook: failed to parse 'endsAt'", "value", endsAtStr, "error", err)
		}

		fingerprint, _ := alert["fingerprint"].(string)
		generatorURL, _ := alert["generatorURL"].(string)
		status, _ := alert["status"].(string)

		if generatorURL != "" {
			labels["generatorURL"] = generatorURL
		}

		subjectKind, subjectName := extractPromSubject(labels)
		namespace := extractPromNamespace(labels)
		if namespace != "" && labels["namespace"] == "" {
			labels["namespace"] = namespace
		}
		if subjectName != "" && labels["service"] == "" {
			labels["service"] = subjectName
		}

		// If subject is the pod and we have a service label, use it as the
		// stable owner since pod names are ephemeral.
		var ownerKind, ownerName string
		if subjectKind == "pod" && labels["service"] != "" && labels["service"] != subjectName {
			ownerKind = "service"
			ownerName = labels["service"]
		}

		alertName := labels["alertname"]
		severity := labels["severity"]
		if severity == "" {
			severity = "warning"
		}
		// Detach from the request context so request completion does not
		// cancel the rule creation, and protect the service from a panic
		// inside CreateEventRule via recover.
		detachedSc := security.NewRequestContext(
			context.WithoutCancel(sc.GetContext()),
			sc.GetSecurityContext(),
			sc.GetLogger(),
			sc.GetTracer(),
			sc.GetMeter(),
		)
		go func(asc *security.RequestContext) {
			defer func() {
				if r := recover(); r != nil {
					asc.GetLogger().Error("prometheus alertmanager webhook: panic in CreateEventRule goroutine", "recover", r, "alert", alertName)
				}
			}()
			eventReq := eventrule.EventConfig{
				Annotations: struct {
					Description string `json:"description"`
					Summary     string `json:"summary"`
					Runbook     string `json:"runbook"`
				}{
					Description: annotations["description"],
					Summary:     annotations["summary"],
					Runbook:     annotations["runbook_url"],
				},
				Labels: struct {
					Severity string `json:"severity"`
				}{Severity: severity},
				Alert:         alertName,
				AccountID:     accountId,
				Source:        IntegrationPrometheusAlertManagerWebhook,
				Category:      "alert",
				Severity:      severity,
				Enabled:       true,
				TriggerParams: []map[string]interface{}{},
				ActionParams:  []map[string]interface{}{},
			}
			if _, err := eventrule.CreateEventRule(asc, eventReq); err != nil {
				asc.GetLogger().Error("prometheus alertmanager webhook: CreateEventRule failed", "error", err, "alert", alertName)
			}
		}(detachedSc)

		title := annotations["summary"]
		if title == "" {
			title = alertName
		}

		var tags []string
		seen := map[string]bool{}
		for _, k := range []string{"cluster", "namespace", "service", "job"} {
			if v := labels[k]; v != "" && !seen[v] {
				tags = append(tags, v)
				seen[v] = true
			}
		}

		parsedPayload := core.EventIncomingWebhook{
			AccountId:             accountId,
			WebhookId:             uuid.NewString(),
			EventType:             alertName,
			EventId:               fingerprint,
			EventUrl:              generatorURL,
			EventStatus:           string(mapGrafanaStatus(status)),
			EventPriority:         string(mapGrafanaSeverity(labels["severity"])),
			EventCreatedAt:        startsAt,
			EventEndsAt:           endsAt,
			EventTitle:            title,
			EventDescription:      annotations["description"],
			EventTags:             tags,
			EventSubjectKind:      subjectKind,
			EventSubjectName:      subjectName,
			EventSubjectNamespace: namespace,
			EventSubjectOwner:     ownerName,
			EventSubjectOwnerKind: ownerKind,
			Investigation: core.EventIncomingWebhookInvestigation{
				RuleName:    alertName,
				Labels:      labels,
				Annotations: annotations,
				RuleType:    "prometheus",
				RuleId:      alertName,
				Fingerprint: fingerprint,
				Status:      mapGrafanaStatus(status),
				Severity:    mapGrafanaSeverity(labels["severity"]),
				SourceUrl:   generatorURL,
			},
		}

		results = append(results, parsedPayload)
	}

	if len(results) == 0 {
		return []core.EventIncomingWebhook{}, fmt.Errorf("no valid alerts found in payload")
	}

	return results, nil
}

// extractPromSubject picks the K8s subject from Prometheus alert labels.
// Priority: K8s controllers > service-mesh workload > app_id > PVC/HPA/Job > service > pod > instance.
// Controllers preferred over pod because pod names are ephemeral. Service
// preferred over pod when no controller label is present since prometheus
// usually labels alerts with the K8s Service name. The `job` label is
// intentionally NOT a controller fallback — in Prometheus it almost always
// refers to the scrape job, not a K8s Job; use `kube_job` for that.
// `destination_workload_name` / `workload` come from service-mesh telemetry
// (Istio, Linkerd) where the K8s controller kind is not in the label set.
//
// Side effect: for a logical-database (datname) subject with no resolvable host,
// it sets nb_skip_workload_match on the passed labels map — which is the alert's
// own label map that becomes the event payload — so subject resolution does not
// name-match the database onto a workload. The map is the caller's, and this is
// deliberate: the opt-out must travel with the event.
func extractPromSubject(labels map[string]string) (kind, name string) {
	for _, k := range []string{"deployment", "statefulset", "daemonset", "replicaset", "cronjob", "kube_job"} {
		if v := labels[k]; v != "" {
			kind = k
			if k == "kube_job" {
				kind = "job"
			}
			return kind, v
		}
	}
	for _, k := range []string{"destination_workload_name", "workload", "source_workload_name"} {
		if v := labels[k]; v != "" {
			return "workload", v
		}
	}
	// NudgeBee-native aggregated alerts (HighErrorCriticalLogs, …) carry none of
	// the standard k8s controller/pod labels — their PromQL groups by (namespace,
	// app_id) so the workload rides only in `app_id` (formatted /k8s/{ns}/{app}).
	// Extract the workload deterministically; an empty subject would otherwise send
	// the alert to the LLM, which hallucinates an unrelated workload. Ranked below
	// explicit controller/mesh labels (which are authoritative) but above the
	// generic service/pod fallbacks. Namespace is intentionally not taken from
	// app_id's middle segment — it is an app-group (e.g. "nudgebee"), not the k8s
	// namespace — so the real `namespace` label (handled by extractPromNamespace)
	// wins.
	if w := extractAppIDWorkload(labels["app_id"]); w != "" {
		return "workload", w
	}
	// Specific k8s resource labels (PVC / HPA / Job) name the resource the alert is
	// actually about and must win over the generic service/pod/instance fallbacks
	// below. E.g. KubePersistentVolumeFillingUp / KubernetesVolumeOutOfDiskSpace carry
	// persistentvolumeclaim=<pvc> alongside instance=<node host> and job=kubelet (the
	// scrape target) — the PVC is the subject, not the node or the kubelet scraper, so
	// without this branch these alerts resolve to the node instance (or the kubelet
	// scrape service). Mirrors the PagerDuty handler's resourceKeys precedence
	// (resolveSubjectFromLabels). `job_name` (NOT the prometheus `job` scrape label) is
	// the real K8s Job that kube-state-metrics emits on kube_job_* series.
	for _, rk := range []struct{ key, kind string }{
		{"persistentvolumeclaim", "persistentvolumeclaim"},
		{"horizontalpodautoscaler", "horizontalpodautoscaler"},
		{"hpa", "horizontalpodautoscaler"},
		{"job_name", "job"},
	} {
		if v := labels[rk.key]; v != "" {
			return rk.kind, v
		}
	}
	if v := labels["service"]; v != "" {
		return "service", v
	}
	if v := labels["pod"]; v != "" {
		return "pod", v
	}
	// PostgreSQL exporter alerts (PostgreSQLCacheHitRatio, …) key on `datname` — the
	// database — with no k8s workload/pod/service label. Prefer the database HOST
	// when the alert carries it (server / instance / host / hostname): it points at
	// the postgres pod or the RDS endpoint, which is the actionable subject, and it
	// resolves against k8s/cloud inventory. Fall back to the database name so the
	// subject is never empty — an empty subject sends the alert to the LLM, which
	// hallucinates an unrelated workload (e.g. datname=rdsadmin → a node-agent
	// daemonset). The port and loopback hosts (a sidecar exporter's
	// server=localhost:5432) are dropped so we skip to the next, real host.
	if dn := labels["datname"]; dn != "" {
		for _, k := range []string{"server", "instance", "host", "hostname"} {
			if h := dbHostOnly(labels[k]); h != "" {
				return "database", h
			}
		}
		// No resolvable host — the subject is the logical database name (e.g.
		// "temporal", "nudgebee"), not a workload. Mark it so subject resolution
		// does not name-match it onto an unrelated workload (a "database nudgebee"
		// alert otherwise lands on a same-named agent DaemonSet in another
		// namespace). It stays typed as a database until Phase 2 gives databases
		// first-class topology nodes.
		labels[core.SkipWorkloadMatchLabel] = "true"
		return "database", dn
	}
	if v := labels["instance"]; v != "" {
		return "instance", v
	}
	return "", ""
}

// extractAppIDWorkload parses NudgeBee's native `app_id` label — formatted
// `/k8s/{namespace}/{workload}` — and returns the workload (last path segment).
// Mirrors the same parse in the PagerDuty webhook handler
// (resolveSubjectFromLabels) so both ingest paths resolve app_id identically.
// Returns "" when app_id is absent or not in the /k8s/{ns}/{workload} form so the
// caller falls through to the next label.
func extractAppIDWorkload(appID string) string {
	if appID == "" {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(appID, "/"), "/")
	if len(parts) == 3 && parts[0] == "k8s" {
		return parts[2]
	}
	return ""
}

// dbHostOnly extracts the bare host from a Prometheus host-label value such as
// "10.0.0.5:9187", "mydb.abc.rds.amazonaws.com:5432", or "http://exp:9187/metrics",
// dropping scheme, path, port and IPv6 brackets. Loopback values — which don't
// identify a real database server (e.g. a sidecar exporter's server=localhost:5432)
// — return "" so the caller falls through to the next label.
func dbHostOnly(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if i := strings.Index(v, "://"); i >= 0 {
		v = v[i+3:]
	}
	if i := strings.IndexByte(v, '/'); i >= 0 {
		v = v[:i]
	}
	if host, _, err := net.SplitHostPort(v); err == nil {
		v = host
	}
	v = strings.Trim(v, "[]")
	switch strings.ToLower(v) {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return ""
	}
	return v
}

// extractPromNamespace picks the namespace from Prometheus alert labels,
// falling through service-mesh telemetry conventions when the canonical
// `namespace` label is absent.
func extractPromNamespace(labels map[string]string) string {
	for _, k := range []string{"namespace", "destination_workload_namespace", "workload_namespace", "source_workload_namespace", "kubernetes_namespace"} {
		if v := labels[k]; v != "" {
			return v
		}
	}
	return ""
}

func mapStringAnyToStringString(input any) map[string]string {
	result := map[string]string{}
	if input == nil {
		return result
	}
	if casted, ok := input.(map[string]any); ok {
		for k, v := range casted {
			if s, ok := v.(string); ok {
				result[k] = s
			}
		}
	}
	return result
}

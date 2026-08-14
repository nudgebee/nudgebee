package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"regexp"
	"sort"
	"strings"

	logadmin "cloud.google.com/go/logging/logadmin"
	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
)

// GCPScopeInput is the alerting context every GCP incident carries (derived from the
// monitored-resource + condition GCP attaches to the alert). It is provider-agnostic
// data the api-server already has on the event's labels.
type GCPScopeInput struct {
	Project        string            // gcp_project_id
	ResourceType   string            // gcp_event_resource_type (the monitored-resource type)
	ResourceLabels map[string]string // resource.labels (resource_* labels, prefix stripped)
	MetricType     string            // gcp_metric_type (may be an SLO expr or a log-based metric)
	AlertType      string            // gcp_alert_type: "metric" | "log"
	PolicyID       string            // gcp_policy_id: projects/<project>/alertPolicies/<id>
}

// ResolvedScope is what every generic GCP evidence query needs: a ready-to-run Cloud
// Logging filter plus the structured resource it scopes to. Source records which
// strategy resolved it (diagnostics / metrics).
type ResolvedScope struct {
	Project        string
	ResourceType   string
	ResourceLabels map[string]string
	LogFilter      string
	Source         string
}

// resolveGcloudScope turns any GCP alert into a concrete query scope, leaning on GCP's
// own authoritative APIs (alertPolicies.get / services.get / metrics.get) instead of
// reverse-engineering the resource. Precedence:
//  0. native log alert                                -> alertPolicies.get -> the policy's own filter
//  1. resource.labels identify a real instance        -> build filter from them
//  2. log-based metric (logging.googleapis.com/user/) -> metrics.get -> the metric's filter
//  3. SLO expression (.../services/<id>/...)          -> services.get -> structured resource
//  4. native log alert                                -> resource.type scope
//  5. fallback                                        -> resource.type project scope
func resolveGcloudScope(ctx providers.CloudProviderContext, account providers.Account, in GCPScopeInput) (ResolvedScope, error) {
	logger := ctx.GetLogger()

	// 0. Native log alert: the policy's log-match filter IS the scope, and it beats the
	// monitored resource. GCP's monitoring and logging resource catalogs disagree for
	// some types — a gke_nodepool alert carries identifying nodepool labels (so step 1
	// would win) but its notification records are written under gke_cluster, so the
	// resource-derived filter matches nothing. Falls through on any failure.
	if in.AlertType == "log" && in.PolicyID != "" {
		filter, err := resolveLogAlertPolicyFilter(ctx, account, in.PolicyID)
		if err != nil {
			logger.Warn("scope: alertPolicies.get failed", "policy", in.PolicyID, "error", err)
		} else if strings.TrimSpace(filter) != "" {
			return ResolvedScope{
				Project:      in.Project,
				ResourceType: in.ResourceType,
				LogFilter:    filter,
				Source:       "log_alert_policy",
			}, nil
		}
	}

	// 1. The monitored resource already identifies a specific instance.
	if in.ResourceType != "" && hasIdentifyingLabels(in.ResourceLabels) {
		return ResolvedScope{
			Project:        in.Project,
			ResourceType:   in.ResourceType,
			ResourceLabels: in.ResourceLabels,
			LogFilter:      buildResourceScopeFilter(in.ResourceType, in.ResourceLabels),
			Source:         "resource_labels",
		}, nil
	}

	// 2. Log-based metric alert: the metric's own filter IS the scope.
	if name, ok := extractLogMetricName(in.MetricType); ok {
		filter, err := resolveLogMetricFilter(ctx, account, in.Project, name)
		if err != nil {
			logger.Warn("scope: metrics.get failed", "metric", name, "error", err)
		} else if strings.TrimSpace(filter) != "" {
			return ResolvedScope{
				Project:      in.Project,
				ResourceType: in.ResourceType,
				LogFilter:    filter,
				Source:       "log_based_metric",
			}, nil
		}
	}

	// 3. SLO / burn-rate alert: services.get resolves the underlying resource structurally.
	if servicePath, ok := extractSLOServicePath(in.MetricType); ok {
		resourceType, labels, err := getMonitoringServiceResource(ctx, account, servicePath)
		if err != nil {
			logger.Warn("scope: services.get failed", "service", servicePath, "error", err)
		} else if resourceType != "" {
			return ResolvedScope{
				Project:        in.Project,
				ResourceType:   resourceType,
				ResourceLabels: labels,
				LogFilter:      buildResourceScopeFilter(resourceType, labels),
				Source:         "slo_service",
			}, nil
		}
	}

	// 4 & 5. Native log alert / generic fallback: scope by resource.type for the project.
	// Nothing above identified an instance, so this is the one place where a monitoring
	// resource type is used verbatim as a logging resource type — remap it where GCP's
	// two catalogs disagree, else the filter matches zero entries.
	if in.ResourceType != "" {
		source := "resource_type_fallback"
		if in.AlertType == "log" {
			source = "native_log"
		}
		resourceType := loggingResourceType(in.ResourceType)
		return ResolvedScope{
			Project:      in.Project,
			ResourceType: resourceType,
			LogFilter:    buildResourceScopeFilter(resourceType, nil),
			Source:       source,
		}, nil
	}

	return ResolvedScope{}, fmt.Errorf("scope: could not resolve scope for project %s (no resource type)", in.Project)
}

// ---- pure helpers (unit-tested) ----

// escapeLogFilterValue escapes a value interpolated into a double-quoted Cloud
// Logging filter string, so a stray quote/backslash can't break the query or inject
// filter clauses. (Values come from GCP, but defense-in-depth is cheap.)
func escapeLogFilterValue(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return v
}

// buildResourceScopeFilter builds the canonical Cloud Logging filter for a monitored resource.
// project_id is dropped (implied by the logadmin client's project scope).
func buildResourceScopeFilter(resourceType string, labels map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `resource.type="%s"`, escapeLogFilterValue(resourceType))
	for _, k := range sortedKeys(labels) {
		if k == "project_id" || labels[k] == "" {
			continue
		}
		fmt.Fprintf(&b, ` AND resource.labels.%s="%s"`, k, escapeLogFilterValue(labels[k]))
	}
	return b.String()
}

// monitoringToLoggingResourceType maps the monitored-resource types Cloud Monitoring
// alerts on to the resource type Cloud Logging actually writes under, for the types
// where GCP's two catalogs disagree. Only entries observed to produce empty evidence
// belong here — a type absent from the map is already correct in both catalogs, so
// adding a new resource type still needs no code.
var monitoringToLoggingResourceType = map[string]string{
	// HTTP(S) load balancers alert as https_lb_rule / l7_lb_rule; their request logs
	// are all written under http_load_balancer.
	"https_lb_rule": "http_load_balancer",
	"l7_lb_rule":    "http_load_balancer",
}

// loggingResourceType returns the Cloud Logging resource type for a monitored-resource
// type, unchanged when the two agree.
func loggingResourceType(monitoringType string) string {
	if mapped, ok := monitoringToLoggingResourceType[monitoringType]; ok {
		return mapped
	}
	return monitoringType
}

// pickLogMatchFilter returns the first log-match condition's filter on an alert policy.
// A native log alert ("condition_matched_log") carries exactly the Cloud Logging filter
// that made it fire; policies mixing condition kinds contribute only their log ones.
func pickLogMatchFilter(policy *monitoringpb.AlertPolicy) string {
	if policy == nil {
		return ""
	}
	for _, cond := range policy.GetConditions() {
		if lm := cond.GetConditionMatchedLog(); lm != nil {
			if f := strings.TrimSpace(lm.GetFilter()); f != "" {
				return f
			}
		}
	}
	return ""
}

// hasIdentifyingLabels reports whether the resource labels scope a specific instance
// (anything beyond project_id).
func hasIdentifyingLabels(labels map[string]string) bool {
	for k, v := range labels {
		if k != "project_id" && v != "" {
			return true
		}
	}
	return false
}

// extractLogMetricName pulls the user metric id from a log-based metric type.
func extractLogMetricName(metricType string) (string, bool) {
	const prefix = "logging.googleapis.com/user/"
	if strings.HasPrefix(metricType, prefix) {
		return strings.TrimPrefix(metricType, prefix), true
	}
	return "", false
}

// sloServiceRe captures the Monitoring service resource name embedded in an SLO
// expression, e.g. select_slo_burn_rate("projects/123/services/gae:proj_default/serviceLevelObjectives/...").
var sloServiceRe = regexp.MustCompile(`(projects/[^/"]+/services/[^/"]+)/serviceLevelObjectives`)

// extractSLOServicePath returns the `projects/N/services/<id>` name to hand to services.get.
func extractSLOServicePath(metricType string) (string, bool) {
	m := sloServiceRe.FindStringSubmatch(metricType)
	if len(m) == 2 {
		return m[1], true
	}
	return "", false
}

// mapMonitoringService maps a Monitoring Service's typed underlying resource to the
// (resource.type, resource.labels) used by Cloud Logging — the structured answer GCP
// gives us instead of parsing the opaque service id.
func mapMonitoringService(svc *monitoringpb.Service) (string, map[string]string) {
	if svc == nil {
		return "", nil
	}
	switch {
	case svc.GetAppEngine() != nil:
		return "gae_app", map[string]string{"module_id": svc.GetAppEngine().GetModuleId()}
	case svc.GetCloudRun() != nil:
		cr := svc.GetCloudRun()
		labels := map[string]string{"service_name": cr.GetServiceName()}
		if cr.GetLocation() != "" {
			labels["location"] = cr.GetLocation()
		}
		return "cloud_run_revision", labels
	case svc.GetClusterIstio() != nil:
		ci := svc.GetClusterIstio()
		return "k8s_container", map[string]string{
			"namespace_name": ci.GetServiceNamespace(),
			"cluster_name":   ci.GetClusterName(),
		}
	case svc.GetMeshIstio() != nil:
		mi := svc.GetMeshIstio()
		return "k8s_container", map[string]string{"namespace_name": mi.GetServiceNamespace()}
	default:
		return "", nil
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---- live GCP helpers (in-band, via the account's session) ----

// resolveLogMetricFilter fetches a log-based metric's filter, reusing the package's
// existing getLogMetricFilter (the same resolution the cloudsql path already uses).
func resolveLogMetricFilter(ctx providers.CloudProviderContext, account providers.Account, project, metricID string) (string, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return "", err
	}
	parent := project
	if parent == "" {
		parent = session.ProjectId
	}
	client, err := logadmin.NewClient(ctx.GetContext(), parent, session.Opts...)
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		return "", err
	}
	defer func() { _ = client.Close() }()

	return getLogMetricFilter(ctx, client, metricID)
}

// resolveLogAlertPolicyFilter fetches a native log alert's policy and returns the
// Cloud Logging filter it fires on (monitoring.alertPolicies.get — the same call the
// alarm updater already makes, so no additional permission is needed).
func resolveLogAlertPolicyFilter(ctx providers.CloudProviderContext, account providers.Account, policyName string) (string, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return "", err
	}
	client, err := monitoring.NewAlertPolicyClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		return "", err
	}
	defer func() { _ = client.Close() }()

	policy, err := client.GetAlertPolicy(ctx.GetContext(), &monitoringpb.GetAlertPolicyRequest{Name: policyName})
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		return "", err
	}
	return pickLogMatchFilter(policy), nil
}

// getMonitoringServiceResource resolves an SLO's Monitoring Service to its structured
// underlying resource (monitoring.services.get).
func getMonitoringServiceResource(ctx providers.CloudProviderContext, account providers.Account, servicePath string) (string, map[string]string, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return "", nil, err
	}
	client, err := monitoring.NewServiceMonitoringClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		return "", nil, err
	}
	defer func() { _ = client.Close() }()

	svc, err := client.GetService(ctx.GetContext(), &monitoringpb.GetServiceRequest{Name: servicePath})
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		return "", nil, err
	}
	resourceType, labels := mapMonitoringService(svc)
	return resourceType, labels, nil
}

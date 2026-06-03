package playbooks

import (
	"log/slog"
	"maps"
	"slices"
)

func init() {
	RegisterAction("k8s_pod_log_enricher", &podLogAction{})
	RegisterAction("k8s_resource", &k8sResourceAction{})
	RegisterAction("k8s_pod_metric_enricher", &podMetricAction{})
	RegisterAction("k8s_pod_cpu_metric_enricher", &podMetricAction{
		autodetectResource: "cpu",
	})
	RegisterAction("k8s_pod_memory_metric_enricher", &podMetricAction{
		autodetectResource: "memory",
	})
	RegisterAction("k8s_kubectl", &k8sKubectlAction{})
	RegisterAction("notification_channel_join", &notificationChannelJoinAction{})
	RegisterAction("notification_channel_message", &notificationChannelMessageAction{})
	RegisterAction("argocd_app_history", &argoCDHistoryAction{})
	RegisterAction("github_pr_history", &githubPRHistoryAction{})
	RegisterAction("deployment_history", &deploymentHistoryAction{})
	RegisterAction("datadog_monitors_search", &datadogMonitorsSearchAction{})
	RegisterAction("nubi_enricher", &nubiEnricherAction{})

	// Proxy agent (forager) actions
	RegisterAction("proxy_db_query", &proxyDBQueryAction{})
	RegisterAction("proxy_http_request", &proxyHTTPRequestAction{})
	RegisterAction("proxy_ssh_command", &proxySSHCommandAction{})

	// Stage 2.2 — server-side enricher chain for trigger-matched K8s events
	// (pod_oom_killer_enricher, report_crash_loop, image_pull_backoff_reporter,
	// job_failure, node_not_ready, Kubernetes Warning Event). Each composes
	// relay primitives (get_resource / prometheus_queries_enricher /
	// logs_enricher); the auto-discovery loop in eventrule/service.go invokes
	// any registered action whose CanAutoExecute predicate matches the event.
	RegisterAction("oom_killer_enricher", &oomKillerAction{})
	RegisterAction("noisy_neighbours_enricher", &noisyNeighboursAction{})
	RegisterAction("pod_node_metrics_enricher_memory", &podNodeMetricsAction{resourceType: "memory"})
	RegisterAction("pod_enricher", &podEnricherAction{})
	RegisterAction("resource_events_enricher", &resourceEventsAction{})
	RegisterAction("impacted_services_enricher", &impactedServicesAction{})
	RegisterAction("job_info_enricher", &jobInfoAction{})
	RegisterAction("job_events_enricher", &jobEventsAction{})
	RegisterAction("job_pod_enricher", &jobPodAction{})
	RegisterAction("node_allocatable_resources_enricher", &nodeAllocatableAction{})
	RegisterAction("node_running_pods_enricher", &nodeRunningPodsAction{})
	RegisterAction("node_status_enricher", &nodeStatusAction{})
	RegisterAction("event_resource_events_enricher", &eventResourceEventsAction{})
}

var actions = make(map[string]PlaybookAction)

func RegisterAction(name string, action PlaybookAction) {
	actions[name] = action
}

func GetAction(name string) (PlaybookAction, bool) {
	action, found := actions[name]
	return action, found
}

func ListActions() []string {
	keys := slices.Collect(maps.Keys(actions))
	slices.Sort(keys)
	return keys
}

type defaultPlaybookActionContext struct {
	accountId string
	logger    *slog.Logger
	event     PlaybookEvent
	tenantId  string
}

func (c *defaultPlaybookActionContext) GetAccountId() string {
	return c.accountId
}

func (c *defaultPlaybookActionContext) GetLogger() *slog.Logger {
	return c.logger
}

func (c *defaultPlaybookActionContext) GetEvent() PlaybookEvent {
	return c.event
}

func (c *defaultPlaybookActionContext) GetTenantId() string {
	return c.tenantId
}

func NewPlaybookActionContext(tenant, account string, logger *slog.Logger, event PlaybookEvent) PlaybookActionContext {
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("event_id", event.EventId, "account_id", account, "event_name", event.Name)
	return &defaultPlaybookActionContext{
		tenantId:  tenant,
		accountId: account,
		logger:    logger,
		event:     event,
	}
}

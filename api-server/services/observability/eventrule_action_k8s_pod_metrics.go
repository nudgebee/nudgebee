package observability

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"sort"

	"github.com/lib/pq"
	"k8s.io/apimachinery/pkg/api/resource"
	"strings"
)

// The action lives in observability rather than in playbooks because the
// metrics-provider lookup is here, and playbooks cannot import this package
// (this package imports playbooks in order to register actions). Same move as
// eventrule_action_noisy_neighbours.go. The three registrations and their
// configuration are carried over verbatim from playbooks/factory.go: the names
// are referenced by stored playbooks and by the auto-discovery allow-list in
// eventrule/service.go, so they must not change.
func init() {
	playbooks.RegisterAction("k8s_pod_metric_enricher", &podMetricAction{})
	playbooks.RegisterAction("k8s_pod_cpu_metric_enricher", &podMetricAction{
		autodetectResource: "cpu",
	})
	playbooks.RegisterAction("k8s_pod_memory_metric_enricher", &podMetricAction{
		autodetectResource: "memory",
	})
}

type podMetricAction struct {
	autodetectResource string
	// workloadResourceSpec resolves the authoritative container spec for the
	// workload backing a pod, from the collected k8s_workloads.meta: the container
	// NAMES, and which of memory-request / memory-limit / cpu-request are set.
	//
	// The names are what lets the metrics query stay per-container. Every
	// provider's utilisation query aggregates a pod to one series and scopes to a
	// single container only when asked by name, so the names have to be known
	// before querying rather than discovered from the result.
	//
	// Overridable in tests; nil means use the real DB-backed
	// resourcePresenceFromWorkloadSpec.
	workloadResourceSpec func(ctx playbooks.PlaybookActionContext, podName, namespace string) podWorkloadSpec
}

type podMetricData struct {
	Name         string           `json:"name"`
	Data         []podMetricEntry `json:"data"`
	ResourceType string           `json:"resource_type"`
}

type podMetricEntry struct {
	Metric     map[string]any `json:"metric"`
	Timestamps []float64      `json:"timestamps"`
	Values     []string       `json:"values"`
}

// VariantKey distinguishes the cpu and memory registrations of this action, which
// are the same type and so would otherwise collapse into one run under
// auto-discovery's per-implementation dedup — leaving OOM events with CPU
// evidence only.
// AgentOptional opts this action out of auto-discovery's "no agent, skip every
// k8s_* action" pre-flight. Its PromQL now goes through the metrics layer, which
// serves an account whose Prometheus is connected directly just as well as one
// with a cluster agent; without this the card stays invisible on exactly the
// accounts this migration was for.
func (a *podMetricAction) AgentOptional() bool { return true }

func (a *podMetricAction) VariantKey() string {
	return a.autodetectResource
}

func (a *podMetricAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	if a.autodetectResource == "" {
		return false
	}
	event := ctx.GetEvent()
	labels := event.Labels
	namespace := event.SubjectNamespace
	if namespace == "" && labels != nil {
		namespace = labels["namespace"]
	}
	if namespace == "" {
		return false
	}
	// Accept either a workload label on the event (Robusta-style Findings
	// carry labels.deployment/statefulset/daemonset/pod) OR the K8s subject
	// metadata the Go agent populates (subject_type=pod, with the owner
	// workload in subject_owner). SubjectName alone is not enough — cloud
	// events (e.g. AWS_EventBridge) carry an instance ID there.
	if labels != nil && (labels["deployment"] != "" || labels["statefulset"] != "" ||
		labels["daemonset"] != "" || labels["pod"] != "") {
		return true
	}
	return strings.EqualFold(event.SubjectType, "pod") && (event.SubjectOwner != "" || event.SubjectName != "")
}

func (a *podMetricAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	if a.autodetectResource == "" {
		return nil, errors.New("autodetect_resource is required")
	}

	labels := ctx.GetEvent().Labels
	namespace := ctx.GetEvent().SubjectNamespace
	if namespace == "" && labels != nil {
		namespace = labels["namespace"]
	}
	if namespace == "" {
		return nil, errors.New("namespace is required")
	}

	podName := podMetricTarget(ctx.GetEvent())
	if podName == "" {
		return nil, errors.New("no pod or workload label found")
	}

	params := map[string]any{
		"pod_name":      podName,
		"namespace":     namespace,
		"resource_type": a.autodetectResource,
	}
	return a.Execute(ctx, params)
}

// podMetricTarget picks the name the metrics are queried for: a workload name
// (matched as a prefix across its pods) or a single pod name (matched exactly).
//
// Workload labels win over the raw "pod" label: for deployment-level alerts
// (e.g. KubeDeploymentRolloutStuck) the "pod" label is the kube-state-metrics
// exporter pod, not the affected deployment's pods.
//
// Go-agent findings set no labels. When their subject is a pod, that pod is the
// target, not its owner: every provider's utilisation query wraps the matched
// series in sum(), so a prefix match on the owner reports the whole workload's
// usage and limits as one series. On an OOMKilled DaemonSet pod that read as a
// 4.6 GiB working set against an 8.4 GiB limit, for a container killed at 600 Mi.
// The owner is only the fallback when the subject names no pod.
func podMetricTarget(event playbooks.PlaybookEvent) string {
	labels := event.Labels
	if labels != nil {
		switch {
		case labels["deployment"] != "":
			return labels["deployment"]
		case labels["statefulset"] != "":
			return labels["statefulset"]
		case labels["daemonset"] != "":
			return labels["daemonset"]
		case labels["pod"] != "":
			return labels["pod"]
		}
	}
	if isPodSubjectTarget(event, event.SubjectName) {
		return event.SubjectName
	}
	if event.SubjectOwner != "" {
		return event.SubjectOwner
	}
	return event.SubjectName
}

// isPodSubjectTarget reports whether name is the event's own pod subject, chosen
// by podMetricTarget because no label identified a workload or pod. podMetricKind
// uses the same test, so a pod picked here is always matched exactly: a
// labels["kind"] set without a workload label would otherwise turn the full pod
// name into a prefix (pod=~"<pod>-.*") and match nothing.
func isPodSubjectTarget(event playbooks.PlaybookEvent, name string) bool {
	if name == "" || name != eventPodSubject(event) {
		return false
	}
	labels := event.Labels
	return labels == nil || (labels["deployment"] == "" && labels["statefulset"] == "" &&
		labels["daemonset"] == "" && labels["pod"] == "")
}

type podMetricEnricherParams struct {
	PodName      string `json:"pod_name,omitempty"`
	Namespace    string `json:"namespace,omitempty"`
	Duration     int    `json:"duration,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	Cluster      string `json:"cluster,omitempty"`
	ClusterLabel string `json:"cluster_label,omitempty"`
}

func (a *podMetricAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params podMetricEnricherParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	// Set defaults. Capture the user's original ResourceType BEFORE the
	// CPU fallback — generateInsights uses the user's intent (empty means
	// "no scope, emit all three"), while the prometheus query path
	// downstream needs a concrete resource type to build the query, so
	// it consumes the post-default value via params.ResourceType.
	userResourceType := params.ResourceType
	if params.Duration == 0 {
		params.Duration = 10
	}
	if params.ResourceType == "" {
		params.ResourceType = "CPU"
	}

	// The container spec is resolved once and used twice: for the container names
	// the metrics query needs, and for the authoritative request/limit presence the
	// insights need.
	spec := a.resolveWorkloadSpec(ctx, params)

	// Whether this invocation matches one pod exactly or a workload's pods by
	// prefix, expressed as the kind the utilisation builders understand.
	metricKind := podMetricKind(ctx, params)

	metricEntries, requestsMap, limitsMap, err := a.fetchContainerMetrics(ctx, params, metricKind, spec.containers)
	if err != nil {
		return nil, err
	}

	// Build the pod metric response structure
	podMetric, insights := a.buildPodMetricResponse(ctx, metricEntries, requestsMap, limitsMap, spec, params, userResourceType)

	additionalInfo := map[string]any{
		"title":              "pod_metric_enricher",
		"action_name":        "pod_metric_enricher",
		"actual_action_name": "pod_metric_enricher",
		"metric_name":        params.ResourceType,
		"pod_name":           params.PodName,
		"namespace":          params.Namespace,
		"cluster":            params.Cluster,
		"cluster_label":      params.ClusterLabel,
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}

	return playbooks.NewPlaybookActionResponseJson(podMetric, additionalInfo, insights, metadata), nil
}

// podWorkloadSpec is the collected container spec for the workload backing a pod:
// the container names, and which resources the spec sets. Resolved once per
// invocation because both the metrics query and the insights need it.
type podWorkloadSpec struct {
	found      bool
	containers []string
	hasMemReq  bool
	hasMemLim  bool
	hasCpuReq  bool
	// requests / limits are the spec's quantities per container, in the same
	// {container: {cpu|memory: value}} shape the metrics path records (bytes,
	// cores). They fill in whatever the metrics backend did not return — which on
	// an OOMKilled Prometheus is everything, because the backend is the pod that
	// died.
	requests map[string]map[string]float64
	limits   map[string]map[string]float64
}

func (a *podMetricAction) resolveWorkloadSpec(ctx playbooks.PlaybookActionContext, params podMetricEnricherParams) podWorkloadSpec {
	lookup := a.workloadResourceSpec
	if lookup == nil {
		lookup = a.resourcePresenceFromWorkloadSpec
	}
	return lookup(ctx, params.PodName, params.Namespace)
}

// workloadKindForUtilisation maps the event's workload kind onto the vocabulary
// the utilisation builders understand. They only branch on "pod" vs everything
// else, so an unrecognised kind is safely reported as a deployment.
func workloadKindForUtilisation(kind string) string {
	switch strings.ToLower(kind) {
	case "statefulset", "daemonset", "pod":
		return strings.ToLower(kind)
	default:
		return "deployment"
	}
}

// podMetricKind decides whether this invocation matches a single pod exactly or
// a workload's pods by name prefix, and returns that decision in the vocabulary
// the utilisation builders consume: "pod" is an exact pod= match, anything else
// is pod=~"<name>-.*".
//
// Extracted from Execute so the decision can be asserted directly: an exact
// match on a workload name and a workload that genuinely has no data produce
// byte-identical output, which is how the empty-card bug survived review.
func podMetricKind(ctx playbooks.PlaybookActionContext, params podMetricEnricherParams) string {
	if isPodSubjectTarget(ctx.GetEvent(), params.PodName) {
		return "pod"
	}
	// Use regex match for workload names (Deployment, StatefulSet, DaemonSet,
	// ReplicaSet, Job, ReplicationController) since their pod names have a
	// random suffix appended by the workload controller. StatefulSet was
	// previously missing here and broke OOMKilled investigations for any
	// StatefulSet-owned pod (e.g. vmsingle-*): the workload-name exact match
	// `pod="vmsingle-victoria-..."` finds zero series and the action returns
	// false-negative "Pod X does not have memory limit specified" insights.
	//
	// We check both the "kind" label (set by matchWorkloadAndEnrich) and the
	// per-workload labels (deployment / statefulset / daemonset / job) which
	// are present on alerts like KubeDeploymentRolloutStuck where the "kind"
	// label is unset.
	kind := ""
	if ctx.GetEvent().Labels != nil {
		kind = ctx.GetEvent().Labels["kind"]
	}
	if kind == "" {
		kind = ctx.GetEvent().SubjectType
	}
	useRegex := strings.EqualFold(kind, "deployment") ||
		strings.EqualFold(kind, "daemonset") ||
		strings.EqualFold(kind, "replicaset") ||
		strings.EqualFold(kind, "statefulset") ||
		strings.EqualFold(kind, "job") ||
		strings.EqualFold(kind, "replicationcontroller")
	// A workload label only asks for a prefix match when it IS the name being
	// queried. When the target is the raw "pod" label, a sibling label names
	// something else: "job" is Prometheus's scrape job on nearly every alert
	// (job="kube-state-metrics"), and a prefix on the pod's full name
	// (pod=~"<pod>-.*") matches nothing.
	if !useRegex && ctx.GetEvent().Labels != nil {
		for _, key := range []string{"deployment", "statefulset", "daemonset", "job", "replicaset"} {
			if value := ctx.GetEvent().Labels[key]; value != "" && value == params.PodName {
				useRegex = true
				break
			}
		}
	}
	// Implicit workload-name signal: AutoExecute resolves `pod_name` to
	// `SubjectOwner` (the owner workload, e.g. the StatefulSet name) when
	// no per-workload label is present on the event. Go-agent findings for
	// `pod_oom_killer_enricher` follow this path. SubjectName then holds
	// the actual pod (`<workload>-<hash>-<suffix>`), so a divergence
	// between params.PodName and SubjectName is a reliable signal that we
	// need regex matching, regardless of how SubjectOwnerKind is labelled.
	if !useRegex && params.PodName != "" && ctx.GetEvent().SubjectName != "" &&
		params.PodName != ctx.GetEvent().SubjectName &&
		strings.HasPrefix(ctx.GetEvent().SubjectName, params.PodName+"-") {
		useRegex = true
	}

	// The utilisation builders distinguish only "pod" (an exact pod= match) from a
	// workload kind (pod=~"<name>-.*"), so the regex decision collapses to which of
	// the two we ask for. params.Cluster / ClusterLabel are no longer consumed
	// here: the metrics layer owns cluster scoping now, expanding __CLUSTER__ for
	// the agent and pinning the integration's own labels for a direct connection.
	//
	// `kind` above falls back to SubjectType, which is "pod" on every agent-sourced
	// pod event -- and "pod" is the one value that makes the builder emit an exact
	// pod= match, silently undoing the regex decision we just made. On a
	// pod_oom_killer_enricher event params.PodName is the WORKLOAD name (AutoExecute
	// resolves it from SubjectOwner), so an exact pod="<workload>" match finds zero
	// series and the card renders empty. Ask the workload's own kind instead.
	metricKind := "pod"
	if useRegex {
		metricKind = workloadKindForUtilisation(regexWorkloadKind(ctx, kind))
	}

	return metricKind
}

// workloadNameMatch resolves a k8s_workloads row from the name the action was
// handed ($3), which is a POD name on alert-driven events and the WORKLOAD name
// itself on agent-driven ones (AutoExecute takes it from SubjectOwner).
//
// Both arms are load-bearing. Prefix-only was the original form and silently
// failed the agent case, because "report-worker" does not satisfy
// `LIKE 'report-worker-%'`; equality-only would fail every pod name. Paired with
// ORDER BY length(name) DESC the ambiguous case resolves correctly: for pod
// "foo-bar" against workloads "foo" and "foo-bar", both arms match and the exact
// row wins, since an exact match is always at least as long as a prefix match.
//
// Kept as a named constant so TestWorkloadNameMatch_DB can execute this exact
// text against a throwaway table. The semantics are the whole fix and there is no
// way to assert them without a real query planner.
const workloadNameMatch = `($3 = name OR $3 LIKE name || '-%')`

// regexWorkloadKind resolves the kind of the WORKLOAD whose pods are about to be
// matched by name prefix. It is only consulted once the caller has decided on a
// prefix match, which makes "pod" the one answer that is never right: it is the
// value that turns the builder's matcher back into an exact pod= comparison.
//
// `kind` arrives as labels["kind"] falling back to SubjectType. Alert-driven
// events set the label to a real workload kind and are returned unchanged.
// Agent-driven pod events (pod_oom_killer_enricher, crash loop) set neither, so
// SubjectType supplies "pod" -- for those, SubjectOwnerKind holds the controller
// kind and is the authoritative answer.
//
// Falling back to "" rather than "pod" is deliberate: workloadKindForUtilisation
// maps it to "deployment", the generic <name>-.* prefix form, which is what the
// prefix decision asked for. Returning "pod" would silently cancel it.
func regexWorkloadKind(ctx playbooks.PlaybookActionContext, kind string) string {
	if kind != "" && !strings.EqualFold(kind, "pod") {
		return kind
	}
	if ownerKind := ctx.GetEvent().SubjectOwnerKind; ownerKind != "" {
		return ownerKind
	}
	return ""
}

// podMetricUsageKey is the abstract utilisation metric for the resource this
// invocation collects. Every provider's builder implements both.
func podMetricUsageKey(resourceType string) string {
	if strings.EqualFold(resourceType, "CPU") {
		return "cpu_usage"
	}
	return "memory_usage"
}

// podMetricSpecKeys are always fetched for both resources: the card renders the
// request and limit lines for cpu and memory regardless of which usage series it
// is drawing, and the allocation insights need all of them.
var podMetricSpecKeys = []string{"cpu_request", "cpu_limit", "memory_request", "memory_limit"}

// podMetricUtilisationRequest builds the request the utilisation layer reads. An
// empty container asks for the pod as a whole; `kind` decides whether the pod
// name is matched exactly or as a workload prefix.
func podMetricUtilisationRequest(params podMetricEnricherParams, kind, container, usageKey string) map[string]any {
	request := map[string]any{
		"workload_namespace": params.Namespace,
		"workload_name":      params.PodName,
		"kind":               kind,
		"metrics":            append([]string{usageKey}, podMetricSpecKeys...),
	}
	if container != "" {
		request["container_name"] = container
	}
	return request
}

// fetchContainerMetrics reads the pod's usage, requests and limits from whichever
// metrics backend the account has configured, one container at a time.
//
// One call per container is what keeps this card per-container. Every provider's
// utilisation query aggregates a pod to a single series -- the Prometheus builder
// wraps everything in sum() with no `by (container)` -- and scopes to one
// container only when asked for it by name. The names come from the collected
// workload spec, because an aggregated result carries no labels to recover them
// from.
//
// Everything is a range query, including requests and limits: the agent's instant
// batch comes back empty at event-processing time while the range path populates
// (the same finding recorded on the noisy-neighbours action), so the spec values
// are read as the last sample of their series.
func (a *podMetricAction) fetchContainerMetrics(ctx playbooks.PlaybookActionContext, params podMetricEnricherParams, kind string, containers []string) ([]podMetricEntry, map[string]map[string]float64, map[string]map[string]float64, error) {
	tenantID, accountID := ctx.GetTenantId(), ctx.GetAccountId()
	if tenantID == "" || accountID == "" {
		return nil, nil, nil, errors.New("k8s_pod_metric_enricher: tenant ID and account ID required")
	}
	requestCtx := security.NewRequestContextForTenantAdmin(tenantID, ctx.GetLogger(), nil, nil)

	startTime, endTime := ctx.GetEvent().ResolveQueryWindow(params.Duration)
	usageKey := podMetricUsageKey(params.ResourceType)

	entries := []podMetricEntry{}
	requestsMap := map[string]map[string]float64{}
	limitsMap := map[string]map[string]float64{}

	// No collected spec means no container names. Ask for the pod as a whole and
	// render the one aggregated series rather than nothing -- but do NOT record
	// requests/limits from it: those queries sum across containers, so the total
	// would be presented as a per-container value and would drive false "missing
	// resource" insights. An absent value is correctly read as "unknown".
	scopes := containers
	aggregated := len(scopes) == 0
	if aggregated {
		scopes = []string{""}
	}

	for _, container := range scopes {
		output, err := FetchMetricUtilisation(requestCtx, GetUtilisationTrendRequest{
			AccountId: accountID,
			StartTime: startTime.UnixMilli(),
			EndTime:   endTime.UnixMilli(),
			// The card is drawn over a ten-minute incident window; the layer's
			// default would spread ~60 points across it with a 60s floor.
			StepInterval: podMetricStepSeconds,
			Request:      podMetricUtilisationRequest(params, kind, container, usageKey),
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("k8s_pod_metric_enricher: metrics: %w", err)
		}

		for _, result := range output.Results {
			if result.Error != nil {
				// The metrics layer reports a failed query per key rather than as an
				// error, so without this a backend outage is indistinguishable from
				// "this pod has no data" — an empty card and nothing to explain it.
				ctx.GetLogger().Warn("pod_metrics_enricher: metrics query failed",
					"query_key", result.QueryKey, "container", container,
					"pod", params.PodName, "namespace", params.Namespace, "error", *result.Error)
				continue
			}
			switch UtilisationKey(result.QueryKey) {
			case UtilisationKey(usageKey):
				entries = append(entries, podUsageEntries(result, container, params)...)
			case KeyCPURequest, KeyMemoryRequest:
				if !aggregated {
					recordSpecValue(requestsMap, container, result)
				}
			case KeyCPULimit, KeyMemoryLimit:
				if !aggregated {
					recordSpecValue(limitsMap, container, result)
				}
			}
		}
	}

	return entries, requestsMap, limitsMap, nil
}

// podMetricStepSeconds matches the resolution the relay facade was asked for
// before this card moved onto the metrics layer.
const podMetricStepSeconds = 30

// podUsageEntries converts one query result into card entries, stamping the
// labels the card renders. They have to be synthesized from what was asked for:
// the provider queries aggregate, so the series come back with no labels at all
// (the Elasticsearch path stamps its own labels for the same reason). The `job`
// label the relay used to carry is not recoverable and is dropped -- nothing
// reads it.
func podUsageEntries(result QueryResult, container string, params podMetricEnricherParams) []podMetricEntry {
	entries := []podMetricEntry{}
	for _, series := range result.Payload {
		if len(series.Values) == 0 {
			// A series with no samples renders as nothing; leaving it out lets the
			// requests/limits seeding below produce the entry instead.
			continue
		}
		// Float seconds and value strings, matching promQLTimestamp / promQLValue —
		// the same encoding every persisted Prometheus card carries.
		timestamps := make([]float64, 0, len(series.Timestamps))
		for i := range series.Timestamps {
			timestamps = append(timestamps, promQLTimestamp(series, i))
		}
		values := make([]string, 0, len(series.Values))
		for _, value := range series.Values {
			values = append(values, promQLValue(value))
		}
		entries = append(entries, podMetricEntry{
			Metric:     podSeriesLabels(container, params),
			Timestamps: timestamps,
			Values:     values,
		})
	}
	return entries
}

func podSeriesLabels(container string, params podMetricEnricherParams) map[string]any {
	labels := map[string]any{
		"pod":       params.PodName,
		"namespace": params.Namespace,
	}
	if container != "" {
		labels["container"] = container
	}
	return labels
}

// recordSpecValue stores the latest sample of a request/limit series under
// {container: {cpu|memory: value}} -- the shape the card and the insights join on.
func recordSpecValue(into map[string]map[string]float64, container string, result QueryResult) {
	resource := "cpu"
	if strings.HasPrefix(result.QueryKey, "memory") {
		resource = "memory"
	}
	for _, series := range result.Payload {
		if len(series.Values) == 0 {
			continue
		}
		if into[container] == nil {
			into[container] = map[string]float64{}
		}
		into[container][resource] = series.Values[len(series.Values)-1]
		break
	}
}

// buildPodMetricResponse builds the response payload and generates
// resource-allocation insights. insightResourceType controls insight scoping
// — see generateInsights for the semantics. The caller passes the user's
// ORIGINAL resource_type (pre-default), which preserves "emit all three"
// for callers that didn't ask for per-resource scoping.
func (a *podMetricAction) buildPodMetricResponse(ctx playbooks.PlaybookActionContext, metricEntries []podMetricEntry, requestsMap, limitsMap map[string]map[string]float64, spec podWorkloadSpec, params podMetricEnricherParams, insightResourceType string) (podMetricData, []playbooks.PlaybookActionResponseInsight) {
	insights := []playbooks.PlaybookActionResponseInsight{}

	// Debug: check if we got any requests/limits data
	ctx.GetLogger().Debug("pod_metrics_enricher: processing requests and limits",
		"requests_found", len(requestsMap),
		"limits_found", len(limitsMap),
		"pod", params.PodName,
		"namespace", params.Namespace)

	// Attach requests and limits. The producer already stamped the label set the
	// card renders (container / pod / namespace), so there is nothing to filter
	// down any more.
	for i := range metricEntries {
		metricEntries[i].Metric["requests"] = a.getResourceValues(requestsMap, spec.requests, metricEntries[i].Metric)
		metricEntries[i].Metric["limits"] = a.getResourceValues(limitsMap, spec.limits, metricEntries[i].Metric)
	}

	// Seed an entry for every known container that returned no usage series, so
	// the card and the tune form list it with its requests/limits. Typical for an
	// OOMKilled container — and when the pod that died is the metrics backend
	// itself, every query for it fails. Legacy Robusta read requests/limits
	// straight off the K8s pod spec (oom_killer.py via
	// pod.spec.containers[].resources) for exactly this case.
	//
	// Per container, not only when every container came back empty: a sidecar
	// with usage used to hide the container that was actually killed, and the
	// tune form then proposed the sidecar's numbers for the pod.
	a.seedContainersWithoutUsage(&metricEntries, requestsMap, limitsMap, spec, params)

	// Scope insight emission to the resource_type being collected. Both
	// the cpu and memory enricher invocations call generateInsights — if
	// each emits all three (memory_limit / memory_request / cpu_request),
	// the user sees 6 insights instead of 3. Scoping each call to its own
	// resource_type keeps the union at exactly three unique insights.
	//
	// Pass the user's ORIGINAL resource_type (pre-default) so callers that
	// drive Execute manually without specifying it keep the legacy
	// "emit all three" behaviour. The defaulted value above is only used
	// by the prometheus query builder, which requires a concrete type.
	// Decide the resource-allocation insights from the AUTHORITATIVE workload
	// spec (k8s_workloads.meta) when available — it is collected from the K8s API
	// and is correct even when the event names a pod/ReplicaSet that has since
	// rolled away (the common webhook-alert case). Only when no workload spec is
	// found do we fall back to the Prometheus series, and then only if the pod is
	// actually observable in metrics: an empty kube_pod_container_resource_*
	// result is otherwise ambiguous ("unset" vs "pod not found") and was
	// previously misread as "unset", fabricating false High/Critical "missing
	// resource" findings that poison downstream RCA.
	if spec.found {
		// Reuse generateInsights by encoding the spec's presence as a synthetic
		// single-container map (value is a sentinel — only key presence matters).
		const specContainer = "workload-spec"
		specReq := map[string]float64{}
		if spec.hasMemReq {
			specReq["memory"] = 1
		}
		if spec.hasCpuReq {
			specReq["cpu"] = 1
		}
		specLim := map[string]float64{}
		if spec.hasMemLim {
			specLim["memory"] = 1
		}
		a.generateInsights(
			map[string]map[string]float64{specContainer: specReq},
			map[string]map[string]float64{specContainer: specLim},
			params.PodName, insightResourceType, &insights)
	} else if len(spec.containers) > 0 && (len(metricEntries) > 0 || len(requestsMap) > 0 || len(limitsMap) > 0) {
		// Reading an empty requests/limits map as "unset" is only sound when those
		// values were actually collected per container. They are not on the
		// aggregated path (no container names to scope by), where the maps are
		// empty by construction -- so this branch is gated on having had container
		// names at all. Without them the resources are UNKNOWN, not absent, and
		// asserting otherwise is what fabricated the High/Critical "missing
		// resource" findings this whole spec lookup exists to prevent.
		a.generateInsights(requestsMap, limitsMap, params.PodName, insightResourceType, &insights)
	} else {
		ctx.GetLogger().Debug("pod_metrics_enricher: no per-container resource spec available; skipping resource-allocation insights to avoid false 'missing resource' findings",
			"pod", params.PodName, "namespace", params.Namespace,
			"spec_found", spec.found, "containers", len(spec.containers))
	}

	// Characterise the shape of the memory curve. The resource-allocation
	// insights above answer "is a limit set"; this answers "would raising it
	// help", which is the question an OOMKill actually poses and the one the
	// event itself cannot answer.
	insights = append(insights, memoryTrendInsights(metricEntries, params.ResourceType)...)

	podMetric := podMetricData{
		Name:         "pod_metric",
		Data:         metricEntries,
		ResourceType: params.ResourceType,
	}

	return podMetric, insights
}

// seedContainersWithoutUsage appends an entry with no samples for each container
// that has spec or requests/limits data but no usage series. Order follows the
// workload spec, then any container only the metrics knew about.
func (a *podMetricAction) seedContainersWithoutUsage(entries *[]podMetricEntry, requestsMap, limitsMap map[string]map[string]float64, spec podWorkloadSpec, params podMetricEnricherParams) {
	seen := map[string]bool{}
	for _, entry := range *entries {
		if c, ok := entry.Metric["container"].(string); ok {
			seen[c] = true
		}
	}
	candidates := append([]string{}, spec.containers...)
	for _, m := range []map[string]map[string]float64{requestsMap, limitsMap} {
		extra := make([]string, 0, len(m))
		for c := range m {
			extra = append(extra, c)
		}
		sort.Strings(extra)
		candidates = append(candidates, extra...)
	}
	for _, container := range candidates {
		if container == "" || seen[container] {
			continue
		}
		seen[container] = true
		metric := map[string]any{
			"container": container,
			"pod":       params.PodName,
			"namespace": params.Namespace,
		}
		metric["requests"] = a.getResourceValues(requestsMap, spec.requests, metric)
		metric["limits"] = a.getResourceValues(limitsMap, spec.limits, metric)
		*entries = append(*entries, podMetricEntry{
			Metric:     metric,
			Timestamps: []float64{},
			Values:     []string{},
		})
	}
}

// getResourceValues returns the container's {cpu, memory} values from the
// metrics, falling back per resource to the workload spec. 0 means neither
// source had it.
func (a *podMetricAction) getResourceValues(resourceMap, specMap map[string]map[string]float64, metric map[string]any) map[string]any {
	result := map[string]any{
		"cpu":    0,
		"memory": 0,
	}

	container := ""
	if c, ok := metric["container"].(string); ok {
		container = c
	}

	for _, source := range []map[string]map[string]float64{specMap, resourceMap} {
		containerResources, ok := source[container]
		if !ok {
			continue
		}
		if cpu, ok := containerResources["cpu"]; ok {
			result["cpu"] = cpu
		}
		if memory, ok := containerResources["memory"]; ok {
			result["memory"] = memory
		}
	}

	return result
}

// resourcePresenceFromWorkloadSpec reads the authoritative container resource
// spec from the collected k8s_workloads.meta for the workload backing podName,
// reporting which of {memory request, memory limit, cpu request} are set.
//
// This is the source of truth — collected from the K8s API — unlike the
// presence/absence of kube_pod_container_resource_* Prometheus series, which is
// empty whenever the pod isn't currently observable (rolled away, stale/
// ReplicaSet-name identity from a webhook alert, scrape gap, KSM not scraped).
// An empty metric result was previously misread as "resource unset", fabricating
// false High/Critical "missing resource" findings for pods that actually have
// them. The workload is matched by longest-prefix on its name, so it resolves
// correctly even after a rollout changed the pod/ReplicaSet hash.
//
// found is false when no active workload row matches (or it has no containers in
// meta); the caller then falls back to the Prometheus-observable heuristic.
func (a *podMetricAction) resourcePresenceFromWorkloadSpec(ctx playbooks.PlaybookActionContext, podName, namespace string) podWorkloadSpec {
	if podName == "" || namespace == "" {
		return podWorkloadSpec{}
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Warn("pod_metrics_enricher: workload spec lookup unavailable; falling back to metric-presence heuristic", "error", err)
		return podWorkloadSpec{}
	}

	// Resolve the workload by longest-prefix match on its name (pod/RS names are
	// "<workload>-<hash>[-<suffix>]"), then OR the resource presence across its
	// containers — mirroring the cross-container OR the Prometheus path uses.
	// The container names come back alongside the presence flags: the metrics
	// query needs them to stay per-container, and this is the same row.
	//
	// The exact-equality arm matters as much as the prefix one: podName is not
	// always a pod name. AutoExecute resolves it from SubjectOwner — the workload
	// name itself — for every agent-sourced pod event, and "report-worker" does not
	// satisfy `LIKE 'report-worker-%'`. Prefix-only meant those events found no
	// spec, so the card lost its per-container split, discarded the requests and
	// limits it had already fetched, and emitted no allocation insights.
	//
	// ORDER BY length(name) DESC still resolves the ambiguous case correctly: for
	// pod "foo-bar" against workloads "foo" and "foo-bar", both arms match and the
	// longer, exact one wins.
	const query = `
		WITH w AS (
			SELECT name, meta
			FROM k8s_workloads
			WHERE cloud_account_id = $1
			  AND namespace = $2
			  AND ` + workloadNameMatch + `
			  AND is_active IS NOT FALSE
			ORDER BY length(name) DESC
			LIMIT 1
		)
		SELECT
			count(c) > 0 AS found,
			coalesce(array_agg(c->>'name') FILTER (WHERE c->>'name' IS NOT NULL), '{}') AS container_names,
			coalesce(bool_or(coalesce((c->'resources'->'requests') ? 'memory', false)), false) AS has_mem_req,
			coalesce(bool_or(coalesce((c->'resources'->'limits')   ? 'memory', false)), false) AS has_mem_lim,
			coalesce(bool_or(coalesce((c->'resources'->'requests') ? 'cpu', false)),    false) AS has_cpu_req,
			coalesce(jsonb_object_agg(c->>'name', coalesce(c->'resources', '{}'::jsonb)) FILTER (WHERE c->>'name' IS NOT NULL), '{}'::jsonb) AS resources
		FROM w CROSS JOIN LATERAL jsonb_array_elements(coalesce(w.meta->'config'->'containers', '[]'::jsonb)) c`

	var spec podWorkloadSpec
	var names pq.StringArray
	var resources []byte
	if err := dbms.Db.QueryRowx(query, ctx.GetAccountId(), namespace, podName).Scan(&spec.found, &names, &spec.hasMemReq, &spec.hasMemLim, &spec.hasCpuReq, &resources); err != nil {
		ctx.GetLogger().Warn("pod_metrics_enricher: workload spec lookup failed; falling back to metric-presence heuristic",
			"error", err, "pod", podName, "namespace", namespace)
		return podWorkloadSpec{}
	}
	if !spec.found {
		// No matching workload, or it carries no container spec — no authoritative
		// answer; let the caller fall back rather than assert "unset".
		return podWorkloadSpec{}
	}
	// `found` deliberately tracks whether the spec exists, NOT whether names came
	// back. A container without a name would only cost us the per-container split;
	// treating that as "no spec" would hand the insights back to the metric-presence
	// heuristic, which is what fabricated false "missing resource" findings.
	spec.containers = []string(names)
	spec.requests, spec.limits = parseSpecResources(resources)
	return spec
}

// parseSpecResources turns {container: {requests: {cpu: "200m"}, limits: {memory: "8Gi"}}}
// into bytes / cores keyed like the metrics maps. Unparseable quantities are
// skipped: a missing value falls back to "unknown", a wrong one would be shown.
func parseSpecResources(raw []byte) (requests, limits map[string]map[string]float64) {
	requests, limits = map[string]map[string]float64{}, map[string]map[string]float64{}
	var byContainer map[string]struct {
		Requests map[string]any `json:"requests"`
		Limits   map[string]any `json:"limits"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &byContainer) != nil {
		return requests, limits
	}
	fill := func(into map[string]map[string]float64, container string, quantities map[string]any) {
		for _, name := range []string{"cpu", "memory"} {
			value, ok := quantities[name]
			if !ok || value == nil {
				continue
			}
			q, err := resource.ParseQuantity(fmt.Sprint(value))
			if err != nil {
				continue
			}
			if into[container] == nil {
				into[container] = map[string]float64{}
			}
			into[container][name] = q.AsApproximateFloat64()
		}
	}
	for container, r := range byContainer {
		fill(requests, container, r.Requests)
		fill(limits, container, r.Limits)
	}
	return requests, limits
}

// generateInsights emits resource-allocation insights scoped by resourceType
// so that the cpu and memory enricher invocations together produce the union
// of three unique insights (memory_limit, memory_request, cpu_request) — never
// six. Per-call emission:
//   - resourceType="memory" → up to 2 insights (memory_limit, memory_request)
//   - resourceType="cpu"    → up to 1 insight  (cpu_request)
//   - resourceType=""       → up to 3 insights (legacy / direct Execute callers
//     that haven't opted into per-resource scoping)
//
// "Up to" because each insight only fires when the corresponding resource is
// unset on the pod; a fully-specified pod emits zero insights regardless of
// resourceType.
func (a *podMetricAction) generateInsights(requestsMap, limitsMap map[string]map[string]float64, podName, resourceType string, insights *[]playbooks.PlaybookActionResponseInsight) {
	// Track if we found any requests or limits
	hasMemoryRequest := false
	hasMemoryLimit := false
	hasCpuRequest := false

	// Check all containers for requests and limits
	for container, resources := range requestsMap {
		if container == "" {
			continue
		}
		if _, ok := resources["memory"]; ok {
			hasMemoryRequest = true
		}
		if _, ok := resources["cpu"]; ok {
			hasCpuRequest = true
		}
	}

	for container, resources := range limitsMap {
		if container == "" {
			continue
		}
		if _, ok := resources["memory"]; ok {
			hasMemoryLimit = true
		}
	}

	rt := strings.ToLower(resourceType)
	emitMemory := rt == "" || rt == "memory"
	emitCPU := rt == "" || rt == "cpu"

	// Generate insights based on missing requests and limits
	if emitMemory && !hasMemoryLimit {
		*insights = append(*insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Pod %s does not have memory limit specified", podName),
			Severity: "High",
		})
	}
	if emitMemory && !hasMemoryRequest {
		*insights = append(*insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Pod %s does not have memory request specified", podName),
			Severity: "Critical",
		})
	}
	if emitCPU && !hasCpuRequest {
		*insights = append(*insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Pod %s does not have CPU request specified", podName),
			Severity: "High",
		})
	}
	// Note: CPU limits are intentionally not checked as they can cause throttling and are often omitted as best practice
}

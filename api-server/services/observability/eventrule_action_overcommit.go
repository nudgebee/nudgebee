package observability

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/security"
)

// The cluster over-commitment enrichers answer the question a
// KubeCPUOvercommit / KubeMemoryOvercommit alert asks and never shows: the
// cluster has promised more CPU (or memory) to pods than it can deliver — by
// how much, and which namespaces are holding the reservations?
//
// Until now these four action names existed as `agent_playbook` rows and
// `event_actions_template.json` entries with no implementation behind them —
// not here and not in the agent — so the alert arrived carrying its own labels
// and a single graph, and nothing else. Registering under the SAME names the
// rows already reference turns those dead rows live; no migration needed.
//
// A tenant on a legacy agent that still emits its own card is not
// double-rendered: executeAutoActions skips a server action whose name already
// appears in the event's evidence.
func init() {
	playbooks.RegisterAction("cpu_overcommited_enricher", &overcommitAction{resource: "cpu"})
	playbooks.RegisterAction("memory_overcommited_enricher", &overcommitAction{resource: "memory"})
	playbooks.RegisterAction("cluster_cpu_requests_enricher", &overcommitAction{resource: "cpu", byNamespace: true})
	playbooks.RegisterAction("cluster_memory_requests_enricher", &overcommitAction{resource: "memory", byNamespace: true})
}

// overcommitAggKeys are the alerts these cards answer. Over-commitment is a
// cluster-capacity statement, so the trigger is the capacity alert itself
// rather than a subject shape — there is no object to key off.
var overcommitAggKeys = map[string]string{
	"KubeCPUOvercommit":         "cpu",
	"KubeMemoryOvercommit":      "memory",
	"KubeCPUQuotaOvercommit":    "cpu",
	"KubeMemoryQuotaOvercommit": "memory",
}

// overcommitNamespaceRows caps the breakdown. The question is who holds the
// reservations, which the top few answer; a 200-namespace table is a wall.
const overcommitNamespaceRows = 15

type overcommitAction struct {
	resource    string // "cpu" | "memory"
	byNamespace bool
}

// VariantKey keeps the four registrations distinct. Without it auto-discovery
// dedups by implementation type and only the first would ever run.
func (a *overcommitAction) VariantKey() string {
	if a.byNamespace {
		return a.resource + "_by_namespace"
	}
	return a.resource + "_summary"
}

func (a *overcommitAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	// Only on the matching resource's alert: a CPU over-commitment alert gets
	// the CPU cards, not both.
	return overcommitAggKeys[ctx.GetEvent().AggregationKey] == a.resource
}

func (a *overcommitAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	return a.Execute(ctx, map[string]any{})
}

func (a *overcommitAction) Execute(ctx playbooks.PlaybookActionContext, _ map[string]any) (playbooks.PlaybookActionResponse, error) {
	if a.resource != "cpu" && a.resource != "memory" {
		return nil, fmt.Errorf("overcommit_enricher: unsupported resource %q", a.resource)
	}
	tenantID, accountID := ctx.GetTenantId(), ctx.GetAccountId()
	if tenantID == "" || accountID == "" {
		return nil, errors.New("overcommit_enricher: tenant ID and account ID required")
	}
	requestCtx := security.NewRequestContextForTenantAdmin(tenantID, ctx.GetLogger(), nil, nil)

	// These numbers have to come from the same place the alert's own rule read
	// them, or the card will disagree with the alert that opened it. Both are
	// kube-state-metrics series, which is what the mixin rule aggregates.
	provider, _, provErr := GetLogsMetricsTracesProvider(requestCtx, accountID, "", "metrics", "")
	if provErr != nil {
		return nil, fmt.Errorf("overcommit_enricher: metrics provider: %w", provErr)
	}
	if provider != integrationPrometheus {
		// kube_* series are Prometheus-shaped. Saying so beats an empty card.
		return nil, fmt.Errorf("overcommit_enricher: needs prometheus metrics, account uses %q", provider)
	}

	start, end := playbooks.RangeQueryWindow(ctx.GetEvent(), overcommitLookbackMinutes, time.Now().UTC())
	output, err := FetchMetricsQuery(requestCtx, FetchMetricsRequest{
		AccountId:      accountID,
		MetricProvider: integrationPrometheus,
		Queries:        a.queries(),
		StartTime:      start.UnixMilli(),
		EndTime:        end.UnixMilli(),
		StepInterval:   60,
	})
	if err != nil {
		return nil, fmt.Errorf("overcommit_enricher: prom: %w", err)
	}
	results := latestValuesByKey(output)

	allocatable := singleValue(results["allocatable"])
	requests := singleValue(results["requests"])
	if allocatable == 0 && requests == 0 {
		// Nothing to say, and an empty table reads as "no over-commitment",
		// which is the opposite of what fired.
		return nil, errors.New("overcommit_enricher: kube-state-metrics returned no capacity or request series")
	}

	if a.byNamespace {
		return a.namespaceTable(results["by_namespace"], allocatable)
	}
	return a.summaryTable(allocatable, requests,
		singleValue(results["largest_node"]), singleValue(results["node_count"]), singleValue(results["requests_pods"]))
}

const overcommitLookbackMinutes = 10

// queries builds the PromQL this action reads. Every one is scoped to this
// action's own resource: a container can set a CPU request and no memory
// request, so counting CPU requests on a memory alert reports a number about a
// different thing while looking authoritative.
//
// Split out from Execute so the per-resource scoping is testable without a
// metrics provider — see TestOvercommitQueriesScopedToResource.
//
// Every series is collapsed to one per node or per container before it is added up
// or counted: other exporters (OpenCost) publish the same series, and a bare sum or
// count reports each node and request once per exporter.
func (a *overcommitAction) queries() map[string]string {
	node := fmt.Sprintf(`max by (node) (kube_node_status_allocatable{resource=%q})`, a.resource)
	container := fmt.Sprintf(`%s (kube_pod_container_resource_requests{resource=%q})`, promSpecPerContainer, a.resource)
	return map[string]string{
		"allocatable":   fmt.Sprintf(`sum(%s)`, node),
		"requests":      fmt.Sprintf(`sum(%s)`, container),
		"by_namespace":  fmt.Sprintf(`sum by (namespace) (%s)`, container),
		"node_count":    fmt.Sprintf(`count(%s)`, node),
		"largest_node":  fmt.Sprintf(`max(%s)`, node),
		"requests_pods": fmt.Sprintf(`count(%s)`, container),
	}
}

// summaryTable states the gap in the alert's own terms: what has been promised
// against what exists, and what is left once the cluster loses its largest node
// — which is the condition the HA half of the mixin rule actually tests.
func (a *overcommitAction) summaryTable(allocatable, requests, largestNode, nodeCount, podCount float64) (playbooks.PlaybookActionResponse, error) {
	rows := [][]any{
		{"Requested by pods", a.format(requests)},
		{"Allocatable across " + formatCount(nodeCount) + " nodes", a.format(allocatable)},
	}

	over := requests - allocatable
	switch {
	case over > 0:
		rows = append(rows,
			[]any{"Over-committed by", a.format(over)},
			[]any{"Requests vs allocatable", formatRatio(requests, allocatable)})
	default:
		rows = append(rows,
			[]any{"Headroom", a.format(-over)},
			[]any{"Requests vs allocatable", formatRatio(requests, allocatable)})
	}

	// The mixin's HA branch compares requests against allocatable minus the
	// largest node, so a cluster can be "fine" today and alert anyway.
	if largestNode > 0 && nodeCount > 1 {
		surviving := allocatable - largestNode
		rows = append(rows, []any{
			"Allocatable if the largest node is lost", a.format(surviving),
		})
		if surviving > 0 {
			rows = append(rows, []any{"Requests vs that", formatRatio(requests, surviving)})
		}
	}
	if podCount > 0 {
		rows = append(rows, []any{
			"Containers with a " + strings.ToLower(a.resourceTitle()) + " request set", formatCount(podCount)})
	}

	insight := a.summaryInsight(requests, allocatable)
	return playbooks.PlaybookActionResponseTable{
		Rows:    rows,
		Headers: []string{"Measure", a.resourceLabel()},
		AdditionalInfo: map[string]any{
			"title":              a.resourceTitle() + " over-commitment",
			"action_name":        a.actionName(),
			"actual_action_name": a.actionName(),
			"resource":           a.resource,
		},
		Insight: insight,
	}, nil
}

// namespaceTable names who holds the reservations, which is the part an
// operator can act on — the summary says there is a problem, this says where.
func (a *overcommitAction) namespaceTable(samples []latestSample, allocatable float64) (playbooks.PlaybookActionResponse, error) {
	type nsRow struct {
		namespace string
		requested float64
	}
	seen := make([]nsRow, 0, len(samples))
	for _, s := range samples {
		ns := strings.TrimSpace(s.Metric["namespace"])
		if ns == "" || s.Value <= 0 {
			continue
		}
		seen = append(seen, nsRow{namespace: ns, requested: s.Value})
	}
	if len(seen) == 0 {
		return nil, errors.New("overcommit_enricher: no per-namespace requests returned")
	}
	sort.Slice(seen, func(i, j int) bool { return seen[i].requested > seen[j].requested })

	truncated := false
	if len(seen) > overcommitNamespaceRows {
		seen = seen[:overcommitNamespaceRows]
		truncated = true
	}

	rows := make([][]any, 0, len(seen))
	for _, r := range seen {
		share := ""
		if allocatable > 0 {
			share = fmt.Sprintf("%.0f%%", (r.requested/allocatable)*100)
		}
		rows = append(rows, []any{r.namespace, a.format(r.requested), share})
	}

	info := map[string]any{
		"title":              a.resourceTitle() + " requests by namespace",
		"action_name":        a.actionName(),
		"actual_action_name": a.actionName(),
		"resource":           a.resource,
	}
	if truncated {
		info["title"] = fmt.Sprintf("%s requests by namespace (top %d)", a.resourceTitle(), overcommitNamespaceRows)
	}

	return playbooks.PlaybookActionResponseTable{
		Rows:           rows,
		Headers:        []string{"Namespace", "Requested", "Share of allocatable"},
		AdditionalInfo: info,
		Insight:        []playbooks.PlaybookActionResponseInsight{},
	}, nil
}

func (a *overcommitAction) summaryInsight(requests, allocatable float64) []playbooks.PlaybookActionResponseInsight {
	if allocatable <= 0 || requests <= allocatable {
		return []playbooks.PlaybookActionResponseInsight{}
	}
	return []playbooks.PlaybookActionResponseInsight{{
		Message: fmt.Sprintf(
			"Pods reserve %s of %s — %s more than the cluster has. Scheduling fails once the unused share is claimed.",
			a.format(requests), a.format(allocatable), a.format(requests-allocatable)),
		Severity: "warning",
	}}
}

func (a *overcommitAction) actionName() string {
	switch {
	case a.byNamespace && a.resource == "cpu":
		return "cluster_cpu_requests_enricher"
	case a.byNamespace:
		return "cluster_memory_requests_enricher"
	case a.resource == "cpu":
		return "cpu_overcommited_enricher"
	default:
		return "memory_overcommited_enricher"
	}
}

func (a *overcommitAction) resourceTitle() string {
	if a.resource == "cpu" {
		return "CPU"
	}
	return "Memory"
}

func (a *overcommitAction) resourceLabel() string {
	if a.resource == "cpu" {
		return "CPU cores"
	}
	return "Memory"
}

// format renders a raw kube-state-metrics value: CPU is cores, memory is bytes.
func (a *overcommitAction) format(v float64) string {
	if a.resource == "cpu" {
		return fmt.Sprintf("%.2f cores", v)
	}
	return formatBytes(v)
}

func formatBytes(v float64) string {
	const unit = 1024.0
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	i := 0
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

func formatRatio(a, b float64) string {
	if b <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", (a/b)*100)
}

func formatCount(v float64) string {
	return fmt.Sprintf("%.0f", v)
}

// singleValue collapses an aggregate query's result. A `sum(...)` returns one
// series; anything else means the query changed shape and the largest value is
// the safest read.
func singleValue(samples []latestSample) float64 {
	out := 0.0
	for _, s := range samples {
		if s.Value > out {
			out = s.Value
		}
	}
	return out
}

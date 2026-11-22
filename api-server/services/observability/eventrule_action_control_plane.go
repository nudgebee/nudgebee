package observability

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/prometheus/prometheus/promql/parser"
)

// control_plane_down_enricher answers what a KubeletDown / KubeSchedulerDown /
// KubeControllerManagerDown / KubeProxyDown / etcd members alert asserts and never
// shows: which of the component's scrape targets are missing, whether Kubernetes
// itself says the component is running, and — when it is — that the alert fired on
// missing data rather than on a missing component.
//
// These rules are `absent(up{job="…"} == 1)` or count-of-up comparisons. They fire
// just as readily when the metrics store returns nothing as when the component is
// gone: KubeletDown fires while every kubelet is up if the metrics store is
// restarting. Until now the only alert-specific evidence was
// the rule's own query chart, a flat "1", which reads as "every kubelet is gone".
//
// The card therefore reads two independent sources:
//   - the metrics store: the rule's own `up{…}` series, per target, plus `count(up)`
//     across every job as a baseline for "was the store answering at all";
//   - the Kubernetes API (through the agent): node Ready conditions for kubelets —
//     kubelets post them, so a Ready node has a running kubelet — and the
//     component's pods for in-cluster components.
//
// The second source is what makes the false alarm visible at the moment the event
// arrives: when the store is still down, the metrics half fails, and the API half
// still says "20 of 20 nodes Ready".
func init() {
	playbooks.RegisterAction("control_plane_down_enricher", &controlPlaneDownAction{})
}

type controlPlaneComponent struct {
	// title names the component in the card and the verdict.
	title string
	// selector is the `up{…}` selector the stock kubernetes-mixin rule uses. The
	// tenant's own rule expression wins when we have it; see controlPlaneSelector.
	selector string
	// nodeScoped components run on every node and are judged by node readiness
	// (kubelet); the others by their pods.
	nodeScoped bool
	// podPrefixes match the component's pods: static pods are `<name>-<node>`,
	// kube-proxy's DaemonSet pods `kube-proxy-<suffix>`, OpenShift prefixes the
	// scheduler with `openshift-`.
	podPrefixes []string
}

// controlPlaneAlerts is keyed by alert name, not by a subject shape: these alerts
// name no object (integrations/core.clusterScopedAlertNames lists the same set for
// subject resolution).
var controlPlaneAlerts = map[string]controlPlaneComponent{
	"KubeletDown":               {title: "kubelet", selector: `up{job="kubelet",metrics_path="/metrics"}`, nodeScoped: true},
	"KubeSchedulerDown":         {title: "kube-scheduler", selector: `up{job="kube-scheduler"}`, podPrefixes: []string{"kube-scheduler", "openshift-kube-scheduler"}},
	"KubeControllerManagerDown": {title: "kube-controller-manager", selector: `up{job="kube-controller-manager"}`, podPrefixes: []string{"kube-controller-manager"}},
	"KubeProxyDown":             {title: "kube-proxy", selector: `up{job="kube-proxy"}`, podPrefixes: []string{"kube-proxy"}},
	"etcdMembersDown":           {title: "etcd", selector: `up{job=~".*etcd.*"}`, podPrefixes: []string{"etcd"}},
	"etcdInsufficientMembers":   {title: "etcd", selector: `up{job=~".*etcd.*"}`, podPrefixes: []string{"etcd"}},
}

const (
	// controlPlaneLookback covers the rule's `for:` (15m in the mixin) and the
	// lead-up before it, so a target's "down since" falls inside the window.
	controlPlaneLookback = 30 * time.Minute
	// controlPlaneAfter shows the minutes after firing, when the event is read later.
	controlPlaneAfter = 5 * time.Minute
	controlPlaneStep  = 30 * time.Second
	// controlPlaneProblemRows caps per-target rows. A cluster-wide outage on a
	// 500-node cluster is one statement, not 500 rows.
	controlPlaneProblemRows = 20
	controlPlaneEventRows   = 8
)

// controlPlanePromQLParser is stateless; ParseExpr builds a fresh parser per call.
var controlPlanePromQLParser = parser.NewParser(parser.Options{})

type controlPlaneDownAction struct{}

func (a *controlPlaneDownAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	_, ok := controlPlaneAlertName(ctx.GetEvent())
	return ok
}

func (a *controlPlaneDownAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	alert, _ := controlPlaneAlertName(ctx.GetEvent())
	return a.Execute(ctx, map[string]any{"alert_name": alert})
}

// controlPlaneAlertName finds the alert name the way the playbook lookup does
// (event/service.go): a PagerDuty event's AggregationKey can be the PagerDuty
// service name, so nb_alert_name and alertname are read first.
func controlPlaneAlertName(event playbooks.PlaybookEvent) (string, bool) {
	candidates := []string{event.Labels["nb_alert_name"], event.Labels["alertname"], event.Name, event.AggregationKey}
	for _, name := range candidates {
		if _, ok := controlPlaneAlerts[name]; ok {
			return name, true
		}
	}
	return "", false
}

func (a *controlPlaneDownAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	alert, _ := rawParams["alert_name"].(string)
	component, ok := controlPlaneAlerts[alert]
	if !ok {
		return nil, fmt.Errorf("control_plane_down_enricher: unsupported alert %q", alert)
	}
	tenantID, accountID := ctx.GetTenantId(), ctx.GetAccountId()
	if tenantID == "" || accountID == "" {
		return nil, errors.New("control_plane_down_enricher: tenant ID and account ID required")
	}
	requestCtx := security.NewRequestContextForTenantAdmin(tenantID, ctx.GetLogger(), nil, nil)

	now := time.Now().UTC()
	anchor := now
	if t := ctx.GetEvent().AlertStart(); t != nil && !t.IsZero() && t.Before(now) {
		anchor = t.UTC()
	}
	// A manual run (execute_action) has no event; `at` anchors it on a past alert.
	if at, _ := rawParams["at"].(string); at != "" {
		if t, err := time.Parse(time.RFC3339, at); err == nil && t.Before(now) {
			anchor = t.UTC()
		}
	}
	window := controlPlaneWindow{start: anchor.Add(-controlPlaneLookback), end: minTime(anchor.Add(controlPlaneAfter), now)}

	selector := controlPlaneSelector(component, core.FetchEventRuleExpr(requestCtx, accountID, alert), ctx.GetEvent().Labels)

	metrics := fetchControlPlaneMetrics(requestCtx, accountID, selector, window)
	api := fetchControlPlaneAPI(ctx, component, ctx.GetEvent().Labels)
	if metrics.err != nil && api.err != nil {
		return nil, fmt.Errorf("control_plane_down_enricher: metrics: %v; kubernetes api: %v", metrics.err, api.err)
	}

	report := buildControlPlaneReport(component, selector, window, metrics, api)
	if report.needsEvents && api.err == nil {
		report.addEvents(fetchControlPlaneEvents(ctx, component, api, report, window))
	}

	return playbooks.PlaybookActionResponseTable{
		Rows:    report.rows,
		Headers: []string{"Check", "Result"},
		AdditionalInfo: map[string]any{
			"title":              "Control plane health: " + component.title,
			"action_name":        "control_plane_down_enricher",
			"actual_action_name": "control_plane_down_enricher",
			"alert_name":         alert,
			"query":              selector,
			"window_start":       window.start.Format(time.RFC3339),
			"window_end":         window.end.Format(time.RFC3339),
		},
		Insight: report.insights,
	}, nil
}

type controlPlaneWindow struct{ start, end time.Time }

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// controlPlaneSelector returns the `up{…}` selector the card queries. The tenant's
// rule expression is the source of truth (a cluster whose kubelet job is named
// differently has a rule that says so); without it the mixin default applies,
// with the alert's own `job` label substituted when it carries one.
func controlPlaneSelector(component controlPlaneComponent, ruleExpr string, labels map[string]string) string {
	if sel := upSelectorFromExpr(ruleExpr); sel != "" {
		return sel
	}
	job := strings.TrimSpace(labels["job"])
	if job == "" || strings.Contains(component.selector, fmt.Sprintf("job=%q", job)) {
		return component.selector
	}
	if component.nodeScoped {
		return fmt.Sprintf(`up{job=%q,metrics_path="/metrics"}`, job)
	}
	return fmt.Sprintf(`up{job=%q}`, job)
}

// upSelectorFromExpr returns the first `up{…}` selector in a rule expression, or ""
// when the expression is not PromQL or reads no `up` series.
func upSelectorFromExpr(expr string) string {
	if strings.TrimSpace(expr) == "" {
		return ""
	}
	node, err := controlPlanePromQLParser.ParseExpr(expr)
	if err != nil {
		return ""
	}
	selector := ""
	parser.Inspect(node, func(n parser.Node, _ []parser.Node) error {
		if vs, ok := n.(*parser.VectorSelector); ok && selector == "" && vs.Name == "up" {
			selector = vs.String()
		}
		return nil
	})
	return selector
}

// ---- metrics half --------------------------------------------------------------

type controlPlaneMetrics struct {
	err error
	// targets is the rule's own `up{…}` series over the window.
	targets []Result
	// baseline is `count(up)` across every job: whether the store had anything at all.
	baseline []Result
	// seenInDay is set only when targets is empty: did any target exist in 24h.
	seenInDay *bool
}

func fetchControlPlaneMetrics(ctx *security.RequestContext, accountID, selector string, window controlPlaneWindow) controlPlaneMetrics {
	provider, _, err := GetLogsMetricsTracesProvider(ctx, accountID, "", "metrics", "")
	if err != nil {
		return controlPlaneMetrics{err: fmt.Errorf("metrics provider: %w", err)}
	}
	if provider != integrationPrometheus {
		return controlPlaneMetrics{err: fmt.Errorf("scrape targets are read from Prometheus; this account's metrics source is %q", provider)}
	}
	out, err := FetchMetricsQuery(ctx, FetchMetricsRequest{
		AccountId:      accountID,
		MetricProvider: integrationPrometheus,
		Queries:        map[string]string{"targets": selector, "baseline": "count(up)"},
		StartTime:      window.start.UnixMilli(),
		EndTime:        window.end.UnixMilli(),
		StepInterval:   int(controlPlaneStep.Seconds()),
	})
	if err != nil {
		return controlPlaneMetrics{err: err}
	}
	m := controlPlaneMetrics{}
	for _, r := range out.Results {
		if r.Error != nil {
			// The store answering one query and not the other says nothing
			// trustworthy about either; report the failure instead.
			return controlPlaneMetrics{err: fmt.Errorf("%s query: %s", r.QueryKey, *r.Error)}
		}
		switch r.QueryKey {
		case "targets":
			m.targets = r.Payload
		case "baseline":
			m.baseline = r.Payload
		}
	}
	if len(m.targets) == 0 && len(m.baseline) > 0 {
		// The store is answering but has no target for this job in the window:
		// distinguish "stopped reporting today" from "never scraped here".
		day, dayErr := FetchMetricsQuery(ctx, FetchMetricsRequest{
			AccountId:      accountID,
			MetricProvider: integrationPrometheus,
			Queries:        map[string]string{"seen": fmt.Sprintf("count(last_over_time(%s[24h]))", selector)},
			StartTime:      window.end.Add(-time.Minute).UnixMilli(),
			EndTime:        window.end.UnixMilli(),
			StepInterval:   60,
		})
		if dayErr == nil {
			seen := false
			for _, r := range day.Results {
				if r.Error == nil && len(r.Payload) > 0 {
					seen = true
				}
			}
			m.seenInDay = &seen
		}
	}
	return m
}

type targetHealth struct {
	name  string
	up    bool
	stale bool      // series stopped before the window ended
	since time.Time // down since / not reporting since
	// sinceBeforeWindow: down for the whole window, so `since` is only a bound.
	sinceBeforeWindow bool
	// recovered: up at the end, but down from downFrom to downTo inside the window.
	// The card can be read after the outage ended; that must not read as "never down".
	recovered        bool
	downFrom, downTo time.Time
}

// rank orders the states when several series describe one target: a series that
// was up throughout beats one that recovered, which beats down, which beats stale
// (the oldest information).
func (h targetHealth) rank() int {
	switch {
	case h.up && !h.recovered:
		return 4
	case h.up:
		return 3
	case !h.stale:
		return 2
	}
	return 1
}

// controlPlaneTargets reduces each `up` series to its state at the end of the
// window, and whether it was down earlier in it. Several series can map to one
// target (a custom selector without metrics_path matches the kubelet's three
// endpoints); the target counts as up if any of them is, which is what
// `absent(up{…} == 1)` itself tests.
func controlPlaneTargets(series []Result, window controlPlaneWindow, identify func(map[string]string) string) []targetHealth {
	byName := map[string]targetHealth{}
	order := []string{}
	staleAfter := window.end.Add(-3 * controlPlaneStep)
	for _, s := range series {
		name := identify(s.Metric)
		if name == "" {
			continue
		}
		last := -1
		for i := len(s.Values) - 1; i >= 0; i-- {
			if i < len(s.Timestamps) && usableValue(s.Values[i]) {
				last = i
				break
			}
		}
		if last < 0 {
			continue
		}
		lastAt := time.Unix(s.Timestamps[last], 0).UTC()
		h := targetHealth{name: name}
		switch {
		case lastAt.Before(staleAfter):
			h.stale = true
			h.since = lastAt
		case s.Values[last] >= 1:
			h.up = true
			h.recovered, h.downFrom, h.downTo = downStretch(s, last)
		default:
			first := last
			for first > 0 && usableValue(s.Values[first-1]) && s.Values[first-1] < 1 {
				first--
			}
			h.since = time.Unix(s.Timestamps[first], 0).UTC()
			h.sinceBeforeWindow = first == 0
		}
		prev, seen := byName[name]
		if !seen {
			order = append(order, name)
			byName[name] = h
			continue
		}
		if h.rank() > prev.rank() {
			byName[name] = h
		}
	}
	out := make([]targetHealth, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// downStretch finds the span in which a series that ends up (index last) was
// down: from its first 0 to the first 1 after its last 0.
func downStretch(s Result, last int) (bool, time.Time, time.Time) {
	first, final := -1, -1
	for i := 0; i < last; i++ {
		if usableValue(s.Values[i]) && s.Values[i] < 1 {
			if first < 0 {
				first = i
			}
			final = i
		}
	}
	if first < 0 {
		return false, time.Time{}, time.Time{}
	}
	return true, time.Unix(s.Timestamps[first], 0).UTC(), time.Unix(s.Timestamps[final+1], 0).UTC()
}

type timeGap struct{ from, to time.Time }

// storeGaps finds stretches with no `count(up)` sample — minutes in which the
// metrics store had nothing for ANY job. An absent() rule evaluated then fires.
// A range query carries the last sample forward for the store's lookback (5m by
// default), so a shorter outage leaves no gap and a longer one shows up late; a
// gap here is evidence, its absence is not proof.
func storeGaps(baseline []Result, window controlPlaneWindow) []timeGap {
	if len(baseline) == 0 {
		return nil
	}
	ts := []int64{}
	for _, s := range baseline {
		for i, v := range s.Values {
			if i < len(s.Timestamps) && usableValue(v) {
				ts = append(ts, s.Timestamps[i])
			}
		}
	}
	if len(ts) == 0 {
		return []timeGap{{from: window.start, to: window.end}}
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	step := int64(controlPlaneStep.Seconds())
	gaps := []timeGap{}
	if first := time.Unix(ts[0], 0).UTC(); first.Sub(window.start) > 2*controlPlaneStep {
		gaps = append(gaps, timeGap{from: window.start, to: first})
	}
	for i := 1; i < len(ts); i++ {
		if ts[i]-ts[i-1] > 2*step {
			gaps = append(gaps, timeGap{from: time.Unix(ts[i-1]+step, 0).UTC(), to: time.Unix(ts[i], 0).UTC()})
		}
	}
	if last := time.Unix(ts[len(ts)-1], 0).UTC(); window.end.Sub(last) > 3*controlPlaneStep {
		gaps = append(gaps, timeGap{from: last, to: window.end})
	}
	return gaps
}

// ---- Kubernetes API half -------------------------------------------------------

type controlPlaneNode struct {
	name          string
	internalIP    string
	ready         bool
	reason        string
	message       string
	transitionAt  time.Time
	lastHeartbeat time.Time
}

type controlPlanePod struct {
	name, namespace, node, phase string
	ready                        bool
	restarts                     int
	lastRestartAt                time.Time
	lastRestartReason            string
}

type controlPlaneAPI struct {
	err       error
	nodes     []controlPlaneNode
	pods      []controlPlanePod
	namespace string // where the component's pods were looked for
	// readAt is when the API was read. The API describes the cluster now, not at
	// the alert, so the rows say when "now" was.
	readAt time.Time
}

func fetchControlPlaneAPI(ctx playbooks.PlaybookActionContext, component controlPlaneComponent, labels map[string]string) controlPlaneAPI {
	// Nodes are needed for every component: for the kubelet they are the verdict,
	// for the rest they map scrape targets to machines.
	data, err := playbooks.GetResourceViaRelay(ctx, map[string]any{
		"resource_type": "nodes", "group": "", "version": "v1",
		"namespace": []string{}, "all_namespaces": false, "name": []string{},
	})
	if err != nil {
		return controlPlaneAPI{err: err}
	}
	api := controlPlaneAPI{nodes: parseControlPlaneNodes(data), readAt: time.Now().UTC()}
	if component.nodeScoped {
		return api
	}
	api.namespace = strings.TrimSpace(labels["namespace"])
	if api.namespace == "" {
		api.namespace = "kube-system"
	}
	pods, err := playbooks.GetResourceViaRelay(ctx, map[string]any{
		"resource_type": "pods", "group": "", "version": "v1",
		"namespace": []string{api.namespace}, "all_namespaces": false, "name": []string{},
	})
	if err != nil {
		return controlPlaneAPI{err: err}
	}
	api.pods = parseControlPlanePods(pods, component.podPrefixes)
	return api
}

func parseControlPlaneNodes(data any) []controlPlaneNode {
	list, _ := data.([]any)
	out := make([]controlPlaneNode, 0, len(list))
	for _, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		meta, _ := obj["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		if name == "" {
			continue
		}
		n := controlPlaneNode{name: name}
		status, _ := obj["status"].(map[string]any)
		for _, a := range anySlice(status["addresses"]) {
			am, _ := a.(map[string]any)
			if t, _ := am["type"].(string); t == "InternalIP" {
				n.internalIP, _ = am["address"].(string)
			}
		}
		for _, c := range anySlice(status["conditions"]) {
			cm, _ := c.(map[string]any)
			if t, _ := cm["type"].(string); t != "Ready" {
				continue
			}
			st, _ := cm["status"].(string)
			n.ready = strings.EqualFold(st, "True")
			n.reason, _ = cm["reason"].(string)
			n.message, _ = cm["message"].(string)
			n.transitionAt = parseAPITime(cm, "last_transition_time", "lastTransitionTime")
			n.lastHeartbeat = parseAPITime(cm, "last_heartbeat_time", "lastHeartbeatTime")
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func parseControlPlanePods(data any, prefixes []string) []controlPlanePod {
	list, _ := data.([]any)
	out := []controlPlanePod{}
	for _, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		meta, _ := obj["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		if !hasComponentPrefix(name, prefixes) {
			continue
		}
		p := controlPlanePod{name: name}
		p.namespace, _ = meta["namespace"].(string)
		spec, _ := obj["spec"].(map[string]any)
		p.node = firstString(spec, "node_name", "nodeName")
		status, _ := obj["status"].(map[string]any)
		p.phase, _ = status["phase"].(string)
		statuses := anySlice(firstPresent(status, "container_statuses", "containerStatuses"))
		p.ready = len(statuses) > 0
		for _, cs := range statuses {
			csm, _ := cs.(map[string]any)
			if r, _ := csm["ready"].(bool); !r {
				p.ready = false
			}
			p.restarts += intValue(firstPresent(csm, "restart_count", "restartCount"))
			last, _ := firstPresent(csm, "last_state", "lastState").(map[string]any)
			term, _ := last["terminated"].(map[string]any)
			if at := parseAPITime(term, "finished_at", "finishedAt"); at.After(p.lastRestartAt) {
				p.lastRestartAt = at
				p.lastRestartReason, _ = term["reason"].(string)
			}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func hasComponentPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if name == p || strings.HasPrefix(name, p+"-") {
			return true
		}
	}
	return false
}

type controlPlaneEvent struct {
	at                   time.Time
	reason, object, text string
	warning              bool
}

// fetchControlPlaneEvents reads Kubernetes events for the nodes or pods the report
// flagged. Only called when something is unhealthy: an all-healthy card has
// nothing for events to explain, and the event list is the largest read here.
func fetchControlPlaneEvents(ctx playbooks.PlaybookActionContext, component controlPlaneComponent, api controlPlaneAPI, report *controlPlaneReport, window controlPlaneWindow) []controlPlaneEvent {
	// Node events are recorded in the `default` namespace.
	namespace, kind := "default", "Node"
	if !component.nodeScoped {
		namespace, kind = api.namespace, "Pod"
	}
	data, err := playbooks.GetResourceViaRelay(ctx, map[string]any{
		"resource_type": "events", "group": "", "version": "v1",
		"namespace": []string{namespace}, "all_namespaces": false, "name": []string{},
	})
	if err != nil {
		ctx.GetLogger().Warn("control_plane_down_enricher: events", "error", err)
		return nil
	}
	return filterControlPlaneEvents(data, kind, report.flagged, window)
}

func filterControlPlaneEvents(data any, kind string, objects map[string]bool, window controlPlaneWindow) []controlPlaneEvent {
	list, _ := data.([]any)
	out := []controlPlaneEvent{}
	for _, item := range list {
		ev, ok := item.(map[string]any)
		if !ok {
			continue
		}
		io, _ := firstPresent(ev, "involved_object", "involvedObject").(map[string]any)
		if k, _ := io["kind"].(string); k != kind {
			continue
		}
		name, _ := io["name"].(string)
		if !objects[name] {
			continue
		}
		at := parseAPITime(ev, "last_timestamp", "lastTimestamp")
		if at.IsZero() {
			at = parseAPITime(ev, "event_time", "eventTime")
		}
		if at.Before(window.start) || at.After(window.end) {
			continue
		}
		reason, _ := ev["reason"].(string)
		msg, _ := ev["message"].(string)
		typ, _ := ev["type"].(string)
		out = append(out, controlPlaneEvent{at: at, reason: reason, object: name, text: msg, warning: typ == "Warning"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].at.After(out[j].at) })
	if len(out) > controlPlaneEventRows {
		out = out[:controlPlaneEventRows]
	}
	return out
}

// ---- report --------------------------------------------------------------------

type controlPlaneReport struct {
	rows        [][]any
	insights    []playbooks.PlaybookActionResponseInsight
	needsEvents bool
	// flagged names the nodes or pods whose events are worth reading.
	flagged map[string]bool
}

func (r *controlPlaneReport) add(check, result string) {
	r.rows = append(r.rows, []any{check, result})
}

func (r *controlPlaneReport) addEvents(events []controlPlaneEvent) {
	for _, e := range events {
		r.add("Event "+clock(e.at), fmt.Sprintf("%s on %s: %s", e.reason, e.object, e.text))
	}
}

func buildControlPlaneReport(component controlPlaneComponent, selector string, window controlPlaneWindow, m controlPlaneMetrics, api controlPlaneAPI) *controlPlaneReport {
	r := &controlPlaneReport{flagged: map[string]bool{}}
	nodes := map[string]controlPlaneNode{}
	ipToNode := map[string]string{}
	for _, n := range api.nodes {
		nodes[n.name] = n
		if n.internalIP != "" {
			ipToNode[n.internalIP] = n.name
		}
	}

	// Metrics: the rule's own series.
	var targets []targetHealth
	gaps := storeGaps(m.baseline, window)
	storeEmpty := m.err == nil && len(m.baseline) == 0
	r.add("Query", selector)
	switch {
	case m.err != nil:
		r.add("Scrape targets", "could not be read: "+m.err.Error())
	case storeEmpty:
		r.add("Scrape targets", fmt.Sprintf("the metrics store returned no data for any job between %s and %s", clock(window.start), clock(window.end)))
	default:
		identify := func(labels map[string]string) string {
			return controlPlaneTargetName(labels, component, nodes, ipToNode)
		}
		targets = controlPlaneTargets(m.targets, window, identify)
	}

	// A stale target whose name matches no node is a node that left the cluster —
	// but only if target names match node names here at all. When none do (an
	// unusual scrape layout), an unmatched name says nothing about the node.
	namesResolve := false
	for _, t := range targets {
		if nodeExists(nodes, t.name) {
			namesResolve = true
			break
		}
	}
	up, down, removed, recovered := 0, []targetHealth{}, []targetHealth{}, []targetHealth{}
	for _, t := range targets {
		switch {
		case t.up:
			up++
			if t.recovered {
				recovered = append(recovered, t)
			}
		case component.nodeScoped && t.stale && namesResolve && !nodeExists(nodes, t.name):
			// Its node is gone: a scale-down, not a dead kubelet.
			removed = append(removed, t)
		default:
			down = append(down, t)
		}
	}
	total := up + len(down)
	if m.err == nil && !storeEmpty {
		switch {
		case total == 0 && m.seenInDay != nil && !*m.seenInDay:
			r.add("Scrape targets", "none in the last 24 hours")
		case total == 0:
			r.add("Scrape targets", fmt.Sprintf("none between %s and %s", clock(window.start), clock(window.end)))
		default:
			r.add("Scrape targets", fmt.Sprintf("%d of %d up", up, total))
		}
		for i, t := range down {
			if i == controlPlaneProblemRows {
				r.add("…", fmt.Sprintf("%d more targets not up", len(down)-i))
				break
			}
			r.add(t.name, targetStateText(t))
			r.flagged[t.name] = true
		}
		for i, t := range recovered {
			if i == controlPlaneProblemRows {
				r.add("…", fmt.Sprintf("%d more targets recovered", len(recovered)-i))
				break
			}
			r.add(t.name, targetStateText(t))
			r.flagged[t.name] = true
		}
		if len(removed) > 0 {
			r.add("Targets of removed nodes", fmt.Sprintf("%d (stopped reporting when their node left the cluster)", len(removed)))
		}
		for _, g := range gaps {
			r.add("Metrics store gap", fmt.Sprintf("no samples for any job from %s to %s", clock(g.from), clock(g.to)))
		}
	}

	// Kubernetes API.
	notReady := []controlPlaneNode{}
	unhealthyPods := []controlPlanePod{}
	if api.err != nil {
		r.add("Kubernetes API", "could not be read: "+api.err.Error())
	} else {
		for _, n := range api.nodes {
			if !n.ready {
				notReady = append(notReady, n)
			}
		}
		r.add("Nodes Ready at "+clock(api.readAt), fmt.Sprintf("%d of %d", len(api.nodes)-len(notReady), len(api.nodes)))
		for i, n := range notReady {
			if i == controlPlaneProblemRows {
				r.add("…", fmt.Sprintf("%d more nodes not Ready", len(notReady)-i))
				break
			}
			r.add(n.name, nodeStateText(n))
			r.flagged[n.name] = true
		}
		if !component.nodeScoped {
			unhealthyPods = controlPlanePodRows(r, component, api, window)
		}
	}

	r.needsEvents = len(down) > 0 || len(recovered) > 0 || len(notReady) > 0 || len(unhealthyPods) > 0
	r.insights = controlPlaneVerdict(component, window, m, storeEmpty, total, up, down, recovered, gaps, api, notReady, unhealthyPods)
	return r
}

func nodeExists(nodes map[string]controlPlaneNode, name string) bool {
	_, ok := nodes[name]
	return ok
}

// controlPlaneTargetName labels a target. For the kubelet the row should name the
// node, but where the node name lives differs by scrape config: the
// kube-prometheus-stack ServiceMonitor writes it to `node` and puts IP:port in
// `instance`; the VictoriaMetrics stack copies node labels (so `node` can be a
// node LABEL such as "db") and puts the node name in `instance`. Matching each
// candidate against the API's node list picks the right one in both.
func controlPlaneTargetName(labels map[string]string, component controlPlaneComponent, nodes map[string]controlPlaneNode, ipToNode map[string]string) string {
	host := labels["instance"]
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host[i+1:], "]") {
		host = host[:i]
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if component.nodeScoped {
		for _, c := range []string{labels["node"], labels["kubernetes_io_hostname"], host} {
			if _, ok := nodes[c]; ok && c != "" {
				return c
			}
		}
		if n := ipToNode[host]; n != "" {
			return n
		}
	} else if p := labels["pod"]; p != "" {
		return p
	}
	if host != "" {
		return host
	}
	return labels["node"]
}

func controlPlanePodRows(r *controlPlaneReport, component controlPlaneComponent, api controlPlaneAPI, window controlPlaneWindow) []controlPlanePod {
	if len(api.pods) == 0 {
		r.add("Pods at "+clock(api.readAt), fmt.Sprintf("no %s pod in namespace %s", component.title, api.namespace))
		return nil
	}
	unhealthy := []controlPlanePod{}
	for _, p := range api.pods {
		restartedInWindow := !p.lastRestartAt.IsZero() && !p.lastRestartAt.Before(window.start)
		if p.ready && p.phase == "Running" && !restartedInWindow {
			continue
		}
		unhealthy = append(unhealthy, p)
	}
	r.add("Pods at "+clock(api.readAt), fmt.Sprintf("%d of %d running and ready in %s", countReady(api.pods), len(api.pods), api.namespace))
	for i, p := range unhealthy {
		if i == controlPlaneProblemRows {
			r.add("…", fmt.Sprintf("%d more pods", len(unhealthy)-i))
			break
		}
		r.add(p.name, podStateText(p))
		r.flagged[p.name] = true
	}
	return unhealthy
}

func countReady(pods []controlPlanePod) int {
	n := 0
	for _, p := range pods {
		if p.ready && p.phase == "Running" {
			n++
		}
	}
	return n
}

func targetStateText(t targetHealth) string {
	switch {
	case t.recovered:
		return fmt.Sprintf("was down from %s to %s, up since", clock(t.downFrom), clock(t.downTo))
	case t.stale:
		return "not reporting since " + clock(t.since)
	case t.sinceBeforeWindow:
		return "down since before " + clock(t.since)
	default:
		return "down since " + clock(t.since)
	}
}

func nodeStateText(n controlPlaneNode) string {
	text := "NotReady"
	if !n.transitionAt.IsZero() {
		text += " since " + clock(n.transitionAt)
	}
	if n.reason != "" {
		text += " (" + n.reason + ")"
	}
	if n.message != "" {
		text += ": " + n.message
	}
	return text
}

func podStateText(p controlPlanePod) string {
	parts := []string{p.phase}
	if !p.ready {
		parts = append(parts, "not ready")
	}
	if p.restarts > 0 {
		restart := fmt.Sprintf("%d restarts", p.restarts)
		if !p.lastRestartAt.IsZero() {
			restart += ", last at " + clock(p.lastRestartAt)
			if p.lastRestartReason != "" {
				restart += " (" + p.lastRestartReason + ")"
			}
		}
		parts = append(parts, restart)
	}
	if p.node != "" {
		parts = append(parts, "on "+p.node)
	}
	return strings.Join(parts, ", ")
}

// controlPlaneVerdict states what the evidence means, in one sentence or two. The
// order matters: a component Kubernetes reports as running is never called down
// because the metrics store went quiet.
func controlPlaneVerdict(component controlPlaneComponent, window controlPlaneWindow, m controlPlaneMetrics, storeEmpty bool, total, up int, down, recovered []targetHealth, gaps []timeGap, api controlPlaneAPI, notReady []controlPlaneNode, unhealthyPods []controlPlanePod) []playbooks.PlaybookActionResponseInsight {
	apiHealthy := ""
	if api.err == nil {
		switch {
		case component.nodeScoped && len(api.nodes) > 0 && len(notReady) == 0:
			apiHealthy = fmt.Sprintf("At %s the Kubernetes API reports all %d nodes Ready; a node stays Ready only while its kubelet reports in, so the kubelets are running.", clock(api.readAt), len(api.nodes))
		case !component.nodeScoped && len(api.pods) > 0 && len(unhealthyPods) == 0:
			apiHealthy = fmt.Sprintf("At %s all %d %s pods are running and ready.", clock(api.readAt), len(api.pods), component.title)
		}
	}
	insight := func(severity string, parts ...string) []playbooks.PlaybookActionResponseInsight {
		kept := make([]string, 0, len(parts))
		for _, p := range parts {
			if p != "" {
				kept = append(kept, p)
			}
		}
		msg := strings.Join(kept, " ")
		return []playbooks.PlaybookActionResponseInsight{{Message: msg, Severity: severity}}
	}

	switch {
	case m.err != nil || storeEmpty:
		lead := "The metrics store returned no data."
		if m.err != nil {
			lead = "The metrics store could not be queried (" + m.err.Error() + ")."
		}
		if apiHealthy != "" {
			return insight("warning", lead, apiHealthy, "This alert also fires when its query finds no data, so this is most likely a monitoring outage, not a "+component.title+" outage: check the metrics pipeline.")
		}
		return insight("warning", lead, "This alert also fires when its query finds no data, so it cannot tell a "+component.title+" outage from a monitoring outage: check the metrics pipeline.")

	case len(down) > 0:
		names := make([]string, 0, 3)
		for i, t := range down {
			if i == 3 {
				names = append(names, fmt.Sprintf("and %d more", len(down)-3))
				break
			}
			names = append(names, t.name+" ("+targetStateText(t)+")")
		}
		msg := fmt.Sprintf("%d of %d %s targets are not up: %s.", len(down), total, component.title, strings.Join(names, ", "))
		// A node is marked NotReady only after the node-monitor grace period, so a
		// Ready node clears its kubelet only when the target has been down longer.
		if apiHealthy != "" && downLongerThan(down, api.readAt, controlPlaneReadyGrace) {
			return insight("warning", msg, apiHealthy, "So the scrape is failing, not the component: look at network, authentication or the metrics agent.")
		}
		return insight("critical", msg)

	case total == 0 && m.seenInDay != nil && !*m.seenInDay:
		return insight("warning", fmt.Sprintf("No %s target has been scraped in the last 24 hours, while other targets were.", component.title),
			apiHealthy,
			"The component is not scraped on this cluster (a managed control plane, or no scrape config for it), so this alert will keep firing until the rule or the scrape config is changed.")

	case total == 0:
		return insight("critical", fmt.Sprintf("No %s target reported between %s and %s, while other targets did.", component.title, clock(window.start), clock(window.end)), apiHealthy)

	case len(recovered) > 0:
		names := make([]string, 0, 3)
		for i, t := range recovered {
			if i == 3 {
				names = append(names, fmt.Sprintf("and %d more", len(recovered)-3))
				break
			}
			names = append(names, fmt.Sprintf("%s (%s to %s)", t.name, clock(t.downFrom), clock(t.downTo)))
		}
		return insight("warning", fmt.Sprintf("%d of %d %s targets were down during the alert and have since recovered: %s.", len(recovered), total, component.title, strings.Join(names, ", ")),
			"The outage was real; the events below may say why.")

	case len(notReady) > 0 || len(unhealthyPods) > 0:
		return insight("warning", fmt.Sprintf("All %d %s targets are up, but Kubernetes reports problems listed above.", up, component.title))

	default:
		gap := ""
		if len(gaps) > 0 {
			gap = fmt.Sprintf("The metrics store had no samples for any job from %s to %s.", clock(gaps[0].from), clock(gaps[0].to))
		}
		return insight("warning", fmt.Sprintf("All %d %s targets were up throughout the alert window.", up, component.title), apiHealthy, gap,
			"The alert most likely fired on missing or delayed metrics, not on a "+component.title+" outage: check the metrics pipeline.")
	}
}

// controlPlaneReadyGrace bounds the node-monitor grace period (40s by default,
// often raised): only a target down longer than this is contradicted by Ready.
const controlPlaneReadyGrace = 5 * time.Minute

func downLongerThan(down []targetHealth, at time.Time, d time.Duration) bool {
	for _, t := range down {
		if at.Sub(t.since) < d {
			return false
		}
	}
	return true
}

// ---- small helpers -------------------------------------------------------------

func clock(t time.Time) string {
	return t.UTC().Format("15:04 UTC")
}

func anySlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func firstPresent(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

func firstString(m map[string]any, keys ...string) string {
	s, _ := firstPresent(m, keys...).(string)
	return s
}

func parseAPITime(m map[string]any, keys ...string) time.Time {
	s := firstString(m, keys...)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func intValue(v any) int {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) {
			return 0
		}
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

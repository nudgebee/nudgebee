package observability

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"nudgebee/services/eventrule/playbooks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var cpAt = time.Date(2026, 10, 9, 13, 10, 0, 0, time.UTC)

func cpWindow() controlPlaneWindow {
	return controlPlaneWindow{start: cpAt.Add(-controlPlaneLookback), end: cpAt.Add(controlPlaneAfter)}
}

// cpSeries builds a 30s-step series over the window from a value function; a
// negative value leaves the sample out (no data at that step).
func cpSeries(labels map[string]string, value func(t time.Time) float64) Result {
	w := cpWindow()
	r := Result{Metric: labels}
	for t := w.start; !t.After(w.end); t = t.Add(controlPlaneStep) {
		v := value(t)
		if v < 0 {
			continue
		}
		r.Timestamps = append(r.Timestamps, t.Unix())
		r.Values = append(r.Values, v)
	}
	return r
}

func cpUp(t time.Time) float64 { return 1 }

func cpNodes(names ...string) []controlPlaneNode {
	out := make([]controlPlaneNode, 0, len(names))
	for _, n := range names {
		out = append(out, controlPlaneNode{name: n, ready: true})
	}
	return out
}

func kubeletTarget(node string, value func(time.Time) float64) Result {
	// kube-prometheus-stack layout: node name in `node`, IP:port in `instance`.
	return cpSeries(map[string]string{"job": "kubelet", "metrics_path": "/metrics", "node": node, "instance": "192.0.2.10:10250"}, value)
}

func cpBaseline(value func(time.Time) float64) []Result {
	return []Result{cpSeries(map[string]string{}, value)}
}

func TestControlPlaneRegistered(t *testing.T) {
	action, found := playbooks.GetAction("control_plane_down_enricher")
	require.True(t, found)
	_, ok := action.(playbooks.PlaybookAutoAction)
	assert.True(t, ok, "must run without an agent_playbook row")
}

// TestControlPlaneGate — a PagerDuty event's AggregationKey can be the PagerDuty
// service name; the alert name lives in nb_alert_name / alertname, which is what
// the playbook lookup reads too.
func TestControlPlaneGate(t *testing.T) {
	a := &controlPlaneDownAction{}
	ctx := func(ev playbooks.PlaybookEvent) playbooks.PlaybookActionContext {
		return playbooks.NewPlaybookActionContext("t", "a", slog.Default(), ev)
	}
	assert.True(t, a.CanAutoExecute(ctx(playbooks.PlaybookEvent{AggregationKey: "KubeletDown"})))
	assert.True(t, a.CanAutoExecute(ctx(playbooks.PlaybookEvent{
		AggregationKey: "example-alert-service",
		Labels:         map[string]string{"nb_alert_name": "KubeSchedulerDown"},
	})), "PagerDuty service name as the key")
	assert.True(t, a.CanAutoExecute(ctx(playbooks.PlaybookEvent{
		AggregationKey: "example-alert-service",
		Labels:         map[string]string{"alertname": "etcdInsufficientMembers"},
	})))
	assert.False(t, a.CanAutoExecute(ctx(playbooks.PlaybookEvent{AggregationKey: "TargetDown"})))
	assert.False(t, a.CanAutoExecute(ctx(playbooks.PlaybookEvent{AggregationKey: "KubePodCrashLooping"})))

	name, _ := controlPlaneAlertName(playbooks.PlaybookEvent{
		AggregationKey: "KubeletDown",
		Labels:         map[string]string{"nb_alert_name": "KubeProxyDown"},
	})
	assert.Equal(t, "KubeProxyDown", name, "the alert name label wins over the key")
}

// TestControlPlaneSelector — the card must query what the tenant's rule queries,
// or "20 of 20 up" describes a different job than the one the alert watched.
func TestControlPlaneSelector(t *testing.T) {
	kubelet := controlPlaneAlerts["KubeletDown"]
	etcd := controlPlaneAlerts["etcdMembersDown"]
	scheduler := controlPlaneAlerts["KubeSchedulerDown"]

	assert.Equal(t, `up{job="kubelet",metrics_path="/metrics"}`,
		controlPlaneSelector(kubelet, `absent(up{job="kubelet", metrics_path="/metrics"} == 1)`, nil))
	// The cluster-label variant reads kube_node_info first; the up selector is still found.
	assert.Equal(t, `up{job="kubelet",metrics_path="/metrics"}`,
		controlPlaneSelector(kubelet, `count by (cluster) (kube_node_info{job="kube-state-metrics"}) unless on (cluster) count by (cluster) (up{job="kubelet", metrics_path="/metrics"} == 1)`, nil))
	assert.Equal(t, `up{job=~".*etcd.*"}`,
		controlPlaneSelector(etcd, `sum(up{job=~".*etcd.*"} == bool 1) without (instance, pod) < ((count(up{job=~".*etcd.*"}) without (instance, pod) + 1) / 2)`, nil))
	// A custom job name in the rule is honoured.
	assert.Equal(t, `up{job="nodes-kubelet"}`, controlPlaneSelector(kubelet, `absent(up{job="nodes-kubelet"} == 1)`, nil))

	// No rule: the mixin default, or the alert's own job label when it differs.
	assert.Equal(t, scheduler.selector, controlPlaneSelector(scheduler, "", map[string]string{"job": "kube-scheduler"}))
	assert.Equal(t, `up{job="scheduler"}`, controlPlaneSelector(scheduler, "", map[string]string{"job": "scheduler"}))
	assert.Equal(t, `up{job="k"}`, controlPlaneSelector(scheduler, "not promql (", map[string]string{"job": "k"}))
	assert.Equal(t, kubelet.selector, controlPlaneSelector(kubelet, `absent(kube_node_info)`, nil), "a rule without up falls back")
}

// TestControlPlaneTargetName — the node name is in `node` for kube-prometheus-stack
// and in `instance` for the VictoriaMetrics stack, whose `node` label can be an
// unrelated node LABEL (e.g. node="db" on every kubelet target).
func TestControlPlaneTargetName(t *testing.T) {
	kubelet := controlPlaneAlerts["KubeletDown"]
	nodes := map[string]controlPlaneNode{"gke-a": {name: "gke-a"}, "ip-10-0-0-5": {name: "ip-10-0-0-5"}}
	ips := map[string]string{"192.0.2.7": "ip-10-0-0-5"}

	assert.Equal(t, "gke-a", controlPlaneTargetName(map[string]string{"node": "gke-a", "instance": "192.0.2.1:10250"}, kubelet, nodes, ips))
	assert.Equal(t, "gke-a", controlPlaneTargetName(map[string]string{"node": "db", "instance": "gke-a"}, kubelet, nodes, ips))
	assert.Equal(t, "ip-10-0-0-5", controlPlaneTargetName(map[string]string{"instance": "192.0.2.7:10250"}, kubelet, nodes, ips))
	assert.Equal(t, "192.0.2.9", controlPlaneTargetName(map[string]string{"instance": "192.0.2.9:10250"}, kubelet, nodes, ips), "unknown node: the host")
	ips["2001:db8::7"] = "gke-a"
	assert.Equal(t, "gke-a", controlPlaneTargetName(map[string]string{"instance": "[2001:db8::7]:10250"}, kubelet, nodes, ips), "IPv6 instance")

	scheduler := controlPlaneAlerts["KubeSchedulerDown"]
	assert.Equal(t, "kube-scheduler-master-1", controlPlaneTargetName(map[string]string{"pod": "kube-scheduler-master-1", "instance": "192.0.2.3:10259"}, scheduler, nodes, ips))
}

func TestControlPlaneTargets(t *testing.T) {
	w := cpWindow()
	identify := func(l map[string]string) string { return l["node"] }
	downAt := cpAt.Add(-5 * time.Minute)
	goneAt := cpAt.Add(-10 * time.Minute)
	series := []Result{
		kubeletTarget("a", cpUp),
		kubeletTarget("b", func(t time.Time) float64 {
			if t.Before(downAt) {
				return 1
			}
			return 0
		}),
		kubeletTarget("c", func(time.Time) float64 { return 0 }),
		kubeletTarget("d", func(t time.Time) float64 {
			if t.After(goneAt) {
				return -1
			}
			return 1
		}),
		// A second series for "c" that is up: any up series means the target is up,
		// which is what absent(up{…} == 1) tests.
		cpSeries(map[string]string{"node": "c", "metrics_path": "/metrics/cadvisor"}, cpUp),
	}
	got := controlPlaneTargets(series, w, identify)
	require.Len(t, got, 4)
	byName := map[string]targetHealth{}
	for _, h := range got {
		byName[h.name] = h
	}
	assert.True(t, byName["a"].up)
	assert.False(t, byName["b"].up)
	assert.Equal(t, downAt, byName["b"].since)
	assert.False(t, byName["b"].sinceBeforeWindow)
	assert.True(t, byName["c"].up)
	assert.True(t, byName["d"].stale)
	assert.Equal(t, goneAt, byName["d"].since)

	whole := controlPlaneTargets([]Result{kubeletTarget("e", func(time.Time) float64 { return 0 })}, w, identify)
	require.Len(t, whole, 1)
	assert.True(t, whole[0].sinceBeforeWindow)
	assert.Equal(t, "down since before "+clock(w.start), targetStateText(whole[0]))
}

func TestStoreGaps(t *testing.T) {
	w := cpWindow()
	gapFrom, gapTo := cpAt.Add(-12*time.Minute), cpAt.Add(-4*time.Minute)
	gaps := storeGaps(cpBaseline(func(t time.Time) float64 {
		if !t.Before(gapFrom) && t.Before(gapTo) {
			return -1
		}
		return 150
	}), w)
	require.Len(t, gaps, 1)
	assert.Equal(t, gapFrom, gaps[0].from)
	assert.Equal(t, gapTo, gaps[0].to)

	assert.Empty(t, storeGaps(cpBaseline(func(time.Time) float64 { return 150 }), w))

	trailing := storeGaps(cpBaseline(func(t time.Time) float64 {
		if t.After(cpAt) {
			return -1
		}
		return 150
	}), w)
	require.Len(t, trailing, 1)
	assert.Equal(t, w.end, trailing[0].to)
}

func cpReport(alert string, m controlPlaneMetrics, api controlPlaneAPI) *controlPlaneReport {
	c := controlPlaneAlerts[alert]
	api.readAt = cpAt.Add(time.Minute)
	return buildControlPlaneReport(c, c.selector, cpWindow(), m, api)
}

func cpRow(r *controlPlaneReport, check string) string {
	for _, row := range r.rows {
		if row[0] == check {
			return row[1].(string)
		}
	}
	return ""
}

// TestControlPlaneStoreDownAtArrival is the false alarm as it happens live: the
// metrics store is restarting when the event arrives, so the metrics half fails,
// and the Kubernetes API still shows every node Ready. The card must call it a
// probable monitoring outage, not a kubelet outage.
func TestControlPlaneStoreDownAtArrival(t *testing.T) {
	r := cpReport("KubeletDown",
		controlPlaneMetrics{err: errors.New("prom: connection refused")},
		controlPlaneAPI{nodes: cpNodes("a", "b", "c")})
	require.Len(t, r.insights, 1)
	msg := r.insights[0].Message
	assert.Contains(t, msg, "could not be queried")
	assert.Contains(t, msg, "all 3 nodes Ready")
	assert.Contains(t, msg, "monitoring outage")
	assert.Equal(t, "3 of 3", cpRow(r, "Nodes Ready at "+clock(cpAt.Add(time.Minute))))
	assert.False(t, r.needsEvents)
}

// TestControlPlaneAllUpFalseAlarm is the same false alarm read once the store has its
// data back: every target up, with the store's gap named.
func TestControlPlaneAllUpFalseAlarm(t *testing.T) {
	gapFrom := cpAt.Add(-12 * time.Minute)
	gapTo := cpAt.Add(-6 * time.Minute)
	missing := func(t time.Time) float64 {
		if !t.Before(gapFrom) && t.Before(gapTo) {
			return -1
		}
		return 1
	}
	r := cpReport("KubeletDown",
		controlPlaneMetrics{
			targets:  []Result{kubeletTarget("a", missing), kubeletTarget("b", missing)},
			baseline: cpBaseline(missing),
		},
		controlPlaneAPI{nodes: cpNodes("a", "b")})
	assert.Equal(t, "2 of 2 up", cpRow(r, "Scrape targets"))
	assert.Equal(t, `up{job="kubelet",metrics_path="/metrics"}`, cpRow(r, "Query"))
	assert.Contains(t, cpRow(r, "Metrics store gap"), clock(gapFrom))
	msg := r.insights[0].Message
	assert.Contains(t, msg, "All 2 kubelet targets were up throughout the alert window.")
	assert.Contains(t, msg, "check the metrics pipeline")
	assert.Contains(t, msg, "no samples for any job from "+clock(gapFrom)+" to "+clock(gapTo))
	assert.Equal(t, "warning", r.insights[0].Severity)
}

func TestControlPlaneKubeletReallyDown(t *testing.T) {
	downAt := cpAt.Add(-16 * time.Minute)
	nodes := cpNodes("a")
	nodes = append(nodes, controlPlaneNode{name: "b", ready: false, reason: "NodeStatusUnknown", message: "Kubelet stopped posting node status.", transitionAt: downAt})
	r := cpReport("KubeletDown",
		controlPlaneMetrics{
			targets: []Result{kubeletTarget("a", cpUp), kubeletTarget("b", func(t time.Time) float64 {
				if t.Before(downAt) {
					return 1
				}
				return 0
			})},
			baseline: cpBaseline(func(time.Time) float64 { return 100 }),
		},
		controlPlaneAPI{nodes: nodes})
	assert.Equal(t, "1 of 2 up", cpRow(r, "Scrape targets"))
	assert.Equal(t, "down since "+clock(downAt), cpRow(r, "b"))
	assert.Equal(t, "critical", r.insights[0].Severity)
	assert.Contains(t, r.insights[0].Message, "1 of 2 kubelet targets are not up: b")
	assert.True(t, r.needsEvents)
	assert.True(t, r.flagged["b"])
}

// TestControlPlaneScrapeFailingNodeReady — target down, node Ready: the kubelet is
// running and the scrape is what failed. Saying "kubelet down" would send on-call
// to the wrong machine.
func TestControlPlaneScrapeFailingNodeReady(t *testing.T) {
	r := cpReport("KubeletDown",
		controlPlaneMetrics{
			targets:  []Result{kubeletTarget("a", cpUp), kubeletTarget("b", func(time.Time) float64 { return 0 })},
			baseline: cpBaseline(func(time.Time) float64 { return 100 }),
		},
		controlPlaneAPI{nodes: cpNodes("a", "b")})
	assert.Equal(t, "warning", r.insights[0].Severity)
	assert.Contains(t, r.insights[0].Message, "the scrape is failing, not the component")
}

// TestControlPlaneRemovedNode — a kubelet whose node left the cluster stops
// reporting; that is a scale-down, not a down kubelet.
func TestControlPlaneRemovedNode(t *testing.T) {
	goneAt := cpAt.Add(-20 * time.Minute)
	r := cpReport("KubeletDown",
		controlPlaneMetrics{
			targets: []Result{kubeletTarget("a", cpUp), kubeletTarget("gone", func(t time.Time) float64 {
				if t.After(goneAt) {
					return -1
				}
				return 1
			})},
			baseline: cpBaseline(func(time.Time) float64 { return 100 }),
		},
		controlPlaneAPI{nodes: cpNodes("a")})
	assert.Equal(t, "1 of 1 up", cpRow(r, "Scrape targets"))
	assert.Contains(t, cpRow(r, "Targets of removed nodes"), "1")
	assert.False(t, r.needsEvents)
}

// TestControlPlaneNeverScraped — managed control planes (GKE, AKS, EKS) do not
// expose the scheduler or controller-manager; the stock rule fires forever.
func TestControlPlaneNeverScraped(t *testing.T) {
	never := false
	r := cpReport("KubeSchedulerDown",
		controlPlaneMetrics{baseline: cpBaseline(func(time.Time) float64 { return 100 }), seenInDay: &never},
		controlPlaneAPI{nodes: cpNodes("a"), namespace: "kube-system"})
	assert.Equal(t, "none in the last 24 hours", cpRow(r, "Scrape targets"))
	assert.Contains(t, r.insights[0].Message, "not scraped on this cluster")
	assert.NotContains(t, r.insights[0].Message, "  ")
}

func TestControlPlaneStoreEmpty(t *testing.T) {
	r := cpReport("KubeletDown", controlPlaneMetrics{}, controlPlaneAPI{nodes: cpNodes("a")})
	assert.Contains(t, cpRow(r, "Scrape targets"), "no data for any job")
	assert.Contains(t, r.insights[0].Message, "monitoring outage")
}

func TestControlPlanePods(t *testing.T) {
	restartAt := cpAt.Add(-3 * time.Minute)
	r := cpReport("KubeSchedulerDown",
		controlPlaneMetrics{
			targets:  []Result{cpSeries(map[string]string{"pod": "kube-scheduler-m1"}, cpUp)},
			baseline: cpBaseline(func(time.Time) float64 { return 100 }),
		},
		controlPlaneAPI{nodes: cpNodes("m1", "m2"), namespace: "kube-system", pods: []controlPlanePod{
			{name: "kube-scheduler-m1", phase: "Running", ready: true},
			{name: "kube-scheduler-m2", phase: "Running", ready: true, restarts: 4, lastRestartAt: restartAt, lastRestartReason: "Error", node: "m2"},
		}})
	assert.Equal(t, "1 of 1 up", cpRow(r, "Scrape targets"))
	assert.Equal(t, "2 of 2 running and ready in kube-system", cpRow(r, "Pods at "+clock(cpAt.Add(time.Minute))))
	assert.Equal(t, "Running, 4 restarts, last at "+clock(restartAt)+" (Error), on m2", cpRow(r, "kube-scheduler-m2"))
	assert.True(t, r.needsEvents, "a restart inside the window is worth its events")
}

func TestParseControlPlaneAPI(t *testing.T) {
	nodes := parseControlPlaneNodes([]any{
		map[string]any{
			"metadata": map[string]any{"name": "n1"},
			"status": map[string]any{
				"addresses": []any{map[string]any{"type": "InternalIP", "address": "192.0.2.4"}},
				"conditions": []any{
					map[string]any{"type": "MemoryPressure", "status": "False"},
					map[string]any{"type": "Ready", "status": "False", "reason": "NodeStatusUnknown", "message": "Kubelet stopped posting node status.", "last_transition_time": "2026-10-09T13:05:00Z"},
				},
			},
		},
	})
	require.Len(t, nodes, 1)
	assert.False(t, nodes[0].ready)
	assert.Equal(t, "192.0.2.4", nodes[0].internalIP)
	assert.Equal(t, "NotReady since 13:05 UTC (NodeStatusUnknown): Kubelet stopped posting node status.", nodeStateText(nodes[0]))

	pods := parseControlPlanePods([]any{
		map[string]any{"metadata": map[string]any{"name": "kube-proxy-x7f2", "namespace": "kube-system"}, "spec": map[string]any{"node_name": "n1"},
			"status": map[string]any{"phase": "Running", "container_statuses": []any{
				map[string]any{"ready": true, "restart_count": float64(2), "last_state": map[string]any{"terminated": map[string]any{"finished_at": "2026-10-09T13:01:00Z", "reason": "OOMKilled"}}},
			}}},
		map[string]any{"metadata": map[string]any{"name": "kube-dns-1"}},
		map[string]any{"metadata": map[string]any{"name": "kube-proxyish"}},
	}, controlPlaneAlerts["KubeProxyDown"].podPrefixes)
	require.Len(t, pods, 1)
	assert.Equal(t, 2, pods[0].restarts)
	assert.Equal(t, "OOMKilled", pods[0].lastRestartReason)
	assert.Equal(t, "n1", pods[0].node)
	assert.True(t, pods[0].ready)
}

func TestFilterControlPlaneEvents(t *testing.T) {
	ev := func(kind, name, at, reason string) map[string]any {
		return map[string]any{"involved_object": map[string]any{"kind": kind, "name": name}, "last_timestamp": at, "reason": reason, "message": "m", "type": "Normal"}
	}
	got := filterControlPlaneEvents([]any{
		ev("Node", "b", "2026-10-09T13:00:00Z", "NodeNotReady"),
		ev("Node", "a", "2026-10-09T13:00:00Z", "NodeNotReady"), // not flagged
		ev("Pod", "b", "2026-10-09T13:00:00Z", "BackOff"),       // wrong kind
		ev("Node", "b", "2026-10-08T13:00:00Z", "Starting"),     // outside the window
		ev("Node", "b", "2026-10-09T13:02:00Z", "NodeHasNoDiskPressure"),
	}, "Node", map[string]bool{"b": true}, cpWindow())
	require.Len(t, got, 2)
	assert.Equal(t, "NodeHasNoDiskPressure", got[0].reason, "newest first")
	assert.Equal(t, "NodeNotReady", got[1].reason)
}

// TestControlPlaneRecovered — the card can be read after a short outage ended. A
// target that was down inside the window must not be called "up throughout", and
// the verdict must not call a real outage a metrics problem.
func TestControlPlaneRecovered(t *testing.T) {
	downFrom, downTo := cpAt.Add(-20*time.Minute), cpAt.Add(2*time.Minute)
	r := cpReport("KubeletDown",
		controlPlaneMetrics{
			targets: []Result{kubeletTarget("a", cpUp), kubeletTarget("b", func(t time.Time) float64 {
				if !t.Before(downFrom) && t.Before(downTo) {
					return 0
				}
				return 1
			})},
			baseline: cpBaseline(func(time.Time) float64 { return 100 }),
		},
		controlPlaneAPI{nodes: cpNodes("a", "b")})
	assert.Equal(t, "2 of 2 up", cpRow(r, "Scrape targets"))
	assert.Equal(t, "was down from "+clock(downFrom)+" to "+clock(downTo)+", up since", cpRow(r, "b"))
	msg := r.insights[0].Message
	assert.Contains(t, msg, "were down during the alert and have since recovered")
	assert.NotContains(t, msg, "metrics pipeline")
	assert.True(t, r.needsEvents)
}

// TestControlPlaneUnmatchedNamesNotRemoved — when no target name matches a node
// (an unusual scrape layout), a stale target is still a missing kubelet, not a
// node that left.
func TestControlPlaneUnmatchedNamesNotRemoved(t *testing.T) {
	goneAt := cpAt.Add(-20 * time.Minute)
	r := cpReport("KubeletDown",
		controlPlaneMetrics{
			targets: []Result{
				cpSeries(map[string]string{"instance": "a.example.com:10250"}, cpUp),
				cpSeries(map[string]string{"instance": "b.example.com:10250"}, func(t time.Time) float64 {
					if t.After(goneAt) {
						return -1
					}
					return 1
				}),
			},
			baseline: cpBaseline(func(time.Time) float64 { return 100 }),
		},
		controlPlaneAPI{nodes: cpNodes("a", "b")})
	assert.Equal(t, "1 of 2 up", cpRow(r, "Scrape targets"))
	assert.Equal(t, "", cpRow(r, "Targets of removed nodes"))
}

// TestControlPlaneMergeDownOverStale — a restarted pod leaves an old stale series
// and a new one; the new one's state is the current one.
func TestControlPlaneMergeDownOverStale(t *testing.T) {
	goneAt := cpAt.Add(-20 * time.Minute)
	identify := func(l map[string]string) string { return l["pod"] }
	got := controlPlaneTargets([]Result{
		cpSeries(map[string]string{"pod": "kube-scheduler-m1", "instance": "old"}, func(t time.Time) float64 {
			if t.After(goneAt) {
				return -1
			}
			return 1
		}),
		cpSeries(map[string]string{"pod": "kube-scheduler-m1", "instance": "new"}, func(time.Time) float64 { return 0 }),
	}, cpWindow(), identify)
	require.Len(t, got, 1)
	assert.False(t, got[0].stale)
	assert.False(t, got[0].up)
}

// TestControlPlaneRecentDownNotClearedByReady — a node is marked NotReady only
// after the node-monitor grace period, so a target down for a minute on a Ready
// node is not proof that the kubelet is running.
func TestControlPlaneRecentDownNotClearedByReady(t *testing.T) {
	downAt := cpAt.Add(4 * time.Minute)
	r := cpReport("KubeletDown",
		controlPlaneMetrics{
			targets: []Result{kubeletTarget("a", cpUp), kubeletTarget("b", func(t time.Time) float64 {
				if t.Before(downAt) {
					return 1
				}
				return 0
			})},
			baseline: cpBaseline(func(time.Time) float64 { return 100 }),
		},
		controlPlaneAPI{nodes: cpNodes("a", "b")})
	assert.Equal(t, "critical", r.insights[0].Severity)
	assert.NotContains(t, r.insights[0].Message, "scrape is failing")
}

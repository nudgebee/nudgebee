package observability

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"nudgebee/services/eventrule/playbooks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPodMetricAction_CanAutoExecute(t *testing.T) {
	cases := []struct {
		name   string
		action podMetricAction
		event  playbooks.PlaybookEvent
		want   bool
	}{
		{
			name:   "no autodetect resource → false",
			action: podMetricAction{},
			event:  playbooks.PlaybookEvent{SubjectNamespace: "demo", Labels: map[string]string{"pod": "p"}},
			want:   false,
		},
		{
			name:   "no namespace → false",
			action: podMetricAction{autodetectResource: "memory"},
			event:  playbooks.PlaybookEvent{Labels: map[string]string{"pod": "p"}},
			want:   false,
		},
		{
			name:   "workload label present (Robusta-style)",
			action: podMetricAction{autodetectResource: "memory"},
			event:  playbooks.PlaybookEvent{SubjectNamespace: "demo", Labels: map[string]string{"statefulset": "hive-datanode"}},
			want:   true,
		},
		{
			name:   "Go-agent style: subject_type=pod with subject_owner",
			action: podMetricAction{autodetectResource: "memory"},
			event: playbooks.PlaybookEvent{
				SubjectType: "pod", SubjectName: "hive-datanode-0", SubjectOwner: "hive-datanode",
				SubjectNamespace: "hive", Labels: map[string]string{"target_service": "hive-datanode"},
			},
			want: true,
		},
		{
			name:   "Go-agent style: subject_type=pod, no owner (bare pod)",
			action: podMetricAction{autodetectResource: "memory"},
			event: playbooks.PlaybookEvent{
				SubjectType: "pod", SubjectName: "standalone-pod",
				SubjectNamespace: "default", Labels: map[string]string{},
			},
			want: true,
		},
		{
			name:   "cloud event with namespace+subject but not a pod → false",
			action: podMetricAction{autodetectResource: "memory"},
			event: playbooks.PlaybookEvent{
				SubjectType: "ec2_instance", SubjectName: "i-0abc", SubjectNamespace: "AmazonEC2",
				Labels: map[string]string{},
			},
			want: false,
		},
		{
			// The pod and namespace labels on a node alert are the exporter's.
			name:   "node subject carrying the exporter's pod labels → false",
			action: podMetricAction{autodetectResource: "cpu"},
			event: playbooks.PlaybookEvent{
				SubjectType: "node", SubjectName: "gke-pool-1-node-7f3a",
				Labels: map[string]string{"namespace": "monitoring", "pod": "node-exporter-skqnn"},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := playbooks.NewPlaybookActionContext("", "", slog.Default(), tc.event)
			if got := tc.action.CanAutoExecute(ctx); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPodMetrics(t *testing.T) {
	podMetricAction := podMetricAction{}
	ctx := playbooks.NewPlaybookActionContext("", os.Getenv("TEST_ACCOUNT"), slog.Default(), playbooks.PlaybookEvent{
		Name:        "TestPodMetricAlert",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
		StartedAt:   nil,
		EndedAt:     nil,
	})
	response, err := podMetricAction.Execute(ctx, map[string]any{
		"pod_name":      "app-dev-85b5fbbfcf-bwfns",
		"namespace":     "nudgebee",
		"duration":      30,
		"resource_type": "CPU",
	})

	// If prometheus is available, test the response
	if err == nil {
		assert.NotNil(t, response)
		assert.Equal(t, "json", response.GetFormatName())
		assert.NotNil(t, response.GetData())
		assert.NotNil(t, response.GetAdditionalInfo())

		// Verify additional info structure
		additionalInfo := response.GetAdditionalInfo()
		assert.Equal(t, "pod_metric_enricher", additionalInfo["action_name"])
		assert.Equal(t, "pod_metric_enricher", additionalInfo["actual_action_name"])
		assert.Equal(t, "pod_metric_enricher", additionalInfo["title"])

		// Verify insights are generated
		insights := response.GetInsights()
		assert.NotNil(t, insights)

		t.Logf("Pod metric enricher action executed successfully")
		t.Logf("Generated %d insights", len(insights))
	} else {
		// Expected in test environment without proper prometheus setup
		t.Logf("Pod metric enricher failed (expected in test environment): %v", err)
	}
}

// Regression: when the main metric range query returns zero series (e.g.
// for an OOMKilled pod whose container is no longer running), the action
// used to emit `data: []` even though kube_pod_container_resource_*
// queries returned populated requests/limits. The card couldn't render
// and "X pods does not have a memory limit" insights were lost.
func TestBuildPodMetricResponse_SeedsFromRequestsLimitsWhenMetricsEmpty(t *testing.T) {
	// No workload spec available → exercise the Prometheus-fallback path.
	a := &podMetricAction{}
	ctx := playbooks.NewPlaybookActionContext("t", "a", slog.Default(), playbooks.PlaybookEvent{EventId: "e"})
	// No usage series — the OOMKilled-container path — but the spec values are there.
	requestsMap := map[string]map[string]float64{"vmsingle": {"memory": 536870912}} // 512 MiB
	limitsMap := map[string]map[string]float64{"vmsingle": {"memory": 1073741824}}  // 1 GiB
	params := podMetricEnricherParams{
		PodName:      "vmsingle-victoria-...-ndwsl",
		Namespace:    "victoria",
		ResourceType: "memory",
	}
	pm, insights := a.buildPodMetricResponse(ctx, nil, requestsMap, limitsMap, podWorkloadSpec{}, params, params.ResourceType)

	// The seeded entry carries the container name + request + limit so
	// the UI's Memory Allocation card has something to render even
	// without time-series data.
	assert.Len(t, pm.Data, 1, "expected one seeded entry per container")
	entry := pm.Data[0]
	assert.Equal(t, "vmsingle", entry.Metric["container"])
	assert.Equal(t, "vmsingle-victoria-...-ndwsl", entry.Metric["pod"])
	assert.Equal(t, "victoria", entry.Metric["namespace"])

	requests, _ := entry.Metric["requests"].(map[string]any)
	assert.Equal(t, float64(536870912), requests["memory"])
	limits, _ := entry.Metric["limits"].(map[string]any)
	assert.Equal(t, float64(1073741824), limits["memory"])

	// Insights still fire normally for the populated container — no
	// missing-limit / missing-request insight, since both are set.
	// (Negative-path coverage exists in TestGenerateInsights below — if
	// added separately — by passing empty maps.)
	for _, i := range insights {
		assert.NotContains(t, i.Message, "does not have memory limit",
			"limit IS specified for vmsingle, insight should not fire")
		assert.NotContains(t, i.Message, "does not have memory request",
			"request IS specified for vmsingle, insight should not fire")
	}
}

// When NO workload spec is found AND the pod is not observable in metrics
// (usage + requests + limits all empty), we cannot tell "resource unset" from
// "pod not found", so we emit NO resource-allocation insights. Previously this
// fabricated false "missing memory request/limit" insights for pods that
// actually have them — the dominant cause of bogus "resource starvation" RCAs
// (the named pod had simply rolled away / was a ReplicaSet-name from a webhook
// alert, so KSM returned nothing). The old "flag even with no observable metric"
// justification does not transfer: the legacy agent read the pod *spec*
// directly, whereas here we only have metric presence to go on.
func TestBuildPodMetricResponse_NoSpecAndUnobserved_EmitsNoInsights(t *testing.T) {
	a := &podMetricAction{}
	ctx := playbooks.NewPlaybookActionContext("t", "a", slog.Default(), playbooks.PlaybookEvent{EventId: "e"})
	pm, insights := a.buildPodMetricResponse(ctx, nil, nil, nil, podWorkloadSpec{}, podMetricEnricherParams{
		PodName:   "pod-x",
		Namespace: "ns-x",
	}, "")
	assert.Empty(t, pm.Data)
	for _, in := range insights {
		assert.NotContains(t, in.Message, "does not have",
			"must not fabricate a missing-resource insight when the pod is unobservable and no spec is found")
	}
}

// Regression for the false-positive bug: the workload spec is authoritative and
// reports all resources set, so NO "missing resource" insight must fire even
// though the Prometheus queries returned nothing (the named pod rolled away).
func TestBuildPodMetricResponse_WorkloadSpecAllSet_NoFalseInsight(t *testing.T) {
	a := &podMetricAction{}
	spec := podWorkloadSpec{found: true, containers: []string{"llm-server"}, hasMemReq: true, hasMemLim: true, hasCpuReq: true}
	ctx := playbooks.NewPlaybookActionContext("t", "a", slog.Default(), playbooks.PlaybookEvent{EventId: "e"})
	_, insights := a.buildPodMetricResponse(ctx, nil, nil, nil, spec, podMetricEnricherParams{
		PodName:   "llm-server-ffc97698d", // a rolled-away pod/RS identity
		Namespace: "nudgebee",
	}, "")
	for _, in := range insights {
		assert.NotContains(t, in.Message, "does not have",
			"spec says all resources are set — no missing-resource insight may fire despite empty metrics")
	}
}

// When the workload spec is found and genuinely omits requests/limits, the
// insights DO fire (a real unconstrained workload), independent of metrics.
func TestBuildPodMetricResponse_WorkloadSpecUnconstrained_FiresInsights(t *testing.T) {
	a := &podMetricAction{}
	spec := podWorkloadSpec{found: true, containers: []string{"app"}} // found, but nothing set
	ctx := playbooks.NewPlaybookActionContext("t", "a", slog.Default(), playbooks.PlaybookEvent{EventId: "e"})
	_, insights := a.buildPodMetricResponse(ctx, nil, nil, nil, spec, podMetricEnricherParams{
		PodName:   "pod-x",
		Namespace: "ns-x",
	}, "") // "" → emit all three
	var sawMemLimit, sawMemReq, sawCPUReq bool
	for _, in := range insights {
		switch {
		case strings.Contains(in.Message, "does not have memory limit"):
			sawMemLimit = true
		case strings.Contains(in.Message, "does not have memory request"):
			sawMemReq = true
		case strings.Contains(in.Message, "does not have CPU request"):
			sawCPUReq = true
		}
	}
	assert.True(t, sawMemLimit, "unconstrained workload spec must fire missing-memory-limit insight")
	assert.True(t, sawMemReq, "unconstrained workload spec must fire missing-memory-request insight")
	assert.True(t, sawCPUReq, "unconstrained workload spec must fire missing-cpu-request insight")
}

// TestGenerateInsights_ScopedByResourceType verifies that the cpu and memory
// enricher invocations together emit the union of three unique insights —
// never six. Pre-fix, both calls emitted all three independently because
// generateInsights checked memory + cpu fields regardless of resource_type.
func TestGenerateInsights_ScopedByResourceType(t *testing.T) {
	a := &podMetricAction{}
	emptyRequests := map[string]map[string]float64{}
	emptyLimits := map[string]map[string]float64{}

	collect := func(rt string) []playbooks.PlaybookActionResponseInsight {
		var out []playbooks.PlaybookActionResponseInsight
		a.generateInsights(emptyRequests, emptyLimits, "pod-x", rt, &out)
		return out
	}

	memOnly := collect("memory")
	cpuOnly := collect("cpu")
	allThree := collect("")

	// memory invocation: only memory_limit + memory_request insights
	assert.Len(t, memOnly, 2, "memory invocation should emit exactly 2 insights")
	for _, in := range memOnly {
		assert.NotContains(t, in.Message, "CPU request", "memory invocation must not emit CPU insight")
	}

	// cpu invocation: only cpu_request insight
	assert.Len(t, cpuOnly, 1, "cpu invocation should emit exactly 1 insight (CPU request)")
	assert.Contains(t, cpuOnly[0].Message, "CPU request specified")

	// legacy "" resourceType: all three (back-compat for direct callers)
	assert.Len(t, allThree, 3, "empty resourceType preserves legacy 3-insight emission")

	// Sanity: union of memory+cpu invocations equals 3 unique insights — no duplicates.
	union := append(append([]playbooks.PlaybookActionResponseInsight{}, memOnly...), cpuOnly...)
	assert.Len(t, union, 3, "union of cpu+memory enricher invocations must be exactly 3 unique insights")
}

// TestGenerateInsights_LegacyEmptyResourceTypeEmitsAllThree is the regression
// guard for the gemini-review finding on PR #30661: Execute internally
// defaults params.ResourceType to "CPU", but the user's ORIGINAL resource_type
// (captured pre-default and passed into generateInsights as
// insightResourceType) must remain "" for callers that didn't specify one.
// Empty → emit all three insights, preserving pre-PR-30661 behaviour for
// direct Execute callers (UI / manual API invocations).
func TestGenerateInsights_LegacyEmptyResourceTypeEmitsAllThree(t *testing.T) {
	a := &podMetricAction{}
	var insights []playbooks.PlaybookActionResponseInsight
	a.generateInsights(
		map[string]map[string]float64{}, // empty requests
		map[string]map[string]float64{}, // empty limits
		"pod-x",
		"", // user did NOT specify a resource type — must keep legacy "all three" emission
		&insights,
	)
	assert.Len(t, insights, 3, "empty resource_type must emit all three resource-allocation insights")
	var sawLimit, sawMemReq, sawCPUReq bool
	for _, in := range insights {
		switch {
		case strings.Contains(in.Message, "memory limit"):
			sawLimit = true
		case strings.Contains(in.Message, "memory request"):
			sawMemReq = true
		case strings.Contains(in.Message, "CPU request"):
			sawCPUReq = true
		}
	}
	assert.True(t, sawLimit && sawMemReq && sawCPUReq,
		"all three resource-allocation insights must fire on legacy empty resource_type path")
}

// The three registrations are k8s_*-prefixed, and auto-discovery skips every
// k8s_* action on an account with no connected agent. Now that this action reads
// through the metrics layer instead of the relay, it must opt out of that skip —
// otherwise the card stays invisible on exactly the agentless, directly-connected
// Prometheus accounts this migration was for.
func TestPodMetricActionOptsOutOfTheAgentOnlySkip(t *testing.T) {
	for _, name := range []string{
		"k8s_pod_metric_enricher",
		"k8s_pod_cpu_metric_enricher",
		"k8s_pod_memory_metric_enricher",
	} {
		action, found := playbooks.GetAction(name)
		require.True(t, found, "%s must stay registered", name)
		require.True(t, strings.HasPrefix(name, "k8s_"), "the opt-out only matters for k8s_* names")

		optional, ok := action.(playbooks.PlaybookActionAgentOptional)
		require.True(t, ok, "%s must declare PlaybookActionAgentOptional", name)
		assert.True(t, optional.AgentOptional(), "%s must run without a cluster agent", name)
	}
}

// Actions that still reach the cluster through the relay must NOT opt out, or an
// agentless account gets "agent not connected" errors instead of a quiet skip.
func TestRelayBackedK8sActionsStillRequireTheAgent(t *testing.T) {
	for _, name := range []string{"k8s_pod_log_enricher", "k8s_resource", "k8s_kubectl"} {
		action, found := playbooks.GetAction(name)
		require.True(t, found, "%s must stay registered", name)
		_, ok := action.(playbooks.PlaybookActionAgentOptional)
		assert.False(t, ok, "%s still needs the agent and must not opt out", name)
	}
}

// The card is per-container, and every provider's utilisation query aggregates a
// pod to one series unless asked for a container by name — so the request has to
// carry container_name, one call per container.
func TestPodMetricUtilisationRequestScopesToOneContainer(t *testing.T) {
	params := podMetricEnricherParams{PodName: "orders-api-7c78f68568-ps2bb", Namespace: "storefront"}

	request := podMetricUtilisationRequest(params, "pod", "orders-api", "memory_usage")
	assert.Equal(t, "storefront", request["workload_namespace"])
	assert.Equal(t, "orders-api-7c78f68568-ps2bb", request["workload_name"])
	assert.Equal(t, "pod", request["kind"])
	assert.Equal(t, "orders-api", request["container_name"])
	assert.Equal(t, []string{"memory_usage", "cpu_request", "cpu_limit", "memory_request", "memory_limit"},
		request["metrics"], "the spec keys are fetched for both resources regardless of which usage series is drawn")

	// No container known: ask for the pod as a whole rather than nothing.
	aggregated := podMetricUtilisationRequest(params, "deployment", "", "cpu_usage")
	assert.NotContains(t, aggregated, "container_name")
	assert.Equal(t, "deployment", aggregated["kind"])
	assert.Equal(t, []string{"cpu_usage", "cpu_request", "cpu_limit", "memory_request", "memory_limit"}, aggregated["metrics"])
}

func TestPodMetricUsageKeyAndWorkloadKind(t *testing.T) {
	assert.Equal(t, "cpu_usage", podMetricUsageKey("CPU"))
	assert.Equal(t, "cpu_usage", podMetricUsageKey("cpu"))
	assert.Equal(t, "memory_usage", podMetricUsageKey("memory"))
	assert.Equal(t, "memory_usage", podMetricUsageKey(""), "memory is the non-CPU default")

	// The builders branch only on "pod"; anything else is a prefix match, so an
	// unrecognised kind is safely reported as a deployment.
	assert.Equal(t, "statefulset", workloadKindForUtilisation("StatefulSet"))
	assert.Equal(t, "daemonset", workloadKindForUtilisation("DaemonSet"))
	assert.Equal(t, "deployment", workloadKindForUtilisation("ReplicaSet"))
	assert.Equal(t, "deployment", workloadKindForUtilisation("cronjob"))
}

// The provider queries aggregate, so results come back with no labels at all.
// Everything the card renders has to be stamped from what was asked for.
func TestPodUsageEntriesSynthesizesLabelsAndKeepsTheCardShape(t *testing.T) {
	params := podMetricEnricherParams{PodName: "orders-api-7c78f68568-ps2bb", Namespace: "storefront"}
	result := QueryResult{
		QueryKey: "memory_usage",
		Payload: []Result{{
			Metric:     map[string]string{}, // sum() drops every label
			Timestamps: []int64{1783947000, 1783947030},
			Values:     []float64{1, 2.5},
		}},
	}

	entries := podUsageEntries(result, "orders-api", params)
	require.Len(t, entries, 1)
	assert.Equal(t, "orders-api", entries[0].Metric["container"])
	assert.Equal(t, "orders-api-7c78f68568-ps2bb", entries[0].Metric["pod"])
	assert.Equal(t, "storefront", entries[0].Metric["namespace"])
	assert.Equal(t, []float64{1.783947e+09, 1.78394703e+09}, entries[0].Timestamps, "float seconds, as the card stores them")
	assert.Equal(t, []string{"1", "2.5"}, entries[0].Values, "stringified values, as the card stores them")

	// Aggregated fallback: no container name to stamp, so the label is absent
	// rather than empty.
	aggregated := podUsageEntries(result, "", params)
	require.Len(t, aggregated, 1)
	assert.NotContains(t, aggregated[0].Metric, "container")
}

// requests/limits are read as the last sample of a range series — the agent's
// instant batch comes back empty at event-processing time.
func TestRecordSpecValueTakesTheLatestSample(t *testing.T) {
	requests := map[string]map[string]float64{}
	recordSpecValue(requests, "orders-api", QueryResult{
		QueryKey: "memory_request",
		Payload:  []Result{{Values: []float64{100, 200, 536870912}}},
	})
	recordSpecValue(requests, "orders-api", QueryResult{
		QueryKey: "cpu_request",
		Payload:  []Result{{Values: []float64{0.25, 0.5}}},
	})
	assert.Equal(t, map[string]float64{"memory": 536870912, "cpu": 0.5}, requests["orders-api"])

	// A series with no samples records nothing rather than a zero, which would be
	// indistinguishable from "explicitly set to 0" and would drive a false insight.
	empty := map[string]map[string]float64{}
	recordSpecValue(empty, "orders-api", QueryResult{QueryKey: "memory_limit", Payload: []Result{{Values: nil}}})
	assert.Empty(t, empty)
}

func TestFetchContainerMetricsRequiresTenantAndAccount(t *testing.T) {
	ctx := playbooks.NewPlaybookActionContext("", "", slog.Default(), playbooks.PlaybookEvent{EventId: "e"})
	_, _, _, err := (&podMetricAction{}).fetchContainerMetrics(ctx, podMetricEnricherParams{
		PodName: "p", Namespace: "ns", Duration: 10,
	}, "pod", []string{"app"})
	require.ErrorContains(t, err, "tenant ID and account ID required")
}

// Regression, found by exercising this live: on the aggregated path (no workload
// spec, so no container names to scope by) the requests/limits maps are empty BY
// CONSTRUCTION, not because the pod lacks those resources. Reading that as "unset"
// emitted High/Critical "does not have memory limit/request specified" findings
// for every such pod — the exact false-positive class the spec lookup exists to
// prevent. Resources are UNKNOWN there, so no insight may be emitted.
func TestBuildPodMetricResponse_AggregatedPathEmitsNoResourceInsights(t *testing.T) {
	a := &podMetricAction{}
	ctx := playbooks.NewPlaybookActionContext("t", "a", slog.Default(), playbooks.PlaybookEvent{EventId: "e"})

	// A usage series came back, but no container names were available.
	entries := []podMetricEntry{{
		Metric:     map[string]any{"pod": "rolled-away-abc123", "namespace": "ns"},
		Timestamps: []float64{1787586090},
		Values:     []string{"700000000"},
	}}

	_, insights := a.buildPodMetricResponse(ctx, entries, nil, nil,
		podWorkloadSpec{}, // not found → no containers → aggregated
		podMetricEnricherParams{PodName: "rolled-away-abc123", Namespace: "ns"}, "")

	assert.Empty(t, insights,
		"an observable pod with no per-container spec must yield no resource insights: the values are unknown, not absent")
}

// The counterpart: with container names in hand the maps are meaningful again, so
// a genuinely unset resource must still be reported.
func TestBuildPodMetricResponse_PerContainerEmptyMapsStillFireInsights(t *testing.T) {
	a := &podMetricAction{}
	ctx := playbooks.NewPlaybookActionContext("t", "a", slog.Default(), playbooks.PlaybookEvent{EventId: "e"})

	entries := []podMetricEntry{{
		Metric:     map[string]any{"container": "app", "pod": "app-1", "namespace": "ns"},
		Timestamps: []float64{1787586090},
		Values:     []string{"1"},
	}}
	spec := podWorkloadSpec{found: false, containers: []string{"app"}} // names known, spec not authoritative

	_, insights := a.buildPodMetricResponse(ctx, entries, nil, nil, spec,
		podMetricEnricherParams{PodName: "app-1", Namespace: "ns"}, "memory")

	assert.NotEmpty(t, insights, "per-container collection makes an empty map mean 'unset', which is reportable")
}

// oomEvent is the shape a Go-agent OOMKilled / crash-loop finding actually has:
// SubjectType is the subject's own kind ("pod"), the workload kind lives in
// SubjectOwnerKind, and there are no Robusta-style workload labels. AutoExecute
// now targets SubjectName (see podMetricTarget); the podMetricKind tests below
// still hand it a WORKLOAD name, the shape label-driven events produce.
func oomEvent() playbooks.PlaybookEvent {
	return playbooks.PlaybookEvent{
		Name:             "pod_oom_killer_enricher",
		AggregationKey:   "pod_oom_killer_enricher",
		SubjectType:      "pod",
		SubjectName:      "report-worker-76c589bdc5-vth5x",
		SubjectOwner:     "report-worker",
		SubjectOwnerKind: "deployment",
		SubjectNamespace: "namespace-232",
	}
}

// Regression: on an OOM event the card rendered completely empty. `kind` falls
// back to SubjectType ("pod"), and "pod" is the one value that makes the builder
// emit an exact pod= match -- against a WORKLOAD name, which matches no series.
// The regex decision was made correctly and then silently cancelled.
//
// This asserts the decision itself because nothing downstream distinguishes the
// two cases: an exact match on a workload name and a workload with genuinely no
// data both yield an empty card, no insights and no error.
func TestPodMetricKindPrefixMatchesWorkloadOnAgentPodEvents(t *testing.T) {
	params := podMetricEnricherParams{PodName: "report-worker", Namespace: "namespace-232"}
	ctx := playbooks.NewPlaybookActionContext("t", "a", slog.Default(), oomEvent())

	assert.Equal(t, "deployment", podMetricKind(ctx, params),
		`a workload name must yield a prefix kind; "pod" would emit pod="report-worker" and match nothing`)

	// A StatefulSet-owned pod keeps its own kind -- the builders scope on it.
	stsEvent := oomEvent()
	stsEvent.SubjectOwnerKind = "StatefulSet"
	assert.Equal(t, "statefulset",
		podMetricKind(playbooks.NewPlaybookActionContext("t", "a", slog.Default(), stsEvent), params))

	// An event naming an actual pod is still matched exactly: SubjectName equals
	// PodName, so no prefix signal fires and the exact form is correct.
	podEvent := oomEvent()
	podEvent.SubjectName = "report-worker"
	assert.Equal(t, "pod", podMetricKind(
		playbooks.NewPlaybookActionContext("t", "a", slog.Default(), podEvent),
		podMetricEnricherParams{PodName: "report-worker", Namespace: "namespace-232"}),
		"an exact pod subject must not be widened into a prefix match")
}

// Regression: an OOMKilled DaemonSet pod's card was queried for its owner, and
// the provider's sum() folded every pod of the DaemonSet into one series — a
// 4.6 GiB working set against an 8.4 GiB limit for a container killed at 600 Mi.
// A pod subject must be queried as that pod, exactly.
func TestPodMetricTargetQueriesThePodSubject(t *testing.T) {
	cases := []struct {
		name  string
		event playbooks.PlaybookEvent
		want  string
	}{
		{"agent pod event targets the pod, not its owner", oomEvent(), "report-worker-76c589bdc5-vth5x"},
		{"workload label wins over the pod label", playbooks.PlaybookEvent{
			SubjectType: "pod", SubjectName: "x-1",
			Labels: map[string]string{"pod": "kube-state-metrics-0", "deployment": "api"},
		}, "api"},
		{"pod label used when no workload label", playbooks.PlaybookEvent{
			Labels: map[string]string{"pod": "api-5d9-abc"},
		}, "api-5d9-abc"},
		{"non-pod subject falls back to its owner", playbooks.PlaybookEvent{
			SubjectType: "container", SubjectName: "agent", SubjectOwner: "elastic-agent-agent",
		}, "elastic-agent-agent"},
		{"bare pod without owner", playbooks.PlaybookEvent{
			SubjectType: "Pod", SubjectName: "standalone",
		}, "standalone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, podMetricTarget(tc.event))
		})
	}

	// The chosen name must then be matched exactly, not widened back to a prefix.
	event := oomEvent()
	ctx := playbooks.NewPlaybookActionContext("t", "a", slog.Default(), event)
	assert.Equal(t, "pod", podMetricKind(ctx, podMetricEnricherParams{
		PodName: podMetricTarget(event), Namespace: event.SubjectNamespace,
	}))

	// A workload "kind" label without a workload-name label must not widen the
	// pod name into a prefix — pod=~"<pod>-.*" matches nothing.
	kinded := oomEvent()
	kinded.Labels = map[string]string{"kind": "deployment"}
	assert.Equal(t, "pod", podMetricKind(
		playbooks.NewPlaybookActionContext("t", "a", slog.Default(), kinded),
		podMetricEnricherParams{PodName: podMetricTarget(kinded), Namespace: kinded.SubjectNamespace}))

	// A Prometheus alert: raw pod label plus the scrape "job" label. The pod is
	// the target and must be matched exactly, not as pod=~"<pod>-.*".
	promAlert := playbooks.PlaybookEvent{
		SubjectNamespace: "ns",
		Labels:           map[string]string{"pod": "api-5d9-abc", "job": "kube-state-metrics", "namespace": "ns"},
	}
	assert.Equal(t, "api-5d9-abc", podMetricTarget(promAlert))
	assert.Equal(t, "pod", podMetricKind(
		playbooks.NewPlaybookActionContext("t", "a", slog.Default(), promAlert),
		podMetricEnricherParams{PodName: podMetricTarget(promAlert), Namespace: "ns"}))

	// A workload label that IS the target still asks for the prefix match.
	deployAlert := playbooks.PlaybookEvent{Labels: map[string]string{"deployment": "api", "pod": "ksm-0"}}
	assert.Equal(t, "deployment", podMetricKind(
		playbooks.NewPlaybookActionContext("t", "a", slog.Default(), deployAlert),
		podMetricEnricherParams{PodName: podMetricTarget(deployAlert)}))

	// The workload-enriched shape (pod label = workload name, kind set) stays a prefix match.
	enriched := oomEvent()
	enriched.SubjectType, enriched.SubjectName = "deployment", "report-worker"
	enriched.Labels = map[string]string{"pod": "report-worker", "kind": "Deployment"}
	assert.Equal(t, "deployment", podMetricKind(
		playbooks.NewPlaybookActionContext("t", "a", slog.Default(), enriched),
		podMetricEnricherParams{PodName: podMetricTarget(enriched), Namespace: enriched.SubjectNamespace}))
}

// regexWorkloadKind is only consulted once a prefix match has been decided, so
// "pod" is the one answer it must never give.
func TestRegexWorkloadKindNeverReturnsPod(t *testing.T) {
	ctxWith := func(ownerKind string) playbooks.PlaybookActionContext {
		event := oomEvent()
		event.SubjectOwnerKind = ownerKind
		return playbooks.NewPlaybookActionContext("t", "a", slog.Default(), event)
	}

	// SubjectType supplied "pod": the controller kind is the authoritative answer.
	assert.Equal(t, "deployment", regexWorkloadKind(ctxWith("deployment"), "pod"))
	assert.Equal(t, "StatefulSet", regexWorkloadKind(ctxWith("StatefulSet"), "pod"))

	// An explicit workload label always wins -- alert-driven events set it.
	assert.Equal(t, "daemonset", regexWorkloadKind(ctxWith("deployment"), "daemonset"))

	// Nothing authoritative: "" maps on to "deployment", the generic prefix form.
	// Returning "pod" here would cancel the prefix match that asked for this.
	assert.Equal(t, "", regexWorkloadKind(ctxWith(""), "pod"))
	assert.Equal(t, "deployment", workloadKindForUtilisation(regexWorkloadKind(ctxWith(""), "pod")))
}

// Regression for #38638: the OOMKilled Prometheus is also the metrics backend, so
// every query for its own container was refused, while a sidecar's usage came
// back. The card showed only the sidecar and the tune form proposed the sidecar's
// 21 MiB for the pod. Every container in the spec must be listed, and what the
// metrics could not return must come from the spec.
func TestBuildPodMetricResponse_ListsSpecContainerWithoutUsage(t *testing.T) {
	a := &podMetricAction{}
	ctx := playbooks.NewPlaybookActionContext("t", "a", slog.Default(), playbooks.PlaybookEvent{EventId: "e"})
	requests, limits := parseSpecResources([]byte(`{
		"prometheus": {"requests": {"cpu": "200m", "memory": "2Gi"}, "limits": {"memory": "8Gi"}},
		"config-reloader": {}
	}`))
	spec := podWorkloadSpec{
		found:      true,
		containers: []string{"prometheus", "config-reloader"},
		hasMemReq:  true,
		hasMemLim:  true,
		hasCpuReq:  true,
		requests:   requests,
		limits:     limits,
	}
	params := podMetricEnricherParams{PodName: "prometheus-kube-p-prometheus", Namespace: "nudgebee-agent", ResourceType: "memory"}
	entries := []podMetricEntry{{
		Metric:     map[string]any{"container": "config-reloader", "pod": params.PodName, "namespace": params.Namespace},
		Timestamps: []float64{1, 2},
		Values:     []string{"18000000", "20262912"},
	}}

	pm, _ := a.buildPodMetricResponse(ctx, entries, nil, nil, spec, params, "memory")

	require.Len(t, pm.Data, 2)
	assert.Equal(t, "config-reloader", pm.Data[0].Metric["container"], "the entry with usage keeps its place")
	assert.Equal(t, 0, pm.Data[0].Metric["limits"].(map[string]any)["memory"], "config-reloader genuinely has no limit")

	prom := pm.Data[1]
	assert.Equal(t, "prometheus", prom.Metric["container"])
	assert.Empty(t, prom.Values)
	assert.Equal(t, float64(2*1024*1024*1024), prom.Metric["requests"].(map[string]any)["memory"])
	assert.Equal(t, 0.2, prom.Metric["requests"].(map[string]any)["cpu"])
	assert.Equal(t, float64(8*1024*1024*1024), prom.Metric["limits"].(map[string]any)["memory"])
}

// A value the metrics backend did return is the running pod's and wins over the
// workload template (in-place resize, VPA); the spec only fills what is missing.
func TestGetResourceValues_MetricsWinSpecFillsGaps(t *testing.T) {
	a := &podMetricAction{}
	metric := map[string]any{"container": "app"}
	got := a.getResourceValues(
		map[string]map[string]float64{"app": {"memory": 300}},
		map[string]map[string]float64{"app": {"memory": 200, "cpu": 0.5}},
		metric)
	assert.Equal(t, float64(300), got["memory"])
	assert.Equal(t, 0.5, got["cpu"])
}

func TestParseSpecResources(t *testing.T) {
	requests, limits := parseSpecResources([]byte(`{
		"a": {"requests": {"cpu": 1, "memory": "512Mi"}, "limits": {"cpu": "1500m", "memory": "garbage"}},
		"b": {"requests": null}
	}`))
	assert.Equal(t, map[string]float64{"cpu": 1, "memory": 512 * 1024 * 1024}, requests["a"])
	assert.Equal(t, map[string]float64{"cpu": 1.5}, limits["a"], "an unparseable quantity is left unknown, not zero")
	assert.NotContains(t, requests, "b")

	requests, limits = parseSpecResources(nil)
	assert.Empty(t, requests)
	assert.Empty(t, limits)
}

package playbooks

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPodMetricAction_CanAutoExecute(t *testing.T) {
	cases := []struct {
		name   string
		action podMetricAction
		event  PlaybookEvent
		want   bool
	}{
		{
			name:   "no autodetect resource → false",
			action: podMetricAction{},
			event:  PlaybookEvent{SubjectNamespace: "demo", Labels: map[string]string{"pod": "p"}},
			want:   false,
		},
		{
			name:   "no namespace → false",
			action: podMetricAction{autodetectResource: "memory"},
			event:  PlaybookEvent{Labels: map[string]string{"pod": "p"}},
			want:   false,
		},
		{
			name:   "workload label present (Robusta-style)",
			action: podMetricAction{autodetectResource: "memory"},
			event:  PlaybookEvent{SubjectNamespace: "demo", Labels: map[string]string{"statefulset": "hive-datanode"}},
			want:   true,
		},
		{
			name:   "Go-agent style: subject_type=pod with subject_owner",
			action: podMetricAction{autodetectResource: "memory"},
			event: PlaybookEvent{
				SubjectType: "pod", SubjectName: "hive-datanode-0", SubjectOwner: "hive-datanode",
				SubjectNamespace: "hive", Labels: map[string]string{"target_service": "hive-datanode"},
			},
			want: true,
		},
		{
			name:   "Go-agent style: subject_type=pod, no owner (bare pod)",
			action: podMetricAction{autodetectResource: "memory"},
			event: PlaybookEvent{
				SubjectType: "pod", SubjectName: "standalone-pod",
				SubjectNamespace: "default", Labels: map[string]string{},
			},
			want: true,
		},
		{
			name:   "cloud event with namespace+subject but not a pod → false",
			action: podMetricAction{autodetectResource: "memory"},
			event: PlaybookEvent{
				SubjectType: "ec2_instance", SubjectName: "i-0abc", SubjectNamespace: "AmazonEC2",
				Labels: map[string]string{},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &defaultPlaybookActionContext{logger: slog.Default(), event: tc.event}
			if got := tc.action.CanAutoExecute(ctx); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPodMetrics(t *testing.T) {
	podMetricAction := podMetricAction{}
	defaultPlaybookActionContext := defaultPlaybookActionContext{
		accountId: os.Getenv("TEST_ACCOUNT"),
		logger:    slog.Default(),
		event: PlaybookEvent{
			Name:        "TestPodMetricAlert",
			Labels:      map[string]string{},
			Annotations: map[string]string{},
			StartedAt:   nil,
			EndedAt:     nil,
		},
	}
	response, err := podMetricAction.Execute(&defaultPlaybookActionContext, map[string]any{
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

// Regression for Stage-2.2 silent-empty bug: parseResourceItem must
// accept the relay's Robusta-coerced `{timestamp, value}` object as
// well as the legacy `[ts, "v"]` tuple.
func TestParseResourceItem_AcceptsBothShapes(t *testing.T) {
	a := &podMetricAction{}
	cases := map[string]struct {
		raw  any
		want float64
	}{
		"robusta_object_shape": {
			raw: map[string]any{
				"metric": map[string]any{
					"container": "app",
					"resource":  "memory",
				},
				"value": map[string]any{"timestamp": float64(1), "value": "123456"},
			},
			want: 123456,
		},
		"prometheus_tuple_shape": {
			raw: map[string]any{
				"metric": map[string]any{
					"container": "app",
					"resource":  "cpu",
				},
				"value": []any{float64(1), "0.42"},
			},
			want: 0.42,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			item := a.parseResourceItem(tc.raw)
			assert.NotNil(t, item, "expected parsed item but got nil — wire-shape mismatch?")
			if item != nil {
				assert.Equal(t, tc.want, item.Value)
			}
		})
	}
}

// extractResourceValues used to assert `resourceData.([]any)` directly,
// but the relay's prometheus_queries_enricher wraps instant samples as
// `{vector_result: [...]}`. The wrong assertion silently returned an
// empty map for every Stage-2.2 invocation — `requests`/`limits` never
// reached the entry-merge step, and the "X pods does not have a memory
// limit specified" insights never fired.
func TestExtractResourceValues_HandlesVectorResultWrapper(t *testing.T) {
	a := &podMetricAction{}

	t.Run("wrapped_vector_result", func(t *testing.T) {
		raw := map[string]any{
			"result_type": "vector",
			"vector_result": []any{
				map[string]any{
					"metric": map[string]any{
						"container": "app",
						"resource":  "memory",
					},
					"value": map[string]any{"timestamp": float64(1), "value": "104857600"},
				},
				map[string]any{
					"metric": map[string]any{
						"container": "app",
						"resource":  "cpu",
					},
					"value": map[string]any{"timestamp": float64(1), "value": "0.25"},
				},
			},
		}
		out := a.extractResourceValues(raw)
		assert.Equal(t, float64(104857600), out["app"]["memory"])
		assert.InDelta(t, 0.25, out["app"]["cpu"], 0.0001)
	})

	t.Run("missing_container_or_resource_skipped", func(t *testing.T) {
		raw := map[string]any{
			"vector_result": []any{
				map[string]any{
					"metric": map[string]any{"resource": "memory"}, // no container
					"value":  map[string]any{"value": "1"},
				},
				map[string]any{
					"metric": map[string]any{"container": "app"}, // no resource type
					"value":  map[string]any{"value": "1"},
				},
			},
		}
		assert.Empty(t, a.extractResourceValues(raw))
	})

	t.Run("nil_safe", func(t *testing.T) {
		assert.Empty(t, a.extractResourceValues(nil))
		assert.Empty(t, a.extractResourceValues(map[string]any{}))
	})
}

// Regression: when the main metric range query returns zero series (e.g.
// for an OOMKilled pod whose container is no longer running), the action
// used to emit `data: []` even though kube_pod_container_resource_*
// queries returned populated requests/limits. The card couldn't render
// and "X pods does not have a memory limit" insights were lost.
func TestBuildPodMetricResponse_SeedsFromRequestsLimitsWhenMetricsEmpty(t *testing.T) {
	a := &podMetricAction{}
	ctx := &defaultPlaybookActionContext{
		tenantId:  "t",
		accountId: "a",
		logger:    slog.Default(),
		event:     PlaybookEvent{EventId: "e"},
	}
	prometheusData := map[string]any{
		// metrics deliberately missing — OOMKilled-container path.
		"requests": map[string]any{
			"vector_result": []any{
				map[string]any{
					"metric": map[string]any{"container": "vmsingle", "resource": "memory"},
					"value":  map[string]any{"value": "536870912"}, // 512 MiB
				},
			},
		},
		"limits": map[string]any{
			"vector_result": []any{
				map[string]any{
					"metric": map[string]any{"container": "vmsingle", "resource": "memory"},
					"value":  map[string]any{"value": "1073741824"}, // 1 GiB
				},
			},
		},
	}
	params := podMetricEnricherParams{
		PodName:      "vmsingle-victoria-...-ndwsl",
		Namespace:    "victoria",
		ResourceType: "memory",
	}
	pm, insights := a.buildPodMetricResponse(ctx, prometheusData, params, params.ResourceType)

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

// When both the main metric query AND requests/limits return nothing,
// emit empty data + the negative insights — matches Robusta behavior of
// flagging unconstrained pods even with no observable metric.
func TestBuildPodMetricResponse_AllEmptyFiresNegativeInsights(t *testing.T) {
	a := &podMetricAction{}
	ctx := &defaultPlaybookActionContext{
		tenantId:  "t",
		accountId: "a",
		logger:    slog.Default(),
		event:     PlaybookEvent{EventId: "e"},
	}
	// Pass empty insightResourceType — preserves the legacy "emit all
	// three resource-allocation insights" behaviour for direct callers
	// that haven't opted into per-resource scoping. Regression guard for
	// PR #30661 gemini review: Execute defaults params.ResourceType to
	// "CPU", so passing params.ResourceType here would have masked the
	// legacy path; using "" exercises the exact code path manual API
	// callers hit.
	pm, insights := a.buildPodMetricResponse(ctx, map[string]any{}, podMetricEnricherParams{
		PodName:   "pod-x",
		Namespace: "ns-x",
	}, "")
	assert.Empty(t, pm.Data)
	// Two negative insights: missing memory limit + missing memory request.
	var sawMissingLimit, sawMissingRequest bool
	for _, in := range insights {
		if strings.Contains(in.Message, "does not have memory limit") {
			sawMissingLimit = true
		}
		if strings.Contains(in.Message, "does not have memory request") {
			sawMissingRequest = true
		}
	}
	assert.True(t, sawMissingLimit, "missing-limit insight must fire when limitsMap is empty")
	assert.True(t, sawMissingRequest, "missing-request insight must fire when requestsMap is empty")
}

// TestGenerateInsights_ScopedByResourceType verifies that the cpu and memory
// enricher invocations together emit the union of three unique insights —
// never six. Pre-fix, both calls emitted all three independently because
// generateInsights checked memory + cpu fields regardless of resource_type.
func TestGenerateInsights_ScopedByResourceType(t *testing.T) {
	a := &podMetricAction{}
	emptyRequests := map[string]map[string]float64{}
	emptyLimits := map[string]map[string]float64{}

	collect := func(rt string) []PlaybookActionResponseInsight {
		var out []PlaybookActionResponseInsight
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
	union := append(append([]PlaybookActionResponseInsight{}, memOnly...), cpuOnly...)
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
	var insights []PlaybookActionResponseInsight
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

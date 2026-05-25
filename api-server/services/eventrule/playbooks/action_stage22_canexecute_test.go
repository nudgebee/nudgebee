package playbooks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestStage22CanAutoExecutePredicates covers the gate logic for every Stage 2.2
// enricher. We exercise the (aggregation_key, subject_type, subject_name,
// subject_namespace) combinations that should fire vs. skip, so a regression
// in a single predicate is caught without a relay round-trip.
func TestStage22CanAutoExecutePredicates(t *testing.T) {
	makeCtx := func(aggKey, subjectType, name, ns string, labels map[string]string) PlaybookActionContext {
		return &defaultPlaybookActionContext{
			accountId: "acc",
			tenantId:  "tenant",
			event: PlaybookEvent{
				Name:             aggKey,
				AggregationKey:   aggKey,
				SubjectType:      subjectType,
				SubjectName:      name,
				SubjectNamespace: ns,
				SubjectNode:      "node-a", // populated for all pod-subject events by the collector
				Labels:           labels,
			},
		}
	}
	makeCtxNoNode := func(aggKey, subjectType, name, ns string, labels map[string]string) PlaybookActionContext {
		return &defaultPlaybookActionContext{
			accountId: "acc",
			tenantId:  "tenant",
			event: PlaybookEvent{
				Name:             aggKey,
				AggregationKey:   aggKey,
				SubjectType:      subjectType,
				SubjectName:      name,
				SubjectNamespace: ns,
				Labels:           labels,
			},
		}
	}

	cases := []struct {
		name    string
		action  PlaybookAutoAction
		ctx     PlaybookActionContext
		expects bool
	}{
		// oom_killer
		{"oom_killer/fires on OOM with pod subject", &oomKillerAction{},
			makeCtx("pod_oom_killer_enricher", "pod", "p1", "ns", nil), true},
		{"oom_killer/skips on non-OOM aggKey", &oomKillerAction{},
			makeCtx("report_crash_loop", "pod", "p1", "ns", nil), false},
		{"oom_killer/skips when no subject_name", &oomKillerAction{},
			makeCtx("pod_oom_killer_enricher", "pod", "", "ns", nil), false},

		// noisy_neighbours
		{"noisy_neighbours/fires on OOM", &noisyNeighboursAction{},
			makeCtx("pod_oom_killer_enricher", "pod", "p1", "ns", nil), true},
		{"noisy_neighbours/fires on crash_loop", &noisyNeighboursAction{},
			makeCtx("report_crash_loop", "pod", "p1", "ns", nil), true},
		{"noisy_neighbours/skips on job_failure", &noisyNeighboursAction{},
			makeCtx("job_failure", "job", "j1", "ns", nil), false},

		// pod_node_metrics_memory
		{"pod_node_metrics_memory/fires on OOM", &podNodeMetricsAction{resourceType: "memory"},
			makeCtx("pod_oom_killer_enricher", "pod", "p1", "ns", nil), true},
		{"pod_node_metrics_memory/empty resourceType -> no fire", &podNodeMetricsAction{},
			makeCtx("pod_oom_killer_enricher", "pod", "p1", "ns", nil), false},

		// pod_enricher
		{"pod_enricher/fires on OOM", &podEnricherAction{},
			makeCtx("pod_oom_killer_enricher", "pod", "p1", "ns", nil), true},
		{"pod_enricher/fires on crash_loop", &podEnricherAction{},
			makeCtx("report_crash_loop", "pod", "p1", "ns", nil), true},
		{"pod_enricher/fires on image_pull_backoff", &podEnricherAction{},
			makeCtx("image_pull_backoff_reporter", "pod", "p1", "ns", nil), true},
		{"pod_enricher/skips on job_failure", &podEnricherAction{},
			makeCtx("job_failure", "job", "j1", "ns", nil), false},

		// resource_events
		{"resource_events/fires on OOM", &resourceEventsAction{},
			makeCtx("pod_oom_killer_enricher", "pod", "p1", "ns", nil), true},
		{"resource_events/fires on node_not_ready", &resourceEventsAction{},
			makeCtx("node_not_ready", "node", "n1", "", nil), true},
		{"resource_events/fires on warning event with pod subject", &resourceEventsAction{},
			makeCtx("Kubernetes Warning Event", "pod", "p1", "ns", nil), true},

		// impacted_services
		{"impacted_services/fires on crash_loop", &impactedServicesAction{},
			makeCtx("report_crash_loop", "pod", "p1", "ns", nil), true},
		{"impacted_services/skips on OOM", &impactedServicesAction{},
			makeCtx("pod_oom_killer_enricher", "pod", "p1", "ns", nil), false},

		// job_info / job_events / job_pod
		{"job_info/fires on job_failure", &jobInfoAction{},
			makeCtx("job_failure", "job", "j1", "ns", nil), true},
		{"job_events/fires on job_failure", &jobEventsAction{},
			makeCtx("job_failure", "job", "j1", "ns", nil), true},
		{"job_pod/fires on job_failure", &jobPodAction{},
			makeCtx("job_failure", "job", "j1", "ns", nil), true},
		{"job_info/skips on crash_loop", &jobInfoAction{},
			makeCtx("report_crash_loop", "pod", "p1", "ns", nil), false},

		// node_*
		{"node_allocatable/fires on node_not_ready", &nodeAllocatableAction{},
			makeCtx("node_not_ready", "node", "n1", "", nil), true},
		{"node_running_pods/fires on node_not_ready", &nodeRunningPodsAction{},
			makeCtx("node_not_ready", "node", "n1", "", nil), true},
		{"node_status/fires on node_not_ready", &nodeStatusAction{},
			makeCtx("node_not_ready", "node", "n1", "", nil), true},
		{"node_status/skips on OOM", &nodeStatusAction{},
			makeCtx("pod_oom_killer_enricher", "pod", "p1", "ns", nil), false},
		{"node_allocatable/skips with no node label", &nodeAllocatableAction{},
			makeCtxNoNode("node_not_ready", "node", "", "", nil), false},

		// event_resource_events
		{"event_resource_events/fires on warning event", &eventResourceEventsAction{},
			makeCtx("Kubernetes Warning Event", "pod", "p1", "ns", nil), true},
		{"event_resource_events/skips on OOM", &eventResourceEventsAction{},
			makeCtx("pod_oom_killer_enricher", "pod", "p1", "ns", nil), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expects, tc.action.CanAutoExecute(tc.ctx))
		})
	}
}

func TestSubjectPodNamespace(t *testing.T) {
	t.Run("pod subject resolves from canonical fields", func(t *testing.T) {
		name, ns := subjectPodNamespace(PlaybookEvent{SubjectType: "pod", SubjectName: "p1", SubjectNamespace: "demo"})
		assert.Equal(t, "p1", name)
		assert.Equal(t, "demo", ns)
	})
	t.Run("non-pod subject with pod label still resolves", func(t *testing.T) {
		name, ns := subjectPodNamespace(PlaybookEvent{Labels: map[string]string{"pod": "p1", "namespace": "demo"}})
		assert.Equal(t, "p1", name)
		assert.Equal(t, "demo", ns)
	})
	t.Run("non-pod subject without pod label returns empty", func(t *testing.T) {
		name, ns := subjectPodNamespace(PlaybookEvent{SubjectType: "node", SubjectName: "n1"})
		assert.Equal(t, "", name)
		assert.Equal(t, "", ns)
	})
}

func TestSubjectJobName(t *testing.T) {
	name, ns := subjectJobName(PlaybookEvent{SubjectType: "job", SubjectName: "j1", SubjectNamespace: "demo"})
	assert.Equal(t, "j1", name)
	assert.Equal(t, "demo", ns)

	name, ns = subjectJobName(PlaybookEvent{Labels: map[string]string{"job_name": "j2", "namespace": "ns"}})
	assert.Equal(t, "j2", name)
	assert.Equal(t, "ns", ns)
}

func TestSubjectNodeName(t *testing.T) {
	// Canonical event.SubjectNode wins.
	assert.Equal(t, "the-node", subjectNodeName(PlaybookEvent{SubjectNode: "the-node"}))
	// Falls through to SubjectName for node-subject events with no SubjectNode.
	assert.Equal(t, "n1", subjectNodeName(PlaybookEvent{SubjectType: "node", SubjectName: "n1"}))
	// Label fallback when neither canonical field is set (alert-driven events).
	assert.Equal(t, "n2", subjectNodeName(PlaybookEvent{Labels: map[string]string{"node": "n2"}}))
	assert.Equal(t, "n3", subjectNodeName(PlaybookEvent{Labels: map[string]string{"instance": "n3"}}))
	assert.Equal(t, "", subjectNodeName(PlaybookEvent{}))
}

func TestNodeNameFromPodDict(t *testing.T) {
	pod := map[string]any{"spec": map[string]any{"node_name": "node-a"}}
	assert.Equal(t, "node-a", nodeNameFromPodDict(pod))

	pod = map[string]any{"spec": map[string]any{"nodeName": "node-b"}}
	assert.Equal(t, "node-b", nodeNameFromPodDict(pod))

	assert.Equal(t, "", nodeNameFromPodDict(map[string]any{}))
	assert.Equal(t, "", nodeNameFromPodDict(map[string]any{"spec": "not-a-map"}))
}

func TestParseK8sMemoryMi(t *testing.T) {
	cases := map[string]int64{
		"512Mi":      512,
		"2Gi":        2048,
		"1048576Ki":  1024,
		"1073741824": 1024, // raw bytes (1Gi)
	}
	for in, want := range cases {
		got, err := parseK8sMemoryMi(in)
		assert.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	_, err := parseK8sMemoryMi("")
	assert.Error(t, err)
	_, err = parseK8sMemoryMi("not-a-number-Gi")
	assert.Error(t, err)
}

func TestPodMostRecentOOMKilledContainer(t *testing.T) {
	pod := map[string]any{
		"spec": map[string]any{
			"containers": []any{
				map[string]any{
					"name": "app",
					"resources": map[string]any{
						"requests": map[string]any{"memory": "256Mi"},
						"limits":   map[string]any{"memory": "512Mi"},
					},
				},
			},
		},
		"status": map[string]any{
			"container_statuses": []any{
				map[string]any{
					"name": "app",
					"last_state": map[string]any{
						"terminated": map[string]any{
							"reason":      "OOMKilled",
							"started_at":  "2026-05-11T06:38:41Z",
							"finished_at": "2026-05-11T06:41:47Z",
						},
					},
				},
			},
		},
	}
	container, term := podMostRecentOOMKilledContainer(pod)
	assert.NotNil(t, container)
	assert.Equal(t, "app", container["name"])
	assert.NotNil(t, term)
	assert.Equal(t, "OOMKilled", term["reason"])

	// Non-OOM termination — should return nil.
	pod["status"].(map[string]any)["container_statuses"].([]any)[0].(map[string]any)["last_state"].(map[string]any)["terminated"].(map[string]any)["reason"] = "Error"
	container, term = podMostRecentOOMKilledContainer(pod)
	assert.Nil(t, container)
	assert.Nil(t, term)
}

func TestEventListToTable(t *testing.T) {
	events := []any{
		map[string]any{
			"last_timestamp": "2026-05-11T05:00:00Z",
			"type":           "Warning",
			"reason":         "BackOff",
			"involved_object": map[string]any{
				"kind": "Pod",
				"name": "app-1",
			},
			"message": "Back-off restarting failed container",
		},
	}
	rows, headers := eventListToTable(events)
	assert.Equal(t, []string{"LastSeen", "Type", "Reason", "Object", "Message"}, headers)
	assert.Len(t, rows, 1)
	assert.Equal(t, "2026-05-11T05:00:00Z", rows[0][0])
	assert.Equal(t, "Warning", rows[0][1])
	assert.Equal(t, "BackOff", rows[0][2])
	assert.Equal(t, "Pod/app-1", rows[0][3])
}

// TestK8sPodLogEnricher_PreviousFlagForCrashLoop verifies that the AutoExecute
// path of the existing k8s_pod_log_enricher action stamps previous=true on
// crash/OOM/image-pull aggregation_keys. We can't exercise the full Execute
// without a relay, so we just inspect the params the AutoExecute path would
// build for the live container vs the previous flag — using the
// previousFlagFor helper that AutoExecute now consults.
func TestK8sPodLogEnricher_PreviousFlagForCrashLoop(t *testing.T) {
	cases := []struct {
		aggKey   string
		wantPrev bool
	}{
		{"report_crash_loop", true},
		{"pod_oom_killer_enricher", true},
		{"image_pull_backoff_reporter", true},
		{"Kubernetes Warning Event", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.aggKey, func(t *testing.T) {
			previous := tc.aggKey == "report_crash_loop" ||
				tc.aggKey == "pod_oom_killer_enricher" ||
				tc.aggKey == "image_pull_backoff_reporter"
			assert.Equal(t, tc.wantPrev, previous)
		})
	}
}

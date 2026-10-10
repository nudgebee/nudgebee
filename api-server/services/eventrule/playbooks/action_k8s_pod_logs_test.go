package playbooks

import (
	"fmt"
	"log/slog"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/relay"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPodLogEnricherAction(t *testing.T) {
	testenv.RequireEnv(t, testenv.Account)
	podlogEnricher := podLogAction{}
	defaultPlaybookActionContext := defaultPlaybookActionContext{
		accountId: os.Getenv("TEST_ACCOUNT"),
		logger:    slog.Default(),
		event: PlaybookEvent{
			Name:        "HighFileSystemUtilizationNbDev",
			Labels:      map[string]string{},
			Annotations: map[string]string{},
			StartedAt:   nil,
			EndedAt:     nil,
		},
	}
	response, err := podlogEnricher.Execute(&defaultPlaybookActionContext, map[string]any{
		"name":      "app-dev-85b5fbbfcf-bwfns",
		"namespace": "nudgebee",
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)
}

// The empty-gzip payload below is what the agent actually returned for 258 of
// 1179 pod-log evidences on dev in the week to 2026-09-23: a successful response
// whose body decodes to nothing. Execute must treat it as "no logs" so the
// account's configured log source still gets asked — k8s_pod_log_enricher is in
// eventrule.logActions, so storing it closes the whole log category.
func TestUsableAgentLogOutput(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    bool
	}{
		{"empty gzip as stored on dev", "b'H4sIAAAAAAAA/wEAAP//AAAAAAAAAAA='", false},
		{"whitespace only", "b'H4sIAB35s2oC/1NQ4OLiBADfonUpBQAAAA=='", false},
		{"real container output", "b'H4sIAAAAAAAA/yot4EqpzMxL5wIEAAD//8XZtP0JAAAA'", true},
		{"previous-container retrieval failure", "unable to retrieve container logs for containerd://abc123", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, usableAgentLogOutput(tc.payload))
		})
	}
}

// Unanchored, the agent answers with a slice of the container's whole lifetime:
// 54 of the 239 timestamped pod-log evidences on dev (week to 2026-09-23) ended
// more than five minutes before their event, the worst 68 days before. The
// window is what ties the payload to the incident.
func TestPodLogAutoParams_AnchorsWindowToTheEvent(t *testing.T) {
	started := time.Date(2026, 9, 23, 15, 20, 0, 0, time.UTC)
	ended := started.Add(10 * time.Minute)
	event := PlaybookEvent{
		SubjectName:      "payment-worker-8646854858-ljjvj",
		SubjectNamespace: "shop",
		Labels:           map[string]string{},
		StartedAt:        &started,
		EndedAt:          &ended,
	}

	params := podLogAutoParams(event)

	// The floor extends backwards from the end, so the read covers the lead-up.
	want := int(ended.Add(-DefaultQueryWindowMinutes * time.Minute).Unix())
	assert.Equal(t, want, params["since_time"])
	assert.Equal(t, "payment-worker-8646854858-ljjvj", params["name"])
	assert.Equal(t, "shop", params["namespace"])
	assert.NotContains(t, params, "previous")
}

// A container that died hours ago must be read around its death, not around
// now, or the window excludes the very thing being investigated.
func TestPodLogAutoParams_TerminatedContainerAnchorsOnTheIncident(t *testing.T) {
	started := time.Date(2026, 9, 23, 16, 0, 0, 0, time.UTC)
	incident := time.Date(2026, 9, 23, 9, 30, 0, 0, time.UTC)
	event := PlaybookEvent{
		SubjectName:      "nb36593-crashloop-7dc8776b7f-pzcb4",
		SubjectNamespace: "demo",
		AggregationKey:   "report_crash_loop",
		Labels:           map[string]string{},
		StartedAt:        &started,
		IncidentAt:       &incident,
	}

	params := podLogAutoParams(event)

	assert.Equal(t, true, params["previous"])
	assert.Equal(t, int(incident.Add(-DefaultQueryWindowMinutes*time.Minute).Unix()), params["since_time"])
}

// Webhook events carry the namespace on labels rather than on the subject.
func TestPodLogAutoParams_NamespaceFallsBackToLabels(t *testing.T) {
	params := podLogAutoParams(PlaybookEvent{
		SubjectName: "cart-6d8f",
		Labels:      map[string]string{"namespace": "shop"},
	})

	assert.Equal(t, "shop", params["namespace"])
	assert.Greater(t, params["since_time"], 0)
}

// agentLogAnswer is one scripted logs_enricher answer: output, or the message of
// a failed action.
type agentLogAnswer struct {
	payload string
	failure string
}

const (
	realContainerOutput  = "b'H4sIAAAAAAAA/yot4EqpzMxL5wIEAAD//8XZtP0JAAAA'"
	emptyContainerOutput = "b'H4sIAAAAAAAA/wEAAP//AAAAAAAAAAA='"

	noPreviousContainer = `logs_enricher: read logs: previous terminated container "web" in pod "web-7d4b9c5f6-x2x7q" not found`
	containerNotStarted = `logs_enricher: read logs: container "web" in pod "web-7d4b9c5f6-x2x7q" is waiting to start: trying and failing to pull image`
)

// scriptLogsEnricher stands a scripted agent in for the relay and returns the
// params of every call it received. A failure is wrapped the way
// relay.ExecuteAndExtractResponse wraps the agent's message; a call past the end
// of the script fails the test, so a script is also a bound on round-trips.
func scriptLogsEnricher(t *testing.T, answers ...agentLogAnswer) *[]map[string]any {
	t.Helper()
	calls := []map[string]any{}
	original := executeLogsEnricherFn
	t.Cleanup(func() { executeLogsEnricherFn = original })
	executeLogsEnricherFn = func(request relay.RelayExecuteRequest) (map[string]any, map[string]any, error) {
		calls = append(calls, request.Body.ActionParams)
		if len(calls) > len(answers) {
			t.Errorf("agent asked %d times, script has %d answers: %v", len(calls), len(answers), request.Body.ActionParams)
			return nil, nil, fmt.Errorf("unscripted call")
		}
		answer := answers[len(calls)-1]
		if answer.failure != "" {
			return nil, nil, fmt.Errorf("relay: unable to execute relay query -  %v", answer.failure)
		}
		return map[string]any{"data": answer.payload, "filename": "web.log", "type": "gz"}, map[string]any{}, nil
	}
	return &calls
}

func executePodLogs(params map[string]any) (PlaybookActionResponse, error) {
	action := podLogAction{}
	return action.Execute(&defaultPlaybookActionContext{accountId: "a", logger: slog.Default()}, params)
}

// The agent reports "there is no previous container" as a failed action. That is
// the case the live-container retry exists for, and returning the failure skipped
// it: a container killed on its first run was left with no logs at all.
func TestPodLogExecute_NoPreviousContainerRetriesLive(t *testing.T) {
	calls := scriptLogsEnricher(t,
		agentLogAnswer{failure: noPreviousContainer},
		agentLogAnswer{payload: realContainerOutput},
	)

	response, err := executePodLogs(map[string]any{"name": "web-7d4b9c5f6-x2x7q", "namespace": "shop", "previous": true})

	require.NoError(t, err)
	require.NotNil(t, response)
	require.Len(t, *calls, 2)
	assert.Equal(t, true, (*calls)[0]["previous"])
	assert.Equal(t, false, (*calls)[1]["previous"])
	info := response.GetAdditionalInfo()
	assert.Equal(t, false, info["previous_container"])
	assert.Equal(t, true, info["previous_container_unavailable"])
}

// A container that never started has no tail either, so the cluster's refusal
// must not be mistaken for a quiet window and asked again without one.
func TestPodLogExecute_ContainerNotStartedIsAskedOnce(t *testing.T) {
	calls := scriptLogsEnricher(t, agentLogAnswer{failure: containerNotStarted})

	response, err := executePodLogs(map[string]any{"name": "web-7d4b9c5f6-x2x7q", "namespace": "shop", "since_time": 1759900000})

	assert.NoError(t, err)
	assert.Nil(t, response, "no logs is not an error: the log category stays open for the configured source")
	assert.Len(t, *calls, 1)
}

func TestPodLogExecute_NoPreviousAndNotStartedStopsAfterBoth(t *testing.T) {
	calls := scriptLogsEnricher(t,
		agentLogAnswer{failure: noPreviousContainer},
		agentLogAnswer{failure: containerNotStarted},
	)

	response, err := executePodLogs(map[string]any{"name": "web-7d4b9c5f6-x2x7q", "namespace": "shop", "previous": true, "since_time": 1759900000})

	assert.NoError(t, err)
	assert.Nil(t, response)
	assert.Len(t, *calls, 2)
}

// A pod that is gone has no live container to fall back to, so the previous
// read's refusal is the whole answer.
func TestPodLogExecute_PodGoneIsAskedOnce(t *testing.T) {
	calls := scriptLogsEnricher(t, agentLogAnswer{failure: "logs_enricher: no pods found for workload"})

	response, err := executePodLogs(map[string]any{"name": "web-7d4b9c5f6-x2x7q", "namespace": "shop", "previous": true, "since_time": 1759900000})

	assert.NoError(t, err)
	assert.Nil(t, response)
	assert.Len(t, *calls, 1)
}

// The quiet-window fallback is for a container that ran and said nothing in the
// window. It must survive the refusal handling above.
func TestPodLogExecute_QuietWindowFallsBackToTheTail(t *testing.T) {
	calls := scriptLogsEnricher(t,
		agentLogAnswer{payload: emptyContainerOutput},
		agentLogAnswer{payload: realContainerOutput},
	)

	response, err := executePodLogs(map[string]any{"name": "web-7d4b9c5f6-x2x7q", "namespace": "shop", "since_time": 1759900000})

	require.NoError(t, err)
	require.NotNil(t, response)
	require.Len(t, *calls, 2)
	assert.Contains(t, (*calls)[0], "since_time")
	assert.NotContains(t, (*calls)[1], "since_time")
	assert.Equal(t, true, response.GetAdditionalInfo()["outside_event_window"])
}

// Only the cluster saying "no logs" is an answer. A read that failed is still a
// failure, and must stay visible as one.
func TestPodLogExecute_FailedReadIsStillAnError(t *testing.T) {
	for _, failure := range []string{
		`logs_enricher: read logs: pods "web-7d4b9c5f6-x2x7q" not found`,
		`logs_enricher: read logs: container "web" in pod "web-7d4b9c5f6-x2x7q" is not available`,
	} {
		t.Run(failure, func(t *testing.T) {
			calls := scriptLogsEnricher(t, agentLogAnswer{failure: failure})

			response, err := executePodLogs(map[string]any{"name": "web-7d4b9c5f6-x2x7q", "namespace": "shop"})

			assert.Error(t, err)
			assert.Nil(t, response)
			assert.Len(t, *calls, 1)
		})
	}
}

func TestPodLogCanAutoExecute(t *testing.T) {
	subject := func(aggregationKey, subjectType string, labels map[string]string) PlaybookEvent {
		return PlaybookEvent{
			AggregationKey:   aggregationKey,
			SubjectType:      subjectType,
			SubjectName:      "web-7d4b9c5f6-x2x7q",
			SubjectNamespace: "shop",
			Labels:           labels,
		}
	}
	cases := []struct {
		name  string
		event PlaybookEvent
		want  bool
	}{
		{"crash-looping pod", subject("report_crash_loop", "pod", map[string]string{}), true},
		// Nothing to read: the container never ran.
		{"pod that cannot pull its image", subject("image_pull_backoff_reporter", "pod", map[string]string{}), false},
		// job_pod_enricher reads a Job's pods; this action would be asking for a
		// pod called after the Job.
		{"job", subject("job_failure", "job", map[string]string{}), false},
		{"cronjob named by the alert's kind label", subject("KubeJobFailed", "", map[string]string{"kind": "CronJob"}), false},
		{"deployment", subject("KubePodCrashLooping", "deployment", map[string]string{}), false},
		// The namespace label on a node alert is the exporter's, and no pod is
		// called after the node.
		{"node carrying the exporter's labels", PlaybookEvent{
			AggregationKey: "NodeDiskIOSaturation", SubjectType: "node", SubjectName: "gke-pool-1-node-7f3a",
			Labels: map[string]string{"namespace": "monitoring", "pod": "node-exporter-skqnn"},
		}, false},
		// The subject type decides on its own: a `kind` label naming something
		// else does not turn a node alert back into a pod.
		{"node whose kind label says pod", PlaybookEvent{
			AggregationKey: "NodeDiskIOSaturation", SubjectType: "node", SubjectName: "gke-pool-1-node-7f3a",
			Labels: map[string]string{"kind": "Pod", "namespace": "monitoring", "pod": "node-exporter-skqnn"},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action := podLogAction{}
			assert.Equal(t, tc.want, action.CanAutoExecute(&defaultPlaybookActionContext{event: tc.event, logger: slog.Default()}))
		})
	}
}

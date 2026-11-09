package playbooks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// podList mimics what get_resource returns for a single-pod query: the whole
// namespace, with the requested pod somewhere other than first. This is the
// shape that made oom_killer_enricher describe an unrelated pod.
func podList() []any {
	return []any{
		map[string]any{
			"metadata": map[string]any{"name": "unrelated-abc", "namespace": "nudgebee"},
			"status":   map[string]any{"container_statuses": []any{}},
		},
		map[string]any{
			"metadata": map[string]any{"name": "k8s-collector-worker-7ccbf9bdc4-b75qf", "namespace": "nudgebee"},
			"status": map[string]any{"container_statuses": []any{
				map[string]any{
					"name":          "k8s-collector-worker",
					"restart_count": 1,
					"state":         map[string]any{"running": map[string]any{"started_at": "2026-09-01T07:54:27Z"}},
					"last_state": map[string]any{"terminated": map[string]any{
						"exit_code":   137,
						"reason":      "OOMKilled",
						"started_at":  "2026-09-01T04:08:40Z",
						"finished_at": "2026-09-01T07:54:26Z",
					}},
				},
			}},
		},
	}
}

func TestResourceDictNamedPicksTheRequestedObject(t *testing.T) {
	got := resourceDictNamed(podList(), "k8s-collector-worker-7ccbf9bdc4-b75qf", "nudgebee")
	require.NotNil(t, got)
	meta := got["metadata"].(map[string]any)
	assert.Equal(t, "k8s-collector-worker-7ccbf9bdc4-b75qf", meta["name"])

	// firstResourceDict is what this replaced: it returns whatever happens to be
	// first, which is how every OOM card lost its container rows.
	assert.Equal(t, "unrelated-abc", firstResourceDict(podList())["metadata"].(map[string]any)["name"])
}

func TestResourceDictNamedReturnsNilRatherThanTheWrongObject(t *testing.T) {
	assert.Nil(t, resourceDictNamed(podList(), "gone-already", "nudgebee"))
	assert.Nil(t, resourceDictNamed(podList(), "k8s-collector-worker-7ccbf9bdc4-b75qf", "other-namespace"))
}

func TestResourceDictNamedFallsBackForSingleDict(t *testing.T) {
	single := map[string]any{"metadata": map[string]any{"name": "n1"}}
	assert.Equal(t, single, resourceDictNamed(single, "anything", ""))
}

func TestOOMKilledContainerIsFoundInAFullNamespaceList(t *testing.T) {
	pod := resourceDictNamed(podList(), "k8s-collector-worker-7ccbf9bdc4-b75qf", "nudgebee")
	require.NotNil(t, pod)
	container, terminated := podMostRecentOOMKilledContainer(pod)
	require.NotNil(t, container)
	require.NotNil(t, terminated)
	assert.Equal(t, "k8s-collector-worker", container["name"])
	assert.Equal(t, "2026-09-01T07:54:26Z", terminated["finished_at"])
}

func TestMostRecentContainerTerminationReadsTheKillTime(t *testing.T) {
	pod := resourceDictNamed(podList(), "k8s-collector-worker-7ccbf9bdc4-b75qf", "nudgebee")
	require.NotNil(t, pod)
	// The fixture is dated, so bypass the freshness bound by checking the parse
	// directly against a pod whose kill is recent.
	recent := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339)
	pod["status"].(map[string]any)["container_statuses"].([]any)[0].(map[string]any)["last_state"] =
		map[string]any{"terminated": map[string]any{"reason": "OOMKilled", "finished_at": recent}}

	got, ok := mostRecentContainerTermination(pod)
	require.True(t, ok)
	assert.Equal(t, recent, got.Format(time.RFC3339))
}

func TestMostRecentContainerTerminationIgnoresAncientRestarts(t *testing.T) {
	pod := map[string]any{"status": map[string]any{"container_statuses": []any{
		map[string]any{"last_state": map[string]any{"terminated": map[string]any{
			"reason": "OOMKilled", "finished_at": "2020-01-01T00:00:00Z"}}},
	}}}
	_, ok := mostRecentContainerTermination(pod)
	assert.False(t, ok, "a kill older than the backtrack bound must not re-anchor the window")
}

func TestIsLogRetrievalFailure(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"kubelet cannot read the previous container",
			"unable to retrieve container logs for containerd://9623098c4cfb14290b8e2f2924756afdd6912f630afe6316b7edc6ad9236ad29", true},
		{"container has not started", `container "web" in pod "web-1" is waiting to start: ContainerCreating`, true},
		{"real logs", "2026-09-01T07:53:35Z INFO started consumer\n2026-09-01T07:53:36Z INFO ready\n", false},
		{"empty", "   ", false},
		{"a log line that merely quotes the phrase mid-stream",
			"line one\nline two\nline three\nERROR unable to retrieve container logs for a downstream call\nline five", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsLogRetrievalFailure(tc.in))
		})
	}
}

func TestResolveQueryWindowAnchorsOnIncidentTime(t *testing.T) {
	detected := time.Date(2026, 9, 1, 8, 36, 10, 0, time.UTC)
	killed := time.Date(2026, 9, 1, 6, 0, 50, 0, time.UTC)

	// Without IncidentAt the window sits entirely after the failure — the 68-hour
	// class of defect, here at 2h35m.
	plain := PlaybookEvent{StartedAt: &detected}
	plainStart, _ := plain.ResolveQueryWindow(0)
	assert.False(t, plainStart.Before(killed), "window should not reach the kill without IncidentAt")

	anchored := PlaybookEvent{StartedAt: &detected, IncidentAt: &killed}
	start, end := anchored.ResolveQueryWindow(0)
	assert.Equal(t, killed, end.UTC())
	assert.Equal(t, killed.Add(-DefaultQueryWindowMinutes*time.Minute), start.UTC())
	assert.True(t, !start.After(killed) && !end.Before(killed), "window must contain the kill")
}

func TestIsTerminatedContainerAggKeyCoversTheDeadContainerClasses(t *testing.T) {
	for _, k := range []string{
		"report_crash_loop", "pod_oom_killer_enricher", "KubePodCrashLooping", "KubeContainerWaiting",
	} {
		assert.True(t, IsTerminatedContainerAggKey(k), k)
	}
	assert.False(t, IsTerminatedContainerAggKey("HighP95Latency"))

	// No container ever started, so there is no previous one to read.
	assert.False(t, IsTerminatedContainerAggKey("image_pull_backoff_reporter"))
	assert.True(t, IsNeverStartedContainerAggKey("image_pull_backoff_reporter"))
	assert.False(t, IsNeverStartedContainerAggKey("report_crash_loop"))

	// Subject is the Job, which job_pod_enricher reads; a finished Job's pod has
	// no previous container either.
	assert.False(t, IsTerminatedContainerAggKey("job_failure"))
	assert.False(t, IsTerminatedContainerAggKey("KubeJobFailed"))
}

func TestNeedsIncidentTimeAnchorIsNarrowerThanTheLogSet(t *testing.T) {
	// Resolving the anchor costs a relay round-trip that returns the whole
	// namespace, so it must not fire where it cannot pay for itself.
	assert.True(t, NeedsIncidentTimeAnchor("pod_oom_killer_enricher"))
	assert.True(t, NeedsIncidentTimeAnchor("report_crash_loop"))

	// Its subject is the workload, never the pod — 56 events over 30 days on dev,
	// none of them pod-subject — so the anchor could never resolve for it.
	assert.False(t, NeedsIncidentTimeAnchor("KubePodCrashLooping"))

	// No container ever started, so there is no termination to read.
	assert.False(t, NeedsIncidentTimeAnchor("image_pull_backoff_reporter"))
	// Subject is the Job, not a pod — the pod lookup could only ever miss.
	assert.False(t, NeedsIncidentTimeAnchor("job_failure"))
	assert.False(t, NeedsIncidentTimeAnchor("KubeJobFailed"))

	assert.True(t, IsTerminatedContainerAggKey("KubePodCrashLooping"),
		"should still get the dead container's logs, just not an anchor lookup")
}

func TestClusterHasNoLogs(t *testing.T) {
	const viaRelay = "relay: unable to execute relay query -  logs_enricher: read logs: "
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"no previous container", viaRelay + `previous terminated container "web" in pod "web-7d4b9c5f6-x2x7q" not found`, true},
		{"config error before start", viaRelay + `container "web" in pod "web-7d4b9c5f6-x2x7q" is waiting to start: CreateContainerConfigError`, true},
		{"image pull back-off", viaRelay + `container "web" in pod "web-7d4b9c5f6-x2x7q" is waiting to start: trying and failing to pull image`, true},
		{"image pull error", viaRelay + `container "web" in pod "web-7d4b9c5f6-x2x7q" is waiting to start: image can't be pulled`, true},
		{"init containers still running", viaRelay + `container "web" in pod "web-7d4b9c5f6-x2x7q" is waiting to start: PodInitializing`, true},
		{"exited, logs gone", viaRelay + `container "web" in pod "web-7d4b9c5f6-x2x7q" is terminated`, true},
		{"subject has no pods", "relay: unable to execute relay query -  logs_enricher: no pods found for workload", true},
		{"kubectl stderr behind its pod-selection notice",
			"Found 5 pods, using pod/web-7d4b9c5f6-x2x7q\nError from server (BadRequest): container \"web\" in pod \"web-7d4b9c5f6-x2x7q\" is terminated\n", true},

		// The read failed, or the answer does not say the logs are absent.
		{"container state unknown", viaRelay + `container "web" in pod "web-7d4b9c5f6-x2x7q" is not available`, false},
		{"pod gone", viaRelay + `pods "web-7d4b9c5f6-x2x7q" not found`, false},
		{"job gone", `error: error from server (NotFound): jobs.batch "report-29856814" not found in namespace "shop"`, false},
		{"kubectl timed out", "error: timed out waiting for the condition", false},
		{"relay unreachable", "unable to consume relay API", false},
		{"not a kubernetes namespace", `relay: unable to execute relay query -  logs_enricher: list pods: invalid namespace "org/repo": [may not contain '/']`, false},
		{"a transport error that happens to say terminated", "relay: session is terminated", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ClusterHasNoLogs(tc.in))
		})
	}
}

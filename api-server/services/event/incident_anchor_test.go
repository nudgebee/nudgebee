package event

import (
	"log/slog"
	"testing"
	"time"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
)

// resolveIncidentAt reads the anchor off a Pod's container statuses, so anything
// that is not a pod subject must be declined before the relay is asked.
//
// Missing is not free: get_resource ignores its name filter, so every futile
// lookup ships back the whole cluster's pods. On dev, events whose subject was a
// workload name (report-worker, web-app — no ReplicaSet hash) each pulled
// 501-543 objects to find nothing. These cases return nil without any relay
// call, which is also why this test can run without one.
func TestResolveIncidentAtSkipsNonPodSubjects(t *testing.T) {
	sc := security.NewRequestContextForTenantAdmin("tenant", slog.Default(), nil, nil)
	startsAt := time.Now().UTC()

	// "" is absent on purpose: an unspecified subject is treated as a pod, the
	// same way linkK8sCloudResourceId does it. The classes that actually carry a
	// non-pod subject are excluded by NeedsIncidentTimeAnchor instead.
	for _, subjectType := range []string{"deployment", "daemonset", "statefulset", "job", "node", "Deployment"} {
		t.Run("subject_type="+subjectType, func(t *testing.T) {
			got := resolveIncidentAt(sc, "pod_oom_killer_enricher", "acct", "report-worker", "namespace-232", subjectType, &startsAt)
			assert.Nil(t, got, "non-pod subject must not trigger a pod lookup")
		})
	}
}

func TestResolveIncidentAtSkipsUnanchoredAggregationKeys(t *testing.T) {
	sc := security.NewRequestContextForTenantAdmin("tenant", slog.Default(), nil, nil)
	startsAt := time.Now().UTC()

	// Classes with no terminated container to read, checked before subject type.
	for _, key := range []string{"image_pull_backoff_reporter", "job_failure", "HighP95Latency", ""} {
		t.Run(key, func(t *testing.T) {
			got := resolveIncidentAt(sc, key, "acct", "some-pod-abc123", "demo", "pod", &startsAt)
			assert.Nil(t, got)
		})
	}
}

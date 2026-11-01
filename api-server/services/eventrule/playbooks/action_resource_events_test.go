package playbooks

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// k8sEvent builds a core/v1 Event the way the agent's get_resource returns it
// (snake_case keys).
func k8sEvent(namespace, kind, name, eventType, reason, lastSeen, message string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"name": name + ".17f0c2a9d3b4e5f6", "namespace": namespace},
		"involved_object": map[string]any{
			"kind":      kind,
			"name":      name,
			"namespace": namespace,
			"uid":       "00000000-0000-0000-0000-000000000000",
		},
		"type":           eventType,
		"reason":         reason,
		"message":        message,
		"last_timestamp": lastSeen,
		"count":          float64(1),
	}
}

// shopNamespaceEvents is one namespace's Event list: several workloads, some
// whose names extend another's, plus a same-named pod in another namespace.
func shopNamespaceEvents() []any {
	ev := func(kind, name, eventType, reason, msg string) any {
		return k8sEvent("shop", kind, name, eventType, reason, "2026-09-25T10:50:00Z", msg)
	}
	// The Node's own OOM kill: the Event lives in "default", its involvedObject
	// is cluster-scoped.
	nodeOOM := k8sEvent("default", "Node", "node-a", "Warning", "OOMKilling", "2026-09-25T10:50:00Z", "Memory cgroup out of memory: Killed process 4242 (java)")
	nodeOOM["involved_object"].(map[string]any)["namespace"] = ""
	return []any{
		// Deployment "checkout": the image pull error is only on the Pod.
		ev("Deployment", "checkout", "Normal", "ScalingReplicaSet", "Scaled up replica set checkout-7d9f8c6b5c from 0 to 1"),
		ev("ReplicaSet", "checkout-7d9f8c6b5c", "Normal", "SuccessfulCreate", "Created pod: checkout-7d9f8c6b5c-x7k2p"),
		ev("Pod", "checkout-7d9f8c6b5c-x7k2p", "Warning", "Failed", "Failed to pull image \"registry.example.com/checkout:1.4\": descriptor not found"),
		// Sibling Deployment "checkout-worker" — must not be swept into "checkout".
		ev("Deployment", "checkout-worker", "Normal", "ScalingReplicaSet", "Scaled up replica set checkout-worker-5c8d9f7b6d from 0 to 1"),
		ev("ReplicaSet", "checkout-worker-5c8d9f7b6d", "Normal", "SuccessfulCreate", "Created pod: checkout-worker-5c8d9f7b6d-q4z8m"),
		ev("Pod", "checkout-worker-5c8d9f7b6d-q4z8m", "Warning", "BackOff", "Back-off restarting failed container"),
		// Job "checkout-v2": its pod has the <name>-<short>-<5> shape of a
		// "checkout" pod with a 2-character hash.
		ev("Pod", "checkout-v2-p9rzt", "Warning", "BackOff", "Back-off restarting failed container"),
		// StatefulSet "ledger" and a sibling "ledger-archive".
		ev("StatefulSet", "ledger", "Normal", "SuccessfulCreate", "create Pod ledger-1 in StatefulSet ledger successful"),
		ev("Pod", "ledger-0", "Warning", "Unhealthy", "Readiness probe failed: connection refused"),
		ev("Pod", "ledger-1", "Warning", "FailedScheduling", "0/3 nodes are available: 3 Insufficient memory."),
		ev("Pod", "ledger-archive-0", "Warning", "Unhealthy", "Liveness probe failed"),
		// Job "report-sync" (one plain pod, one indexed pod) and a sibling "report-sync-full".
		ev("Job", "report-sync", "Normal", "SuccessfulCreate", "Created pod: report-sync-b7kx9"),
		ev("Pod", "report-sync-b7kx9", "Warning", "FailedScheduling", "0/3 nodes are available: pod has unbound immediate PersistentVolumeClaims."),
		ev("Pod", "report-sync-0-h8wd4", "Normal", "Scheduled", "Successfully assigned shop/report-sync-0-h8wd4 to node-a"),
		ev("Job", "report-sync-full", "Normal", "SuccessfulCreate", "Created pod: report-sync-full-z2mqp"),
		ev("Pod", "report-sync-full-z2mqp", "Warning", "BackOff", "Back-off restarting failed container"),
		// CronJob "nightly-report" and a sibling "nightly-report-eu".
		ev("CronJob", "nightly-report", "Normal", "SuccessfulCreate", "Created job nightly-report-29312640"),
		ev("Job", "nightly-report-29312640", "Normal", "SuccessfulCreate", "Created pod: nightly-report-29312640-k7x2d"),
		ev("Pod", "nightly-report-29312640-k7x2d", "Warning", "Failed", "Error: ImagePullBackOff"),
		ev("Job", "nightly-report-eu-29312640", "Normal", "SuccessfulCreate", "Created pod: nightly-report-eu-29312640-m4n5p"),
		// DaemonSet "log-shipper" and a sibling "log-shipper-canary".
		ev("DaemonSet", "log-shipper", "Normal", "SuccessfulCreate", "Created pod: log-shipper-9xkqz"),
		ev("Pod", "log-shipper-9xkqz", "Warning", "FailedMount", "MountVolume.SetUp failed for volume \"config\""),
		ev("Pod", "log-shipper-canary-9xkqz", "Warning", "FailedMount", "MountVolume.SetUp failed for volume \"config\""),
		// Same pod name, other namespace.
		k8sEvent("other", "Pod", "checkout-7d9f8c6b5c-x7k2p", "Warning", "Failed", "2026-09-25T10:50:00Z", "other namespace"),
		nodeOOM,
	}
}

func objectColumn(rows [][]any) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r[3].(string)
	}
	return out
}

func TestFilterEventsForSubject_IncludesOwnedObjects(t *testing.T) {
	cases := []struct {
		kind, name string
		want       []string
	}{
		{"Deployment", "checkout", []string{
			"Pod/checkout-7d9f8c6b5c-x7k2p", "Deployment/checkout", "ReplicaSet/checkout-7d9f8c6b5c",
		}},
		{"Deployment", "checkout-worker", []string{
			"Pod/checkout-worker-5c8d9f7b6d-q4z8m", "Deployment/checkout-worker", "ReplicaSet/checkout-worker-5c8d9f7b6d",
		}},
		{"Rollout", "checkout", []string{
			"Pod/checkout-7d9f8c6b5c-x7k2p", "ReplicaSet/checkout-7d9f8c6b5c",
		}},
		{"Statefulset", "ledger", []string{
			"Pod/ledger-0", "Pod/ledger-1", "StatefulSet/ledger",
		}},
		{"Job", "report-sync", []string{
			"Pod/report-sync-b7kx9", "Job/report-sync", "Pod/report-sync-0-h8wd4",
		}},
		{"CronJob", "nightly-report", []string{
			"Pod/nightly-report-29312640-k7x2d", "CronJob/nightly-report", "Job/nightly-report-29312640",
		}},
		{"Daemonset", "log-shipper", []string{
			"Pod/log-shipper-9xkqz", "DaemonSet/log-shipper",
		}},
		// A Pod subject keeps only its own Events, as before.
		{"Pod", "checkout-7d9f8c6b5c-x7k2p", []string{"Pod/checkout-7d9f8c6b5c-x7k2p"}},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.name, func(t *testing.T) {
			matched := filterEventsForSubject(shopNamespaceEvents(), tc.kind, tc.name, "shop")
			rows, _, insight := resourceEventsCard(matched, tc.kind, tc.name, nil, time.Now())
			assert.Equal(t, tc.want, objectColumn(rows), "Warnings first, then the rest in list order")
			assert.Empty(t, insight)
		})
	}
}

// The node-scoped OOM lookup still lands in the Pod card next to the Pod's own Events.
func TestResourceEventsCard_KeepsNodeScopedOOM(t *testing.T) {
	data := shopNamespaceEvents()
	matched := filterEventsForSubject(data, "Pod", "checkout-7d9f8c6b5c-x7k2p", "shop")
	matched = append(matched, nodeScopedFailureEvents(data, "node-a")...)
	rows, _, _ := resourceEventsCard(matched, "Pod", "checkout-7d9f8c6b5c-x7k2p", nil, time.Now())
	assert.Equal(t, []string{"Pod/checkout-7d9f8c6b5c-x7k2p", "Node/node-a"}, objectColumn(rows))
}

func TestResourceEventsCard_EmptyMessage(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) *time.Time { t := now.Add(-ago); return &t }
	// As the event service builds it: StartedAt is the query window start,
	// ten minutes before the alert.
	alert55mAgo := PlaybookEvent{StartedAt: at(65 * time.Minute), DetectedAt: at(55 * time.Minute)}

	cases := []struct {
		name       string
		kind       string
		alertStart *time.Time
		want       string
	}{
		{"fresh deployment alert: no TTL claim", "Deployment", at(12 * time.Minute),
			"No Kubernetes Events were recorded for Deployment/checkout or its ReplicaSets and Pods."},
		{"alert start, not window start, decides the age", "Deployment", alert55mAgo.AlertStart(),
			"No Kubernetes Events were recorded for Deployment/checkout or its ReplicaSets and Pods."},
		{"old statefulset alert: TTL explains it", "Statefulset", at(3 * time.Hour),
			"No Kubernetes Events found for Statefulset/checkout or its Pods. The alert started 3 hours ago, longer than the cluster's event TTL (1h by default), so Events from around the alert have likely been garbage collected."},
		{"cronjob names its jobs and pods", "CronJob", at(5 * time.Minute),
			"No Kubernetes Events were recorded for CronJob/checkout or its Jobs and Pods."},
		{"unknown alert start: no claim either way", "Pod", nil,
			"No Kubernetes Events found for Pod/checkout."},
		{"zero alert start is unknown, not decades old", "Pod", &time.Time{},
			"No Kubernetes Events found for Pod/checkout."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, headers, insight := resourceEventsCard([]any{}, tc.kind, "checkout", tc.alertStart, now)
			assert.Empty(t, rows)
			assert.Equal(t, []string{"LastSeen", "Type", "Reason", "Object", "Message"}, headers)
			require.Len(t, insight, 1)
			assert.Equal(t, tc.want, insight[0].Message)
		})
	}
}

func TestResourceEventsCard_CapsWarningsFirstNewestFirst(t *testing.T) {
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	events := []any{}
	for i := 0; i < 57; i++ {
		ts := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		events = append(events, k8sEvent("shop", "Pod", fmt.Sprintf("checkout-7d9f8c6b5c-n%04d", i), "Normal", "Pulled", ts, "Container image already present on machine"))
	}
	// Three old Warnings, one carrying only a sub-second event_time.
	events = append(events,
		k8sEvent("shop", "Pod", "checkout-7d9f8c6b5c-w0001", "Warning", "BackOff", base.Add(-3*time.Hour).Format(time.RFC3339), "Back-off restarting failed container"),
		k8sEvent("shop", "Pod", "checkout-7d9f8c6b5c-w0002", "Warning", "BackOff", base.Add(-1*time.Hour).Format(time.RFC3339), "Back-off restarting failed container"),
	)
	micro := k8sEvent("shop", "Pod", "checkout-7d9f8c6b5c-w0003", "Warning", "FailedScheduling", "", "0/3 nodes are available")
	micro["event_time"] = base.Add(-2 * time.Hour).Add(123456 * time.Microsecond).Format(time.RFC3339Nano)
	events = append(events, micro)

	rows, _, insight := resourceEventsCard(events, "Deployment", "checkout", nil, time.Now())

	require.Len(t, rows, maxResourceEventRows)
	objects := objectColumn(rows)
	assert.Equal(t, []string{
		"Pod/checkout-7d9f8c6b5c-w0002", "Pod/checkout-7d9f8c6b5c-w0003", "Pod/checkout-7d9f8c6b5c-w0001",
		"Pod/checkout-7d9f8c6b5c-n0056", "Pod/checkout-7d9f8c6b5c-n0055",
	}, objects[:5])
	assert.Equal(t, "Pod/checkout-7d9f8c6b5c-n0010", objects[len(objects)-1], "the oldest Normal events are the ones dropped")
	require.Len(t, insight, 1)
	assert.Equal(t, "Showing 50 of 60 Events, Warnings first and newest first.", insight[0].Message)
}

package playbooks

import (
	"log/slog"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/relay"
)

// IsTerminatedContainerAggKey reports whether an event class describes a
// container that has already exited, and so should be enriched with the dead
// container's logs rather than its replacement's.
func IsTerminatedContainerAggKey(aggregationKey string) bool {
	return terminatedContainerAggKeys[aggregationKey]
}

// NeedsIncidentTimeAnchor is deliberately narrower than
// IsTerminatedContainerAggKey: resolving the anchor costs a relay round-trip,
// and get_resource ignores its name filter for pods, so that round-trip returns
// the whole namespace. Only ask where it can pay for itself.
//
// image_pull_backoff_reporter is excluded because the container never started
// and there is no termination to read; job_failure and KubeJobFailed because the
// subject is the Job, not a pod, so the lookup could only ever miss.
func NeedsIncidentTimeAnchor(aggregationKey string) bool {
	switch aggregationKey {
	// KubePodCrashLooping is deliberately absent: it is a Prometheus alert whose
	// subject is the workload, never the pod. Across 30 days on dev its 56 events
	// were 34 deployment, 5 daemonset and 17 unspecified — not one a pod — so the
	// anchor could never resolve for it, and every attempt cost a 500-object
	// get_resource response.
	case "pod_oom_killer_enricher", "report_crash_loop":
		return true
	}
	return false
}

// maxTerminationBacktrack bounds how far back an event may be re-anchored. A
// pod that has sat in CrashLoopBackOff for a fortnight would otherwise drag the
// whole evidence window back to a restart nobody is asking about.
const maxTerminationBacktrack = 7 * 24 * time.Hour

// PodTerminationTime asks the cluster when the pod's most recently terminated
// container actually died.
//
// Detection time and failure time are not the same thing. Kubernetes reports a
// pod's restart state, not a stream of kill events, so a re-reported occurrence
// carries the timestamp of the sweep that noticed it: measured on dev, 21 of 140
// OOMKilled events were anchored away from the real kill, the worst by 68 hours.
// One of them fired at 08:36 for a container whose own pod object recorded
// exit_code 137 at 06:00:50 — and had been running healthily ever since. Every
// window derived from that event covered 07:36-08:36, an hour in which nothing
// happened, while the logs and metrics that explain the kill sat 2.5 hours
// earlier.
//
// Returns false whenever the cluster cannot tell us, so callers keep the
// event's own timestamps.
func PodTerminationTime(accountId, podName, namespace string, logger *slog.Logger) (time.Time, bool) {
	if accountId == "" || podName == "" || namespace == "" {
		return time.Time{}, false
	}
	resp, _, err := relay.ExecuteAndExtractResponse(relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  accountId,
			ActionName: "get_resource",
			ActionParams: map[string]any{
				"resource_type":  "pods",
				"group":          "",
				"version":        "v1",
				"namespace":      []string{namespace},
				"all_namespaces": false,
				"name":           []string{podName},
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	})
	if err != nil {
		if logger != nil {
			logger.Info("event: could not resolve container termination time", "pod", podName, "namespace", namespace, "error", err)
		}
		return time.Time{}, false
	}

	data := resp["data"]
	if s, ok := data.(string); ok {
		var parsed any
		if err := common.UnmarshalJson([]byte(s), &parsed); err != nil {
			return time.Time{}, false
		}
		data = parsed
	}
	pod := resourceDictNamed(data, podName, namespace)
	if pod == nil {
		// Usually the pod has already been replaced by the time a re-reported
		// occurrence is enriched — which is exactly the population with the
		// largest skew, so it is worth saying out loud rather than silently
		// falling back to the detection time.
		if logger != nil {
			logger.Info("event: no incident anchor — pod not found",
				"pod", podName, "namespace", namespace, "objects_returned", relayObjectCount(data))
		}
		return time.Time{}, false
	}
	terminatedAt, ok := mostRecentContainerTermination(pod)
	if !ok && logger != nil {
		logger.Info("event: no incident anchor — pod has no readable container termination",
			"pod", podName, "namespace", namespace,
			"container_statuses", len(getArrayField(getMapField(pod, "status"), "container_statuses", "containerStatuses")))
	}
	return terminatedAt, ok
}

// relayObjectCount reports how many objects the relay actually returned, so the
// "pod not found" line distinguishes an empty answer from a large one that
// simply did not contain the pod.
func relayObjectCount(data any) int {
	if list, ok := data.([]any); ok {
		return len(list)
	}
	return -1
}

// mostRecentContainerTermination returns the latest finishedAt across the pod's
// container statuses, checking the current terminated state before lastState so
// a restartPolicy:Never pod (which records the kill in state, not lastState) is
// not missed.
func mostRecentContainerTermination(pod map[string]any) (time.Time, bool) {
	status := getMapField(pod, "status")
	if status == nil {
		return time.Time{}, false
	}
	statuses := getArrayField(status, "container_statuses", "containerStatuses")
	if statuses == nil {
		return time.Time{}, false
	}
	var latest time.Time
	for _, item := range statuses {
		cs, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, field := range []string{"state", "last_state", "lastState"} {
			term := getMapField(getMapField(cs, field), "terminated")
			if term == nil {
				continue
			}
			finished := ""
			for _, key := range []string{"finished_at", "finishedAt"} {
				if v, ok := term[key].(string); ok && v != "" {
					finished = v
					break
				}
			}
			if finished == "" {
				continue
			}
			t, err := time.Parse(time.RFC3339, finished)
			if err != nil {
				continue
			}
			if t.After(latest) {
				latest = t.UTC()
			}
		}
	}
	// A kill older than the bound is not an anchor worth moving the window to.
	if latest.IsZero() || time.Since(latest) > maxTerminationBacktrack {
		return time.Time{}, false
	}
	return latest, true
}

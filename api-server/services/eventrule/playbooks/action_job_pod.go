package playbooks

import (
	"errors"
	"fmt"
	"nudgebee/services/relay"
)

// job_pod_enricher attaches the failed pod's events + (previous) logs to the
// Job-failure finding. Selects the latest failed pod for the Job
// (label-selector `job-name=<job>`) and renders events + logs.
//
// Flow:
//  1. Use get_resource with a label-selector to find pods for the job.
//  2. Pick the first pod whose status indicates failure.
//  3. Use logs_enricher (via relay) for previous-container logs.
//  4. Reuse eventListToTable for the pod's events.
type jobPodAction struct{}

func (a *jobPodAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if ctx.GetEvent().AggregationKey != "job_failure" {
		return false
	}
	name, ns := subjectJobName(ctx.GetEvent())
	return name != "" && ns != ""
}

func (a *jobPodAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	name, ns := subjectJobName(ctx.GetEvent())
	return a.Execute(ctx, map[string]any{"job_name": name, "namespace": ns})
}

func (a *jobPodAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	jobName, _ := rawParams["job_name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	if jobName == "" || namespace == "" {
		return nil, errors.New("job_pod_enricher: job_name + namespace required")
	}

	// Fetch pods owned by the Job via label-selector — works without traversing
	// the ReplicaSet/Job ownership graph.
	podData, _, err := getResourceViaRelay(ctx, map[string]any{
		"resource_type":  "pods",
		"group":          "",
		"version":        "v1",
		"namespace":      []string{namespace},
		"all_namespaces": false,
		"name":           []string{},
		"label_selector": fmt.Sprintf("job-name=%s", jobName),
	})
	if err != nil {
		return nil, fmt.Errorf("job_pod_enricher: list pods: %w", err)
	}
	podName := pickFailedPodName(podData)
	if podName == "" {
		return nil, errors.New("job_pod_enricher: no failed pod found for job")
	}

	// Fetch previous-container logs through relay's logs_enricher.
	logsResp, _, err := relay.ExecuteAndExtractResponse(relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  ctx.GetAccountId(),
			ActionName: "logs_enricher",
			ActionParams: map[string]any{
				"name":      podName,
				"namespace": namespace,
				"previous":  true,
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	})
	if err != nil {
		ctx.GetLogger().Warn("job_pod_enricher: logs_enricher failed", "error", err)
	}

	// Build a file response when logs were retrieved. Falls back to events
	// table when not.
	if logsResp != nil {
		if data, ok := logsResp["data"].(string); ok && data != "" {
			filename, _ := logsResp["filename"].(string)
			typeVal, _ := logsResp["type"].(string)
			return PlaybookActionResponseFile{
				Type:     typeVal,
				Filename: filename,
				Data:     data,
				AdditionalInfo: map[string]any{
					"title":              "Failed job pod logs (previous)",
					"action_name":        "job_pod_enricher",
					"actual_action_name": "job_pod_enricher",
					"pod_name":           podName,
					"namespace":          namespace,
					"job_name":           jobName,
				},
				Insight: InsightFromRelayResponse(logsResp),
			}, nil
		}
	}

	// Fallback — return the failed pod's events as a table. The agent ignores
	// field_selector so we fetch the namespace's events and filter
	// client-side on involvedObject (same pattern as resource_events).
	eventsData, _, err := getResourceViaRelay(ctx, map[string]any{
		"resource_type":  "events",
		"group":          "",
		"version":        "v1",
		"namespace":      []string{namespace},
		"all_namespaces": false,
		"name":           []string{},
	})
	if err != nil {
		ctx.GetLogger().Warn("job_pod_enricher: events fallback failed", "error", err)
		return nil, fmt.Errorf("job_pod_enricher: %w", err)
	}
	rows, headers := eventListToTable(filterEventsByInvolvedObject(eventsData, "Pod", podName, namespace))
	return PlaybookActionResponseTable{
		Rows:    rows,
		Headers: headers,
		AdditionalInfo: map[string]any{
			"title":              "Failed job pod events",
			"action_name":        "job_pod_enricher",
			"actual_action_name": "job_pod_enricher",
			"pod_name":           podName,
			"namespace":          namespace,
			"job_name":           jobName,
		},
		Insight: []PlaybookActionResponseInsight{},
	}, nil
}

// pickFailedPodName scans the relay's pod list for the first pod whose
// containerStatuses indicate failure (lastState.terminated.exit_code != 0),
// falling back to status.phase == "Failed" / "Unknown".
func pickFailedPodName(data any) string {
	arr, ok := data.([]any)
	if !ok {
		return ""
	}
	for _, item := range arr {
		pod, ok := item.(map[string]any)
		if !ok {
			continue
		}
		md, _ := pod["metadata"].(map[string]any)
		name, _ := md["name"].(string)
		status, _ := pod["status"].(map[string]any)
		if status == nil {
			continue
		}
		if phase, _ := status["phase"].(string); phase == "Failed" {
			return name
		}
		cs := getArrayField(status, "container_statuses", "containerStatuses")
		for _, c := range cs {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			last := getMapField(cm, "last_state", "lastState")
			if last == nil {
				continue
			}
			term := getMapField(last, "terminated")
			if term == nil {
				continue
			}
			if code, ok := term["exit_code"].(float64); ok && code != 0 {
				return name
			}
			if code, ok := term["exitCode"].(float64); ok && code != 0 {
				return name
			}
		}
	}
	// As a last resort, return the first pod (so the caller has something).
	if len(arr) > 0 {
		if pod, ok := arr[0].(map[string]any); ok {
			if md, ok := pod["metadata"].(map[string]any); ok {
				if name, ok := md["name"].(string); ok {
					return name
				}
			}
		}
	}
	return ""
}

package playbooks

import (
	"errors"
	"fmt"
)

// job_events_enricher returns K8s Events whose involvedObject is the failed
// Job — kubectl-style.
type jobEventsAction struct{}

func (a *jobEventsAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if ctx.GetEvent().AggregationKey != "job_failure" {
		return false
	}
	name, ns := subjectJobName(ctx.GetEvent())
	return name != "" && ns != ""
}

func (a *jobEventsAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	name, ns := subjectJobName(ctx.GetEvent())
	return a.Execute(ctx, map[string]any{"job_name": name, "namespace": ns})
}

func (a *jobEventsAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	jobName, _ := rawParams["job_name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	if jobName == "" || namespace == "" {
		return nil, errors.New("job_events_enricher: job_name + namespace required")
	}

	data, _, err := getResourceViaRelay(ctx, map[string]any{
		"resource_type":  "events",
		"group":          "",
		"version":        "v1",
		"namespace":      []string{namespace},
		"all_namespaces": false,
		"name":           []string{},
	})
	if err != nil {
		return nil, fmt.Errorf("job_events_enricher: %w", err)
	}
	rows, headers := eventListToTable(filterEventsByInvolvedObject(data, "Job", jobName, namespace))
	return PlaybookActionResponseTable{
		Rows:    rows,
		Headers: headers,
		AdditionalInfo: map[string]any{
			"title":              "Job Events",
			"action_name":        "job_events_enricher",
			"actual_action_name": "job_events_enricher",
			"job_name":           jobName,
			"namespace":          namespace,
		},
		Insight: []PlaybookActionResponseInsight{},
	}, nil
}

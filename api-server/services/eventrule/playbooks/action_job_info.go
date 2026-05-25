package playbooks

import (
	"errors"
	"fmt"
)

// job_info_enricher returns a table summarising the failed Job:
// completions / failed / backoffLimit / start-completion timestamps + container
// resources.
type jobInfoAction struct{}

func (a *jobInfoAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if ctx.GetEvent().AggregationKey != "job_failure" {
		return false
	}
	name, ns := subjectJobName(ctx.GetEvent())
	return name != "" && ns != ""
}

func (a *jobInfoAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	name, ns := subjectJobName(ctx.GetEvent())
	return a.Execute(ctx, map[string]any{"job_name": name, "namespace": ns})
}

func (a *jobInfoAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	jobName, _ := rawParams["job_name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	if jobName == "" || namespace == "" {
		return nil, errors.New("job_info_enricher: job_name + namespace required")
	}

	data, _, err := getResourceViaRelay(ctx, map[string]any{
		"resource_type":  "jobs",
		"group":          "batch",
		"version":        "v1",
		"namespace":      []string{namespace},
		"all_namespaces": false,
		"name":           []string{jobName},
	})
	if err != nil {
		return nil, fmt.Errorf("job_info_enricher: %w", err)
	}
	job := firstResourceDict(data)
	if job == nil {
		return nil, errors.New("job_info_enricher: job not found")
	}

	rows := [][]any{
		{"Job", jobName},
		{"Namespace", namespace},
	}
	if spec, ok := job["spec"].(map[string]any); ok {
		if v, ok := spec["completions"]; ok {
			rows = append(rows, []any{"Completions", v})
		}
		if v, ok := spec["parallelism"]; ok {
			rows = append(rows, []any{"Parallelism", v})
		}
		if v, ok := spec["backoff_limit"]; ok {
			rows = append(rows, []any{"Backoff Limit", v})
		} else if v, ok := spec["backoffLimit"]; ok {
			rows = append(rows, []any{"Backoff Limit", v})
		}
	}
	if status, ok := job["status"].(map[string]any); ok {
		if v, ok := status["succeeded"]; ok {
			rows = append(rows, []any{"Succeeded", v})
		}
		if v, ok := status["failed"]; ok {
			rows = append(rows, []any{"Failed", v})
		}
		if v, ok := status["active"]; ok {
			rows = append(rows, []any{"Active", v})
		}
		if v := firstStringField(status, "start_time", "startTime"); v != "" {
			rows = append(rows, []any{"Start Time", v})
		}
		if v := firstStringField(status, "completion_time", "completionTime"); v != "" {
			rows = append(rows, []any{"Completion Time", v})
		}
		// Latest Failed condition message — useful one-liner cause.
		if conds := getArrayField(status, "conditions"); conds != nil {
			for _, c := range conds {
				cm, ok := c.(map[string]any)
				if !ok {
					continue
				}
				condType, _ := cm["type"].(string)
				condStatus, _ := cm["status"].(string)
				if condType == "Failed" && condStatus == "True" {
					rows = append(rows, []any{"Failure Reason", firstStringField(cm, "reason")})
					if msg, _ := cm["message"].(string); msg != "" {
						rows = append(rows, []any{"Failure Message", msg})
					}
				}
			}
		}
	}

	return PlaybookActionResponseTable{
		Rows:    rows,
		Headers: []string{"Field", "Value"},
		AdditionalInfo: map[string]any{
			"title":              "Job details",
			"action_name":        "job_info_enricher",
			"actual_action_name": "job_info_enricher",
			"job_name":           jobName,
			"namespace":          namespace,
		},
		Insight: []PlaybookActionResponseInsight{},
	}, nil
}

package playbooks

import (
	"errors"
	"fmt"
)

// impacted_services_enricher answers "what upstream services were hitting
// this pod when it crashed?" Fires on crash-loop. Reads coroot-node-agent /
// istio HTTP metrics for the pod.
type impactedServicesAction struct{}

func (a *impactedServicesAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if ctx.GetEvent().AggregationKey != "report_crash_loop" {
		return false
	}
	name, ns := subjectPodNamespace(ctx.GetEvent())
	return name != "" && ns != ""
}

func (a *impactedServicesAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	podName, ns := subjectPodNamespace(ctx.GetEvent())
	return a.Execute(ctx, map[string]any{"pod_name": podName, "namespace": ns})
}

func (a *impactedServicesAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	podName, _ := rawParams["pod_name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	if podName == "" || namespace == "" {
		return nil, errors.New("impacted_services_enricher: pod_name + namespace required")
	}

	// coroot-node-agent emits container_http_requests_total with source/destination
	// workload labels. Aggregating by source_workload gives the upstream caller
	// fan-in across the last 30 minutes.
	q := fmt.Sprintf(
		`sum by (source_workload_name, source_workload_namespace) (`+
			`rate(container_http_requests_total{__CLUSTER__ destination_workload_name="%s", destination_workload_namespace="%s"}[30m])`+
			`)`,
		podName, namespace,
	)
	res, err := promRangeQueries(ctx, []NamedQuery{{Key: "impacted", Query: q}}, 30)
	if err != nil {
		return nil, fmt.Errorf("impacted_services_enricher: prom: %w", err)
	}

	rows := [][]any{}
	headers := []string{"Service", "Namespace", "Requests/sec (avg 30m)"}
	if v, ok := res["impacted"]; ok {
		for _, s := range seriesListEntries(v) {
			svc, _ := s.metric["source_workload_name"].(string)
			ns, _ := s.metric["source_workload_namespace"].(string)
			rate := s.lastValue
			rows = append(rows, []any{svc, ns, fmt.Sprintf("%.3f", rate)})
		}
	}

	return PlaybookActionResponseTable{
		Rows:    rows,
		Headers: headers,
		AdditionalInfo: map[string]any{
			"title":              "Upstream services hitting this pod (30m)",
			"action_name":        "impacted_services_enricher",
			"actual_action_name": "impacted_services_enricher",
			"pod_name":           podName,
			"namespace":          namespace,
		},
		Insight: []PlaybookActionResponseInsight{},
	}, nil
}

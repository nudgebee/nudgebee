package observability

import (
	"encoding/json"
	"testing"

	"nudgebee/services/query"
)

// Placeholder Cloud SQL instance ids (project:instance) — real deployment names
// are blocked from source by the internal gitleaks rules.
const (
	alertingInstance = "demo-proj:orders-primary"
	otherInstance    = "demo-proj:orders-replica"
)

// nestedWorkloadClause mirrors the shape autoExecuteByWorkload builds: every predicate
// lives under And/Or, so the top-level Binary map is empty.
func nestedWorkloadClause(workload, namespace string) query.QueryWhereClause {
	return query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{Or: []query.QueryWhereClause{
				{Binary: query.BinaryWhereClause{
					"destination_workload_name":      {query.Eq: workload},
					"destination_workload_namespace": {query.Eq: namespace},
				}},
				{Binary: query.BinaryWhereClause{
					"workload_name":      {query.Eq: workload},
					"workload_namespace": {query.Eq: namespace},
				}},
			}},
			{Or: []query.QueryWhereClause{
				{Binary: query.BinaryWhereClause{"status_code": {query.Eq: "Error"}}},
			}},
		},
	}
}

func cloudSQLSpan(traceID, spanID, spanName, instance string) map[string]any {
	attrs := map[string]string{"service.name": "appdb", "database": "appdb"}
	if instance != "" {
		attrs["instance"] = instance
	}
	raw, _ := json.Marshal(attrs)
	return map[string]any{
		"trace_id":        traceID,
		"span_id":         spanID,
		"span_name":       spanName,
		"service_name":    "appdb",
		"workload_name":   "appdb",
		"span_attributes": string(raw),
	}
}

// The enricher nests its predicates, which used to read back as "no filter" — the query
// then ran unscoped over the whole GCP project.
func TestExtractTraceStringFilterFindsNestedWorkload(t *testing.T) {
	where := nestedWorkloadClause(alertingInstance, "Cloud SQL")

	if got := requestedWorkload(where); got != alertingInstance {
		t.Fatalf("requestedWorkload(nested) = %q, want the workload the caller asked for", got)
	}
}

func TestExtractTraceStringFilterIgnoresNegatedClause(t *testing.T) {
	excluded := nestedWorkloadClause(alertingInstance, "Cloud SQL")
	where := query.QueryWhereClause{Not: &excluded}

	if got := requestedWorkload(where); got != "" {
		t.Fatalf("requestedWorkload(negated) = %q, want \"\" — a NOT names what to exclude, not the scope", got)
	}
}

func TestPushableAsServiceName(t *testing.T) {
	tests := []struct {
		workload string
		want     bool
	}{
		{"cartservice", true},
		{"", false},
		{alertingInstance, false}, // Cloud SQL instance id — colon breaks LABEL:VALUE
	}
	for _, tt := range tests {
		if got := pushableAsServiceName(tt.workload); got != tt.want {
			t.Errorf("pushableAsServiceName(%q) = %v, want %v", tt.workload, got, tt.want)
		}
	}
}

// The reported defect: a slow-query incident on one instance carried another instance's
// queries as evidence, because the unpushable filter left the result set project-wide.
func TestScopeUnpushedWorkloadDropsOtherInstance(t *testing.T) {
	spans := []map[string]any{
		cloudSQLSpan("t1", "s1", "Cloud SQL Query", alertingInstance),
		cloudSQLSpan("t1", "s2", "Seq Scan", ""),
		cloudSQLSpan("t2", "s3", "Cloud SQL Query", otherInstance),
		cloudSQLSpan("t2", "s4", "Aggregate", ""),
	}

	got := scopeUnpushedWorkload(spans, nestedWorkloadClause(alertingInstance, "Cloud SQL"))

	if len(got) != 2 {
		t.Fatalf("kept %d spans, want 2 (both spans of the matching trace only)", len(got))
	}
	for _, sp := range got {
		if getSpanString(sp, "trace_id") != "t1" {
			t.Errorf("kept span from trace %q, want only t1", getSpanString(sp, "trace_id"))
		}
	}
}

// Plan-node children carry no instance label of their own; matching span-by-span would
// strip them off their root and leave a bare timing with no query plan.
func TestScopeUnpushedWorkloadKeepsPlanChildren(t *testing.T) {
	spans := []map[string]any{
		cloudSQLSpan("t1", "s1", "Cloud SQL Query", alertingInstance),
		cloudSQLSpan("t1", "s2", "Seq Scan", ""),
		cloudSQLSpan("t1", "s3", "Aggregate", ""),
	}

	got := scopeUnpushedWorkload(spans, nestedWorkloadClause(alertingInstance, "Cloud SQL"))

	if len(got) != 3 {
		t.Fatalf("kept %d spans, want all 3 — the trace must stay whole", len(got))
	}
}

func TestScopeUnpushedWorkloadStrictOnNoMatch(t *testing.T) {
	spans := []map[string]any{
		cloudSQLSpan("t2", "s3", "Cloud SQL Query", otherInstance),
	}

	got := scopeUnpushedWorkload(spans, nestedWorkloadClause(alertingInstance, "Cloud SQL"))

	if len(got) != 0 {
		t.Fatalf("kept %d spans, want 0 — returning the project window is the bug being fixed", len(got))
	}
}

// Pushed-down filters were already scoped at the source; those paths must be untouched.
func TestScopeUnpushedWorkloadLeavesPushedDownResults(t *testing.T) {
	spans := []map[string]any{
		{"trace_id": "t1", "span_id": "s1", "service_name": "cartservice", "workload_name": "cartservice"},
		{"trace_id": "t2", "span_id": "s2", "service_name": "frontend", "workload_name": "frontend"},
	}

	got := scopeUnpushedWorkload(spans, nestedWorkloadClause("cartservice", "otel-demo"))

	if len(got) != 2 {
		t.Fatalf("kept %d spans, want 2 — a pushable workload is scoped by Cloud Trace, not here", len(got))
	}
}

func TestBuildCloudTracesRequestSkipsUnpushableServiceName(t *testing.T) {
	s := &GcpTraceSource{}

	cloudSQL := s.buildCloudTracesRequest(TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{Where: nestedWorkloadClause(alertingInstance, "Cloud SQL")},
	})
	if cloudSQL.Query.ServiceName != "" {
		t.Errorf("ServiceName = %q, want \"\" — QueryTracesList would turn it into a service.name filter that matches nothing", cloudSQL.Query.ServiceName)
	}
	if cloudSQL.Query.Filter != "" {
		t.Errorf("Filter = %q, want \"\" — the instance id is not expressible in Cloud Trace's grammar", cloudSQL.Query.Filter)
	}

	cloudRun := s.buildCloudTracesRequest(TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{Where: nestedWorkloadClause("cartservice", "otel-demo")},
	})
	if cloudRun.Query.ServiceName != "cartservice" {
		t.Errorf("ServiceName = %q, want \"cartservice\"", cloudRun.Query.ServiceName)
	}
	if cloudRun.Query.Filter != "service.name:cartservice" {
		t.Errorf("Filter = %q, want \"service.name:cartservice\"", cloudRun.Query.Filter)
	}
}

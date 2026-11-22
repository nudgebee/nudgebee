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

// The reported defect: a slow-query incident on one instance carried another instance's
// queries as evidence, because the filter left the result set project-wide.
func TestScopeToWorkloadDropsOtherInstance(t *testing.T) {
	spans := []map[string]any{
		cloudSQLSpan("t1", "s1", "Cloud SQL Query", alertingInstance),
		cloudSQLSpan("t1", "s2", "Seq Scan", ""),
		cloudSQLSpan("t2", "s3", "Cloud SQL Query", otherInstance),
		cloudSQLSpan("t2", "s4", "Aggregate", ""),
	}

	got := scopeToWorkload(spans, nestedWorkloadClause(alertingInstance, "Cloud SQL"))

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
func TestScopeToWorkloadKeepsPlanChildren(t *testing.T) {
	spans := []map[string]any{
		cloudSQLSpan("t1", "s1", "Cloud SQL Query", alertingInstance),
		cloudSQLSpan("t1", "s2", "Seq Scan", ""),
		cloudSQLSpan("t1", "s3", "Aggregate", ""),
	}

	got := scopeToWorkload(spans, nestedWorkloadClause(alertingInstance, "Cloud SQL"))

	if len(got) != 3 {
		t.Fatalf("kept %d spans, want all 3 — the trace must stay whole", len(got))
	}
}

func TestScopeToWorkloadStrictOnNoMatch(t *testing.T) {
	spans := []map[string]any{
		cloudSQLSpan("t2", "s3", "Cloud SQL Query", otherInstance),
	}

	got := scopeToWorkload(spans, nestedWorkloadClause(alertingInstance, "Cloud SQL"))

	if len(got) != 0 {
		t.Fatalf("kept %d spans, want 0 — returning the project window is the bug being fixed", len(got))
	}
}

// Cloud Trace cannot push service, so a plain (no-colon) service name is now narrowed in
// memory too — the Service filter's whole point (#41419). Previously these passed through
// untouched on the assumption Cloud Trace had already scoped them.
func TestScopeToWorkloadNarrowsPlainServiceName(t *testing.T) {
	spans := []map[string]any{
		{"trace_id": "t1", "span_id": "s1", "service_name": "cartservice", "workload_name": "cartservice"},
		{"trace_id": "t2", "span_id": "s2", "service_name": "frontend", "workload_name": "frontend"},
	}

	got := scopeToWorkload(spans, nestedWorkloadClause("cartservice", "otel-demo"))

	if len(got) != 1 {
		t.Fatalf("kept %d spans, want 1 — only the cartservice trace", len(got))
	}
	if getSpanString(got[0], "trace_id") != "t1" {
		t.Errorf("kept trace %q, want t1", getSpanString(got[0], "trace_id"))
	}
}

// The trace UI sends the Service filter as workload_name:{_in:"nudgebee"} — a scalar,
// not a list. A list-only type switch read it as "no filter", so the GCP query ran
// unscoped over the whole project and the Service filter did nothing (#41419).
func TestTraceFilterScalarHandlesScalarIn(t *testing.T) {
	ops := map[query.BinaryWhereClauseType]any{query.In: "nudgebee"}
	if got := traceFilterScalar(ops); got != "nudgebee" {
		t.Fatalf("traceFilterScalar(scalar _in) = %q, want \"nudgebee\"", got)
	}
}

// The scalar _in workload is still extracted (so the in-memory scope can use it), but it is
// NO LONGER pushed to Cloud Trace — service.name there matches nothing (#41419).
func TestBuildCloudTracesRequestScalarInNotPushed(t *testing.T) {
	s := &GcpTraceSource{}
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"workload_name": {query.In: "nudgebee"},
		},
	}

	if got := requestedWorkload(where); got != "nudgebee" {
		t.Fatalf("requestedWorkload(scalar _in) = %q, want \"nudgebee\" — needed for in-memory scoping", got)
	}

	req := s.buildCloudTracesRequest(TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{Where: where},
	})
	if req.Query.ServiceName != "" {
		t.Errorf("ServiceName = %q, want \"\" — service is scoped in memory, not pushed", req.Query.ServiceName)
	}
	if req.Query.Filter != "" {
		t.Errorf("Filter = %q, want \"\" — service.name must not be pushed to Cloud Trace", req.Query.Filter)
	}
}

// No service/workload is ever pushed to Cloud Trace — neither a Cloud SQL instance id nor a
// plain service name — because its service.name filter matches nothing. Both are scoped in
// memory by scopeToWorkload instead.
func TestBuildCloudTracesRequestNeverPushesService(t *testing.T) {
	s := &GcpTraceSource{}

	for _, tc := range []struct {
		name  string
		where query.QueryWhereClause
	}{
		{"cloud sql instance", nestedWorkloadClause(alertingInstance, "Cloud SQL")},
		{"plain service name", nestedWorkloadClause("cartservice", "otel-demo")},
	} {
		req := s.buildCloudTracesRequest(TracesV3Request{
			QueryRequest: TracesQueryBuilderRequest{Where: tc.where},
		})
		if req.Query.ServiceName != "" {
			t.Errorf("%s: ServiceName = %q, want \"\" — service is never pushed", tc.name, req.Query.ServiceName)
		}
		if req.Query.Filter != "" {
			t.Errorf("%s: Filter = %q, want \"\" — service.name must not reach Cloud Trace", tc.name, req.Query.Filter)
		}
	}
}

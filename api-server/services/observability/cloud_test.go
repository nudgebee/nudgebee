package observability

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"nudgebee/services/cloud"
	"nudgebee/services/security"
)

func TestGetStepDuration_ExplicitInterval(t *testing.T) {
	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()

	got := getStepDuration(300, &start, &end)
	if got != 300*time.Second {
		t.Errorf("getStepDuration(300, ...) = %v, want %v", got, 300*time.Second)
	}
}

func TestGetStepDuration_ZeroInterval_CalculatesFromRange(t *testing.T) {
	start := time.Now().Add(-6 * time.Hour)
	end := time.Now()

	got := getStepDuration(0, &start, &end)
	// 6h = 21600s, 21600/60 = 360s
	expected := 360 * time.Second
	if got != expected {
		t.Errorf("getStepDuration(0, 6h range) = %v, want %v", got, expected)
	}
}

func TestGetStepDuration_ShortRange_MinimumStep(t *testing.T) {
	start := time.Now().Add(-10 * time.Minute)
	end := time.Now()

	got := getStepDuration(0, &start, &end)
	// 10min = 600s, 600/60 = 10s < 60, so clamp to 60s
	expected := 60 * time.Second
	if got != expected {
		t.Errorf("getStepDuration(0, 10min range) = %v, want %v", got, expected)
	}
}

func TestGetStepDuration_NilTimes_Default(t *testing.T) {
	got := getStepDuration(0, nil, nil)
	expected := 60 * time.Second
	if got != expected {
		t.Errorf("getStepDuration(0, nil, nil) = %v, want %v", got, expected)
	}
}

func TestGetStepDuration_OnlyStartNil(t *testing.T) {
	end := time.Now()
	got := getStepDuration(0, nil, &end)
	expected := 60 * time.Second
	if got != expected {
		t.Errorf("getStepDuration(0, nil, end) = %v, want %v", got, expected)
	}
}

func TestGetStepDuration_24hRange(t *testing.T) {
	start := time.Now().Add(-24 * time.Hour)
	end := time.Now()

	got := getStepDuration(0, &start, &end)
	// 24h = 86400s, 86400/60 = 1440s
	expected := 1440 * time.Second
	if got != expected {
		t.Errorf("getStepDuration(0, 24h range) = %v, want %v", got, expected)
	}
}

// Every Metrics Insights target runs in its own call, in REF ID order, and a
// failing target becomes an error result instead of hiding the others (#39201).
func TestCloudMetricsFetchMetricsQuery_RunsEveryQueryKey(t *testing.T) {
	var ran []string
	orig := cloudQueryMetrics
	t.Cleanup(func() { cloudQueryMetrics = orig })
	cloudQueryMetrics = func(_ *security.RequestContext, req cloud.QueryMetricsRequest) (cloud.QueryMetricsResponse, error) {
		ran = append(ran, req.Query.Query)
		if req.Query.Query == "SELECT bad" {
			return cloud.QueryMetricsResponse{}, errors.New("ValidationError")
		}
		return cloud.QueryMetricsResponse{Items: []cloud.MetricItem{{Name: req.Query.Query, Values: []float64{1}}}}, nil
	}

	out, err := (&cloudMetrics{}).FetchMetricsQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), FetchMetricsRequest{
		Queries: map[string]string{"C": "SELECT c", "A": "SELECT a", "B": "SELECT bad"},
	})
	if err != nil {
		t.Fatalf("FetchMetricsQuery error = %v", err)
	}
	if want := []string{"SELECT a", "SELECT bad", "SELECT c"}; !reflect.DeepEqual(ran, want) {
		t.Fatalf("queries ran = %v, want %v", ran, want)
	}
	if len(out.Results) != 3 {
		t.Fatalf("got %d results, want 3: %+v", len(out.Results), out.Results)
	}
	if r := out.Results[0]; r.QueryKey != "A" || r.Payload[0].Metric["name"] != "SELECT a" {
		t.Errorf("result[0] = %+v, want key A with series SELECT a", r)
	}
	if r := out.Results[1]; r.QueryKey != "B" || r.Error == nil || *r.Error != "ValidationError" {
		t.Errorf("result[1] = %+v, want key B with error", r)
	}
	if r := out.Results[2]; r.QueryKey != "C" || r.Payload[0].Metric["name"] != "SELECT c" {
		t.Errorf("result[2] = %+v, want key C with series SELECT c", r)
	}
}

// A single target keeps the old contract: its error fails the whole call.
func TestCloudMetricsFetchMetricsQuery_SingleKeyErrorReturned(t *testing.T) {
	orig := cloudQueryMetrics
	t.Cleanup(func() { cloudQueryMetrics = orig })
	cloudQueryMetrics = func(*security.RequestContext, cloud.QueryMetricsRequest) (cloud.QueryMetricsResponse, error) {
		return cloud.QueryMetricsResponse{}, errors.New("boom")
	}
	_, err := (&cloudMetrics{}).FetchMetricsQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), FetchMetricsRequest{
		Queries: map[string]string{"A": "SELECT a"},
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}
}

// Keys without a statement make the same call; it runs once. More than
// cloudMaxQueriesPerRequest statements: the rest come back as not run.
func TestCloudMetricsFetchMetricsQuery_DedupesEmptyAndCaps(t *testing.T) {
	var ran []string
	orig := cloudQueryMetrics
	t.Cleanup(func() { cloudQueryMetrics = orig })
	cloudQueryMetrics = func(_ *security.RequestContext, req cloud.QueryMetricsRequest) (cloud.QueryMetricsResponse, error) {
		ran = append(ran, req.Query.Query)
		return cloud.QueryMetricsResponse{Items: []cloud.MetricItem{{Name: "m", Values: []float64{1}}}}, nil
	}
	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)

	if _, err := (&cloudMetrics{}).FetchMetricsQuery(ctx, FetchMetricsRequest{Queries: map[string]string{"A": "", "B": " "}}); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 {
		t.Fatalf("empty keys made %d calls, want 1", len(ran))
	}

	// Mixed: only the statement runs; blank targets make no broad call but still get a result.
	ran = nil
	out, err := (&cloudMetrics{}).FetchMetricsQuery(ctx, FetchMetricsRequest{Queries: map[string]string{"A": "", "B": "SELECT b", "C": ""}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ran, []string{"SELECT b"}) {
		t.Fatalf("mixed request ran %v, want only SELECT b", ran)
	}
	keysSeen := map[string]string{}
	for _, r := range out.Results {
		keysSeen[r.QueryKey] = r.Note
	}
	if len(keysSeen) != 3 || !strings.Contains(keysSeen["A"], "no Metrics Insights statement") || !strings.Contains(keysSeen["C"], "no Metrics Insights statement") {
		t.Fatalf("results = %+v, want A, B and C with A and C marked not run", out.Results)
	}

	ran = nil
	queries := map[string]string{"blank": ""}
	_ = out
	for i := 0; i < cloudMaxQueriesPerRequest+2; i++ {
		queries[fmt.Sprintf("q%02d", i)] = fmt.Sprintf("SELECT %d", i)
	}
	out, err = (&cloudMetrics{}).FetchMetricsQuery(ctx, FetchMetricsRequest{Queries: queries})
	if err != nil {
		t.Fatal(err)
	}
	if len(ran) != cloudMaxQueriesPerRequest {
		t.Fatalf("ran %d calls, want %d", len(ran), cloudMaxQueriesPerRequest)
	}
	last := out.Results[len(out.Results)-1]
	if last.Error == nil || !strings.Contains(*last.Error, "not run") {
		t.Fatalf("last result = %+v, want a not-run error", last)
	}
}

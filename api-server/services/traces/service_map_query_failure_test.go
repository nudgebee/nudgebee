package traces

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/security"
)

func TestFocusedTraceQueries(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("tenant", slog.Default(), nil, nil)
	params := TraceQueryParams{AccountID: "account", WorkloadName: "checkout", StartTime: time.Now().Add(-time.Hour), EndTime: time.Now()}
	for _, tc := range []struct {
		name     string
		workload string
		fail     string
		wantErr  bool
	}{
		{"single query fails", "", "all", true},
		{"both queries fail", "checkout", "all", true},
		{"source query fails", "checkout", "workloadNameSourceFilter", false},
		{"destination query fails", "checkout", "workloadNameDestinationFilter", false},
		{"empty query succeeds", "", "none", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := params
			p.WorkloadName = tc.workload
			result, err := fetchTracesWithFocusedStrategy(ctx, p, NewTraceServiceMapBuilder(), func(_ *security.RequestContext, _ TraceQueryParams, q FocusedQueryParams) ([]common.OpenTelemetryTrace, error) {
				if tc.fail == "all" || tc.fail == q.strategy {
					return nil, errors.New("sensitive provider response")
				}
				return nil, nil
			})
			if tc.wantErr {
				if err == nil || result != nil || strings.Contains(err.Error(), "sensitive provider response") {
					t.Fatalf("got map=%v error=%v, want a sanitized error", result, err)
				}
			} else if err != nil || result == nil || len(result.Applications) != 0 {
				t.Fatalf("got map=%v error=%v, want successful empty map", result, err)
			}
		})
	}
}

func TestFocusedTraceQueriesCanceled(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	ctx := security.NewRequestContext(parent, security.NewSecurityContextForTenantAdmin("tenant"), slog.Default(), nil, nil)
	result, err := fetchTracesWithFocusedStrategy(ctx, TraceQueryParams{AccountID: "account"}, NewTraceServiceMapBuilder(), func(*security.RequestContext, TraceQueryParams, FocusedQueryParams) ([]common.OpenTelemetryTrace, error) {
		return nil, context.Canceled
	})
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("got map=%v error=%v", result, err)
	}
}

func TestFocusedTraceQueriesRequireAccountID(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("tenant", slog.Default(), nil, nil)
	called := false
	result, err := fetchTracesWithFocusedStrategy(ctx, TraceQueryParams{}, NewTraceServiceMapBuilder(), func(*security.RequestContext, TraceQueryParams, FocusedQueryParams) ([]common.OpenTelemetryTrace, error) {
		called = true
		return nil, nil
	})
	if result != nil || err == nil || err.Error() != "account ID is required" || called {
		t.Fatalf("got map=%v error=%v fetchCalled=%v, want account ID validation before querying", result, err, called)
	}
}

func TestFetchTraceServiceMapReturnsQueryFailure(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("tenant", slog.Default(), nil, nil)
	result, err := FetchTracesAndBuildServiceMap(ctx, TraceQueryParams{AccountID: "account"})
	if err == nil || err.Error() != "all focused trace queries failed" || result != nil {
		t.Fatalf("got map=%v error=%v, want query failure", result, err)
	}
}

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A slow utilisation chart can only be attributed if every call says which provider
// served it and how long it took (#39744).

// failingKeySource answers every key, marking the ones in fail as failed the way a
// relay source reports a per-key Prometheus error.
type failingKeySource struct {
	MetricSource
	fail map[string]bool
}

func (f *failingKeySource) FetchMetricsQuery(_ *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	out := OutputMetricQuery{}
	for key, query := range req.Queries {
		r := QueryResult{QueryKey: key, Query: query}
		if f.fail[key] {
			msg := "dial tcp: connect: connection refused"
			r.Error = &msg
		}
		out.Results = append(out.Results, r)
	}
	return out, nil
}

func loggingRequestContext(buf *bytes.Buffer) *security.RequestContext {
	return security.NewRequestContext(context.Background(), nil, slog.New(slog.NewJSONHandler(buf, nil)), nil, nil)
}

func utilisationServedLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	for _, line := range bytes.Split(buf.Bytes(), []byte("\n")) {
		var rec map[string]any
		if json.Unmarshal(line, &rec) == nil && rec["msg"] == "observability: utilisation served" {
			return rec
		}
	}
	t.Fatalf("no utilisation served line in log:\n%s", buf.String())
	return nil
}

func workloadUtilisationRequest(metrics ...any) GetUtilisationTrendRequest {
	end := time.Now().UTC()
	return GetUtilisationTrendRequest{
		AccountId: "acc",
		StartTime: end.Add(-time.Hour).UnixMilli(),
		EndTime:   end.UnixMilli(),
		Request: map[string]any{
			"kind":               "deployment",
			"workload_namespace": "shop",
			"workload_name":      "api",
			"metrics":            metrics,
		},
	}
}

func TestFetchMetricUtilisation_LogsProviderAndDuration(t *testing.T) {
	stubUtilisationProvider(t, "prometheus", "agent")
	stubMetricSource(t, &failingKeySource{fail: map[string]bool{"cpu_usage": true}})

	var buf bytes.Buffer
	out, err := FetchMetricUtilisation(loggingRequestContext(&buf), workloadUtilisationRequest("cpu_usage", "memory_usage"))
	require.NoError(t, err)
	require.Len(t, out.Results, 2)

	rec := utilisationServedLine(t, &buf)
	assert.Equal(t, "prometheus", rec["provider"])
	assert.Equal(t, "agent", rec["source"])
	assert.Equal(t, "acc", rec["account_id"])
	assert.EqualValues(t, 2, rec["keys"])
	assert.EqualValues(t, 1, rec["failed_keys"])
	assert.Contains(t, rec, "duration")
	assert.Nil(t, rec["error"])
}

func TestFetchMetricUtilisation_LogsTheError(t *testing.T) {
	stubUtilisationProvider(t, "no_such_provider", "user")

	var buf bytes.Buffer
	_, err := FetchMetricUtilisation(loggingRequestContext(&buf), workloadUtilisationRequest("cpu_usage"))
	require.Error(t, err)

	rec := utilisationServedLine(t, &buf)
	assert.Equal(t, "no_such_provider", rec["provider"])
	assert.Contains(t, rec["error"], "not supporting this metrics provider")
}

package observability

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fetchSplunkEnterpriseUtilisation end to end against a fake Splunk: catalog lookup,
// per-key resolution, dispatch through FetchMetricsQuery with the provider pinned, and the
// merge of served keys with the ones that could not be served.
func TestFetchSplunkEnterpriseUtilisationEndToEnd(t *testing.T) {
	var mu sync.Mutex
	var searches []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		spl := r.PostForm.Get("search")
		mu.Lock()
		searches = append(searches, spl)
		mu.Unlock()

		var results []map[string]any
		switch {
		case strings.HasPrefix(spl, "| mcatalog values(metric_name)"):
			results = []map[string]any{{"values(metric_name)": []any{"k8s.pod.cpu.usage", "k8s.pod.memory.usage"}}}
		case strings.HasPrefix(spl, "| mstats"):
			results = []map[string]any{{"_time": "2026-09-25T10:00:00.000+00:00", "nb_value": "1.5"}}
		default:
			t.Errorf("unexpected search: %s", spl)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	defer server.Close()

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(integrations.GetSplunkEnterpriseConfig,
		func(_ *security.RequestContext, _ string) (integrations.SplunkEnterpriseConfig, error) {
			return integrations.SplunkEnterpriseConfig{
				URL: server.URL, AuthType: integrations.SplunkEnterpriseAuthBasic,
				Username: "u", Password: "p", MetricIndex: "otel_metrics", App: "search",
			}, nil
		})

	var gotProvider, gotSource string
	origSource := metricsSourceForAccount
	metricsSourceForAccount = func(_ *security.RequestContext, _ string, provider, source string) (MetricSource, error) {
		gotProvider, gotSource = provider, source
		return &SplunkEnterpriseMetricSource{}, nil
	}
	defer func() { metricsSourceForAccount = origSource }()

	now := time.Now()
	req := GetUtilisationTrendRequest{AccountId: "acct", StartTime: now.Add(-time.Hour).UnixMilli(), EndTime: now.UnixMilli()}
	meta := RequestMetadata{
		Namespace: "demo", Name: "checkout", Kind: "deployment", Step: "60s",
		RequestedMetrics: []string{"cpu_usage", "cpu_total", "no_such_key"},
	}

	out, err := fetchSplunkEnterpriseUtilisation(mockRequestContext(), req, meta, "user", true)
	require.NoError(t, err)

	assert.Equal(t, "splunk_enterprise", gotProvider, "the query must be pinned to the Splunk source")
	assert.Equal(t, "user", gotSource)

	byKey := map[string]QueryResult{}
	for _, r := range out.Results {
		byKey[r.QueryKey] = r
	}
	require.Len(t, byKey, 3, "every requested key gets exactly one result")

	served := byKey["cpu_usage"]
	require.Nil(t, served.Error)
	require.Len(t, served.Payload, 1)
	assert.Equal(t, []float64{1.5}, served.Payload[0].Values)
	assert.Contains(t, served.Query, `mstats avg("k8s.pod.cpu.usage")`)
	assert.Contains(t, served.Query, `k8s.pod.name="checkout-*"`)
	assert.Contains(t, served.Query, "BY k8s.namespace.name, k8s.pod.name")

	// "no metric here" and "this key is not served" are capability statements, so both come
	// back empty WITH a note and no error. Only a query that actually failed sets Error.
	assert.Nil(t, byKey["cpu_total"].Error)
	assert.Contains(t, byKey["cpu_total"].Note, "no metric in index")
	assert.Empty(t, byKey["cpu_total"].Payload)
	assert.Nil(t, byKey["no_such_key"].Error)
	assert.Contains(t, byKey["no_such_key"].Note, "not served by this provider")
	assert.Empty(t, byKey["no_such_key"].Payload)

	// One catalog read, one search for the one resolvable key -- unresolved keys must not
	// cost a Splunk search.
	assert.Len(t, searches, 2)
}

// Regression for the sample-sum inflation, against a live Splunk whose collector sends
// several samples per bucket (the fixtures that hid the bug had exactly one).
//
// Ground truth is computed here in Go from the raw samples -- mean per series per bucket,
// summed across series, averaged across complete buckets -- and compared with what the
// generated query returns. The old `mstats sum(...) span=` shape fails this by the
// samples-per-bucket factor.
//
// Skipped unless SPLUNK_ENTERPRISE_TEST_URL, _USERNAME and _PASSWORD are set.
// SPLUNK_ENTERPRISE_TEST_METRIC_INDEX (default otel_metrics) and _FAMILY (default
// k8s.pod.cpu.usage) pick the data.
func TestSplunkUtilisationDoesNotSumSamplesLive(t *testing.T) {
	url := os.Getenv("SPLUNK_ENTERPRISE_TEST_URL")
	if url == "" || os.Getenv("SPLUNK_ENTERPRISE_TEST_USERNAME") == "" {
		t.Skip("SPLUNK_ENTERPRISE_TEST_URL / _USERNAME / _PASSWORD not set")
	}
	index := envOr("SPLUNK_ENTERPRISE_TEST_METRIC_INDEX", "otel_metrics")
	family := envOr("SPLUNK_ENTERPRISE_TEST_FAMILY", "k8s.pod.cpu.usage")
	cfg := integrations.SplunkEnterpriseConfig{
		URL: url, AuthType: integrations.SplunkEnterpriseAuthBasic,
		Username: os.Getenv("SPLUNK_ENTERPRISE_TEST_USERNAME"), Password: os.Getenv("SPLUNK_ENTERPRISE_TEST_PASSWORD"),
		MetricIndex: index, App: "search", InsecureSkipVerify: true,
	}

	end := time.Now().Unix()
	start := end - 30*60
	from, to := strconv.FormatInt(start, 10), strconv.FormatInt(end, 10)

	dims := splunkUtilisationSeriesDims(family)
	raw, err := execSplunkEnterpriseSearch(cfg,
		fmt.Sprintf(`| mstats avg("%s") AS v WHERE index="%s" span=1s BY %s`, family, index, strings.Join(dims, ", ")),
		from, to, splunkEnterpriseMetricMaxRows, splunkEnterpriseMetricSearchTimeout)
	require.NoError(t, err)
	require.NotEmpty(t, raw, "no %s samples in %s over the last 30m", family, index)

	for _, step := range []int64{60, 300} {
		t.Run(fmt.Sprintf("span=%ds", step), func(t *testing.T) {
			type cell struct {
				sum float64
				n   int
			}
			buckets := map[int64]map[string]*cell{}
			maxSamples := 0
			for _, row := range raw {
				ts := splunkEnterpriseRowTimestampMs(row, end*1000) / 1000 // OSS reads ms (#733); bucket in seconds
				bucket := ts - ts%step
				if bucket+step > end {
					continue // still filling; the query drops it too
				}
				v, perr := strconv.ParseFloat(formatSplunkValue(row["v"]), 64)
				if perr != nil {
					continue
				}
				series := make([]string, 0, len(dims))
				for _, d := range dims {
					series = append(series, formatSplunkValue(row[d]))
				}
				key := strings.Join(series, "|")
				if buckets[bucket] == nil {
					buckets[bucket] = map[string]*cell{}
				}
				c := buckets[bucket][key]
				if c == nil {
					c = &cell{}
					buckets[bucket][key] = c
				}
				c.sum += v
				c.n++
				maxSamples = max(maxSamples, c.n)
			}
			require.NotEmpty(t, buckets)
			require.Greater(t, maxSamples, 1, "the data must carry several samples per series per bucket, or this test proves nothing")

			var total float64
			for _, series := range buckets {
				var bucketSum float64
				for _, c := range series {
					bucketSum += c.sum / float64(c.n)
				}
				total += bucketSum
			}
			truth := total / float64(len(buckets))

			spl, err := buildSplunkUtilisationQuery(family, index, int(step), "avg", nil)
			require.NoError(t, err)
			require.NoError(t, validateSplunkEnterpriseMetricQuery(spl, index))
			rows, err := execSplunkEnterpriseSearch(cfg, spl, from, to, splunkEnterpriseMetricMaxRows, splunkEnterpriseMetricSearchTimeout)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			got, err := strconv.ParseFloat(formatSplunkValue(rows[0][splunkEnterpriseValueColumn]), 64)
			require.NoError(t, err)

			assert.InDelta(t, 1.0, got/truth, 0.02, "query %.4f vs ground truth %.4f (max %d samples per bucket)", got, truth, maxSamples)
			assert.False(t, math.IsNaN(got))
		})
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

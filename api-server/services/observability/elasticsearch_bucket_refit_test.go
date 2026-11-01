package observability

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The refusal as Elasticsearch 8.19 returns it: the exception sits in caused_by, with
// an empty root_cause. Captured from a 6h cluster-scope p90_mem search.
const esTooManyBucketsBody = `{"error":{"root_cause":[],"type":"search_phase_execution_exception","reason":"","phase":"fetch","grouped":true,"failed_shards":[],"caused_by":{"type":"too_many_buckets_exception","reason":"Trying to create too many buckets. Must be less than or equal to: [65536] but this number of buckets was exceeded. This limit can be set by changing the [search.max_buckets] cluster level setting.","max_buckets":65536}},"status":400}`

func TestESUtilTooManyBuckets(t *testing.T) {
	limit, ok := esUtilTooManyBuckets(&esStatusError{operation: "utilisation query", status: 400, body: []byte(esTooManyBucketsBody)})
	assert.True(t, ok)
	assert.EqualValues(t, 65536, limit)

	// A cluster with a raised limit is refitted against that limit, not the default.
	raised := strings.Replace(esTooManyBucketsBody, `"max_buckets":65536`, `"max_buckets":200000`, 1)
	limit, ok = esUtilTooManyBuckets(fmt.Errorf("wrapped: %w", &esStatusError{status: 400, body: []byte(raised)}))
	assert.True(t, ok)
	assert.EqualValues(t, 200000, limit)

	// Any other failure is not retried.
	for _, err := range []error{
		nil,
		errors.New("dial tcp: connection refused"),
		&esStatusError{status: 400, body: []byte(`{"error":{"type":"parsing_exception","root_cause":[{"type":"parsing_exception"}]},"status":400}`)},
		&esStatusError{status: 502, body: []byte(`<html>bad gateway</html>`)},
	} {
		_, ok := esUtilTooManyBuckets(err)
		assert.Falsef(t, ok, "%v", err)
	}
}

func TestESUtilStepForBucketLimit(t *testing.T) {
	const sixHours = 6 * 3600

	// The dev cluster: ~530 pods plus the time bucket itself, refused at the derived 72s.
	step := esUtilStepForBucketLimit(sixHours, 531, 65536)
	require.Greater(t, step, int64(72))
	assert.LessOrEqual(t, (sixHours/step+1)*531, int64(65536), "the refitted search must fit the limit")

	// Nothing fits: one group set already overflows two time buckets.
	assert.Zero(t, esUtilStepForBucketLimit(sixHours, 40000, 65536))
	assert.Zero(t, esUtilStepForBucketLimit(0, 531, 65536))
	assert.Zero(t, esUtilStepForBucketLimit(sixHours, 0, 65536))
}

// fakeES answers utilisation searches the way Elasticsearch counts buckets. Each
// src_* aggregation reports groups[name] series in every time bucket (absent = the
// cluster does not ship that source: no documents, no buckets), and a search whose
// time buckets x (1 + groups), summed over the sources it carries, exceeds maxBuckets
// is refused with the real error body. Every request's source names and steps are
// recorded.
type fakeES struct {
	rangeSec   int64
	groups     map[string]int64
	maxBuckets int64

	calls    atomic.Int32
	requests [][]string // per request: "src_N@<interval>"
}

func (f *fakeES) serve(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Aggs map[string]struct {
				Aggs struct {
					TS struct {
						DateHistogram struct {
							FixedInterval string `json:"fixed_interval"`
						} `json:"date_histogram"`
					} `json:"ts"`
				} `json:"aggs"`
			} `json:"aggs"`
		}
		require.NoError(t, json.Unmarshal(raw, &body))

		// Count first, as Elasticsearch does: a refused search builds no response.
		stepOf := map[string]int64{}
		var seen []string
		var total int64
		for name, a := range body.Aggs {
			interval := a.Aggs.TS.DateHistogram.FixedInterval
			seen = append(seen, name+"@"+interval)
			step, err := strconv.ParseInt(strings.TrimSuffix(interval, "s"), 10, 64)
			require.NoError(t, err)
			stepOf[name] = step
			if g := f.groups[name]; g > 0 {
				total += (f.rangeSec/step + 1) * (1 + g)
			}
		}
		f.requests = append(f.requests, seen)
		if total > f.maxBuckets {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(esTooManyBucketsBody))
			return
		}

		aggs := map[string]any{}
		for name, step := range stepOf {
			g := f.groups[name]
			if g == 0 {
				aggs[name] = map[string]any{"doc_count": 0, "ts": map[string]any{"buckets": []any{}}}
				continue
			}
			timeBuckets := f.rangeSec/step + 1
			buckets := make([]any, 0, timeBuckets)
			for i := int64(0); i < timeBuckets; i++ {
				g0 := make([]any, 0, g)
				for j := int64(0); j < g; j++ {
					g0 = append(g0, map[string]any{"key": fmt.Sprintf("s-%d", j), "v": map[string]any{"value": 1}})
				}
				buckets = append(buckets, map[string]any{"key": i * step * 1000, "total": map[string]any{"value": g}, "g0": map[string]any{"buckets": g0}})
			}
			aggs[name] = map[string]any{"doc_count": g * timeBuckets, "ts": map[string]any{"buckets": buckets}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"aggregations": aggs})
	}))
}

// sent reports whether request i carried source name, and at which interval.
func (f *fakeES) sent(i int, name string) (string, bool) {
	for _, s := range f.requests[i] {
		if n, interval, _ := strings.Cut(s, "@"); n == name {
			return interval, true
		}
	}
	return "", false
}

// Cluster-scope p90_mem: src_0 sums nodes (ECS), src_1 sums pods (ECS, Fallback),
// src_2/src_3 are the OTLP and OTel node sums, src_4 the OTel pod sum (Fallback).
func p90MemSpec(t *testing.T, srvURL string, startMs, rangeSec int64) esUtilSearchSpec {
	plan, ok := esUtilisationMetric("p90_mem", esScopeCluster, false)
	require.True(t, ok)
	require.True(t, plan.Sources[1].Fallback && plan.Sources[4].Fallback, "the pod sums are the fallbacks")
	require.False(t, plan.Sources[0].Fallback || plan.Sources[2].Fallback || plan.Sources[3].Fallback)
	return esUtilSearchSpec{
		cfg: &ElasticsearchConfig{Url: srvURL}, esURL: srvURL + "/metrics-*/_search",
		plan: plan, scope: esScopeCluster, startMs: startMs, endMs: startMs + rangeSec*1000,
	}
}

const testStartMs = int64(1_700_000_000_000)

// The dev cluster's shape: 18 nodes, ~526 pods. Sent together, the pod fallback alone
// breaches the limit at 6h; sent node-first, the node sum answers at full resolution
// and the pod sum is never computed.
func TestESUtilSearch_NodeSourcesAnswerWithoutThePodFallback(t *testing.T) {
	const rangeSec = 6 * 3600
	f := &fakeES{rangeSec: rangeSec, maxBuckets: 65536, groups: map[string]int64{"src_0": 18, "src_1": 526}}
	srv := f.serve(t)
	defer srv.Close()

	out, sent, err := p90MemSpec(t, srv.URL, testStartMs, rangeSec).search(72)
	require.NoError(t, err)
	assert.EqualValues(t, 1, f.calls.Load())
	_, podSent := f.sent(0, "src_1")
	assert.False(t, podSent, "the pod fallback must not be sent when nodes answer")
	interval, _ := f.sent(0, "src_0")
	assert.Equal(t, "72s", interval, "full resolution, no refit")
	assert.Contains(t, sent, `"fixed_interval":"72s"`)
	assert.Positive(t, esUtilMatchedDocs(out))
}

// A cluster without the node metricset: the node-only search matches nothing, so the
// full search is sent — sized first, since it sums pods across the cluster.
func TestESUtilSearch_FallsBackWhenNoNodeSourceHasData(t *testing.T) {
	const rangeSec = 3600
	f := &fakeES{rangeSec: rangeSec, maxBuckets: 65536, groups: map[string]int64{"src_1": 50}}
	srv := f.serve(t)
	defer srv.Close()

	out, sent, err := p90MemSpec(t, srv.URL, testStartMs, rangeSec).search(60)
	require.NoError(t, err)
	assert.EqualValues(t, 3, f.calls.Load(), "node-only, probe, full")
	interval, podSent := f.sent(2, "src_1")
	assert.True(t, podSent)
	assert.Equal(t, "60s", interval, "a search that fits keeps its step")
	assert.Contains(t, sent, `"fixed_interval":"60s"`)
	assert.Positive(t, esUtilMatchedDocs(out))
}

// A pod-summed search too big for its step is sized BEFORE it is sent — on a real
// cluster its refusal takes longer than the client waits — and the next load of the
// same window reuses the step without probing.
func TestESUtilSearch_PodSearchIsSizedBeforeSendingAndRemembered(t *testing.T) {
	const rangeSec = 6 * 3600
	f := &fakeES{rangeSec: rangeSec, maxBuckets: 65536, groups: map[string]int64{"src_1": 300}}
	srv := f.serve(t)
	defer srv.Close()

	out, sent, err := p90MemSpec(t, srv.URL, testStartMs, rangeSec).search(72)
	require.NoError(t, err)
	assert.Positive(t, esUtilMatchedDocs(out))
	require.EqualValues(t, 3, f.calls.Load(), "node-only, probe, full at the fitted step — never refused")
	probeInterval, _ := f.sent(1, "src_1")
	assert.Equal(t, fmt.Sprintf("%ds", rangeSec/esUtilProbeBuckets), probeInterval)
	fitted, _ := f.sent(2, "src_1")
	assert.NotEqual(t, "72s", fitted)
	assert.Contains(t, sent, `"fixed_interval":"`+fitted+`"`, "the result carries the query that answered")

	// A minute later, same window length: no probe.
	_, _, err = p90MemSpec(t, srv.URL, testStartMs+60_000, rangeSec).search(72)
	require.NoError(t, err)
	assert.EqualValues(t, 5, f.calls.Load(), "node-only, then full at the remembered step")
	again, _ := f.sent(4, "src_1")
	assert.Equal(t, fitted, again)
}

// A node-grouped search is sent as before; if it is refused it is refitted, and the
// refitted step is remembered.
func TestESUtilSearch_RefusedNodeSearchIsRefittedAndRemembered(t *testing.T) {
	const rangeSec = 6 * 3600
	f := &fakeES{rangeSec: rangeSec, maxBuckets: 65536, groups: map[string]int64{"src_0": 300}}
	srv := f.serve(t)
	defer srv.Close()

	plan, ok := esUtilisationMetric("cpu_total", esScopeCluster, false)
	require.True(t, ok)
	spec := esUtilSearchSpec{cfg: &ElasticsearchConfig{Url: srv.URL}, esURL: srv.URL + "/m/_search",
		plan: plan, scope: esScopeCluster, startMs: testStartMs, endMs: testStartMs + rangeSec*1000}
	require.False(t, spec.groupsByPodAtWideScope(false), "node-grouped: not probed up front")

	_, sent, err := spec.search(72)
	require.NoError(t, err)
	require.EqualValues(t, 3, f.calls.Load(), "search (refused), probe, retry")
	retry, _ := f.sent(2, "src_0")
	assert.NotEqual(t, "72s", retry)
	assert.Contains(t, sent, `"fixed_interval":"`+retry+`"`)

	spec.startMs, spec.endMs = spec.startMs+60_000, spec.endMs+60_000
	_, _, err = spec.search(72)
	require.NoError(t, err)
	assert.EqualValues(t, 4, f.calls.Load(), "straight to the remembered step")
	again, _ := f.sent(3, "src_0")
	assert.Equal(t, retry, again)
}

// When even the probe is over the limit, no step can answer, and nothing else is sent.
func TestESUtilSearch_UnanswerableReturnsTheRefusal(t *testing.T) {
	const rangeSec = 6 * 3600
	f := &fakeES{rangeSec: rangeSec, maxBuckets: 65536, groups: map[string]int64{"src_1": 40000}}
	srv := f.serve(t)
	defer srv.Close()

	_, _, err := p90MemSpec(t, srv.URL, testStartMs, rangeSec).search(72)
	require.Error(t, err)
	_, refused := esUtilTooManyBuckets(err)
	assert.True(t, refused)
	assert.EqualValues(t, 2, f.calls.Load(), "node-only, probe — the doomed search is not sent")
}

// A key with no fallback sources is sent once, as before.
func TestESUtilSearch_PlanWithoutFallbackIsSentOnce(t *testing.T) {
	const rangeSec = 3600
	f := &fakeES{rangeSec: rangeSec, maxBuckets: 65536, groups: map[string]int64{"src_0": 18}}
	srv := f.serve(t)
	defer srv.Close()

	plan, ok := esUtilisationMetric("cpu_total", esScopeCluster, false)
	require.True(t, ok)
	require.False(t, esUtilHasFallback(plan))
	spec := esUtilSearchSpec{cfg: &ElasticsearchConfig{Url: srv.URL}, esURL: srv.URL + "/m/_search",
		plan: plan, scope: esScopeCluster, startMs: testStartMs, endMs: testStartMs + rangeSec*1000}
	_, sent, err := spec.search(60)
	require.NoError(t, err)
	assert.EqualValues(t, 1, f.calls.Load())
	assert.JSONEq(t, mustRender(t, spec, 60), sent, "byte-identical to the pre-change query")
}

func mustRender(t *testing.T, s esUtilSearchSpec, step int64) string {
	b, err := json.Marshal(buildESUtilisationAggQuery(s.plan, s.meta, s.scope, s.startMs, s.endMs, step))
	require.NoError(t, err)
	return string(b)
}

func TestESUtilStepMemory(t *testing.T) {
	m := &esUtilStepMemory{steps: map[string]esUtilRememberedStep{}}
	now := time.Unix(1_700_000_000, 0)

	m.put("k", 300, now)
	step, ok := m.get("k", now.Add(esUtilStepMemoryTTL-time.Second))
	assert.True(t, ok)
	assert.EqualValues(t, 300, step)

	_, ok = m.get("k", now.Add(esUtilStepMemoryTTL+time.Second))
	assert.False(t, ok, "expired steps are forgotten so a shrunk cluster gets its resolution back")

	// Bounded: a full memory sweeps expired entries, and starts over if none were.
	for i := 0; i < esUtilStepMemoryMax; i++ {
		m.put(fmt.Sprintf("k%d", i), 60, now)
	}
	m.put("new", 120, now)
	assert.LessOrEqual(t, len(m.steps), esUtilStepMemoryMax)
	step, ok = m.get("new", now)
	assert.True(t, ok)
	assert.EqualValues(t, 120, step)
}

// The memory is keyed by integration and index, not by URL: two integrations on one
// URL may see different documents, while one integration is one connection however
// its URL is spelled.
func TestESUtilStepMemoryKey_IdentifiesTheIntegrationAndIndex(t *testing.T) {
	plan, ok := esUtilisationMetric("cpu_request", esScopeCluster, false)
	require.True(t, ok)
	spec := func(integrationID, url, index string, startMs int64) esUtilSearchSpec {
		return esUtilSearchSpec{cfg: &ElasticsearchConfig{Url: url, IntegrationID: integrationID},
			esURL: url + "/" + index + "/_search", index: index, plan: plan, scope: esScopeCluster,
			startMs: startMs, endMs: startMs + 6*3600*1000}
	}
	key := func(s esUtilSearchSpec) string {
		k, err := s.stepMemoryKey(72, false)
		require.NoError(t, err)
		return k
	}

	base := key(spec("int-a", "https://es:9200", "metrics-*", testStartMs))
	assert.Equal(t, base, key(spec("int-a", "https://es:9200", "metrics-*", testStartMs+60_000)), "same window later")
	assert.Equal(t, base, key(spec("int-a", "https://es.example:9200", "metrics-*", testStartMs)), "one integration, URL spelled differently")
	assert.NotEqual(t, base, key(spec("int-b", "https://es:9200", "metrics-*", testStartMs)), "another integration on the same URL")
	assert.NotEqual(t, base, key(spec("int-a", "https://es:9200", "metrics-other-*", testStartMs)), "another index")

	// Without an integration the URL, which carries the index, is the connection.
	noID := key(spec("", "https://es:9200", "metrics-*", testStartMs))
	assert.NotEqual(t, base, noID)
	assert.NotEqual(t, noID, key(spec("", "https://es:9200", "metrics-other-*", testStartMs)))
}

// A pod-summed search over a window with no data is not "too big": it is sent as
// before and answers empty, and a cluster shipping neither node nor pod data still
// reaches the full search.
func TestESUtilSearch_NoDataIsNotTooManyBuckets(t *testing.T) {
	const rangeSec = 6 * 3600
	f := &fakeES{rangeSec: rangeSec, maxBuckets: 65536, groups: map[string]int64{}}
	srv := f.serve(t)
	defer srv.Close()

	plan, ok := esUtilisationMetric("cpu_request", esScopeCluster, false)
	require.True(t, ok)
	spec := esUtilSearchSpec{cfg: &ElasticsearchConfig{Url: srv.URL}, esURL: srv.URL + "/m/_search",
		plan: plan, scope: esScopeCluster, startMs: testStartMs, endMs: testStartMs + rangeSec*1000}
	require.True(t, spec.groupsByPodAtWideScope(false))
	out, sent, err := spec.search(72)
	require.NoError(t, err)
	assert.Zero(t, esUtilMatchedDocs(out))
	assert.Contains(t, sent, `"fixed_interval":"72s"`, "nothing to size, so the derived step is kept")

	_, _, err = p90MemSpec(t, srv.URL, testStartMs, rangeSec).search(72)
	require.NoError(t, err, "node-only empty, then the full search — also empty, not an error")
}

package observability

// Runs the zero-match sample against a real Elasticsearch. Skipped unless
// NB_TEST_ES_URL is set, like the other live tests in this package.
//
//	NB_TEST_ES_URL=https://localhost:9200 NB_TEST_ES_USER=elastic NB_TEST_ES_PASSWORD=... \
//	NB_TEST_ES_INSECURE=true NB_TEST_ES_REMOTE=ccs-self \
//	go test -race -count=1 -run TestLiveESZeroMatchSample ./observability/
//
// NB_TEST_ES_REMOTE names a cross-cluster remote that points back at the same cluster;
// when set, every case also runs through "<remote>:<index>". The test writes into data
// streams named metrics-nbtest.*-<random> and deletes them afterwards.
//
// The layouts are random on purpose. A test built from the layouts we know passes for
// the same reason the code does; random field names, depths and timestamp fields are
// what checks that the sample needs no knowledge of the layout.

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func liveESConfig(t *testing.T) *ElasticsearchConfig {
	t.Helper()
	url := os.Getenv("NB_TEST_ES_URL")
	if url == "" {
		t.Skip("NB_TEST_ES_URL not set")
	}
	return &ElasticsearchConfig{
		Url:           strings.TrimRight(url, "/"),
		Username:      os.Getenv("NB_TEST_ES_USER"),
		Password:      os.Getenv("NB_TEST_ES_PASSWORD"),
		AuthType:      "basic",
		TLSSkipVerify: os.Getenv("NB_TEST_ES_INSECURE") == "true",
	}
}

// liveESIndexExprs is the local data stream name, plus its remote form when a loopback
// remote is configured.
func liveESIndexExprs(ds string) []string {
	out := []string{ds}
	if r := os.Getenv("NB_TEST_ES_REMOTE"); r != "" {
		out = append(out, r+":"+ds)
	}
	return out
}

func liveESBulk(t *testing.T, cfg *ElasticsearchConfig, ds string, docs []map[string]any) {
	t.Helper()
	var b strings.Builder
	for _, d := range docs {
		b.WriteString(`{"create":{}}` + "\n")
		line, err := json.Marshal(d)
		require.NoError(t, err)
		b.Write(line)
		b.WriteByte('\n')
	}
	resp, err := esRequest("POST", fmt.Sprintf("%s/%s/_bulk?refresh=true", cfg.Url, ds), b.String(), cfg) //nolint:bodyclose
	require.NoError(t, err)
	raw, err := readResponse(resp, "bulk")
	require.NoError(t, err)
	var parsed struct {
		Errors bool `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(raw, &parsed))
	require.False(t, parsed.Errors, "bulk load failed: %.500s", raw)
	t.Cleanup(func() {
		resp, err := esRequest("DELETE", fmt.Sprintf("%s/_data_stream/%s", cfg.Url, ds), "", cfg)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Logf("cleanup of %s returned %d", ds, resp.StatusCode)
			}
		}
	})
}

func liveESRun(t *testing.T, cfg *ElasticsearchConfig, index, dsl string, start, end time.Time) QueryResult {
	t.Helper()
	out, err := runESMetricsQueries(cfg, index, FetchMetricsRequest{
		Request:   map[string]any{"query_type": "dsl", "zero_match_sample": true},
		Queries:   map[string]string{"q": dsl},
		StartTime: start.UnixMilli(), EndTime: end.UnixMilli(),
	})
	require.NoError(t, err)
	require.Len(t, out.Results, 1)
	r := out.Results[0]
	require.Nil(t, r.Error, "query error: %v", r.Error)
	return r
}

// randomLayoutDoc builds a document with random nested field names. It holds needle at
// one random path, a number and a string array elsewhere, and its time at timeField.
func randomLayoutDoc(rng *rand.Rand, needle, timeField string, ts time.Time) (map[string]any, string) {
	word := func() string {
		const letters = "abcdefghijklmnopqrstuvwxyz"
		n := 3 + rng.Intn(6)
		b := make([]byte, n)
		for i := range b {
			b[i] = letters[rng.Intn(len(letters))]
		}
		return string(b)
	}
	path := func() []string {
		p := make([]string, 1+rng.Intn(4))
		for i := range p {
			p[i] = "f" + word()
		}
		return p
	}
	doc := map[string]any{}
	set := func(p []string, v any) {
		m := doc
		for _, k := range p[:len(p)-1] {
			next, ok := m[k].(map[string]any)
			if !ok {
				next = map[string]any{}
				m[k] = next
			}
			m = next
		}
		m[p[len(p)-1]] = v
	}
	needlePath := path()
	set(needlePath, needle)
	set(path(), rng.Float64()*100)
	set(path(), []any{word(), word()})
	doc[timeField] = ts.UTC().Format(time.RFC3339Nano)
	return doc, strings.Join(needlePath, ".")
}

func TestLiveESZeroMatchSample_RandomLayouts(t *testing.T) {
	cfg := liveESConfig(t)
	enableSample(t, true)
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	now := time.Now()

	for i := 0; i < 5; i++ {
		i := i
		t.Run(fmt.Sprintf("layout-%d", i), func(t *testing.T) {
			ds := fmt.Sprintf("metrics-nbtest.random%d-%d", i, rng.Int63())
			needle := fmt.Sprintf("needle%dx%d", i, rng.Intn(1e6))
			doc, needlePath := randomLayoutDoc(rng, needle, "@timestamp", now)
			noise := []map[string]any{doc}
			for j := 0; j < 50; j++ {
				d, _ := randomLayoutDoc(rng, fmt.Sprintf("other%d", j), "@timestamp", now)
				noise = append(noise, d)
			}
			liveESBulk(t, cfg, ds, noise)

			for _, idx := range liveESIndexExprs(ds) {
				// The value is right, the field is not: the sample must show where it lives.
				r := liveESRun(t, cfg, idx, fmt.Sprintf(`{"query":{"term":{"no.such.field":%q}}}`, needle),
					now.Add(-time.Hour), now.Add(time.Minute))
				require.NotNil(t, r.DocsMatched)
				assert.Zero(t, *r.DocsMatched)
				assert.Contains(t, r.Sample, needlePath+": "+needle, "index %s, note: %s", idx, r.Note)
				assert.NotContains(t, r.Note, "no documents at all")

				// A wildcard on the value's prefix finds it too.
				r = liveESRun(t, cfg, idx, fmt.Sprintf(`{"query":{"wildcard":{"no.such.field":{"value":%q}}}}`,
					needle[:len(needle)-3]+"*"), now.Add(-time.Hour), now.Add(time.Minute))
				assert.Contains(t, r.Sample, needlePath+": "+needle, "wildcard, index %s, note: %s", idx, r.Note)
			}
		})
	}
}

// The time sits in a field the window does not look at: the window matches nothing, and
// that must read as "the index has data", never as an empty index.
func TestLiveESZeroMatchSample_UnknownTimeFieldIsNotAbsence(t *testing.T) {
	cfg := liveESConfig(t)
	enableSample(t, true)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	now := time.Now()
	ds := fmt.Sprintf("metrics-nbtest.timefield-%d", rng.Int63())
	doc, needlePath := randomLayoutDoc(rng, "needletime", "observed_at", now)
	// Data streams require @timestamp; put it far outside the window, as a delayed or
	// back-filled pipeline would.
	doc["@timestamp"] = now.Add(-72 * time.Hour).UTC().Format(time.RFC3339Nano)
	liveESBulk(t, cfg, ds, []map[string]any{doc})

	for _, idx := range liveESIndexExprs(ds) {
		r := liveESRun(t, cfg, idx, `{"query":{"term":{"no.such.field":"needletime"}}}`,
			now.Add(-time.Hour), now.Add(time.Minute))
		assert.Contains(t, r.Note, "has data", "index %s", idx)
		assert.NotContains(t, r.Note, "no documents at all")
		assert.Contains(t, r.Sample, needlePath+": needletime")
		assert.Contains(t, r.Sample, "observed_at: ")
	}
}

// The b0680e8e case on its real shape: a few RDS documents among many load-balancer
// documents in one CloudWatch stream, queried with the filters that failed there.
func TestLiveESZeroMatchSample_CloudWatchStreamRareService(t *testing.T) {
	cfg := liveESConfig(t)
	enableSample(t, true)
	now := time.Now()
	ds := fmt.Sprintf("metrics-nbtest.cloudwatch-%d", now.UnixNano())

	var rds map[string]any
	require.NoError(t, json.Unmarshal([]byte(cloudwatchStreamDoc), &rds))
	rds["@timestamp"] = now.Add(-5 * time.Minute).UTC().Format(time.RFC3339Nano)
	docs := []map[string]any{rds}
	for i := 0; i < 300; i++ {
		ms := map[string]any{
			"metric_name": "RejectedFlowCount", "namespace": []any{"AWS", "NetworkELB"},
			"dimensions": map[string]any{"LoadBalancer": fmt.Sprintf("net/example-nlb-%d/0", i%7)},
			"unit":       "Count", "value": map[string]any{"count": 1, "max": 0, "min": 0, "sum": 0},
		}
		docs = append(docs, map[string]any{
			"@timestamp": now.Add(-time.Duration(i) * time.Second).UTC().Format(time.RFC3339Nano),
			"tags":       []any{"forwarded"},
			"metricset":  map[string]any{"NetworkELB": ms, "metric_name": ms["metric_name"], "namespace": ms["namespace"], "dimensions": ms["dimensions"]},
		})
	}
	liveESBulk(t, cfg, ds, docs)

	for _, idx := range liveESIndexExprs(ds) {
		r := liveESRun(t, cfg, idx, b0680e8eQuery, now.Add(-time.Hour), now.Add(time.Minute))
		require.NotNil(t, r.DocsMatched)
		assert.Zero(t, *r.DocsMatched, "the original filters still match nothing")
		assert.Contains(t, r.Sample, `metricset.namespace: ["AWS","RDS"]`, "index %s, note: %s", idx, r.Note)
		assert.Contains(t, r.Sample, "metricset.metric_name: CPUUtilization")
		assert.Contains(t, r.Note, "contain one or more of the values")
		t.Logf("%s sample:\n%s", idx, r.Sample)
	}
}

// An index with more searchable fields than Elasticsearch will expand a "*" search to.
// The value search must still find the document, by retrying on the text-like fields.
func TestLiveESZeroMatchSample_WideIndex(t *testing.T) {
	cfg := liveESConfig(t)
	enableSample(t, true)
	now := time.Now()
	index := fmt.Sprintf("nbtest-wide-%d", now.UnixNano())

	resp, err := esRequest("PUT", fmt.Sprintf("%s/%s", cfg.Url, index), `{"settings":{"index.mapping.total_fields.limit":5000}}`, cfg) //nolint:bodyclose
	require.NoError(t, err)
	_, err = readResponse(resp, "create index")
	require.NoError(t, err)
	t.Cleanup(func() {
		if resp, err := esRequest("DELETE", fmt.Sprintf("%s/%s", cfg.Url, index), "", cfg); err == nil {
			_ = resp.Body.Close()
		}
	})

	doc := map[string]any{"@timestamp": now.UTC().Format(time.RFC3339Nano), "deep": map[string]any{"name": "needlewide"}}
	for i := 0; i < 1200; i++ {
		doc[fmt.Sprintf("wide.f%d", i)] = fmt.Sprintf("v%d", i)
	}
	line, err := json.Marshal(doc)
	require.NoError(t, err)
	resp, err = esRequest("POST", fmt.Sprintf("%s/%s/_bulk?refresh=true", cfg.Url, index), `{"index":{}}`+"\n"+string(line)+"\n", cfg) //nolint:bodyclose
	require.NoError(t, err)
	_, err = readResponse(resp, "bulk")
	require.NoError(t, err)

	for _, idx := range liveESIndexExprs(index) {
		r := liveESRun(t, cfg, idx, `{"query":{"term":{"no.such.field":"needlewide"}}}`, now.Add(-time.Hour), now.Add(time.Minute))
		assert.Contains(t, r.Sample, "deep.name: needlewide", "index %s, note: %s", idx, r.Note)
		assert.Contains(t, r.Note, "contain one or more of the values", "the value search, not the any-document fallback, found it")
	}
}

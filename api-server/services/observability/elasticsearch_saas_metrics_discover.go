package observability

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Discovery lets the agent look at an index before querying it: which datasets it
// holds, and what one dataset's documents look like. Nothing here knows a layout.
// Customer indexes come in layouts nobody listed; instead of describing them, the
// agent reads them (#39201). Only the agent's es_metrics_query tool sends a
// discovery request.

// esDiscoverRequestKey selects discovery: "datasets" or "sample" (with "dataset").
const esDiscoverRequestKey = "discover"

const (
	// esDiscoverMaxDatasets bounds the listing. The count of datasets left out is
	// reported, never dropped silently.
	esDiscoverMaxDatasets = 100
	// esDiscoverBudget caps the rendered listing; datasets past it are listed by name
	// and count only.
	esDiscoverBudget = 3000
	// esDiscoverTimeout bounds the listing search on remote clusters and frozen tiers.
	esDiscoverTimeout = "10s"
	// esDiscoverFieldsPerDataset is how many example fields each listing line shows.
	esDiscoverFieldsPerDataset = 5
)

// esDiscover answers a discovery request as a QueryResult: the listing or the sample
// in Sample, and in Note what was looked at and whether it is complete.
func esDiscover(cfg *ElasticsearchConfig, index, mode, dataset string, startMillis, endMillis int64) QueryResult {
	qr := QueryResult{QueryKey: esDiscoverRequestKey, Query: fmt.Sprintf(`{"discover":%q,"dataset":%q}`, mode, dataset)}
	var err error
	switch mode {
	case "datasets":
		qr.Sample, qr.Note, err = esDiscoverDatasets(cfg, index, startMillis, endMillis)
	case "sample":
		qr.Sample, qr.Note, err = esDiscoverSample(cfg, index, dataset)
	default:
		err = fmt.Errorf("unknown discover mode %q; use \"datasets\" or \"sample\"", mode)
	}
	if err != nil {
		msg := fmt.Sprintf("discovery on %s did not run: %v", index, err)
		qr.Error = &msg
	}
	return qr
}

type esDatasetBucket struct {
	Key      string `json:"key"`
	DocCount int64  `json:"doc_count"`
	Doc      struct {
		Hits struct {
			Hits []esSampleHit `json:"hits"`
		} `json:"hits"`
	} `json:"doc"`
}

type esDatasetTerms struct {
	SumOtherDocCount int64             `json:"sum_other_doc_count"`
	Buckets          []esDatasetBucket `json:"buckets"`
}

// esDiscoverDatasets lists the datasets in index: one line each with its document
// count in the window and a few example fields from its newest document. Datasets are
// grouped by `data_stream.dataset`; an index without data streams is grouped by index.
func esDiscoverDatasets(cfg *ElasticsearchConfig, index string, startMillis, endMillis int64) (string, string, error) {
	topDoc := map[string]any{"top_hits": map[string]any{
		"size": 1,
		"sort": []any{
			map[string]any{"@timestamp": map[string]any{"order": "desc", "unmapped_type": "date"}},
			map[string]any{"time": map[string]any{"order": "desc", "unmapped_type": "date"}},
		},
	}}
	body := map[string]any{
		"size":             0,
		"timeout":          esDiscoverTimeout,
		"track_total_hits": false,
		"aggs": map[string]any{
			"by_dataset": map[string]any{"terms": map[string]any{"field": "data_stream.dataset", "size": esDiscoverMaxDatasets},
				"aggs": map[string]any{"doc": topDoc}},
			"by_index": map[string]any{"terms": map[string]any{"field": "_index", "size": esDiscoverMaxDatasets},
				"aggs": map[string]any{"doc": topDoc}},
		},
	}
	window := "at all (no time filter)"
	if startMillis > 0 && endMillis > 0 {
		body["query"] = map[string]any{"bool": map[string]any{"filter": []any{esMetricsTimeRangeClause(startMillis, endMillis)}}}
		window = fmt.Sprintf("between %s and %s", esMillisToRFC3339(startMillis), esMillisToRFC3339(endMillis))
	}
	resp, err := esRequestJSON("POST", esIndexURL(cfg.Url, index, "_search"), body, cfg) //nolint:bodyclose // closed by readResponse
	if err != nil {
		return "", "", err
	}
	raw, err := readResponse(resp, "dataset listing")
	if err != nil {
		return "", "", err
	}
	var parsed struct {
		TimedOut     bool `json:"timed_out"`
		Aggregations struct {
			ByDataset esDatasetTerms `json:"by_dataset"`
			ByIndex   esDatasetTerms `json:"by_index"`
		} `json:"aggregations"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", "", fmt.Errorf("unreadable response: %w", err)
	}

	terms, groupedBy := parsed.Aggregations.ByDataset, "dataset (`data_stream.dataset`)"
	if len(terms.Buckets) == 0 {
		terms, groupedBy = esGroupIndexBuckets(parsed.Aggregations.ByIndex), "index (no `data_stream.dataset` field)"
	}
	if len(terms.Buckets) == 0 {
		if err := esRemoteClustersDidNotAnswer(raw, true); err != nil {
			return "", "", err
		}
		note := fmt.Sprintf("%s has no documents %s.", index, window)
		if parsed.TimedOut {
			note = fmt.Sprintf("The dataset listing on %s timed out after %s before finding any documents, so this does not show whether the index has data.", index, esDiscoverTimeout)
		}
		return "", note, nil
	}

	var b strings.Builder
	brief := 0
	for _, bk := range terms.Buckets {
		line := fmt.Sprintf("%s: %d docs", bk.Key, bk.DocCount)
		if hits := bk.Doc.Hits.Hits; len(hits) > 0 {
			if ex := esExampleFields(hits[0].Source); ex != "" {
				line += " | e.g. " + ex
			}
		}
		if b.Len()+len(line)+1 > esDiscoverBudget {
			line = fmt.Sprintf("%s: %d docs", bk.Key, bk.DocCount)
			brief++
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}

	note := fmt.Sprintf("%d %s value(s) in %s %s, largest first. Sample one with discover \"sample\" and its name to see its documents.",
		len(terms.Buckets), groupedBy, index, window)
	if brief > 0 {
		note += fmt.Sprintf(" %d listed by name only, to keep the listing short.", brief)
	}
	if terms.SumOtherDocCount > 0 {
		note += fmt.Sprintf(" More exist than are listed: %d documents belong to datasets past the first %d.", terms.SumOtherDocCount, esDiscoverMaxDatasets)
	}
	if parsed.TimedOut {
		note += fmt.Sprintf(" The listing timed out after %s, so it may be incomplete.", esDiscoverTimeout)
	}
	return strings.TrimRight(b.String(), "\n"), note, nil
}

// esDiscoverSample returns the newest documents of one dataset, with no time filter,
// flattened to `path: value` lines. A dataset that came from the index grouping (no
// data streams) is matched by index name.
func esDiscoverSample(cfg *ElasticsearchConfig, index, dataset string) (string, string, error) {
	if strings.TrimSpace(dataset) == "" {
		return "", "", fmt.Errorf("a dataset is required; list them with discover \"datasets\"")
	}
	match := map[string]any{"bool": map[string]any{
		"minimum_should_match": 1,
		"should": []any{
			map[string]any{"term": map[string]any{"data_stream.dataset": dataset}},
			// Index-grouped listings name an index without its cluster prefix, and a
			// data stream without its ".ds-" backing-index prefix and date suffix.
			map[string]any{"wildcard": map[string]any{"_index": map[string]any{"value": esIndexWithoutCluster(dataset) + "*"}}},
			map[string]any{"wildcard": map[string]any{"_index": map[string]any{"value": ".ds-" + esIndexWithoutCluster(dataset) + "-*"}}},
		},
	}}
	hits, err := esSampleSearch(esIndexURL(cfg.Url, index, "_search"), cfg, map[string]any{
		"query": match,
		"sort": []any{
			map[string]any{"@timestamp": map[string]any{"order": "desc", "unmapped_type": "date"}},
			map[string]any{"time": map[string]any{"order": "desc", "unmapped_type": "date"}},
		},
	})
	if err != nil {
		return "", "", err
	}
	if len(hits) == 0 {
		return "", fmt.Sprintf("No documents of %q in %s. Check the name against discover \"datasets\".", dataset, index), nil
	}
	return renderESSample(hits, nil),
		fmt.Sprintf("The %d newest document(s) of %q in %s (no time filter), flattened to field paths and values. Query these paths.", len(hits), dataset, index), nil
}

// esExampleFields picks a few fields from a document to show what it holds, with no
// knowledge of any layout: numeric values first (they are what a metric query reads),
// then text values under the same top-level object as the first number (usually its
// name or dimensions). Long values are cut.
func esExampleFields(src map[string]any) string {
	lines := flattenESSource(src)
	var numeric, text []string
	for _, l := range lines {
		path, val, ok := strings.Cut(l, ": ")
		if !ok || strings.HasPrefix(path, "@") {
			continue
		}
		if esLooksNumeric(val) {
			numeric = append(numeric, l)
		} else {
			text = append(text, path+": "+val)
		}
	}
	var out []string
	root := ""
	for _, l := range numeric {
		if len(out) == esDiscoverFieldsPerDataset-2 {
			break
		}
		if root == "" {
			root = strings.SplitN(l, ".", 2)[0]
		}
		out = append(out, esCut(l, 70))
	}
	for _, l := range text {
		if len(out) == esDiscoverFieldsPerDataset {
			break
		}
		if root == "" || strings.HasPrefix(l, root+".") {
			out = append(out, esCut(l, 70))
		}
	}
	return strings.Join(out, ", ")
}

var esNumberRe = regexp.MustCompile(`^-?\d+(\.\d+)?([eE][-+]?\d+)?$`)

func esLooksNumeric(v string) bool { return esNumberRe.MatchString(strings.TrimSpace(v)) }

func esCut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// esIndexWithoutCluster drops a cross-cluster prefix ("remote:index" → "index").
func esIndexWithoutCluster(s string) string {
	if i := strings.Index(s, ":"); i >= 0 {
		return s[i+1:]
	}
	return s
}

var esBackingIndexRe = regexp.MustCompile(`^(?:[^:]+:)?(?:\.ds-)?(.+?)(?:-\d{4}\.\d{2}\.\d{2}(?:-\d+)?)?$`)

// esGroupIndexBuckets folds backing indices of one stream (".ds-x-2026.10.08-000001",
// "...-000002") into one entry named "x", summing counts and keeping the example
// document of the largest one.
func esGroupIndexBuckets(in esDatasetTerms) esDatasetTerms {
	byName := map[string]*esDatasetBucket{}
	var order []string
	for _, b := range in.Buckets {
		name := b.Key
		if m := esBackingIndexRe.FindStringSubmatch(b.Key); m != nil {
			name = m[1]
		}
		if g, ok := byName[name]; ok {
			g.DocCount += b.DocCount
			continue
		}
		cp := b
		cp.Key = name
		byName[name] = &cp
		order = append(order, name)
	}
	out := esDatasetTerms{SumOtherDocCount: in.SumOtherDocCount}
	for _, n := range order {
		out.Buckets = append(out.Buckets, *byName[n])
	}
	sort.SliceStable(out.Buckets, func(i, j int) bool { return out.Buckets[i].DocCount > out.Buckets[j].DocCount })
	return out
}

func esMillisToRFC3339(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

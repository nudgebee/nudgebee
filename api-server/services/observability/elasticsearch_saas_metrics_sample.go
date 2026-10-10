package observability

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// When a metrics query matches no documents, the agent needs to see what the index
// really holds — not a list of field names we wrote down for the layouts we know. The
// metric series this backend returns carry shortened labels (`__name__`, a bare
// dimension key), and an agent that filters on those, or on a value stored in a
// different shape (`"AWS/RDS"` against `["AWS","RDS"]`), gets zero documents and
// concludes the data is not ingested. In conversation b0680e8e that happened on two
// turns; the third found the data only because the orchestrator switched to the logs
// agent, which read raw documents. esZeroMatchSample does that read on every miss.
//
// It never decides what a document means. It returns documents, flattened to
// `path: value` lines, and says which search found them. The agent matches them to
// its question.

// esZeroMatchSampleRequestKey is the request field a caller sets to receive a sample
// when its query matches nothing. Only the agent's es_metrics_query tool sets it.
const esZeroMatchSampleRequestKey = "zero_match_sample"

const (
	// esSampleDocs is how many documents a sample shows. Enough to see the layout and
	// a second variant, small enough to stay inside the planner's view.
	esSampleDocs = 3
	// esSampleMaxValues bounds how many literals from the failed query are searched for.
	esSampleMaxValues = 8
	// esSampleMaxValueLen drops literals too long to be a field value worth searching.
	esSampleMaxValueLen = 100
	// esSampleBudget caps the rendered sample. The llm-server shows the agent at most
	// 6,000 characters of a tool result, and the sample is placed first in it.
	esSampleBudget = 3000
	// esSampleValueLen cuts long values (messages, ARNs, file paths) in the rendering.
	esSampleValueLen = 80
	// esSampleOmittedReserve keeps room in the budget for the "(N more field(s) not
	// shown)" line.
	esSampleOmittedReserve = 40
	// esSampleTimeout bounds each extra search, which can reach a remote cluster or a
	// frozen tier. A timed-out search is reported as "check did not run".
	esSampleTimeout = "5s"
	// esSampleTerminateAfter stops each shard early; the sample needs a few documents,
	// not a count.
	esSampleTerminateAfter = 10000
	// esSampleMaxPerRequest is how many zero-match queries in one request get a sample.
	esSampleMaxPerRequest = 2
)

// esSampleSkipKeys are DSL keys whose values are options or field names, not values a
// document holds.
var esSampleSkipKeys = map[string]bool{
	"range": true, "exists": true, "_source": true, "sort": true, "aggs": true,
	"aggregations": true, "format": true, "time_zone": true, "analyzer": true,
	"operator": true, "minimum_should_match": true, "relation": true, "type": true,
	"script": true, "fields": true, "default_field": true, "rewrite": true,
	"fuzziness": true, "boost": true, "_name": true, "zero_terms_query": true,
	"path": true, "score_mode": true, "field": true, "minimum_should_match_field": true,
	"distance": true, "unit": true, "validation_method": true, "flags": true,
	"quote_field_suffix": true, "ignore_unmapped": true, "case_insensitive": true,
	"default_operator": true, "tie_breaker": true, "slop": true, "max_expansions": true,
	"prefix_length": true, "transpositions": true, "fuzzy_transpositions": true,
	"auto_generate_synonyms_phrase_query": true, "lenient": true, "order": true,
	"size": true, "from": true, "track_total_hits": true, "timeout": true,
}

// esQueryValues are what a failed query was looking for: literal values from term,
// terms, match, wildcard and similar clauses, and the text of any query_string or
// simple_query_string clause, which is already in query-string syntax.
type esQueryValues struct {
	Literals []string
	Raw      []string
}

func (v esQueryValues) empty() bool { return len(v.Literals)+len(v.Raw) == 0 }

func (v esQueryValues) String() string {
	parts := append([]string{}, v.Literals...)
	for _, r := range v.Raw {
		parts = append(parts, fmt.Sprintf("query text %q", r))
	}
	return strings.Join(parts, ", ")
}

// queryString renders the values as one query_string query: literals quoted (or kept
// as wildcards), query-string text grouped as written, all joined by OR so the
// best-scored documents hold the most of them.
func (v esQueryValues) queryString() string {
	terms := make([]string, 0, len(v.Literals)+len(v.Raw))
	for _, l := range v.Literals {
		terms = append(terms, esQueryStringTerm(l))
	}
	for _, r := range v.Raw {
		terms = append(terms, "("+r+")")
	}
	return strings.Join(terms, " OR ")
}

// esQueryLiterals collects what a DSL query filters on. Field names are map keys, so
// they are never collected; options are skipped by key. Numbers and booleans are kept
// as text, because a value search over every field matches them that way too.
func esQueryLiterals(query any) esQueryValues {
	var out esQueryValues
	seen := map[string]bool{}
	full := func() bool { return len(out.Literals)+len(out.Raw) >= esSampleMaxValues }
	add := func(s string) {
		s = strings.TrimSpace(s)
		// A value made only of wildcards matches every document and says nothing.
		if s == "" || strings.Trim(s, "*?") == "" || len(s) > esSampleMaxValueLen || seen[s] || full() {
			return
		}
		seen[s] = true
		out.Literals = append(out.Literals, s)
	}
	var walk func(v any)
	walk = func(v any) {
		if full() {
			return
		}
		switch t := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys) // deterministic order for tests and logs
			for _, k := range keys {
				if esSampleSkipKeys[k] {
					continue
				}
				if k == "query_string" || k == "simple_query_string" {
					if m, ok := t[k].(map[string]any); ok {
						if q, _ := m["query"].(string); strings.TrimSpace(q) != "" && !seen[q] && !full() {
							seen[q] = true
							out.Raw = append(out.Raw, strings.TrimSpace(q))
						}
					}
					continue
				}
				walk(t[k])
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case string:
			add(t)
		case float64:
			add(strconv.FormatFloat(t, 'f', -1, 64))
		case bool:
			add(strconv.FormatBool(t))
		}
	}
	walk(query)
	return out
}

// esQueryStringTerm renders one literal for a query_string search across every field.
// A literal with a trailing `*` (or `?`) stays a wildcard with its other reserved
// characters escaped; any other literal is a quoted phrase, which matches a keyword
// field exactly and a text field as a phrase.
func esQueryStringTerm(v string) string {
	if strings.ContainsAny(v, "*?") {
		var b strings.Builder
		for _, r := range v {
			if strings.ContainsRune(`+-=&|><!(){}[]^"~:\/ `, r) {
				b.WriteRune('\\')
			}
			b.WriteRune(r)
		}
		return b.String()
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

// esSampleResult is what a zero-match query gets back alongside its empty payload.
type esSampleResult struct {
	Sample string // rendered documents; empty when none were found
	Note   string // which search produced them, or why there are none
}

// esZeroMatchSample runs at most two bounded searches on index after query matched
// nothing (three, when the index is too wide for a search across every field):
//  1. documents holding the most of the query's values, in the query's time window,
//     searched across every field;
//  2. if none, any documents in the index, with no time filter.
//
// Absence is reported only when the second search succeeds and finds nothing: an empty
// index. Every other outcome says what was or was not checked. fields lists the index's
// fields for the wide-index retry; it is the caller's cached lookup.
func esZeroMatchSample(cfg *ElasticsearchConfig, index string, query any, startMillis, endMillis int64,
	fields func() ([]OutputMetricLabels, error)) esSampleResult {
	values := esQueryLiterals(query)
	url := esIndexURL(cfg.Url, index, "_search")
	windowed := startMillis > 0 && endMillis > 0

	// A query with no values to search for (only `exists` filters) cannot use the value
	// search. When it names a field this index does not have, find where a field with
	// that name does live and show documents carrying it.
	if values.empty() {
		if res, ok := esExistsFieldSample(cfg, url, esExistsFields(query), fields, startMillis, endMillis); ok {
			return res
		}
	}

	var step1Err error
	if !values.empty() {
		qs := map[string]any{"query": values.queryString(), "default_field": "*", "lenient": true}
		bq := map[string]any{"must": []any{map[string]any{"query_string": qs}}}
		if windowed {
			bq["filter"] = []any{esMetricsTimeRangeClause(startMillis, endMillis)}
		}
		searched := "searched across every field"
		hits, err := esSampleSearch(url, cfg, map[string]any{"query": map[string]any{"bool": bq}})
		if err != nil && strings.Contains(err.Error(), "matches too many fields") {
			// Wide indexes (Elastic Agent integrations map thousands of fields) refuse a
			// search across every field. Retry on the fields that hold text values; a
			// value search matches numbers and booleans as text there too.
			if names := esSampleTextFields(fields); len(names) > 0 {
				delete(qs, "default_field")
				qs["fields"] = names
				searched = fmt.Sprintf("searched across the index's %d text fields (it has too many fields to search all)", len(names))
				hits, err = esSampleSearch(url, cfg, map[string]any{"query": map[string]any{"bool": bq}})
			}
		}
		if err == nil && len(hits) > 0 {
			where := "from the same index and time window"
			if !windowed {
				where = "from the same index (the query had no time window)"
			}
			return esSampleResult{
				Sample: renderESSample(hits, values.Literals),
				Note: fmt.Sprintf("The query matched no documents. Below are %d document(s) %s that contain one or more "+
					"of the values the query used (%s), best matches first, %s. Use their field paths and value shapes "+
					"to write the filter.", len(hits), where, values, searched),
			}
		}
		step1Err = err
	}

	hits, err := esSampleSearch(url, cfg, map[string]any{
		"query": map[string]any{"match_all": map[string]any{}},
		// Newest first by whichever of the two standard time fields the index has.
		"sort": []any{
			map[string]any{"@timestamp": map[string]any{"order": "desc", "unmapped_type": "date"}},
			map[string]any{"time": map[string]any{"order": "desc", "unmapped_type": "date"}},
		},
	})
	if err != nil {
		slog.Warn("ES metrics zero-match sample failed", "index", index, "values_err", step1Err, "err", err)
		return esSampleResult{Note: fmt.Sprintf("The query matched no documents. A follow-up read of sample documents "+
			"from %s did not run (%v), so this does not show whether the index has data.", index, err)}
	}
	if len(hits) == 0 {
		return esSampleResult{Note: fmt.Sprintf("The query matched no documents, and %s has no documents at all.", index)}
	}

	var why string
	switch {
	case values.empty():
		why = "The query had no values to search for."
	case step1Err != nil:
		why = fmt.Sprintf("The search for the query's values (%s) did not run (%v).", values, step1Err)
	case windowed:
		why = fmt.Sprintf("No document in the time window contains any of the query's values (%s).", values)
	default:
		why = fmt.Sprintf("No document contains any of the query's values (%s).", values)
	}
	return esSampleResult{
		Sample: renderESSample(hits, values.Literals),
		Note: fmt.Sprintf("The query matched no documents, but %s has data. %s Below are %d recent document(s) from the "+
			"index with no time filter. If they fall outside the query's time window, check which field holds the "+
			"document's time; the window is applied to `@timestamp` and `time`.", index, why, len(hits)),
	}
}

// esSampleMaxFields stays under Elasticsearch's field-expansion limit (2,048 on 8.x,
// counting each sub-field).
const esSampleMaxFields = 1500

// esSampleTextTypes are the field types a string value can match.
var esSampleTextTypes = map[string]bool{
	"keyword": true, "constant_keyword": true, "wildcard": true, "text": true, "match_only_text": true,
}

// esSampleTextFields lists the index's text-like fields, for retrying a value search
// that was refused for expanding to too many fields. Nil when the list cannot be read.
// A field mapped with different types across indices is listed under one of them, so a
// field that is text in only some indices can be missed.
func esSampleTextFields(fields func() ([]OutputMetricLabels, error)) []string {
	all, err := fields()
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range all {
		t, _ := f.Attributes["type"].(string)
		if esSampleTextTypes[t] && !strings.HasSuffix(f.Label, ".keyword") {
			out = append(out, f.Label)
			if len(out) == esSampleMaxFields {
				break
			}
		}
	}
	return out
}

type esSampleHit struct {
	Index  string         `json:"_index"`
	Source map[string]any `json:"_source"`
}

func esSampleSearch(url string, cfg *ElasticsearchConfig, body map[string]any) ([]esSampleHit, error) {
	body["size"] = esSampleDocs
	body["timeout"] = esSampleTimeout
	// Stopping early would sort only the documents collected so far, so a sorted
	// ("newest") sample relies on the timeout alone.
	if _, sorted := body["sort"]; !sorted {
		body["terminate_after"] = esSampleTerminateAfter
	}
	body["track_total_hits"] = false
	resp, err := esRequestJSON("POST", url, body, cfg) //nolint:bodyclose // closed by readResponse
	if err != nil {
		return nil, err
	}
	raw, err := readResponse(resp, "metric sample")
	if err != nil {
		return nil, err
	}
	var parsed struct {
		TimedOut bool `json:"timed_out"`
		Hits     struct {
			Hits []esSampleHit `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("unreadable response: %w", err)
	}
	if len(parsed.Hits.Hits) == 0 {
		if parsed.TimedOut {
			return nil, fmt.Errorf("timed out after %s", esSampleTimeout)
		}
		// No hit count is tracked here, so readResponse cannot tell a skipped remote
		// from an empty one; an empty sample must not read as "no documents".
		if err := esRemoteClustersDidNotAnswer(raw, true); err != nil {
			return nil, err
		}
	}
	return parsed.Hits.Hits, nil
}

// flattenESSource turns a document into sorted `path: value` lines. Arrays are kept
// whole as JSON, because their shape is often the thing a filter got wrong.
func flattenESSource(src map[string]any) []string {
	var lines []string
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		if m, ok := v.(map[string]any); ok && len(m) > 0 {
			for k, child := range m {
				p := k
				if prefix != "" {
					p = prefix + "." + k
				}
				walk(p, child)
			}
			return
		}
		var s string
		if str, ok := v.(string); ok {
			s = str
		} else {
			b, _ := json.Marshal(v)
			s = string(b)
		}
		if len(s) > esSampleValueLen {
			cut := esSampleValueLen
			for cut > 0 && !utf8.RuneStart(s[cut]) {
				cut-- // never split a multi-byte character
			}
			s = s[:cut] + "…"
		}
		lines = append(lines, prefix+": "+s)
	}
	walk("", src)
	sort.Strings(lines)
	return lines
}

// renderESSample renders hits within esSampleBudget. Lines holding one of the query's
// values come first in each document, so the fields the agent was looking for survive
// a cut. After the first document, lines identical to one already shown are left out:
// they are the shared envelope, not what tells two documents apart.
func renderESSample(hits []esSampleHit, values []string) string {
	var b strings.Builder
	shown := map[string]bool{}
	for i, h := range hits {
		lines := flattenESSource(h.Source)
		sort.SliceStable(lines, func(a, c int) bool {
			return esSampleLineHasValue(lines[a], values) && !esSampleLineHasValue(lines[c], values)
		})
		header := fmt.Sprintf("--- document %d (index %s)\n", i+1, h.Index)
		if i > 0 {
			header = fmt.Sprintf("--- document %d (index %s; lines identical to an earlier document omitted)\n", i+1, h.Index)
		}
		if b.Len()+len(header) > esSampleBudget {
			break
		}
		b.WriteString(header)
		omitted := 0
		for _, l := range lines {
			if shown[l] {
				continue
			}
			// Leave room for the "not shown" line, which must fit too.
			if b.Len()+len(l)+1 > esSampleBudget-esSampleOmittedReserve {
				omitted++
				continue
			}
			shown[l] = true
			b.WriteString(l)
			b.WriteByte('\n')
		}
		if omitted > 0 {
			fmt.Fprintf(&b, "(%d more field(s) not shown)\n", omitted)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func esSampleLineHasValue(line string, values []string) bool {
	i := strings.Index(line, ": ")
	if i < 0 {
		return false
	}
	v := strings.ToLower(line[i+2:])
	for _, want := range values {
		w := strings.ToLower(strings.Trim(want, "*?"))
		if w != "" && strings.Contains(v, w) {
			return true
		}
	}
	return false
}

// esExistsFields collects the field names of every `exists` clause in a query.
func esExistsFields(query any) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if ex, ok := t["exists"].(map[string]any); ok {
				if f, _ := ex["field"].(string); f != "" && !seen[f] {
					seen[f] = true
					out = append(out, f)
				}
			}
			for _, c := range t {
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(query)
	sort.Strings(out)
	return out
}

// esResolveFieldName returns the index's field paths that end in the same name as
// asked (the last path segment, case-insensitive), when the exact path is not there.
// `DBInstanceIdentifier` finds `metricset.dimensions.DBInstanceIdentifier` or
// `aws.dimensions.DBInstanceIdentifier`, whichever this index has.
func esResolveFieldName(asked string, fields []OutputMetricLabels) (exact bool, matches []string) {
	want := strings.ToLower(asked)
	if i := strings.LastIndex(want, "."); i >= 0 {
		want = want[i+1:]
	}
	for _, f := range fields {
		if f.Label == asked {
			return true, nil
		}
		name := strings.ToLower(strings.TrimSuffix(f.Label, ".keyword"))
		if name == want || strings.HasSuffix(name, "."+want) {
			matches = append(matches, strings.TrimSuffix(f.Label, ".keyword"))
		}
	}
	sort.Strings(matches)
	return false, esDedupStrings(matches)
}

func esDedupStrings(in []string) []string {
	var out []string
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// esExistsFieldSample handles a missed query whose only filters are `exists` on
// fields the index does not have. ok is false when it does not apply (no such
// fields, the field list cannot be read, or nothing resolves), and the caller falls
// back to the usual sample.
func esExistsFieldSample(cfg *ElasticsearchConfig, url string, asked []string,
	fields func() ([]OutputMetricLabels, error), startMillis, endMillis int64) (esSampleResult, bool) {
	if len(asked) == 0 || fields == nil {
		return esSampleResult{}, false
	}
	all, err := fields()
	if err != nil {
		return esSampleResult{}, false
	}
	var found []string
	var notes []string
	for _, a := range asked {
		exact, m := esResolveFieldName(a, all)
		if exact || len(m) == 0 {
			continue
		}
		if len(m) > 5 {
			m = m[:5]
		}
		found = append(found, m...)
		notes = append(notes, fmt.Sprintf("`%s` is not a field in this index; fields with that name: %s", a, strings.Join(m, ", ")))
	}
	if len(found) == 0 {
		return esSampleResult{}, false
	}
	should := make([]any, 0, len(found))
	for _, f := range found {
		should = append(should, map[string]any{"exists": map[string]any{"field": f}})
	}
	bq := map[string]any{"minimum_should_match": 1, "should": should}
	if startMillis > 0 && endMillis > 0 {
		bq["filter"] = []any{esMetricsTimeRangeClause(startMillis, endMillis)}
	}
	hits, err := esSampleSearch(url, cfg, map[string]any{"query": map[string]any{"bool": bq}})
	if err != nil || len(hits) == 0 {
		return esSampleResult{Note: "The query matched no documents. " + strings.Join(notes, "; ") + "."}, true
	}
	return esSampleResult{
		Sample: renderESSample(hits, nil),
		Note: fmt.Sprintf("The query matched no documents. %s. Below are %d document(s) carrying those fields; use their paths.",
			strings.Join(notes, "; "), len(hits)),
	}, true
}

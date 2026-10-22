package observability

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/services/security"
	"sort"
	"strconv"
	"strings"
)

// Count mode (log_count.go) on a hosted Elasticsearch / OpenSearch (LogCountSource).
//
// The counts come from one `composite` aggregation, not nested `terms`: a `terms`
// bucket's doc_count is approximate on a multi-shard index once a shard holds more
// distinct keys than its shard_size, and the number stays plausible while reading low
// (see esUtilTermsSize). A composite aggregation enumerates every group exactly, takes
// several fields in one level, and exists on every Elasticsearch since 6.1 and on every
// OpenSearch, so no version probe is needed. `missing_bucket` gives the lines without a
// group field their own bucket, which is how Ungrouped is measured rather than derived.

var _ LogCountSource = (*ElasticSaasSource)(nil)

// esAggregatePageSize is the composite page size. Each page re-runs the search, so few
// large pages beat many small ones; 10,000 stays well under search.max_buckets. A var so
// tests can page with a handful of buckets.
var esAggregatePageSize = 10000

const (
	// esAggregateMaxPages caps the pages one call reads: 50,000 (group, bucket) pairs,
	// e.g. 2,000 pods × 24 hourly buckets. Past it the answer is marked incomplete.
	esAggregateMaxPages = 5
	// esAggregateTimeKey names the date_histogram source; group sources are g0, g1, g2.
	esAggregateTimeKey = "t"
)

// ResolveGroupByFields implements LogCountSource: each name becomes the first of its
// shipper spellings (esCandidateFields) the index has, as its aggregatable `.keyword`
// sub-field when the field is text.
func (e *ElasticSaasSource) ResolveGroupByFields(ctx *security.RequestContext, q LogCountQuery, names []string) ([]string, error) {
	fields, err := e.QueryIndexFields(ctx, FetchLogLabelRequest{AccountId: q.Request.AccountId, Request: q.Request.Request})
	if err != nil {
		return nil, fmt.Errorf("listing the index's fields to group by: %w", err)
	}
	return resolveESGroupByFields(fields, names)
}

func resolveESGroupByFields(fields []OutputLogLabelFields, names []string) ([]string, error) {
	present := make(map[string]map[string]any, len(fields))
	for _, f := range fields {
		present[f.Field] = f.Attributes
	}
	out := make([]string, len(names))
	for i, name := range names {
		candidates := esCandidateFields(name)
		if candidates == nil {
			candidates = []string{name}
		}
		field := ""
		for _, c := range candidates {
			if _, ok := present[c]; ok {
				field = c
				break
			}
		}
		if field == "" {
			return nil, fmt.Errorf("cannot group by %q: the index has no such field%s", name, esGroupableCanonicalHint(present))
		}
		field = resolveAggregatableESField(fields, field)
		if isTextFieldAttributes(present[field]) {
			return nil, fmt.Errorf("cannot group by %q: %s is a text field with no keyword sub-field", name, field)
		}
		out[i] = field
	}
	return out, nil
}

// esGroupableCanonicalHint lists the Nudgebee names the index can be grouped by, for an
// error about one it cannot.
func esGroupableCanonicalHint(present map[string]map[string]any) string {
	var names []string
	for canonical := range esCanonicalK8sFields {
		for _, c := range esCandidateFields(canonical) {
			if _, ok := present[c]; ok {
				names = append(names, canonical)
				break
			}
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return " (it has " + strings.Join(names, ", ") + ", or name one of its keyword fields)"
}

// PlanLogCount implements LogCountSource: the one _search body that counts q, exactly as
// RunLogCount sends it (paging only adds `after`).
func (e *ElasticSaasSource) PlanLogCount(ctx *security.RequestContext, q LogCountQuery) (LogCountPlan, error) {
	// As in QueryLogsDetailed: a where clause that failed to render must never reach the
	// cluster as an unfiltered count.
	if strings.TrimSpace(q.Request.Query) == "" && hasWhereData(q.Request.QueryRequest.Where) {
		_, qErr := e.GetQuery(ctx, q.Request)
		if qErr == nil {
			qErr = errors.New("filter rendered to an empty query")
		}
		return LogCountPlan{}, fmt.Errorf("elasticsearch: refusing to run an unfiltered count: %w", qErr)
	}
	if queryType, _ := q.Request.Request["query_type"].(string); queryType != "" && queryType != "dsl" {
		return LogCountPlan{}, fmt.Errorf("counting takes a where filter, not a %s query", queryType)
	}
	return esCountPlan(q)
}

// esCountPlan renders q's count body as the plan's single query.
func esCountPlan(q LogCountQuery) (LogCountPlan, error) {
	body, err := esAggregateQueryBody(q)
	if err != nil {
		return LogCountPlan{}, err
	}
	rendered, err := json.Marshal(body)
	if err != nil {
		return LogCountPlan{}, fmt.Errorf("rendering the elasticsearch count: %w", err)
	}
	return LogCountPlan{Queries: []string{string(rendered)}}, nil
}

// RunLogCount implements LogCountSource.
func (e *ElasticSaasSource) RunLogCount(ctx *security.RequestContext, q LogCountQuery, plan LogCountPlan) (LogCountRows, error) {
	cfg, err := GetElasticsearchConfig(ctx, q.Request.AccountId)
	if err != nil {
		return LogCountRows{}, err
	}
	index, _ := q.Request.Request["index"].(string)
	if index == "" {
		index = cfg.LogIndex
	}
	if index == "" {
		return LogCountRows{}, fmt.Errorf("index is required to count logs")
	}
	searchURL := esIndexURL(cfg.Url, index, "_search")
	return esRunCount(q, plan, func(body map[string]any) ([]byte, error) {
		resp, err := esRequestJSON("POST", searchURL, body, cfg) //nolint:bodyclose // readResponse closes it
		if err != nil {
			return nil, fmt.Errorf("failed to execute elasticsearch count: %w", err)
		}
		return readResponse(resp, "elasticsearch count")
	})
}

// esRunCount runs a plan's body through search page by page, and folds the buckets into
// rows.
func esRunCount(q LogCountQuery, plan LogCountPlan, search func(map[string]any) ([]byte, error)) (LogCountRows, error) {
	if len(plan.Queries) != 1 {
		return LogCountRows{}, fmt.Errorf("elasticsearch count: want one planned query, got %d", len(plan.Queries))
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(plan.Queries[0]), &body); err != nil {
		return LogCountRows{}, fmt.Errorf("elasticsearch count: reading the planned query: %w", err)
	}
	rows := LogCountRows{Complete: true}

	composite, ok := esCompositeOf(body)
	if !ok {
		// Neither groups nor buckets: the count is hits.total.
		raw, err := search(body)
		if err != nil {
			return LogCountRows{}, err
		}
		page, err := parseESAggregatePage(raw)
		if err != nil {
			return LogCountRows{}, err
		}
		rows.Total = page.total
		rows.Groups = []Result{{Metric: map[string]string{}, Timestamps: []int64{q.Request.EndTime / 1000}, Values: []float64{float64(page.total)}}}
		return rows, nil
	}

	groups := map[string]*Result{}
	var order []string
	for pageNo := 0; ; pageNo++ {
		raw, err := search(body)
		if err != nil {
			return LogCountRows{}, err
		}
		page, err := parseESAggregatePage(raw)
		if err != nil {
			return LogCountRows{}, err
		}
		if pageNo == 0 {
			rows.Total = page.total
		}
		for _, b := range page.buckets {
			labels, missing := esAggregateBucketLabels(b.Key, q.GroupBy)
			if missing {
				rows.Ungrouped += b.DocCount
				continue
			}
			key := esAggregateGroupKey(labels, q.GroupBy)
			g, ok := groups[key]
			if !ok {
				g = &Result{Metric: labels}
				groups[key] = g
				order = append(order, key)
			}
			ts := q.Request.EndTime / 1000
			if q.Interval > 0 {
				ts = esAggregateBucketMillis(b.Key[esAggregateTimeKey]) / 1000
			}
			g.Timestamps = append(g.Timestamps, ts)
			g.Values = append(g.Values, float64(b.DocCount))
		}
		// A short page is the last one: Elasticsearch still sends an after_key on it,
		// and following that key would cost an empty search.
		if len(page.afterKey) == 0 || len(page.buckets) < esAggregatePageSize {
			break
		}
		if pageNo+1 >= esAggregateMaxPages {
			rows.Complete = false
			break
		}
		composite["after"] = page.afterKey
	}
	rows.Groups = make([]Result, 0, len(order))
	for _, key := range order {
		rows.Groups = append(rows.Groups, *groups[key])
	}
	return rows, nil
}

// esAggregateQueryBody renders the count: the where filter AND the window [start, end),
// size 0, an exact hits.total, and — when there is anything to group or bucket by — one
// composite aggregation named "g". The end is exclusive so a line stamped exactly at it
// does not open a bucket outside the window.
func esAggregateQueryBody(q LogCountQuery) (map[string]any, error) {
	filter := map[string]any{"match_all": map[string]any{}}
	if strings.TrimSpace(q.Request.Query) != "" {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(q.Request.Query), &parsed); err != nil {
			return nil, fmt.Errorf("failed to parse log query body: %w", err)
		}
		if userQuery, ok := parsed["query"].(map[string]any); ok {
			filter = userQuery
		}
	}
	body := map[string]any{
		"size":             0,
		"track_total_hits": true,
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			filter,
			map[string]any{"range": map[string]any{"@timestamp": map[string]any{
				"gte": q.Request.StartTime, "lt": q.Request.EndTime, "format": "epoch_millis",
			}}},
		}}},
	}

	sources := make([]any, 0, len(q.GroupBy)+1)
	for i, field := range q.GroupBy {
		sources = append(sources, map[string]any{"g" + strconv.Itoa(i): map[string]any{
			"terms": map[string]any{"field": field, "missing_bucket": true},
		}})
	}
	if q.Interval > 0 {
		sources = append(sources, map[string]any{esAggregateTimeKey: map[string]any{
			"date_histogram": map[string]any{
				"field":          "@timestamp",
				"fixed_interval": strconv.FormatInt(int64(q.Interval.Seconds()), 10) + "s",
			},
		}})
	}
	if len(sources) > 0 {
		body["aggs"] = map[string]any{"g": map[string]any{
			"composite": map[string]any{"size": esAggregatePageSize, "sources": sources},
		}}
	}
	return body, nil
}

// esCompositeOf returns the composite aggregation of a body esAggregateQueryBody built,
// so paging can set its `after`.
func esCompositeOf(body map[string]any) (map[string]any, bool) {
	aggs, _ := body["aggs"].(map[string]any)
	g, _ := aggs["g"].(map[string]any)
	composite, ok := g["composite"].(map[string]any)
	return composite, ok
}

type esAggregateBucket struct {
	Key      map[string]any `json:"key"`
	DocCount int64          `json:"doc_count"`
}

type esAggregatePage struct {
	total    int64
	buckets  []esAggregateBucket
	afterKey map[string]any
}

func parseESAggregatePage(raw []byte) (esAggregatePage, error) {
	var resp struct {
		Hits struct {
			Total json.RawMessage `json:"total"`
		} `json:"hits"`
		Aggregations struct {
			G struct {
				AfterKey map[string]any      `json:"after_key"`
				Buckets  []esAggregateBucket `json:"buckets"`
			} `json:"g"`
		} `json:"aggregations"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return esAggregatePage{}, fmt.Errorf("failed to decode elasticsearch count: %w", err)
	}
	total, relation, ok := parseESHitsTotal(resp.Hits.Total)
	if !ok {
		return esAggregatePage{}, fmt.Errorf("elasticsearch count: the response has no hits.total")
	}
	if relation != "eq" {
		// track_total_hits:true asks for an exact total; a lower bound is not an answer.
		return esAggregatePage{}, fmt.Errorf("elasticsearch count: the cluster returned a lower bound (%d+) instead of an exact total", total)
	}
	page := esAggregatePage{total: total, buckets: resp.Aggregations.G.Buckets}
	// The last page still carries an after_key on some versions; an empty page ends it.
	if len(page.buckets) > 0 {
		page.afterKey = resp.Aggregations.G.AfterKey
	}
	return page, nil
}

// esAggregateBucketLabels reads a composite bucket's group values, keyed by field.
// missing reports a bucket where some group field had no value (missing_bucket's null).
func esAggregateBucketLabels(key map[string]any, fields []string) (map[string]string, bool) {
	labels := make(map[string]string, len(fields))
	for i, field := range fields {
		v := key["g"+strconv.Itoa(i)]
		if v == nil {
			return nil, true
		}
		labels[field] = esAggregateKeyString(v)
	}
	return labels, false
}

func esAggregateKeyString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprint(t)
	}
}

func esAggregateBucketMillis(v any) int64 {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}

// esAggregateGroupKey identifies a group across pages: composite pages are ordered by
// key, so a group's time buckets can span a page boundary.
func esAggregateGroupKey(labels map[string]string, fields []string) string {
	parts := make([]string, len(fields))
	for i, field := range fields {
		parts[i] = labels[field]
	}
	return strings.Join(parts, "\x00")
}

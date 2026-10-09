package observability

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"nudgebee/services/security"
)

// LogQueryDetails is what a source knows about a log result beyond its lines: how many
// lines matched, and the counts the query asked the provider for.
type LogQueryDetails struct {
	// Total is the provider's match count; nil when it did not report one.
	Total *int64
	// TotalRelation is "eq" when Total is exact, "gte" when it is a lower bound.
	TotalRelation string
	// Returned is how many lines the provider sent back, before Nudgebee dropped any it
	// could not parse; zero when the source does not say. Truncation compares Total
	// with this, so unparseable lines are not mistaken for a cut.
	Returned int64
	// NoLines is set when the query asked for no lines at all (an Elasticsearch body
	// with size 0), so an empty Logs is not a cut-off answer.
	NoLines bool
	// Series is the provider's counts flattened into the metrics shape.
	Series []Result
	// HasCounts is set when the provider returned counts. With an empty Series it
	// means the counts did not fit series and are only in AggregationsRaw.
	HasCounts bool
	// GroupsOmitted is set when a grouping level left groups out (an Elasticsearch
	// terms aggregation with sum_other_doc_count > 0), so Series is not every group.
	GroupsOmitted bool
	// AggregationsRaw and TotalRaw are the provider's own fragments, verbatim. Sources
	// fill them only when the request set IncludeRaw.
	AggregationsRaw json.RawMessage
	TotalRaw        json.RawMessage
}

// DetailedLogSource is a LogSource that can report LogQueryDetails alongside its lines.
// FetchLogs prefers it over QueryLogs; sources that do not implement it get the
// central truncation fallback (lines returned == limit) and no series.
type DetailedLogSource interface {
	QueryLogsDetailed(ctx *security.RequestContext, req FetchLogRequest) ([]OutputLog, LogQueryDetails, error)
}

// countsQueryMatched reports a counts query (size 0, or aggregations) that matched
// something. It returns no lines by design, so FetchLogs must not treat it as an empty
// result: the empty-result diagnosis would replace its counts with an empty answer.
// "Matched something" is a positive total or any series, so a provider that reports no
// total still keeps its counts; with no evidence of a match the diagnosis runs as before.
func countsQueryMatched(details LogQueryDetails) bool {
	if !details.NoLines && !details.HasCounts {
		return false
	}
	return (details.Total != nil && *details.Total > 0) || len(details.Series) > 0
}

// applyLogQueryDetails fills the truncation, series and raw fields of a log result.
//
// truncated is exact when the provider reported a total: more matched than the lines
// skipped (offset) plus the lines returned. Otherwise it falls back to "as many lines
// came back as the limit allowed", which over-reports when exactly limit lines
// matched — callers read it as "may be cut off".
func applyLogQueryDetails(result *FetchLogsResult, details LogQueryDetails, limit, offset int, partial bool, includeRaw bool) {
	returned := int64(len(result.Logs))
	if details.Returned > returned {
		returned = details.Returned
	}
	switch {
	case details.NoLines:
		result.Truncated = false
	case details.Total != nil:
		result.Truncated = *details.Total > int64(offset)+returned
	default:
		// Exactly limit rows: the limit was applied and may have cut. More than limit
		// means the provider did not apply it (a Loki metric query returns every
		// point), so there is no sign of a cut.
		result.Truncated = limit > 0 && returned == int64(limit)
	}
	if partial {
		result.Truncated = true
	}
	result.Total = details.Total
	if details.Total != nil {
		result.TotalRelation = details.TotalRelation
	}

	result.Series = details.Series
	if details.GroupsOmitted && len(details.Series) > 0 {
		result.SeriesNote = "a terms grouping left some groups out (sum_other_doc_count > 0), so series does not hold every group; raise that terms aggregation's size"
	}
	if details.HasCounts && len(details.Series) == 0 {
		if includeRaw {
			result.SeriesNote = "the provider's counts do not fit series (only nested terms groupings optionally ending in one date_histogram do); they are in aggregations_raw"
		} else {
			result.SeriesNote = "the provider's counts do not fit series (only nested terms groupings optionally ending in one date_histogram do); set include_raw to get them in aggregations_raw"
		}
	}
	if includeRaw {
		result.AggregationsRaw = details.AggregationsRaw
		result.TotalRaw = details.TotalRaw
	}
}

// esLogQueryDetails reads the match count and aggregations from an Elasticsearch
// _search response. aggsDef is the request's aggregation definition ("aggs" or
// "aggregations" of the body sent), which says what each response level is: a
// response alone cannot tell a terms bucket on a date field from a date_histogram one.
// endMillis timestamps the single point of a series with no time histogram.
func esLogQueryDetails(rawJSON []byte, aggsDef map[string]any, size any, endMillis int64, includeRaw bool) LogQueryDetails {
	var probe struct {
		Hits struct {
			Total json.RawMessage   `json:"total"`
			Hits  []json.RawMessage `json:"hits"`
		} `json:"hits"`
		Aggregations json.RawMessage `json:"aggregations"`
	}
	details := LogQueryDetails{NoLines: esSizeIsZero(size)}
	if err := json.Unmarshal(rawJSON, &probe); err != nil {
		return details
	}

	details.Returned = int64(len(probe.Hits.Hits))
	if total, relation, ok := parseESHitsTotal(probe.Hits.Total); ok {
		details.Total = &total
		details.TotalRelation = relation
	}

	if len(probe.Aggregations) > 0 && string(probe.Aggregations) != "null" {
		details.HasCounts = true
		var aggs map[string]any
		if err := json.Unmarshal(probe.Aggregations, &aggs); err == nil {
			if endMillis <= 0 {
				endMillis = time.Now().UnixMilli()
			}
			if series, omitted, ok := esSeriesFromAggregations(aggsDef, aggs, endMillis); ok {
				details.Series = series
				details.GroupsOmitted = omitted
			}
		}
	}

	if includeRaw {
		if details.HasCounts {
			details.AggregationsRaw = probe.Aggregations
		}
		if len(probe.Hits.Total) > 0 && string(probe.Hits.Total) != "null" {
			details.TotalRaw = probe.Hits.Total
		}
	}
	return details
}

// parseESHitsTotal reads hits.total: an object {value, relation} on ES 7+ and
// OpenSearch, a bare number on ES 6 (always exact).
func parseESHitsTotal(raw json.RawMessage) (int64, string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, "", false
	}
	var asObject struct {
		Value    *int64 `json:"value"`
		Relation string `json:"relation"`
	}
	if err := json.Unmarshal(raw, &asObject); err == nil && asObject.Value != nil {
		relation := "eq"
		if asObject.Relation == "gte" {
			relation = "gte"
		}
		return *asObject.Value, relation, true
	}
	var asNumber int64
	if err := json.Unmarshal(raw, &asNumber); err == nil {
		return asNumber, "eq", true
	}
	return 0, "", false
}

// esSizeIsZero reports whether a body's size asks for no hits. JSON numbers decode
// as float64; a size the caller wrote as a string is honoured the way ES honours it.
func esSizeIsZero(size any) bool {
	switch v := size.(type) {
	case float64:
		return v == 0
	case int:
		return v == 0
	case int64:
		return v == 0
	case json.Number:
		return v.String() == "0"
	case string:
		return v == "0"
	}
	return false
}

// esAggsDefinition returns the aggregation definition of a _search body, under either
// of the two keys Elasticsearch accepts.
func esAggsDefinition(body map[string]any) map[string]any {
	if aggs, ok := body["aggs"].(map[string]any); ok {
		return aggs
	}
	if aggs, ok := body["aggregations"].(map[string]any); ok {
		return aggs
	}
	return nil
}

// esSeriesFromAggregations flattens counts into series when the aggregation is zero or
// more nested terms levels, optionally ending in one date_histogram:
//
//   - date_histogram only: one series, no labels, a point per time bucket;
//   - terms levels only: a series per group, one point at endMillis;
//   - terms levels, then date_histogram: a series per group, a point per time bucket.
//
// Each level must hold exactly one aggregation, and the last level none beyond it. The
// labels are the terms keys, named after each terms aggregation's field; the values
// are doc_count. Anything else (several aggregations at a level, metric
// sub-aggregations, other bucket types, two levels on one field) returns ok=false: the
// counts stay in the raw fragment rather than being half-flattened. omitted reports a
// terms level that left groups out.
func esSeriesFromAggregations(aggsDef map[string]any, aggs map[string]any, endMillis int64) (series []Result, omitted bool, ok bool) {
	levels, ok := esAggregationLevels(aggsDef)
	if !ok {
		return nil, false, false
	}
	if !esFlattenLevel(levels, aggs, map[string]string{}, endMillis, &series, &omitted) {
		return nil, false, false
	}
	// Bucket order follows the provider (by count, by key); sort for a stable answer.
	// Keys are built once per series, not once per comparison.
	keyed := make([]struct {
		key    string
		series Result
	}, len(series))
	for i, s := range series {
		keyed[i].key, keyed[i].series = esSeriesKey(s.Metric), s
	}
	sort.SliceStable(keyed, func(i, j int) bool { return keyed[i].key < keyed[j].key })
	for i := range keyed {
		series[i] = keyed[i].series
	}
	return series, omitted, true
}

type esAggLevel struct {
	name  string
	kind  string // "terms" | "date_histogram"
	field string
}

// esAggregationLevels walks the definition down its single-aggregation chain.
func esAggregationLevels(def map[string]any) ([]esAggLevel, bool) {
	var levels []esAggLevel
	fields := map[string]bool{}
	for len(def) > 0 {
		if len(def) != 1 {
			return nil, false
		}
		var name string
		var body map[string]any
		for k, v := range def {
			name = k
			body, _ = v.(map[string]any)
		}
		if body == nil {
			return nil, false
		}
		level := esAggLevel{name: name}
		var sub map[string]any
		for k, v := range body {
			switch k {
			case "terms", "date_histogram":
				if level.kind != "" {
					return nil, false
				}
				params, _ := v.(map[string]any)
				level.kind = k
				level.field, _ = params["field"].(string)
			case "aggs", "aggregations":
				sub, _ = v.(map[string]any)
			case "meta":
			default:
				return nil, false
			}
		}
		if level.kind == "" || (level.kind == "terms" && level.field == "") {
			return nil, false
		}
		if level.kind == "date_histogram" && len(sub) > 0 {
			return nil, false
		}
		// Labels are named after the field, so two levels on one field would collide.
		if level.kind == "terms" {
			if fields[level.field] {
				return nil, false
			}
			fields[level.field] = true
		}
		levels = append(levels, level)
		def = sub
	}
	return levels, len(levels) > 0
}

func esFlattenLevel(levels []esAggLevel, aggs map[string]any, labels map[string]string, endMillis int64, out *[]Result, omitted *bool) bool {
	level := levels[0]
	node, ok := aggs[level.name].(map[string]any)
	if !ok {
		return false
	}
	buckets, ok := node["buckets"].([]any)
	if !ok {
		// Keyed buckets ("keyed": true) come back as an object; not a shape we flatten.
		return false
	}

	if level.kind == "date_histogram" {
		series := Result{Metric: copyLabels(labels), Timestamps: make([]int64, 0, len(buckets)), Values: make([]float64, 0, len(buckets))}
		for _, b := range buckets {
			bucket, ok := b.(map[string]any)
			if !ok {
				return false
			}
			keyMillis, ok := bucket["key"].(float64)
			if !ok {
				return false
			}
			count, _ := bucket["doc_count"].(float64)
			series.Timestamps = append(series.Timestamps, int64(keyMillis)/1000)
			series.Values = append(series.Values, count)
		}
		*out = append(*out, series)
		return true
	}

	if other, _ := node["sum_other_doc_count"].(float64); other > 0 {
		*omitted = true
	}
	for _, b := range buckets {
		bucket, ok := b.(map[string]any)
		if !ok {
			return false
		}
		next := copyLabels(labels)
		next[level.field] = esBucketKey(bucket)
		if len(levels) == 1 {
			count, _ := bucket["doc_count"].(float64)
			*out = append(*out, Result{Metric: next, Timestamps: []int64{endMillis / 1000}, Values: []float64{count}})
			continue
		}
		if !esFlattenLevel(levels[1:], bucket, next, endMillis, out, omitted) {
			return false
		}
	}
	return true
}

// esBucketKey renders a terms bucket key: key_as_string when ES sent one (dates,
// booleans), else the key itself, with whole numbers printed without a fraction.
func esBucketKey(bucket map[string]any) string {
	if s, ok := bucket["key_as_string"].(string); ok {
		return s
	}
	switch k := bucket["key"].(type) {
	case string:
		return k
	case float64:
		return strconv.FormatFloat(k, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", k)
	}
}

func copyLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		out[k] = v
	}
	return out
}

func esSeriesKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	key := ""
	for _, k := range keys {
		key += k + "\x00" + labels[k] + "\x00"
	}
	return key
}

// labelAliasTargetsSource is a LogSource that translates a Nudgebee label name into
// more than one provider field (Elasticsearch: each shipper spells `pod` differently).
type labelAliasTargetsSource interface {
	LabelAliasTargets(name string) []string
}

// annotateLogLabelKinds sets Kind and Field on a label listing. A name is a "field"
// when the backend reported it or the mapping leaves it unchanged; otherwise it is an
// "alias", and Field names the first of its targets the backend reported. A target the
// backend did not report is never named (Loki maps `message` to `log`, which is a line
// filter, not a stream label), so a caller writing a native query is only pointed at
// names the backend knows.
func annotateLogLabelKinds(labels []OutputLogLabel, mergedMapping map[string]string, discovered []OutputLogLabel, source LogSource) {
	// Nothing reported means nothing is known: calling every name an alias would tell a
	// caller that real labels (Loki's `namespace`) are not fields. Leave Kind unset.
	if len(discovered) == 0 {
		return
	}
	reported := make(map[string]struct{}, len(discovered))
	for _, d := range discovered {
		reported[d.Label] = struct{}{}
	}
	expander, _ := source.(labelAliasTargetsSource)
	for i := range labels {
		name := labels[i].Label
		mapped := mergedMapping[name]
		if _, ok := reported[name]; ok || mapped == name {
			labels[i].Kind, labels[i].Field = "field", ""
			continue
		}
		labels[i].Kind, labels[i].Field = "alias", ""
		targets := []string{}
		if mapped != "" {
			targets = append(targets, mapped)
		}
		if expander != nil {
			targets = append(targets, expander.LabelAliasTargets(name)...)
		}
		for _, t := range targets {
			if _, ok := reported[t]; ok && t != name {
				labels[i].Field = t
				break
			}
		}
	}
}

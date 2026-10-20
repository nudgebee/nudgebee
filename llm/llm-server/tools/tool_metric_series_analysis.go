package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

func init() {
	core.RegisterNBToolFactory(ToolAnalyzeMetricSeries, func(accountId string) (core.NBTool, error) {
		return MetricSeriesAnalysisTool{}, nil
	})
}

// ToolAnalyzeMetricSeries computes trend, baseline and time-to-threshold for a
// metric series an earlier step already fetched (#41760).
const ToolAnalyzeMetricSeries = "analyze_metric_series"

// Analysis limits and rules. They are documented here because the model quotes
// the verdicts ("steady", "no forecast") the rules produce.
const (
	// metricSeriesMaxFileBytes caps a workspace file read.
	metricSeriesMaxFileBytes = 20 << 20
	// metricSeriesMaxAnalyzed is how many series one call reports.
	metricSeriesMaxAnalyzed = 10
	// A forecast needs at least this many points over at least this span.
	metricSeriesMinForecastPoints    = 8
	metricSeriesMinForecastSpanHours = 2.0
	// A trend is steady when the line explains at least this share of the
	// variance (r2), the fitted change over the window is material (at least
	// twice the residual standard deviation and at least 1% of the series
	// level), and level shifts do not account for half or more of it.
	metricSeriesSteadyR2         = 0.6
	metricSeriesMaterialNoiseMul = 2.0
	metricSeriesMaterialLevelPct = 0.01
	metricSeriesStepShareOfTrend = 0.5
	// A gap is spacing larger than this multiple of the median step.
	metricSeriesGapFactor = 3.0
	metricSeriesMaxGaps   = 10
	// A step change is a jump between consecutive points larger than
	// metricSeriesStepFactor x the median absolute point-to-point change and at
	// least metricSeriesStepMinRangeShare of the series range, whose new level
	// holds: the median of up to metricSeriesStepWindow points after it differs
	// from the median before it by at least half the jump. A one-sample spike
	// is not a level shift.
	metricSeriesStepFactor        = 5.0
	metricSeriesStepMinRangeShare = 0.10
	metricSeriesStepWindow        = 5
	metricSeriesMaxSteps          = 3
	// A projection longer than this multiple of the observed span is flagged.
	metricSeriesExtrapolationMul = 3.0
	// z for a 95% interval on the slope.
	metricSeriesZ95 = 1.96
	// metricSeriesMaxDepth bounds the JSON walk.
	metricSeriesMaxDepth = 14
	metricSeriesMaxName  = 240
)

const metricSeriesSupportedShapes = "Prometheus/metrics tool files ({query: [{metric, timestamps, values}]}), " +
	"any JSON holding equal-length \"timestamps\" and \"values\" arrays (e.g. event_<id>_evidence.json), " +
	"AWS CloudWatch get-metric-data (MetricDataResults) and get-metric-statistics (Datapoints), " +
	"Azure az monitor metrics list (value[].timeseries[].data), " +
	"GCP time-series list (points[].interval/value)"

var errMetricSeriesFileTooLarge = fmt.Errorf("file is larger than %d MB", metricSeriesMaxFileBytes>>20)

// readMetricSeriesFile reads a workspace file, at most maxBytes+1 bytes so an
// oversized file is detected without loading it. A var so tests need no
// workspace.
var readMetricSeriesFile = func(nbCtx core.NbToolContext, path string, maxBytes int64) ([]byte, error) {
	if nbCtx.Ctx == nil {
		return nil, errors.New("readMetricSeriesFile: no request context")
	}
	stream, err := workspace.NewWorkspaceManager().ReadFileStream(nbCtx.Ctx, nbCtx.AccountId, nbCtx.ConversationId, path)
	if err != nil {
		return nil, fmt.Errorf("readMetricSeriesFile: %w", err)
	}
	defer func() { _ = stream.Close() }()
	data, err := io.ReadAll(io.LimitReader(stream, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("readMetricSeriesFile: reading %s: %w", path, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, errMetricSeriesFileTooLarge
	}
	return data, nil
}

// MetricSeriesAnalysisTool computes, from points a previous step saved, whether
// a metric is getting worse, when it reaches the next threshold, and whether a
// threshold sits inside normal operation. It never queries a metrics provider.
type MetricSeriesAnalysisTool struct {
	core.ReadOnlyTool
}

func (t MetricSeriesAnalysisTool) Name() string             { return ToolAnalyzeMetricSeries }
func (t MetricSeriesAnalysisTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t MetricSeriesAnalysisTool) Description() string {
	return "Computes trend, forecast and threshold statistics for metric time series a previous step already fetched; it never queries a metrics source. " +
		"Use it when an investigation needs to say whether a metric is getting worse, when it reaches the next threshold, or whether the threshold sits inside normal operation " +
		"(also whether an alert is flapping or firing on missing data). " +
		"Pass file_ref = the exact workspace file name a metrics or CLI step saved (e.g. metrics_prometheus_<n>.json, a cloud CLI metric output file, event_<id>_evidence.json), " +
		"copied from that step's result; never guess a name. Or pass points inline. " +
		"Reads Prometheus/metrics tool files, AWS CloudWatch get-metric-data and get-metric-statistics, Azure az monitor metrics list, GCP time-series list, " +
		"and any JSON with matching timestamps/values arrays. " +
		"Returns per series: baseline (first/last/min/median/p95/max), least-squares trend with r2 and whether it is steady, step changes, data gaps, " +
		"and per threshold whether it is past now, the percent of samples past it, the number of crossings, and a time-to-reach range or the reason none is given. " +
		"Quote only numbers it returns, and cite this step."
}

func (t MetricSeriesAnalysisTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"file_ref": {
				Type: core.ToolSchemaTypeString,
				Description: "Exact workspace file name a previous tool saved (e.g. metrics_prometheus_<n>.json, a cloud CLI metric output file, event_<id>_evidence.json). " +
					"Copy it from that step's result; never guess. Either file_ref or points is required.",
			},
			"points": {
				Type: core.ToolSchemaTypeArray,
				Description: "Inline alternative to file_ref: [[timestamp, value], ...]; a JSON string of {\"timestamps\":[...],\"values\":[...]} is also accepted. " +
					"Timestamps: unix seconds, unix milliseconds or RFC3339 strings.",
				Items: map[string]any{"type": "array", "items": map[string]any{"type": "number"}},
			},
			"thresholds": {
				Type: core.ToolSchemaTypeArray,
				Description: "Alert levels to test: {value: number, direction: \"above\"|\"below\" (the alert fires when the metric goes above / below value), label: e.g. \"P1 · 5%\" or \"memory limit\"}. " +
					"Use the same unit as the series values.",
				Items: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"value":     map[string]any{"type": "number"},
						"direction": map[string]any{"type": "string", "enum": []string{"above", "below"}},
						"label":     map[string]any{"type": "string"},
					},
					"required": []string{"value"},
				},
			},
			"match": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional filter when the file holds many series: a substring of the series name/labels, or key=value (label key, value substring).",
			},
			"statistic": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional CloudWatch/Azure statistic to read (Average, Maximum, Minimum, Sum, SampleCount/Count, or an extended statistic like p99). Default Average when present, else the first available.",
			},
		},
		RequiredOneOf: [][]string{{"file_ref", "points"}},
	}
}

func (t MetricSeriesAnalysisTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	logger := slog.Default()
	if nbCtx.Ctx != nil {
		logger = nbCtx.Ctx.GetLogger()
	}
	args := input.Arguments
	if len(args) == 0 && strings.HasPrefix(strings.TrimSpace(input.Command), "{") {
		_ = json.Unmarshal([]byte(input.Command), &args)
	}
	fileRef := strings.Trim(strings.TrimSpace(metricSeriesArgString(args, "file_ref")), "\"'`")
	match := strings.TrimSpace(metricSeriesArgString(args, "match"))
	statistic := strings.TrimSpace(metricSeriesArgString(args, "statistic"))

	thresholds, err := parseMetricSeriesThresholds(args["thresholds"])
	if err != nil {
		return metricSeriesFailure(err.Error()), nil
	}

	var (
		series []msSeries
		notes  []string
		source string
	)
	switch {
	case fileRef != "":
		source = fileRef
		raw, readErr := readMetricSeriesFile(nbCtx, fileRef, metricSeriesMaxFileBytes)
		if readErr == nil && len(raw) > metricSeriesMaxFileBytes {
			readErr = errMetricSeriesFileTooLarge
		}
		if errors.Is(readErr, errMetricSeriesFileTooLarge) {
			return metricSeriesFailure(fmt.Sprintf("Workspace file %q is larger than %d MB. Re-run the metrics query over a shorter range or a larger step, or pass points inline.",
				fileRef, metricSeriesMaxFileBytes>>20)), nil
		}
		if readErr != nil {
			logger.Warn("analyze_metric_series: reading workspace file failed", "file_ref", fileRef, "error", readErr)
			return metricSeriesFailure(fmt.Sprintf("Could not read workspace file %q: %v. Pass the exact file name a previous metrics or CLI step saved (it appears in that step's result); do not guess a name.",
				fileRef, readErr)), nil
		}
		series, notes, err = parseMetricSeriesFile(raw, statistic)
		if err != nil {
			return metricSeriesFailure(fmt.Sprintf("%q: %v", fileRef, err)), nil
		}
	case args["points"] != nil:
		source = "points"
		s, perr := parseMetricSeriesPoints(args["points"])
		if perr != nil {
			return metricSeriesFailure(perr.Error()), nil
		}
		series = []msSeries{s}
	default:
		return metricSeriesFailure("Pass file_ref (the exact workspace file name a metrics or CLI step saved) or points ([[timestamp, value], ...])."), nil
	}

	report := buildMetricSeriesReport(source, series, notes, thresholds, match)
	if report.SeriesMatched == 0 {
		names := make([]string, 0, metricSeriesMaxAnalyzed)
		for i := 0; i < len(series) && i < metricSeriesMaxAnalyzed; i++ {
			names = append(names, series[i].name)
		}
		return metricSeriesFailure(fmt.Sprintf("No series matched %q among %d series. First series names: %s", match, len(series), strings.Join(names, " | "))), nil
	}
	data, err := json.Marshal(report)
	if err != nil {
		logger.Error("analyze_metric_series: marshal failed", "error", err)
		return metricSeriesFailure(fmt.Sprintf("Error formatting response: %s", err.Error())), nil
	}
	return core.NBToolResponse{
		Data:   string(data),
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

func metricSeriesFailure(msg string) core.NBToolResponse {
	return core.NBToolResponse{Data: msg, Type: core.NBToolResponseTypeText, Status: core.NBToolResponseStatusError}
}

func metricSeriesArgString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// ---------------------------------------------------------------------------
// Input parsing
// ---------------------------------------------------------------------------

type msPoint struct {
	at time.Time
	v  float64
}

type msSeries struct {
	name      string
	labels    map[string]string
	statistic string
	points    []msPoint
}

type msThreshold struct {
	Value     float64
	Direction string // "above" or "below"
	Label     string
}

// metricSeriesDecodeLoose turns a JSON string argument into its value, so an
// array the model sent as a string still parses.
func metricSeriesDecodeLoose(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") && !strings.HasPrefix(s, "{") {
		return v
	}
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return v
	}
	return out
}

func parseMetricSeriesThresholds(raw any) ([]msThreshold, error) {
	raw = metricSeriesDecodeLoose(raw)
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		items = []any{raw}
	}
	out := make([]msThreshold, 0, len(items))
	for i, item := range items {
		var th msThreshold
		switch v := item.(type) {
		case map[string]any:
			value, ok := msNumber(v["value"])
			if !ok {
				return nil, fmt.Errorf("thresholds[%d]: value must be a number", i)
			}
			th.Value = value
			dir, _ := v["direction"].(string)
			switch strings.ToLower(strings.TrimSpace(dir)) {
			case "", "above", ">", ">=", "gt", "gte", "over":
				th.Direction = "above"
			case "below", "<", "<=", "lt", "lte", "under":
				th.Direction = "below"
			default:
				return nil, fmt.Errorf("thresholds[%d]: direction must be \"above\" or \"below\", got %q", i, dir)
			}
			th.Label, _ = v["label"].(string)
		default:
			value, ok := msNumber(v)
			if !ok {
				return nil, fmt.Errorf("thresholds[%d]: expected {value, direction, label}", i)
			}
			th.Value, th.Direction = value, "above"
		}
		if strings.TrimSpace(th.Label) == "" {
			th.Label = msFmt(th.Value)
		}
		out = append(out, th)
	}
	return out, nil
}

func parseMetricSeriesPoints(raw any) (msSeries, error) {
	raw = metricSeriesDecodeLoose(raw)
	s := msSeries{name: "points"}
	switch v := raw.(type) {
	case map[string]any:
		if found, ok := msSeriesFromValuesTimestamps(v, "points"); ok {
			found.name = "points"
			return msNormalize(found), nil
		}
		return s, errors.New("points: expected {\"timestamps\":[...],\"values\":[...]} with equal lengths")
	case []any:
		for _, item := range v {
			if p, ok := msPointFromPair(item); ok {
				s.points = append(s.points, p)
			}
		}
	default:
		return s, errors.New("points: expected [[timestamp, value], ...]")
	}
	s = msNormalize(s)
	if len(s.points) == 0 {
		return s, errors.New("points: no valid [timestamp, value] pairs (timestamps: RFC3339, unix seconds or ms; values numeric)")
	}
	return s, nil
}

// msPointFromPair reads [ts, v] or {timestamp|time|t, value|v}.
func msPointFromPair(item any) (msPoint, bool) {
	switch p := item.(type) {
	case []any:
		if len(p) < 2 {
			return msPoint{}, false
		}
		at, okT := msTime(p[0])
		v, okV := msNumber(p[1])
		return msPoint{at: at, v: v}, okT && okV
	case map[string]any:
		var ts, val any
		for _, k := range []string{"timestamp", "Timestamp", "timeStamp", "time", "t"} {
			if x, ok := p[k]; ok {
				ts = x
				break
			}
		}
		for _, k := range []string{"value", "Value", "v"} {
			if x, ok := p[k]; ok {
				val = x
				break
			}
		}
		at, okT := msTime(ts)
		v, okV := msNumber(val)
		return msPoint{at: at, v: v}, okT && okV
	}
	return msPoint{}, false
}

// msNumber reads a finite number from a JSON number or numeric string.
func msNumber(v any) (float64, bool) {
	var f float64
	switch x := v.(type) {
	case float64:
		f = x
	case float32:
		f = float64(x)
	case int:
		f = float64(x)
	case int64:
		f = float64(x)
	case json.Number:
		var err error
		if f, err = x.Float64(); err != nil {
			return 0, false
		}
	case string:
		var err error
		if f, err = strconv.ParseFloat(strings.TrimSpace(x), 64); err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

var msTimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05Z0700",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04",
}

// msTime reads an RFC3339 string or a unix timestamp. The unit of a number is
// told by magnitude: >= 1e17 nanoseconds, >= 1e14 microseconds, >= 1e11
// milliseconds (1973 onwards), else seconds (up to year 5138).
func msTime(v any) (time.Time, bool) {
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return msTimeFromNumber(f)
		}
		for _, layout := range msTimeLayouts {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC(), true
			}
		}
		return time.Time{}, false
	}
	f, ok := msNumber(v)
	if !ok {
		return time.Time{}, false
	}
	return msTimeFromNumber(f)
}

func msTimeFromNumber(f float64) (time.Time, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
		return time.Time{}, false
	}
	var secs float64
	switch abs := math.Abs(f); {
	case abs >= 1e17:
		secs = f / 1e9
	case abs >= 1e14:
		secs = f / 1e6
	case abs >= 1e11:
		secs = f / 1e3
	default:
		secs = f
	}
	whole := math.Floor(secs)
	return time.Unix(int64(whole), int64(math.Round((secs-whole)*1e9))).UTC(), true
}

// msNormalize sorts points by time and keeps the last value of a repeated
// timestamp.
func msNormalize(s msSeries) msSeries {
	sort.SliceStable(s.points, func(i, j int) bool { return s.points[i].at.Before(s.points[j].at) })
	out := s.points[:0]
	for _, p := range s.points {
		if n := len(out); n > 0 && out[n-1].at.Equal(p.at) {
			out[n-1] = p
			continue
		}
		out = append(out, p)
	}
	s.points = out
	return s
}

// metricSeriesDecodeJSON decodes the first JSON value in raw. A cloud CLI tool
// may prefix stdout with a bracketed line such as "[account: name]"; such
// leading lines are dropped when the content does not parse as it is.
func metricSeriesDecodeJSON(raw []byte) (any, error) {
	body := bytes.TrimSpace(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")))
	var firstErr error
	for attempt := 0; attempt < 4 && len(body) > 0; attempt++ {
		var out any
		dec := json.NewDecoder(bytes.NewReader(body))
		err := dec.Decode(&out)
		if err == nil {
			return out, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		nl := bytes.IndexByte(body, '\n')
		if nl < 0 {
			break
		}
		first := bytes.TrimSpace(body[:nl])
		if !bytes.HasPrefix(first, []byte("[")) || !bytes.HasSuffix(first, []byte("]")) {
			break
		}
		body = bytes.TrimSpace(body[nl+1:])
	}
	if firstErr == nil {
		firstErr = errors.New("empty content")
	}
	return nil, fmt.Errorf("metricSeriesDecodeJSON: not JSON: %w", firstErr)
}

// parseMetricSeriesFile finds every series in a saved file by its structure.
func parseMetricSeriesFile(raw []byte, statistic string) ([]msSeries, []string, error) {
	decoded, err := metricSeriesDecodeJSON(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("content is not JSON (%v). Supported shapes: %s", err, metricSeriesSupportedShapes)
	}
	w := &msWalker{statistic: statistic}
	w.walk(decoded, "", 0)
	if len(w.series) == 0 {
		msg := "no time series found. Supported shapes: " + metricSeriesSupportedShapes
		if len(w.notes) > 0 {
			msg += ". " + strings.Join(w.notes, "; ")
		}
		return nil, w.notes, errors.New(msg)
	}
	return w.series, w.notes, nil
}

type msWalker struct {
	statistic string
	series    []msSeries
	notes     []string
}

// msContainerKeys say nothing about the series under them, so the key above
// them (a query, a metric group) names the series instead.
var msContainerKeys = map[string]bool{
	"data": true, "result": true, "results": true, "items": true, "series": true, "payload": true,
	"series_list_result": true, "value": true, "metrics": true, "timeseries": true, "timeSeries": true,
}

func (w *msWalker) add(s msSeries) {
	s = msNormalize(s)
	if len(s.points) == 0 {
		w.notes = append(w.notes, fmt.Sprintf("series %q has no numeric points", s.name))
		return
	}
	w.series = append(w.series, s)
}

func (w *msWalker) walk(node any, parentKey string, depth int) {
	if depth > metricSeriesMaxDepth {
		return
	}
	switch v := node.(type) {
	case map[string]any:
		if w.tryShapes(v, parentKey) {
			return
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			childKey := k
			if msContainerKeys[k] {
				childKey = parentKey
			}
			w.walk(v[k], childKey, depth+1)
		}
	case []any:
		for _, item := range v {
			w.walk(item, parentKey, depth+1)
		}
	}
}

// tryShapes recognizes one series-bearing object; true when m was consumed.
func (w *msWalker) tryShapes(m map[string]any, parentKey string) bool {
	// AWS CloudWatch get-metric-data: MetricDataResults[] {Id, Label, Timestamps, Values}.
	if ts, okT := m["Timestamps"].([]any); okT {
		if vals, okV := m["Values"].([]any); okV {
			w.addCloudWatchResult(m, ts, vals)
			return true
		}
	}
	// AWS CloudWatch get-metric-statistics: {Label, Datapoints: [{Timestamp, Average, ...}]}.
	if dps, ok := m["Datapoints"].([]any); ok {
		label, _ := m["Label"].(string)
		w.addStatPoints(label, map[string]string{"Label": label}, dps, "Timestamp", msCloudWatchStatOrder)
		return true
	}
	// Azure az monitor metrics list: {name: {value}, timeseries: [{metadatavalues, data: [{timeStamp, average}]}]}.
	if tss, ok := m["timeseries"].([]any); ok && msLooksAzure(tss) {
		w.addAzureMetric(m, tss)
		return true
	}
	// GCP time series: {metric, resource, points: [{interval: {endTime}, value: {doubleValue}}]}.
	if pts, ok := m["points"].([]any); ok && msLooksGCP(pts) {
		w.addGCPSeries(m, pts)
		return true
	}
	// Prometheus/metrics tool and the generic structural case.
	if s, ok := msSeriesFromValuesTimestamps(m, parentKey); ok {
		w.add(s)
		return true
	}
	return false
}

func (w *msWalker) addCloudWatchResult(m map[string]any, ts, vals []any) {
	label, _ := m["Label"].(string)
	id, _ := m["Id"].(string)
	name := label
	if name == "" {
		name = id
	} else if id != "" && id != label {
		name = label + " (" + id + ")"
	}
	labels := map[string]string{"Label": label, "Id": id}
	if len(ts) != len(vals) {
		w.notes = append(w.notes, fmt.Sprintf("series %q skipped: %d timestamps vs %d values", name, len(ts), len(vals)))
		return
	}
	if code, _ := m["StatusCode"].(string); code == "PartialData" {
		w.notes = append(w.notes, fmt.Sprintf("series %q: CloudWatch returned PartialData (more pages exist); analysis covers the returned points only", name))
	}
	s := msSeries{name: msTruncate(name), labels: labels}
	for i := range ts {
		at, okT := msTime(ts[i])
		v, okV := msNumber(vals[i])
		if okT && okV {
			s.points = append(s.points, msPoint{at: at, v: v})
		}
	}
	w.add(s)
}

var (
	msCloudWatchStatOrder = []string{"Average", "Maximum", "Minimum", "Sum", "SampleCount"}
	msAzureStatOrder      = []string{"average", "maximum", "minimum", "total", "count"}
	// msStatAliases maps a requested statistic to the names providers use.
	msStatAliases = map[string][]string{
		"average": {"average", "avg", "mean"}, "avg": {"average", "avg", "mean"}, "mean": {"average", "avg", "mean"},
		"maximum": {"maximum", "max"}, "max": {"maximum", "max"},
		"minimum": {"minimum", "min"}, "min": {"minimum", "min"},
		"sum": {"sum", "total"}, "total": {"sum", "total"},
		"samplecount": {"samplecount", "count"}, "count": {"samplecount", "count"},
	}
)

// addStatPoints reads datapoints that carry one value per statistic.
func (w *msWalker) addStatPoints(name string, labels map[string]string, dps []any, tsKey string, order []string) {
	type row struct {
		at    time.Time
		stats map[string]float64
	}
	rows := make([]row, 0, len(dps))
	available := map[string]bool{}
	for _, item := range dps {
		dp, ok := item.(map[string]any)
		if !ok {
			continue
		}
		at, ok := msTime(dp[tsKey])
		if !ok {
			continue
		}
		r := row{at: at, stats: map[string]float64{}}
		for k, raw := range dp {
			if k == tsKey || strings.EqualFold(k, "unit") {
				continue
			}
			if ext, ok := raw.(map[string]any); ok && k == "ExtendedStatistics" {
				for ek, ev := range ext {
					if f, ok := msNumber(ev); ok {
						r.stats[ek] = f
						available[ek] = true
					}
				}
				continue
			}
			if f, ok := msNumber(raw); ok {
				r.stats[k] = f
				available[k] = true
			}
		}
		rows = append(rows, r)
	}
	stat, err := msPickStatistic(available, w.statistic, order)
	if err != nil {
		w.notes = append(w.notes, fmt.Sprintf("series %q skipped: %v", name, err))
		return
	}
	s := msSeries{name: msTruncate(name), labels: labels, statistic: stat}
	for _, r := range rows {
		if v, ok := r.stats[stat]; ok {
			s.points = append(s.points, msPoint{at: r.at, v: v})
		}
	}
	w.add(s)
}

// msPickStatistic picks the requested statistic (case-insensitive, with
// aliases), else the first of order that is present, else the first available.
func msPickStatistic(available map[string]bool, requested string, order []string) (string, error) {
	if len(available) == 0 {
		return "", errors.New("no numeric datapoints")
	}
	names := make([]string, 0, len(available))
	for k := range available {
		names = append(names, k)
	}
	sort.Strings(names)
	if requested != "" {
		want := strings.ToLower(requested)
		candidates := msStatAliases[want]
		if len(candidates) == 0 {
			candidates = []string{want}
		}
		for _, c := range candidates {
			for _, n := range names {
				if strings.ToLower(n) == c {
					return n, nil
				}
			}
		}
		return "", fmt.Errorf("statistic %q not present (available: %s)", requested, strings.Join(names, ", "))
	}
	for _, o := range order {
		if available[o] {
			return o, nil
		}
	}
	return names[0], nil
}

func msLooksAzure(tss []any) bool {
	for _, t := range tss {
		if m, ok := t.(map[string]any); ok {
			if _, ok := m["data"].([]any); ok {
				return true
			}
		}
	}
	return false
}

func (w *msWalker) addAzureMetric(m map[string]any, tss []any) {
	metricName := ""
	if n, ok := m["name"].(map[string]any); ok {
		metricName, _ = n["value"].(string)
		if metricName == "" {
			metricName, _ = n["localizedValue"].(string)
		}
	} else if n, ok := m["name"].(string); ok {
		metricName = n
	}
	for _, t := range tss {
		ts, ok := t.(map[string]any)
		if !ok {
			continue
		}
		data, _ := ts["data"].([]any)
		labels := map[string]string{"metric": metricName}
		var parts []string
		if mds, ok := ts["metadatavalues"].([]any); ok {
			for _, md := range mds {
				mm, ok := md.(map[string]any)
				if !ok {
					continue
				}
				key := ""
				if n, ok := mm["name"].(map[string]any); ok {
					key, _ = n["value"].(string)
				}
				val := fmt.Sprint(mm["value"])
				if key != "" {
					labels[key] = val
					parts = append(parts, key+"="+val)
				}
			}
		}
		name := metricName
		if len(parts) > 0 {
			sort.Strings(parts)
			name += "{" + strings.Join(parts, ",") + "}"
		}
		w.addStatPoints(name, labels, data, "timeStamp", msAzureStatOrder)
	}
}

func msLooksGCP(pts []any) bool {
	for _, p := range pts {
		if m, ok := p.(map[string]any); ok {
			_, hasInterval := m["interval"].(map[string]any)
			_, hasValue := m["value"].(map[string]any)
			return hasInterval && hasValue
		}
	}
	return false
}

func (w *msWalker) addGCPSeries(m map[string]any, pts []any) {
	labels := map[string]string{}
	metricType := ""
	if metric, ok := m["metric"].(map[string]any); ok {
		metricType, _ = metric["type"].(string)
		if ls, ok := metric["labels"].(map[string]any); ok {
			for k, v := range ls {
				labels[k] = fmt.Sprint(v)
			}
		}
	}
	if res, ok := m["resource"].(map[string]any); ok {
		if rt, ok := res["type"].(string); ok {
			labels["resource_type"] = rt
		}
		if ls, ok := res["labels"].(map[string]any); ok {
			for k, v := range ls {
				labels[k] = fmt.Sprint(v)
			}
		}
	}
	labels["metric"] = metricType
	s := msSeries{name: msTruncate(msLabelName(metricType, labels, "metric")), labels: labels}
	for _, item := range pts {
		p, ok := item.(map[string]any)
		if !ok {
			continue
		}
		var at time.Time
		okT := false
		if iv, ok := p["interval"].(map[string]any); ok {
			if at, okT = msTime(iv["endTime"]); !okT {
				at, okT = msTime(iv["startTime"])
			}
		}
		val, okV := msGCPValue(p["value"])
		if okT && okV {
			s.points = append(s.points, msPoint{at: at, v: val})
		}
	}
	w.add(s)
}

func msGCPValue(v any) (float64, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return 0, false
	}
	for _, k := range []string{"doubleValue", "int64Value", "double_value", "int64_value"} {
		if x, ok := m[k]; ok {
			return msNumber(x)
		}
	}
	if d, ok := m["distributionValue"].(map[string]any); ok {
		return msNumber(d["mean"])
	}
	return 0, false
}

// msSeriesFromValuesTimestamps reads {timestamps: [...], values: [...]} of equal
// length, or a Prometheus matrix {metric, values: [[ts, "v"], ...]}.
func msSeriesFromValuesTimestamps(m map[string]any, parentKey string) (msSeries, bool) {
	values, okV := m["values"].([]any)
	if !okV || len(values) == 0 {
		return msSeries{}, false
	}
	labels := map[string]string{}
	if lm, ok := m["metric"].(map[string]any); ok {
		for k, v := range lm {
			labels[k] = fmt.Sprint(v)
		}
	}
	s := msSeries{labels: labels}
	stamps, okT := m["timestamps"].([]any)
	switch {
	case okT && len(stamps) == len(values):
		for i := range values {
			at, okAt := msTime(stamps[i])
			v, okVal := msNumber(values[i])
			if okAt && okVal {
				s.points = append(s.points, msPoint{at: at, v: v})
			}
		}
	case !okT:
		pair, isPair := values[0].([]any)
		if !isPair || len(pair) != 2 {
			return msSeries{}, false
		}
		for _, item := range values {
			if p, ok := msPointFromPair(item); ok {
				s.points = append(s.points, p)
			}
		}
	default:
		return msSeries{}, false
	}
	base := parentKey
	for _, k := range []string{"name", "label", "Label"} {
		if n, ok := m[k].(string); ok && n != "" {
			base = n
			break
		}
	}
	if n, ok := m["metric"].(string); ok && n != "" {
		base = n
	}
	if mn, ok := labels["__name__"]; ok {
		base = mn
	}
	s.name = msTruncate(msLabelName(base, labels, "__name__"))
	if s.name == "" {
		s.name = "series"
	}
	return s, true
}

// msLabelName renders base{k=v,...} with sorted keys, leaving out skip.
func msLabelName(base string, labels map[string]string, skip string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if k != skip {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return base
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+labels[k])
	}
	return base + "{" + strings.Join(parts, ",") + "}"
}

func msTruncate(s string) string {
	if len(s) <= metricSeriesMaxName {
		return s
	}
	cut := metricSeriesMaxName
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

// msMatches applies the match filter: key=value (label key, value substring,
// case-insensitive), else a substring of the name or any label value.
func msMatches(s msSeries, match string) bool {
	if match == "" {
		return true
	}
	lower := strings.ToLower(match)
	if strings.Contains(strings.ToLower(s.name), lower) {
		return true
	}
	if key, val, ok := strings.Cut(match, "="); ok {
		key = strings.TrimSpace(key)
		val = strings.ToLower(strings.Trim(strings.TrimSpace(val), "\"'"))
		for k, v := range s.labels {
			if strings.EqualFold(k, key) && strings.Contains(strings.ToLower(v), val) {
				return true
			}
		}
		return false
	}
	for _, v := range s.labels {
		if strings.Contains(strings.ToLower(v), lower) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Analysis
// ---------------------------------------------------------------------------

type metricSeriesReport struct {
	Source         string           `json:"source"`
	SeriesFound    int              `json:"series_found"`
	SeriesMatched  int              `json:"series_matched"`
	SeriesAnalyzed int              `json:"series_analyzed"`
	SeriesSkipped  int              `json:"series_skipped,omitempty"`
	Notes          []string         `json:"notes,omitempty"`
	Series         []seriesAnalysis `json:"series"`
}

type seriesAnalysis struct {
	Name              string                `json:"name"`
	Summary           string                `json:"summary"`
	Statistic         string                `json:"statistic,omitempty"`
	Points            int                   `json:"points"`
	Start             string                `json:"start"`
	End               string                `json:"end"`
	SpanHours         float64               `json:"span_hours"`
	MedianStepSeconds float64               `json:"median_step_seconds"`
	GapCount          int                   `json:"gap_count"`
	Gaps              []seriesGap           `json:"gaps,omitempty"`
	Baseline          seriesBaseline        `json:"baseline"`
	Trend             *seriesTrend          `json:"trend,omitempty"`
	StepChanges       []seriesStep          `json:"step_changes,omitempty"`
	Thresholds        []seriesThresholdStat `json:"thresholds,omitempty"`
}

type seriesGap struct {
	From    string  `json:"from"`
	To      string  `json:"to"`
	Minutes float64 `json:"minutes"`
}

type seriesBaseline struct {
	First  float64 `json:"first"`
	Last   float64 `json:"last"`
	Min    float64 `json:"min"`
	Median float64 `json:"median"`
	P95    float64 `json:"p95"`
	Max    float64 `json:"max"`
}

type seriesTrend struct {
	RatePerHour float64 `json:"rate_per_hour"`
	RatePerDay  float64 `json:"rate_per_day"`
	R2          float64 `json:"r2"`
	Direction   string  `json:"direction"`
	Steady      bool    `json:"steady"`
	Note        string  `json:"note,omitempty"`

	// Unrounded fit, for the forecast.
	slope, intercept, slopeSE float64
	stepDominated             bool
}

type seriesStep struct {
	At    string  `json:"at"`
	From  float64 `json:"from"`
	To    float64 `json:"to"`
	Delta float64 `json:"delta"`
}

type seriesThresholdStat struct {
	Label             string       `json:"label"`
	Value             float64      `json:"value"`
	Direction         string       `json:"direction"`
	CurrentlyPast     bool         `json:"currently_past"`
	PctTimePast       float64      `json:"pct_time_past"`
	Crossings         int          `json:"crossings"`
	TimeToReach       *seriesReach `json:"time_to_reach,omitempty"`
	NotForecastReason string       `json:"not_forecast_reason,omitempty"`
}

type seriesReach struct {
	HoursEstimate float64  `json:"hours_estimate"`
	HoursLow      float64  `json:"hours_low"`
	HoursHigh     *float64 `json:"hours_high"`
	Earliest      string   `json:"earliest"`
	Latest        string   `json:"latest,omitempty"`
	Note          string   `json:"note,omitempty"`
}

func buildMetricSeriesReport(source string, series []msSeries, notes []string, thresholds []msThreshold, match string) metricSeriesReport {
	report := metricSeriesReport{Source: source, SeriesFound: len(series), Notes: notes, Series: []seriesAnalysis{}}
	for _, s := range series {
		if !msMatches(s, match) {
			continue
		}
		report.SeriesMatched++
		if len(report.Series) >= metricSeriesMaxAnalyzed {
			report.SeriesSkipped++
			continue
		}
		report.Series = append(report.Series, analyzeMetricSeries(s, thresholds))
	}
	report.SeriesAnalyzed = len(report.Series)
	if report.SeriesSkipped > 0 {
		report.Notes = append(report.Notes, fmt.Sprintf("analyzed the first %d of %d matching series; pass match to select others", metricSeriesMaxAnalyzed, report.SeriesMatched))
	}
	return report
}

func analyzeMetricSeries(s msSeries, thresholds []msThreshold) seriesAnalysis {
	pts := s.points
	n := len(pts)
	start, end := pts[0].at, pts[n-1].at
	spanH := end.Sub(start).Hours()
	out := seriesAnalysis{
		Name:      s.name,
		Statistic: s.statistic,
		Points:    n,
		Start:     start.Format(time.RFC3339),
		End:       end.Format(time.RFC3339),
		SpanHours: math.Round(spanH*100) / 100,
	}

	vals := make([]float64, n)
	for i, p := range pts {
		vals[i] = p.v
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	median := msQuantile(sorted, 0.5)
	rng := sorted[n-1] - sorted[0]
	out.Baseline = seriesBaseline{
		First: msRound(vals[0]), Last: msRound(vals[n-1]), Min: msRound(sorted[0]),
		Median: msRound(median), P95: msRound(msQuantile(sorted, 0.95)), Max: msRound(sorted[n-1]),
	}

	// Spacing and gaps.
	if n >= 2 {
		steps := make([]float64, 0, n-1)
		for i := 1; i < n; i++ {
			steps = append(steps, pts[i].at.Sub(pts[i-1].at).Seconds())
		}
		sortedSteps := append([]float64(nil), steps...)
		sort.Float64s(sortedSteps)
		medStep := msQuantile(sortedSteps, 0.5)
		out.MedianStepSeconds = math.Round(medStep*10) / 10
		if medStep > 0 {
			for i, st := range steps {
				if st > metricSeriesGapFactor*medStep {
					out.GapCount++
					if len(out.Gaps) < metricSeriesMaxGaps {
						out.Gaps = append(out.Gaps, seriesGap{
							From: pts[i].at.Format(time.RFC3339), To: pts[i+1].at.Format(time.RFC3339),
							Minutes: math.Round(st/60*10) / 10,
						})
					}
				}
			}
		}
	}

	steps, stepSum := msStepChanges(pts, rng)
	for _, st := range steps {
		out.StepChanges = append(out.StepChanges, seriesStep{
			At: st.at.Format(time.RFC3339), From: msRound(st.from), To: msRound(st.to), Delta: msRound(st.to - st.from),
		})
	}

	if n >= 3 && spanH > 0 {
		out.Trend = msTrend(pts, spanH, median, rng, stepSum, len(steps))
	}

	for _, th := range thresholds {
		out.Thresholds = append(out.Thresholds, msThresholdStat(pts, spanH, out.Trend, th))
	}
	out.Summary = msSummary(out, spanH)
	return out
}

// msQuantile is the linear-interpolated quantile of sorted values.
func msQuantile(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	pos := q * float64(n-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}

type msStep struct {
	at       time.Time
	from, to float64
}

// msStepChanges finds level shifts (see metricSeriesStepFactor). It returns the
// largest metricSeriesMaxSteps in time order, and the signed sum of all.
func msStepChanges(pts []msPoint, rng float64) ([]msStep, float64) {
	n := len(pts)
	if n < 3 || rng <= 0 {
		return nil, 0
	}
	diffs := make([]float64, n-1)
	abs := make([]float64, n-1)
	for i := 0; i < n-1; i++ {
		diffs[i] = pts[i+1].v - pts[i].v
		abs[i] = math.Abs(diffs[i])
	}
	sortedAbs := append([]float64(nil), abs...)
	sort.Float64s(sortedAbs)
	medAbs := msQuantile(sortedAbs, 0.5)

	// Candidate jumps; consecutive same-sign jumps form one group (a shift
	// spread over two samples).
	type group struct{ first, last int } // diff indices: jump from point first to point last+1
	var groups []group
	for i := 0; i < n-1; i++ {
		if !(abs[i] > metricSeriesStepFactor*medAbs && abs[i] >= metricSeriesStepMinRangeShare*rng) {
			continue
		}
		if g := len(groups); g > 0 && groups[g-1].last == i-1 && (diffs[i] > 0) == (diffs[i-1] > 0) {
			groups[g-1].last = i
			continue
		}
		groups = append(groups, group{first: i, last: i})
	}

	var accepted []msStep
	sum := 0.0
	for gi, g := range groups {
		beforeFrom := max(g.first-metricSeriesStepWindow+1, 0)
		if gi > 0 {
			beforeFrom = max(beforeFrom, groups[gi-1].last+1)
		}
		afterTo := min(g.last+metricSeriesStepWindow, n-1)
		if gi+1 < len(groups) {
			afterTo = min(afterTo, groups[gi+1].first)
		}
		before := pts[beforeFrom : g.first+1]
		after := pts[g.last+1 : afterTo+1]
		// The level must hold for two samples on each side unless the side is
		// the edge of the series: a one-sample spike is not a level shift.
		if (len(before) < 2 && beforeFrom > 0) || (len(after) < 2 && afterTo < n-1) {
			continue
		}
		medBefore, medAfter := msMedianOf(before), msMedianOf(after)
		shift := medAfter - medBefore
		jump := pts[g.last+1].v - pts[g.first].v
		if (shift > 0) != (jump > 0) || math.Abs(shift) < 0.5*math.Abs(jump) || math.Abs(shift) < metricSeriesStepMinRangeShare*rng {
			continue
		}
		accepted = append(accepted, msStep{at: pts[g.last+1].at, from: medBefore, to: medAfter})
		sum += shift
	}
	sort.SliceStable(accepted, func(i, j int) bool {
		return math.Abs(accepted[i].to-accepted[i].from) > math.Abs(accepted[j].to-accepted[j].from)
	})
	if len(accepted) > metricSeriesMaxSteps {
		accepted = accepted[:metricSeriesMaxSteps]
	}
	sort.SliceStable(accepted, func(i, j int) bool { return accepted[i].at.Before(accepted[j].at) })
	return accepted, sum
}

func msMedianOf(pts []msPoint) float64 {
	vals := make([]float64, len(pts))
	for i, p := range pts {
		vals[i] = p.v
	}
	sort.Float64s(vals)
	return msQuantile(vals, 0.5)
}

// msTrend fits value = a + b*hours by least squares and judges steadiness
// (see metricSeriesSteadyR2).
func msTrend(pts []msPoint, spanH, median, rng, stepSum float64, stepCount int) *seriesTrend {
	n := float64(len(pts))
	start := pts[0].at
	var sx, sy float64
	for _, p := range pts {
		sx += p.at.Sub(start).Hours()
		sy += p.v
	}
	mx, my := sx/n, sy/n
	var sxx, sxy, syy float64
	for _, p := range pts {
		dx := p.at.Sub(start).Hours() - mx
		dy := p.v - my
		sxx += dx * dx
		sxy += dx * dy
		syy += dy * dy
	}
	if sxx == 0 {
		return nil
	}
	b := sxy / sxx
	a := my - b*mx
	sse := math.Max(syy-b*sxy, 0)
	r2 := 0.0
	if syy > 0 {
		r2 = math.Max(0, math.Min(1, 1-sse/syy))
	}
	resSD := math.Sqrt(sse / (n - 2))
	se := math.Sqrt(sse / (n - 2) / sxx)

	change := b * spanH
	level := math.Max(math.Abs(median), rng)
	material := rng > 0 && math.Abs(change) >= metricSeriesMaterialNoiseMul*resSD && math.Abs(change) >= metricSeriesMaterialLevelPct*level
	direction := "flat"
	if material {
		direction = "rising"
		if change < 0 {
			direction = "falling"
		}
	}
	stepDominated := stepCount > 0 && change != 0 && (stepSum > 0) == (change > 0) &&
		math.Abs(stepSum) >= metricSeriesStepShareOfTrend*math.Abs(change)
	t := &seriesTrend{
		RatePerHour: msRound(b), RatePerDay: msRound(b * 24), R2: math.Round(r2*1000) / 1000,
		Direction: direction, slope: b, intercept: a, slopeSE: se, stepDominated: stepDominated,
	}
	switch {
	case stepDominated:
		t.Note = fmt.Sprintf("most of the change came in %d level shift(s), not a steady trend", stepCount)
	case r2 < metricSeriesSteadyR2:
		t.Note = fmt.Sprintf("no steady trend (r2=%s)", msFmtR2(r2))
	case !material:
		t.Note = fmt.Sprintf("change over the window is within noise (r2=%s)", msFmtR2(r2))
	default:
		t.Steady = true
	}
	return t
}

func msThresholdStat(pts []msPoint, spanH float64, trend *seriesTrend, th msThreshold) seriesThresholdStat {
	past := func(v float64) bool {
		if th.Direction == "below" {
			return v < th.Value
		}
		return v > th.Value
	}
	out := seriesThresholdStat{Label: th.Label, Value: th.Value, Direction: th.Direction}
	count := 0
	for i, p := range pts {
		if past(p.v) {
			count++
			if i > 0 && !past(pts[i-1].v) {
				out.Crossings++
			}
		}
	}
	n := len(pts)
	out.PctTimePast = msRound(100 * float64(count) / float64(n))
	last := pts[n-1].v
	out.CurrentlyPast = past(last)

	switch {
	case out.CurrentlyPast:
		out.NotForecastReason = "already past"
	case n < metricSeriesMinForecastPoints || spanH < metricSeriesMinForecastSpanHours || trend == nil:
		out.NotForecastReason = fmt.Sprintf("insufficient history: %d points over %s h (need >= %d points over >= %s h)",
			n, msFmt(spanH), metricSeriesMinForecastPoints, msFmt(metricSeriesMinForecastSpanHours))
	case !trend.Steady:
		out.NotForecastReason = trend.Note
	case (th.Direction == "above" && trend.slope <= 0) || (th.Direction == "below" && trend.slope >= 0):
		out.NotForecastReason = "moving away from the threshold"
	default:
		out.TimeToReach = msReach(pts, spanH, trend, th, last)
	}
	return out
}

// msReach projects the fitted line to the threshold; the range uses the slope
// ± 1.96 standard errors. Treat it as a rough range, not a 95% interval:
// samples at a fixed step are autocorrelated, so the plain OLS standard error
// understates the uncertainty.
func msReach(pts []msPoint, spanH float64, trend *seriesTrend, th msThreshold, last float64) *seriesReach {
	end := pts[len(pts)-1].at
	b := trend.slope
	base := trend.intercept + b*spanH
	if (th.Direction == "above" && base >= th.Value) || (th.Direction == "below" && base <= th.Value) {
		base = last // the fit is past the threshold while the last point is not
	}
	dist := th.Value - base
	sign := 1.0
	if b < 0 {
		sign = -1
	}
	fast := b + sign*metricSeriesZ95*trend.slopeSE
	slow := b - sign*metricSeriesZ95*trend.slopeSE
	hours := dist / b
	low := dist / fast
	r := &seriesReach{
		HoursEstimate: msRound(hours),
		HoursLow:      msRound(low),
		Earliest:      end.Add(msHoursDuration(low)).Format(time.RFC3339),
	}
	longest := hours
	if (slow > 0) == (b > 0) && slow != 0 {
		high := dist / slow
		hh := msRound(high)
		r.HoursHigh = &hh
		r.Latest = end.Add(msHoursDuration(high)).Format(time.RFC3339)
		longest = high
	} else {
		r.Note = "upper bound open: the slope's range includes zero"
	}
	if longest > metricSeriesExtrapolationMul*spanH {
		extra := fmt.Sprintf("projection runs past %sx the observed span (%s h); treat it as indicative", msFmt(metricSeriesExtrapolationMul), msFmt(spanH))
		if r.Note != "" {
			r.Note += "; " + extra
		} else {
			r.Note = extra
		}
	}
	return r
}

func msHoursDuration(h float64) time.Duration {
	const maxHours = 24 * 365 * 100
	h = math.Max(0, math.Min(h, maxHours))
	return time.Duration(h * float64(time.Hour))
}

func msSummary(a seriesAnalysis, spanH float64) string {
	var b strings.Builder
	t := a.Trend
	switch {
	case t == nil:
		fmt.Fprintf(&b, "Too short to judge a trend (%d points over %s)", a.Points, msFmtSpan(spanH))
	case t.stepDominated:
		word := "up"
		if t.slope < 0 {
			word = "down"
		}
		parts := make([]string, 0, len(a.StepChanges))
		for _, st := range a.StepChanges {
			parts = append(parts, fmt.Sprintf("%s to %s at %s", msFmt(st.From), msFmt(st.To), st.At))
		}
		fmt.Fprintf(&b, "Stepped %s in %d level shift(s) (%s), not a steady trend", word, len(a.StepChanges), strings.Join(parts, "; "))
	case t.Steady:
		fmt.Fprintf(&b, "%s steadily", msTitle(t.Direction))
	case t.Direction == "flat":
		b.WriteString("Flat")
	default:
		fmt.Fprintf(&b, "%s but not steadily", msTitle(t.Direction))
	}
	if t != nil {
		fmt.Fprintf(&b, ": %s to %s over %s, trend %s (r2 %s)", msFmt(a.Baseline.Min), msFmt(a.Baseline.Max), msFmtSpan(spanH), msFmtRate(t, spanH), msFmtR2(t.R2))
	} else {
		fmt.Fprintf(&b, ": %s to %s", msFmt(a.Baseline.Min), msFmt(a.Baseline.Max))
	}
	if t != nil && !t.stepDominated && len(a.StepChanges) > 0 {
		fmt.Fprintf(&b, "; %d level shift(s)", len(a.StepChanges))
	}
	if a.GapCount > 0 {
		fmt.Fprintf(&b, "; %d gap(s) in the data", a.GapCount)
	}
	for _, th := range a.Thresholds {
		b.WriteString("; ")
		switch {
		case th.CurrentlyPast:
			fmt.Fprintf(&b, "past %s now", th.Label)
		case th.TimeToReach != nil:
			r := th.TimeToReach
			if r.HoursHigh != nil {
				fmt.Fprintf(&b, "reaches %s in ~%s (%s to %s)", th.Label, msFmtSpan(r.HoursEstimate), msFmtSpan(r.HoursLow), msFmtSpan(*r.HoursHigh))
			} else {
				fmt.Fprintf(&b, "reaches %s in ~%s (at the earliest %s; no upper bound)", th.Label, msFmtSpan(r.HoursEstimate), msFmtSpan(r.HoursLow))
			}
		default:
			fmt.Fprintf(&b, "no forecast to %s (%s)", th.Label, msShortReason(th.NotForecastReason, t))
		}
		if th.PctTimePast > 0 {
			fmt.Fprintf(&b, ", past it %s%% of samples, %d crossing(s)", msFmt(th.PctTimePast), th.Crossings)
		}
	}
	return b.String()
}

// msShortReason drops what the summary already said: the r2 and the level
// shifts.
func msShortReason(reason string, t *seriesTrend) string {
	if t != nil && t.stepDominated && reason == t.Note {
		return "level shifts, not a steady trend"
	}
	short, _, _ := strings.Cut(reason, " (r2=")
	return short
}

func msTitle(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func msFmtRate(t *seriesTrend, spanH float64) string {
	if spanH >= 48 {
		return msFmt(t.RatePerDay) + "/day"
	}
	return msFmt(t.RatePerHour) + "/h"
}

func msFmtR2(r2 float64) string {
	return strconv.FormatFloat(math.Round(r2*100)/100, 'f', 2, 64)
}

func msFmtSpan(h float64) string {
	switch {
	case h < 1:
		return msFmt(math.Round(h*60)) + " min"
	case h < 48:
		return msFmt(math.Round(h*10)/10) + " h"
	default:
		return msFmt(math.Round(h/24*10)/10) + " d"
	}
}

// msRound keeps 4 significant digits, so small values (0.0033) keep precision.
func msRound(x float64) float64 {
	if x == 0 || math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	mag := math.Pow(10, 3-math.Floor(math.Log10(math.Abs(x))))
	r := math.Round(x*mag) / mag
	if math.IsInf(r, 0) || math.IsNaN(r) {
		return x
	}
	return r
}

// msFmt renders a number for the summary with 4 significant digits.
func msFmt(x float64) string {
	r := msRound(x)
	if a := math.Abs(r); a != 0 && (a >= 1e7 || a < 1e-4) {
		return strconv.FormatFloat(r, 'g', 4, 64)
	}
	return strconv.FormatFloat(r, 'f', -1, 64)
}

package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Metric charts (#40909). A metrics tool builds the finished nb-chart spec from the
// exact series it holds and returns it in its response metadata (Charts) under an
// opaque chart_ref, which is saved with the tool call row; the model is only told the
// ref. The model copies the ref into its answer, and ResolveMetricCharts swaps it for
// the saved spec before the answer is saved, so no chart number is ever typed by a
// model.

const (
	// metricChartMaxSeries matches AgentChart.jsx MAX_SERIES.
	metricChartMaxSeries = 6
	// metricChartMaxPoints matches AgentChart.jsx's line/area label limit.
	metricChartMaxPoints   = 120
	metricChartMaxKeyRunes = 60
	// metricChartMinAxisShare is how much of the top series' time axis the chart
	// keeps when another series joins it.
	metricChartMinAxisShare = 0.9
)

// MetricChartSeries is one time series as a metrics backend returned it.
// Timestamps are unix seconds or milliseconds.
type MetricChartSeries struct {
	Labels     map[string]string
	Timestamps []int64
	Values     []float64
}

// MetricChart is the nb-chart spec rendered by AgentChart.jsx and nb_chart.py.
type MetricChart struct {
	Type   string            `json:"type"`
	Title  string            `json:"title"`
	Labels []string          `json:"labels"`
	Data   []metricChartData `json:"series"`
	Format string            `json:"format"`
	// Source marks a chart built from queried data, so only these are kept out
	// of model inputs; renderers ignore it.
	Source string `json:"source"`
	// RestLatest sums the latest values of the series not charted.
	RestLatest float64 `json:"rest_latest,omitempty"`
}

const metricChartSource = "metrics"

// MetricChartFormats are the value units a metric chart can carry; AgentChart.jsx
// FORMATTERS and nb_chart.py render each one.
var MetricChartFormats = map[string]bool{
	"number": true, "percent": true, "bytes": true, "bytes_per_second": true, "seconds": true,
}

type metricChartData struct {
	Key  string    `json:"key"`
	Data []float64 `json:"data"`
}

// BuildMetricChart returns a line chart of the top series by |max|, on the
// timestamps they share, thinned evenly to metricChartMaxPoints. Values are the
// backend's own, never interpolated. ok is false when there is nothing to plot.
func BuildMetricChart(title string, series []MetricChartSeries) (MetricChart, bool) {
	var usable []MetricChartSeries
	for _, s := range series {
		n := min(len(s.Timestamps), len(s.Values))
		if n == 0 {
			continue
		}
		usable = append(usable, MetricChartSeries{Labels: s.Labels, Timestamps: s.Timestamps[:n], Values: s.Values[:n]})
	}
	if len(usable) == 0 {
		return MetricChart{}, false
	}
	sort.SliceStable(usable, func(i, j int) bool { return seriesMaxAbs(usable[i]) > seriesMaxAbs(usable[j]) })

	// The most significant series sets the time axis. The next ones join, in rank
	// order, only while the chart keeps nearly all of that axis: a series missing
	// part of the window is skipped rather than cutting every series to the
	// overlap, and no point is ever invented.
	chosen := []MetricChartSeries{usable[0]}
	shared := sharedTimestamps(chosen)
	minShared := int(math.Ceil(float64(len(shared)) * metricChartMinAxisShare))
	// rest is the latest value of every series left out, so a share-of-total
	// view can show them as "Other" instead of overstating the charted ones.
	rest := 0.0
	for _, s := range usable[1:] {
		if len(chosen) < metricChartMaxSeries {
			if next := sharedTimestamps(append(slices.Clone(chosen), s)); len(next) >= minShared {
				chosen, shared = append(chosen, s), next
				continue
			}
		}
		rest += sanitizeChartValue(s.Values[len(s.Values)-1])
	}
	usable = chosen
	shared = thin(shared, metricChartMaxPoints)

	keys := seriesKeys(usable)
	chart := MetricChart{Type: "line", Title: strings.TrimSpace(title + " (UTC)"), Format: "number", Source: metricChartSource, RestLatest: rest}
	chart.Labels = timeLabels(shared)
	for i, s := range usable {
		byTs := make(map[int64]float64, len(s.Timestamps))
		for k, ts := range s.Timestamps {
			byTs[ts] = sanitizeChartValue(s.Values[k])
		}
		data := make([]float64, len(shared))
		for k, ts := range shared {
			data[k] = byTs[ts]
		}
		chart.Data = append(chart.Data, metricChartData{Key: keys[i], Data: data})
	}
	return chart, true
}

// AddMetricChart adds chart to charts, the set a tool call returns in its
// metadata, and returns its chart_ref.
func AddMetricChart(charts map[string]json.RawMessage, chart MetricChart) (string, error) {
	b, err := json.Marshal(chart)
	if err != nil {
		return "", fmt.Errorf("metric chart: marshal: %w", err)
	}
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("metric chart: ref: %w", err)
	}
	ref := "chart_" + hex.EncodeToString(buf)
	charts[ref] = b
	return ref, nil
}

// MetricChartMetadata is the response metadata carrying charts, or nil when there are none.
func MetricChartMetadata(charts map[string]json.RawMessage) *NBToolResponseMetadata {
	if len(charts) == 0 {
		return nil
	}
	return &NBToolResponseMetadata{Charts: charts}
}

// MetricChartNote tells the model how to show a saved chart.
func MetricChartNote(ref, title string) string {
	if title != "" {
		title = " (" + title + ")"
	}
	return fmt.Sprintf("Chart%s: chart_ref %q. Only if the user asked for a chart or graph, put "+
		"```nb-chart\n{\"chart_ref\":%q}\n``` in your answer (optional: \"top\" N series; \"type\" area, or bar/pie of latest values; \"title\"; \"format\" bytes/bytes_per_second/seconds/percent/number when the unit is not shown); never type chart numbers.",
		title, ref, ref)
}

// The quotes may arrive JSON-escaped: tool results are often a JSON document.
var chartRefRe = regexp.MustCompile(`chart_ref \\?"(chart_[0-9a-f]+)`)

// MetricChartRefs returns the chart_refs a tool result carries, in order.
func MetricChartRefs(text string) []string {
	var refs []string
	for _, m := range chartRefRe.FindAllStringSubmatch(text, -1) {
		if !slices.Contains(refs, m[1]) {
			refs = append(refs, m[1])
		}
	}
	return refs
}

var nbChartBlockRe = regexp.MustCompile("(?s)```nb-chart[ \\t]*\\r?\\n?(.*?)```")

const metricChartMissingNote = "_Chart not shown: its data could not be found. Ask again to redraw it._"

// ResolveMetricCharts replaces every nb-chart block that carries a chart_ref with the
// saved spec lookup returns. A ref that cannot be found becomes a one-line note, never
// an empty chart. Blocks without a chart_ref (FinOps) are left as they are.
func ResolveMetricCharts(text string, lookup func(ref string) ([]byte, bool)) string {
	if !strings.Contains(text, "chart_ref") {
		return text
	}
	return nbChartBlockRe.ReplaceAllStringFunc(text, func(block string) string {
		m := nbChartBlockRe.FindStringSubmatch(block)
		var ref struct {
			ChartRef string `json:"chart_ref"`
			Top      int    `json:"top"`
			Type     string `json:"type"`
			Title    string `json:"title"`
			Format   string `json:"format"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(m[1])), &ref) != nil || ref.ChartRef == "" {
			return block
		}
		stored, ok := lookup(ref.ChartRef)
		var chart MetricChart
		if !ok || json.Unmarshal(stored, &chart) != nil || len(chart.Labels) == 0 {
			return metricChartMissingNote
		}
		if ref.Top > 0 && ref.Top < len(chart.Data) {
			chart.Data = chart.Data[:ref.Top]
		}
		if MetricChartFormats[ref.Format] {
			chart.Format = ref.Format
		}
		if ref.Title = strings.TrimSpace(ref.Title); ref.Title != "" {
			chart.Title = ref.Title
			if !strings.Contains(ref.Title, "UTC") {
				chart.Title += " (UTC)"
			}
		}
		var spec any = chart
		switch ref.Type {
		case "area":
			chart.Type = "area"
			spec = chart
		case "bar", "pie", "doughnut":
			spec = latestValueChart(chart, ref.Type)
		}
		b, err := json.Marshal(spec)
		if err != nil {
			return metricChartMissingNote
		}
		return "```nb-chart\n" + string(b) + "\n```"
	})
}

// latestValueChart compares the series by their latest value: a bar per series, or
// a pie/doughnut of their shares.
func latestValueChart(c MetricChart, typ string) map[string]any {
	keys := make([]string, len(c.Data))
	latest := make([]float64, len(c.Data))
	for i, s := range c.Data {
		keys[i] = s.Key
		latest[i] = s.Data[len(s.Data)-1]
	}
	title := strings.TrimSuffix(c.Title, " (UTC)") + " at " + c.Labels[len(c.Labels)-1] + " UTC"
	spec := map[string]any{"type": typ, "title": title, "labels": keys, "format": c.Format, "source": c.Source}
	if typ == "bar" {
		spec["series"] = []metricChartData{{Key: "latest", Data: latest}}
		return spec
	}
	if c.RestLatest != 0 {
		spec["labels"], latest = append(keys, "Other"), append(latest, c.RestLatest)
	}
	spec["values"] = latest
	return spec
}

// StripChartData replaces each metric chart with a one-line placeholder, for text
// that is replayed to a model: the chart is for the reader, and its points would
// cost every later turn tokens. Hand-written charts (FinOps) are left in place.
func StripChartData(text string) string {
	if !strings.Contains(text, "```nb-chart") {
		return text
	}
	return nbChartBlockRe.ReplaceAllStringFunc(text, func(block string) string {
		m := nbChartBlockRe.FindStringSubmatch(block)
		var c struct {
			Title  string `json:"title"`
			Source string `json:"source"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(m[1])), &c) != nil || c.Source != metricChartSource {
			return block
		}
		if c.Title == "" {
			return "[chart]"
		}
		return "[chart: " + c.Title + "]"
	})
}

// RestoreCharts puts original's nb-chart blocks back into rewritten, a model's
// rewrite of StripChartData(original): each at its placeholder, or appended when
// the rewrite dropped the placeholder.
func RestoreCharts(rewritten, original string) string {
	for _, block := range nbChartBlockRe.FindAllString(original, -1) {
		placeholder := StripChartData(block)
		if placeholder == block {
			continue // not stripped, so the rewrite saw it as it is
		}
		if strings.Contains(rewritten, placeholder) {
			rewritten = strings.Replace(rewritten, placeholder, block, 1)
		} else {
			rewritten = strings.TrimRight(rewritten, "\n") + "\n\n" + block
		}
	}
	return rewritten
}

// thinIndices picks limit evenly spaced indices of n, always keeping the first and last.
func thinIndices(n, limit int) []int {
	if n <= limit {
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	idx := make([]int, limit)
	for i := range idx {
		idx[i] = int(math.Round(float64(i) * float64(n-1) / float64(limit-1)))
	}
	return idx
}

func thin(ts []int64, limit int) []int64 {
	idx := thinIndices(len(ts), limit)
	out := make([]int64, len(idx))
	for i, k := range idx {
		out[i] = ts[k]
	}
	return out
}

func seriesMaxAbs(s MetricChartSeries) float64 {
	m := math.Inf(-1)
	for _, v := range s.Values {
		if a := math.Abs(v); !math.IsNaN(a) && !math.IsInf(a, 0) && a > m {
			m = a
		}
	}
	return m
}

func sharedTimestamps(series []MetricChartSeries) []int64 {
	count := map[int64]int{}
	for _, s := range series {
		seen := map[int64]bool{}
		for _, ts := range s.Timestamps {
			if !seen[ts] {
				seen[ts] = true
				count[ts]++
			}
		}
	}
	var shared []int64
	for ts, c := range count {
		if c == len(series) {
			shared = append(shared, ts)
		}
	}
	slices.Sort(shared)
	return shared
}

// toUnixSeconds treats values past year 5138 in seconds as milliseconds.
func toUnixSeconds(ts int64) int64 {
	if ts > 1e11 || ts < -1e11 {
		return ts / 1000
	}
	return ts
}

func timeLabels(ts []int64) []string {
	if len(ts) == 0 {
		return nil
	}
	layout := "15:04"
	if span := toUnixSeconds(ts[len(ts)-1]) - toUnixSeconds(ts[0]); span > 24*3600 {
		layout = "Jan 02 15:04"
	} else if span < 5*60 {
		layout = "15:04:05"
	}
	labels := make([]string, len(ts))
	for i, t := range ts {
		labels[i] = time.Unix(toUnixSeconds(t), 0).UTC().Format(layout)
	}
	return labels
}

// seriesKeys names each series by the label values that differ between them.
func seriesKeys(series []MetricChartSeries) []string {
	var differing []string
	seen := map[string]bool{}
	for _, s := range series {
		for k := range s.Labels {
			if seen[k] {
				continue
			}
			seen[k] = true
			for _, o := range series {
				if o.Labels[k] != s.Labels[k] {
					differing = append(differing, k)
					break
				}
			}
		}
	}
	slices.Sort(differing)
	keys := make([]string, len(series))
	used := map[string]int{}
	for i, s := range series {
		var parts []string
		for _, k := range differing {
			if v := s.Labels[k]; v != "" {
				parts = append(parts, v)
			}
		}
		key := strings.Join(parts, "/")
		if key == "" {
			key = s.Labels["__name__"]
		}
		if key == "" {
			key = "value"
		}
		if r := []rune(key); len(r) > metricChartMaxKeyRunes {
			key = string(r[:metricChartMaxKeyRunes-1]) + "…"
		}
		used[key]++
		if used[key] > 1 {
			key = fmt.Sprintf("%s #%d", key, used[key])
		}
		keys[i] = key
	}
	return keys
}

func sanitizeChartValue(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

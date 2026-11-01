package observability

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// Reducing a requested aggregate locally, because OpenObserve does not evaluate one.
//
// `promApplyAggregate` collapses every built query to one point by wrapping it as
// `<agg>_over_time((inner)[window:step])` (see promQLOverTime), and the layer then asks for
// it as an instant query. Prometheus evaluates that wrapper over the window. OpenObserve
// does not, whenever the inner expression collapses to a single series: it returns the value
// at the evaluation instant and discards the wrapper entirely. Measured on dev against
// `sum(max by (namespace,pod,container) (kube_pod_container_resource_limits{…}))` over one
// day, where the workload demonstrably ranged 1..2 cores:
//
//	inner sum() at now              = 1
//	max_over_time(inner[1d:75s])    = 1
//	min_over_time(inner[1d:75s])    = 1
//	avg_over_time(inner[1d:75s])    = 1
//
// Three different statistics, one answer, and it is the window's low value rather than any
// of them. That is the "aggregate accepted then ignored" failure: the request succeeds, one
// point comes back, and it is not the figure that was asked for. A peak equal to the
// average under-states spikes, which is the direction that under-provisions.
//
// So the wrapper is unwrapped here and the statistic computed in Go from a range fetch of
// the inner expression. This is the same remedy Datadog needed (#39138): stop pushing the
// aggregate down to a backend that mishandles it, and reduce from a bounded fetch instead.
//
// It deliberately does NOT change the query the builders produce. The wrapper is how every
// other provider is served, and rewriting the shared builders for one backend's engine gap
// would make the other providers' behaviour depend on this one.

// openObserveLocalAggregate describes an `*_over_time` wrapper this source must evaluate
// itself: the expression to fetch, and the statistic to reduce it with.
type openObserveLocalAggregate struct {
	Inner    string
	Op       string  // max | min | avg | sum | count | stddev | stdvar | quantile
	Quantile float64 // only meaningful when Op == "quantile"
	StepSecs int     // the wrapper's own sampling step, 0 when unparseable
}

// openObserveOverTimeOps are the wrappers promQLOverTime can emit. `present_over_time` and
// `last_over_time` are not in that set, so they are not accepted here -- an expression this
// function does not recognise is left for OpenObserve to evaluate, which is the safe
// direction: a user's own query keeps whatever semantics the backend gives it.
var openObserveOverTimeOps = map[string]string{
	"max_over_time":    "max",
	"min_over_time":    "min",
	"avg_over_time":    "avg",
	"sum_over_time":    "sum",
	"count_over_time":  "count",
	"stddev_over_time": "stddev",
	"stdvar_over_time": "stdvar",
}

// parseOpenObserveOverTime recognises exactly the shape promQLOverTime produces:
//
//	<op>_over_time((inner)[window:step])
//	quantile_over_time(<phi>, (inner)[window:step])
//
// It reports ok=false for anything else, including a nested wrapper or a wrapper that is
// only part of a larger expression -- those are left alone rather than half-rewritten.
func parseOpenObserveOverTime(expr string) (openObserveLocalAggregate, bool) {
	var out openObserveLocalAggregate
	trimmed := strings.TrimSpace(expr)

	open := strings.Index(trimmed, "(")
	if open == -1 || !strings.HasSuffix(trimmed, ")") {
		return out, false
	}
	fname := strings.TrimSpace(trimmed[:open])
	body := trimmed[open+1 : len(trimmed)-1]

	// The call must span the WHOLE expression. `max_over_time(a[1d:75s]) + 1` shares the
	// prefix but is not a wrapper we can unwrap, and reducing its first operand alone
	// would silently answer a different question.
	if !spansWholeCall(trimmed, open) {
		return out, false
	}

	if fname == "quantile_over_time" {
		comma := splitTopLevelComma(body)
		if comma == -1 {
			return out, false
		}
		q, err := strconv.ParseFloat(strings.TrimSpace(body[:comma]), 64)
		if err != nil {
			return out, false
		}
		out.Op, out.Quantile = "quantile", q
		body = strings.TrimSpace(body[comma+1:])
	} else {
		op, known := openObserveOverTimeOps[fname]
		if !known {
			return out, false
		}
		out.Op = op
	}

	inner, stepSecs, ok := stripSubquery(body)
	if !ok {
		return out, false
	}
	out.Inner, out.StepSecs = inner, stepSecs
	return out, true
}

// spansWholeCall reports whether the parenthesis at `open` closes on the expression's final
// character, i.e. the whole string is one call rather than a call inside something larger.
func spansWholeCall(expr string, open int) bool {
	depth := 0
	runes := []rune(expr)
	for i := open; i < len(runes); i++ {
		switch runes[i] {
		case '"', '\'', '`':
			i = skipQuoted(runes, i) - 1
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i == len(runes)-1
			}
		}
	}
	return false
}

// stripSubquery turns `(inner)[window:step]` into `inner` plus the step in seconds. The
// brackets must close the expression, and the range must carry a `:` -- a plain range
// selector like `x[5m]` is part of the inner expression, not the subquery this unwraps.
func stripSubquery(body string) (inner string, stepSecs int, ok bool) {
	body = strings.TrimSpace(body)
	if !strings.HasSuffix(body, "]") {
		return "", 0, false
	}
	open := strings.LastIndex(body, "[")
	if open == -1 {
		return "", 0, false
	}
	rng := body[open+1 : len(body)-1]
	colon := strings.Index(rng, ":")
	if colon == -1 {
		return "", 0, false
	}
	stepSecs = parsePromDurationSeconds(strings.TrimSpace(rng[colon+1:]))

	inner = strings.TrimSpace(body[:open])
	// promQLOverTime always parenthesises the inner expression. Unwrap that one layer only
	// when it is genuinely the outermost pair, so `(a) + (b)` is not mangled into `a) + (b`.
	if strings.HasPrefix(inner, "(") && strings.HasSuffix(inner, ")") && spansWholeCall(inner, 0) {
		inner = strings.TrimSpace(inner[1 : len(inner)-1])
	}
	if inner == "" {
		return "", 0, false
	}
	return inner, stepSecs, true
}

// parsePromDurationSeconds handles the units promAggWindow emits. It returns 0 for anything
// else, which the caller reads as "fall back to the request's own step".
func parsePromDurationSeconds(d string) int {
	if d == "" {
		return 0
	}
	var mult int
	switch d[len(d)-1] {
	case 's':
		mult = 1
	case 'm':
		mult = 60
	case 'h':
		mult = 3600
	case 'd':
		mult = 86400
	default:
		return 0
	}
	n, err := strconv.Atoi(d[:len(d)-1])
	if err != nil || n <= 0 {
		return 0
	}
	return n * mult
}

// splitTopLevelComma returns the index of the first comma not nested inside brackets or a
// quoted literal, or -1.
func splitTopLevelComma(s string) int {
	runes := []rune(s)
	depth := 0
	for i := 0; i < len(runes); i++ {
		switch {
		case runes[i] == '"' || runes[i] == '\'' || runes[i] == '`':
			i = skipQuoted(runes, i) - 1
		case runes[i] == '(' || runes[i] == '[' || runes[i] == '{':
			depth++
		case runes[i] == ')' || runes[i] == ']' || runes[i] == '}':
			depth--
		case runes[i] == ',' && depth == 0:
			return i
		}
	}
	return -1
}

// reduceOpenObserveSeries collapses each series to exactly one point, which is the contract
// an aggregate promises: one point per SERIES, not one per workload. A series with no
// numeric samples is dropped rather than reported as zero -- "no data" and "zero" are
// different answers, and the layer's callers cannot tell them apart once a zero is emitted.
//
// The timestamp kept is the series' last, so a consumer plotting the collapsed point puts it
// at the end of the window it describes.
func reduceOpenObserveSeries(in []Result, agg openObserveLocalAggregate) []Result {
	out := make([]Result, 0, len(in))
	for _, s := range in {
		// Both are guarded, not just Values. The decode above fills them in lockstep, but
		// Result is a shared type with other producers, and indexing Timestamps on the
		// strength of a Values check panics with index out of range [-1] on a series that
		// carries samples and no timestamps. A malformed series is dropped rather than
		// emitted at epoch 0, which would plot it in 1970.
		if len(s.Values) == 0 || len(s.Timestamps) == 0 {
			continue
		}
		v, ok := reduceValues(s.Values, agg)
		if !ok {
			continue
		}
		ts := s.Timestamps[len(s.Timestamps)-1]
		out = append(out, Result{
			Metric:     s.Metric,
			Timestamps: []int64{ts},
			Values:     []float64{v},
		})
	}
	return out
}

// reduceValues computes one statistic over a series' samples, matching what the
// corresponding PromQL `*_over_time` would have returned.
func reduceValues(vals []float64, agg openObserveLocalAggregate) (float64, bool) {
	clean := make([]float64, 0, len(vals))
	for _, v := range vals {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			clean = append(clean, v)
		}
	}
	// count is the one statistic that is not in the metric's unit -- it is how many
	// readings there were -- so it is answered even when every reading was dropped above.
	if agg.Op == "count" {
		return float64(len(clean)), true
	}
	if len(clean) == 0 {
		return 0, false
	}
	switch agg.Op {
	case "max":
		m := clean[0]
		for _, v := range clean[1:] {
			if v > m {
				m = v
			}
		}
		return m, true
	case "min":
		m := clean[0]
		for _, v := range clean[1:] {
			if v < m {
				m = v
			}
		}
		return m, true
	case "sum":
		return sumOf(clean), true
	case "avg":
		return sumOf(clean) / float64(len(clean)), true
	case "stddev":
		return math.Sqrt(varianceOf(clean)), true
	case "stdvar":
		return varianceOf(clean), true
	case "quantile":
		return quantileOf(clean, agg.Quantile), true
	}
	return 0, false
}

func sumOf(v []float64) float64 {
	var t float64
	for _, x := range v {
		t += x
	}
	return t
}

func varianceOf(v []float64) float64 {
	mean := sumOf(v) / float64(len(v))
	var acc float64
	for _, x := range v {
		acc += (x - mean) * (x - mean)
	}
	return acc / float64(len(v))
}

// quantileOf uses the same linear interpolation between adjacent ranks as PromQL's
// quantile_over_time, so a p90 here matches a p90 asked of Prometheus for the same samples.
// Out-of-range phi follows PromQL too: below 0 is -Inf, above 1 is +Inf.
func quantileOf(v []float64, phi float64) float64 {
	if math.IsNaN(phi) {
		return math.NaN()
	}
	if phi < 0 {
		return math.Inf(-1)
	}
	if phi > 1 {
		return math.Inf(+1)
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if len(s) == 1 {
		return s[0]
	}
	rank := phi * float64(len(s)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return s[lo]
	}
	return s[lo] + (s[hi]-s[lo])*(rank-float64(lo))
}

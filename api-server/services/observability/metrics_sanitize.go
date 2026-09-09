package observability

import (
	"encoding/json"
	"math"
)

// Keys used in Result.NonFinite. They name what the sample actually was, which is
// the whole point of the field — "no value here" is not useful on its own, "this
// one was +Inf" tells the reader their query divided by zero.
const (
	nonFiniteNaN         = "nan"
	nonFinitePosInf      = "+inf"
	nonFiniteNegInf      = "-inf"
	nonFiniteUnparseable = "unparseable"
)

// MarshalJSON sends non-finite samples as JSON `null` and reports what each one
// was in `non_finite`.
//
// Without it, a single NaN or ±Inf destroys the whole response. `Values` is
// []float64 and the response leaves the api-server through gin's c.JSON, whose
// render.WriteJSON writes the `application/json` Content-Type BEFORE json.Marshal
// runs. Marshal rejects non-finite floats, and gin's Context.Render can then only
// record the error and abort — the 200 and the header are already on the wire. The
// client gets 200 + application/json + an EMPTY body, which the app gateway
// reported as "Upstream response parse failed for metrics_list".
//
// Null rather than dropping the sample, for two reasons. It keeps Values and
// Timestamps the same length, which several consumers rely on — `panelSeries.ts`
// zips them per series, and `api1/kubernetes/index.ts` indexes *every* series
// against one shared timestamp axis, so a series that quietly lost three samples
// would render shifted against its neighbours rather than merely incomplete. And a
// null is a gap at the correct point in time, which is what a chart should show; an
// absent sample is invisible, and a coerced 0 is a lie indistinguishable from a
// measured zero.
//
// This lives on Result rather than at the API entry points so no future caller can
// marshal one of these and reintroduce the blank response. Every provider fills the
// same struct, so every provider is covered by construction.
func (r Result) MarshalJSON() ([]byte, error) {
	// Mirror of Result with nullable values. The distinct type also breaks the
	// recursion back into this method.
	type wireResult struct {
		Metric     map[string]string `json:"metric"`
		Timestamps []int64           `json:"timestamps"`
		Values     []*float64        `json:"values"`
		NonFinite  map[string]int    `json:"non_finite,omitempty"`
	}

	out := wireResult{Metric: r.Metric, Timestamps: r.Timestamps, NonFinite: r.NonFinite}

	var found map[string]int
	if r.Values != nil {
		// One backing array of exactly the right size, pointed into per element, so a
		// series costs two allocations rather than one per sample.
		backing := make([]float64, len(r.Values))
		out.Values = make([]*float64, len(r.Values))
		for i, v := range r.Values {
			kind := nonFiniteKind(v)
			if kind == "" {
				backing[i] = v
				out.Values[i] = &backing[i]
				continue
			}
			// Left nil, which marshals as null; the count below says what it was.
			if found == nil {
				found = make(map[string]int, 3)
			}
			found[kind]++
		}
	}

	// Merge rather than overwrite: a parser may already have recorded samples it
	// could not read at all (nonFiniteUnparseable), which are not visible here
	// because they arrive as NaN. Build a new map so marshalling never mutates the
	// Result it was handed.
	if len(found) > 0 {
		merged := make(map[string]int, len(r.NonFinite)+len(found))
		for k, v := range r.NonFinite {
			merged[k] = v
		}
		for k, v := range found {
			merged[k] += v
		}
		// Unreadable samples were stored as NaN placeholders, so they have just been
		// counted a second time under "nan". The parser's label is the more specific
		// fact about them, so discount it here — otherwise the counts overlap and a
		// caller reporting them adds up to more nulls than the series contains.
		if placeholders := r.NonFinite[nonFiniteUnparseable]; placeholders > 0 {
			if merged[nonFiniteNaN] -= placeholders; merged[nonFiniteNaN] <= 0 {
				delete(merged, nonFiniteNaN)
			}
		}
		out.NonFinite = merged
	}

	return json.Marshal(out)
}

// nonFiniteKind names v when JSON cannot carry it, and returns "" when it can.
func nonFiniteKind(v float64) string {
	switch {
	case math.IsNaN(v):
		return nonFiniteNaN
	case math.IsInf(v, 1):
		return nonFinitePosInf
	case math.IsInf(v, -1):
		return nonFiniteNegInf
	default:
		return ""
	}
}

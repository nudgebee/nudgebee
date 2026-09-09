package observability

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// The failure this guards against, end to end: a `+Inf` sample used to blank the
// entire metrics response. gin's WriteJSON writes the Content-Type before
// json.Marshal runs, so the marshal error arrives too late — the client got
// 200 + application/json + an empty body and reported a parse failure.
func TestNonFiniteSampleNoLongerBlanksTheResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Exactly what VictoriaMetrics returns for the nubi_nonvalue_char probe.
	negInf, negUnreadable := toFloat64Slice([]any{"-Inf", "-Inf"})
	normal, normalUnreadable := toFloat64Slice([]any{"42", "43"})
	posInf, posUnreadable := toFloat64Slice([]any{"+Inf", "+Inf"})
	if negUnreadable != 0 || normalUnreadable != 0 || posUnreadable != 0 {
		t.Fatalf("provider-reported infinities are values, not parse failures; got %d/%d/%d", negUnreadable, normalUnreadable, posUnreadable)
	}

	resp := OutputMetricQuery{Results: []QueryResult{{
		QueryKey: "q0",
		Query:    "nubi_nonvalue_char",
		Payload: []Result{
			{Metric: map[string]string{"kind": "neg_inf"}, Timestamps: []int64{1, 2}, Values: negInf},
			{Metric: map[string]string{"kind": "normal"}, Timestamps: []int64{1, 2}, Values: normal},
			{Metric: map[string]string{"kind": "pos_inf"}, Timestamps: []int64{1, 2}, Values: posInf},
		},
	}}}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.JSON(200, resp)

	if len(c.Errors) != 0 {
		t.Fatalf("gin recorded a render error: %v", c.Errors.String())
	}
	if w.Body.Len() == 0 {
		t.Fatal("empty body: the response was blanked, which is the reported bug")
	}

	var decoded struct {
		Results []struct {
			Payload []struct {
				Metric     map[string]string `json:"metric"`
				Timestamps []int64           `json:"timestamps"`
				Values     []*float64        `json:"values"`
				NonFinite  map[string]int    `json:"non_finite"`
			} `json:"payload"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("client-side parse failed: %v (body=%q)", err, w.Body.String())
	}
	rows := decoded.Results[0].Payload

	// Every series keeps both samples — the gap sits at the right point in time
	// rather than the series being shortened or emptied.
	for _, row := range rows {
		if len(row.Values) != 2 || len(row.Timestamps) != 2 {
			t.Fatalf("series %q lost its shape: %d values, %d timestamps", row.Metric["kind"], len(row.Values), len(row.Timestamps))
		}
	}

	if v := rows[1].Values[0]; v == nil || *v != 42 {
		t.Fatalf("the finite series was damaged: %v", rows[1].Values)
	}
	if rows[1].NonFinite != nil {
		t.Errorf("an entirely finite series should carry no non_finite: %v", rows[1].NonFinite)
	}

	// And the reader can tell WHAT the gaps were, not merely that they exist.
	for _, tc := range []struct {
		row  int
		kind string
	}{{0, nonFiniteNegInf}, {2, nonFinitePosInf}} {
		row := rows[tc.row]
		if row.Values[0] != nil || row.Values[1] != nil {
			t.Errorf("%s series should be null-valued, got %v", tc.kind, row.Values)
		}
		if row.NonFinite[tc.kind] != 2 {
			t.Errorf("series %q should report 2 %s samples, got %v", row.Metric["kind"], tc.kind, row.NonFinite)
		}
	}
}

// Every non-finite kind is named separately: "no value" is not useful on its own,
// "this was +Inf" tells the reader their query divided by zero.
func TestMarshalNamesEachNonFiniteKind(t *testing.T) {
	r := Result{
		Timestamps: []int64{1, 2, 3, 4},
		Values:     []float64{math.NaN(), math.Inf(1), math.Inf(-1), 7},
	}
	var got struct {
		Values    []*float64     `json:"values"`
		NonFinite map[string]int `json:"non_finite"`
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	if len(got.Values) != 4 {
		t.Fatalf("values must stay index-aligned with timestamps; got %d", len(got.Values))
	}
	if got.Values[0] != nil || got.Values[1] != nil || got.Values[2] != nil {
		t.Errorf("non-finite samples should be null: %v", got.Values)
	}
	if got.Values[3] == nil || *got.Values[3] != 7 {
		t.Errorf("finite sample was lost: %v", got.Values)
	}
	want := map[string]int{nonFiniteNaN: 1, nonFinitePosInf: 1, nonFiniteNegInf: 1}
	for k, v := range want {
		if got.NonFinite[k] != v {
			t.Errorf("non_finite[%q] = %d, want %d (full: %v)", k, got.NonFinite[k], v, got.NonFinite)
		}
	}
}

// A value the parser cannot read at all is reported under its own name, so
// "the provider sent us garbage" is distinguishable from "the query returned NaN".
func TestUnreadableSamplesAreCountedSeparatelyAndStayAligned(t *testing.T) {
	values, unreadable := toFloat64Slice([]any{"42", "not-a-number", "NaN", true})
	if unreadable != 2 {
		t.Fatalf("unreadable = %d, want 2 (the non-numeric string and the bool)", unreadable)
	}
	if len(values) != 4 {
		t.Fatalf("every input needs an output entry to stay aligned with timestamps; got %d", len(values))
	}
	if !math.IsNaN(values[1]) || !math.IsNaN(values[3]) {
		t.Errorf("unreadable samples should become NaN placeholders: %v", values)
	}

	r := Result{Timestamps: []int64{1, 2, 3, 4}, Values: values, NonFinite: unreadableSamples(unreadable)}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var got struct {
		Values    []*float64     `json:"values"`
		NonFinite map[string]int `json:"non_finite"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	// The parser's count survives alongside the marshaller's own findings, and the
	// two do not overlap: the series holds three nulls, reported as one genuine NaN
	// plus two samples that were never numbers. Counting the placeholders under both
	// labels would imply five.
	if got.NonFinite[nonFiniteUnparseable] != 2 {
		t.Errorf("parser-reported count was lost: %v", got.NonFinite)
	}
	if got.NonFinite[nonFiniteNaN] != 1 {
		t.Errorf("non_finite[nan] = %d, want 1 — the two placeholders belong to unparseable: %v", got.NonFinite[nonFiniteNaN], got.NonFinite)
	}
	total := 0
	for _, v := range got.NonFinite {
		total += v
	}
	if nulls := 3; total != nulls {
		t.Errorf("counts sum to %d but the series has %d nulls: %v", total, nulls, got.NonFinite)
	}
	if got.Values[0] == nil || *got.Values[0] != 42 {
		t.Errorf("the readable sample was lost: %v", got.Values)
	}
}

func TestMarshalDoesNotMutateTheResult(t *testing.T) {
	seed := unreadableSamples(1)
	r := Result{Timestamps: []int64{1, 2}, Values: []float64{math.NaN(), 1}, NonFinite: seed}
	if _, err := json.Marshal(r); err != nil {
		t.Fatal(err)
	}
	if len(seed) != 1 || seed[nonFiniteUnparseable] != 1 {
		t.Errorf("marshalling mutated the caller's map: %v", seed)
	}
	if !math.IsNaN(r.Values[0]) || r.Values[1] != 1 {
		t.Errorf("marshalling mutated the values: %v", r.Values)
	}
}

func TestFiniteSeriesMarshalsUnchanged(t *testing.T) {
	r := Result{Metric: map[string]string{"pod": "a"}, Timestamps: []int64{1, 2}, Values: []float64{0, -1.5}}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"metric":{"pod":"a"},"timestamps":[1,2],"values":[0,-1.5]}`; string(raw) != want {
		t.Errorf("finite series changed shape on the wire:\n got %s\nwant %s", raw, want)
	}
}

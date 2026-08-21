package anomoly

import (
	"encoding/json"
	"testing"
)

func money(v float64) string {
	return "$" + trimFloat(v)
}

// A decoder using UseNumber hands back json.Number, a named string type that a
// plain `case string` will not match.
func TestToFloatAcceptsDecoderNumberTypes(t *testing.T) {
	cases := map[string]any{
		"json.Number": json.Number("0.14"),
		"float64":     0.14,
		"float32":     float32(0.5),
		"int":         2,
		"int32":       int32(3),
		"int64":       int64(4),
		"string":      " 0.75 ",
	}
	for name, value := range cases {
		if _, ok := toFloat(value); !ok {
			t.Errorf("%s: toFloat rejected %#v", name, value)
		}
	}

	for name, value := range map[string]any{
		"nil":          nil,
		"bool":         true,
		"non-numeric":  "abc",
		"empty string": "",
	} {
		if _, ok := toFloat(value); ok {
			t.Errorf("%s: toFloat should reject %#v", name, value)
		}
	}
}

// The ML detector nests the baseline under insights; the Prometheus detector
// writes a historical_<N>_days_value key. Both must yield a baseline.
func TestMetricStatLabelsBaselineShapes(t *testing.T) {
	cases := []struct {
		name      string
		anomaly   *Anomaly
		current   string
		baseline  string
		noBaselne bool
	}{
		{
			name: "ml insights shape",
			anomaly: &Anomaly{
				AnomalyType:  MetricAnomolyTypeCPU,
				CurrentValue: 0.82,
				OldValue: map[string]any{
					"insights": []any{
						map[string]any{"value": 0.82, "baseline_value": 0.14},
					},
				},
			},
			current:  "820m",
			baseline: "140m",
		},
		{
			name: "prometheus historical shape",
			anomaly: &Anomaly{
				AnomalyType:  MetricAnomolyTypeMemory,
				CurrentValue: 108253184,
				OldValue: map[string]any{
					"historical_7_days_value": 88383488.0,
					"query_name":              "memory_usage",
				},
			},
			current:  "103.24 Mi",
			baseline: "84.29 Mi",
		},
		{
			name: "values arriving as strings after a db round-trip",
			anomaly: &Anomaly{
				AnomalyType:  MetricAnomolyTypeCPU,
				CurrentValue: 2,
				OldValue: map[string]any{
					"historical_1_days_value": "0.5",
				},
			},
			current:  "2 cores",
			baseline: "500m",
		},
		{
			name: "unknown reference shape yields current only",
			anomaly: &Anomaly{
				AnomalyType:  MetricAnomolyTypeLatency,
				CurrentValue: 412.5,
				OldValue:     map[string]any{"stats": map[string]any{"whatever": 1}},
			},
			current:   "412.5",
			noBaselne: true,
		},
		{
			name: "nil reference does not panic",
			anomaly: &Anomaly{
				AnomalyType:  MetricAnomolyTypeNetwork,
				CurrentValue: 12,
			},
			current:   "12",
			noBaselne: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			labels := metricStatLabels(tc.anomaly)
			if labels[LabelAnomalyCurrent] != tc.current {
				t.Fatalf("current = %q, want %q", labels[LabelAnomalyCurrent], tc.current)
			}
			baseline, ok := labels[LabelAnomalyBaseline]
			if tc.noBaselne {
				if ok {
					t.Fatalf("expected no baseline, got %q", baseline)
				}
				return
			}
			if baseline != tc.baseline {
				t.Fatalf("baseline = %q, want %q", baseline, tc.baseline)
			}
		})
	}
}

func TestSpendStatLabels(t *testing.T) {
	anomaly := &Anomaly{
		AnomalyType:  MetricAnomolyTypeCloudSpendAccount,
		CurrentValue: 208,
		OldValue: map[string]any{
			"mean":       14.0,
			"z_score":    4.2,
			"pct_change": 1385.7,
		},
	}

	labels := spendStatLabels(anomaly, money)

	if labels[LabelAnomalyCurrent] != "$208" {
		t.Fatalf("current = %q", labels[LabelAnomalyCurrent])
	}
	if labels[LabelAnomalyBaseline] != "$14" {
		t.Fatalf("baseline = %q", labels[LabelAnomalyBaseline])
	}
	if labels[LabelAnomalyZScore] != "4.20" {
		t.Fatalf("z-score = %q", labels[LabelAnomalyZScore])
	}
	if labels[LabelAnomalyChange] != "+$194 (+1385.7%)" {
		t.Fatalf("change = %q", labels[LabelAnomalyChange])
	}
}

func TestSpendStatLabelsWithoutReferenceData(t *testing.T) {
	labels := spendStatLabels(&Anomaly{CurrentValue: 50}, money)

	if labels[LabelAnomalyCurrent] != "$50" {
		t.Fatalf("current = %q", labels[LabelAnomalyCurrent])
	}
	for _, key := range []string{LabelAnomalyBaseline, LabelAnomalyZScore, LabelAnomalyChange} {
		if value, ok := labels[key]; ok {
			t.Fatalf("%s should be absent, got %q", key, value)
		}
	}
}

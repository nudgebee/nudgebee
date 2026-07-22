package triage

import (
	"math"
	"strings"
	"testing"
)

func TestRewritePromQLThreshold(t *testing.T) {
	cases := []struct {
		name      string
		expr      string
		newValue  float64
		wantValue float64 // re-extracted threshold expected
		wantOp    string
		wantErr   bool
	}{
		{
			name:      "simple greater than",
			expr:      `node_cpu_usage > 90`,
			newValue:  72,
			wantValue: 72,
			wantOp:    ">",
		},
		{
			name:      "greater or equal",
			expr:      `rate(http_errors[5m]) >= 0.5`,
			newValue:  0.8,
			wantValue: 0.8,
			wantOp:    ">=",
		},
		{
			name:      "less than",
			expr:      `node_memory_available < 100`,
			newValue:  50,
			wantValue: 50,
			wantOp:    "<",
		},
		{
			name:      "reversed comparison N < expr",
			expr:      `90 < node_cpu_usage`,
			newValue:  72,
			wantValue: 72,
			wantOp:    ">", // parser flips reversed comparisons
		},
		{
			name:      "parenthesized expr",
			expr:      `(sum(rate(reqs[5m]))) > 1000`,
			newValue:  2000,
			wantValue: 2000,
			wantOp:    ">",
		},
		{
			name:      "compound AND with guard leg",
			expr:      `cpu_ratio > 0.9 and instance_count > 0`,
			newValue:  0.75,
			wantValue: 0.75,
			wantOp:    ">",
		},
		{
			name:      "arithmetic constant 25/100 replaced with absolute",
			expr:      `disk_used_ratio > 25 / 100`,
			newValue:  0.5,
			wantValue: 0.5,
			wantOp:    ">",
		},
		{
			name:     "absent() is not tunable",
			expr:     `absent(up{job="api"})`,
			newValue: 1,
			wantErr:  true,
		},
		{
			name:     "dynamic threshold both sides expr",
			expr:     `requests > avg(requests) * 2`,
			newValue: 5,
			wantErr:  true,
		},
		{
			name:     "invalid promql",
			expr:     `this is not promql ((`,
			newValue: 5,
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RewritePromQLThreshold(tc.expr, tc.newValue)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got rewritten expr %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// The rewritten expr must re-parse and yield exactly the target threshold.
			parsed, perr := extractPromQLThreshold(got)
			if perr != nil {
				t.Fatalf("rewritten expr %q failed to re-parse: %v", got, perr)
			}
			if math.Abs(parsed.Value-tc.wantValue) > floatEpsilon {
				t.Errorf("threshold = %v, want %v (expr %q)", parsed.Value, tc.wantValue, got)
			}
			if parsed.Operator != tc.wantOp {
				t.Errorf("operator = %q, want %q (expr %q)", parsed.Operator, tc.wantOp, got)
			}

			// The query side must be unchanged from the original.
			orig, _ := extractPromQLThreshold(tc.expr)
			if orig != nil && parsed.QueryExpr != orig.QueryExpr {
				t.Errorf("query expr changed: %q -> %q", orig.QueryExpr, parsed.QueryExpr)
			}
		})
	}
}

func TestSetAzureCriteriaThreshold(t *testing.T) {
	in := `{"allOf":[{"metricName":"Percentage CPU","operator":"GreaterThan","threshold":80}]}`
	out, err := setAzureCriteriaThreshold(in, 60, true, 10, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"threshold":60`) {
		t.Errorf("threshold not patched: %s", out)
	}
	if !strings.Contains(out, `"windowSize":"PT10M"`) {
		t.Errorf("windowSize not patched: %s", out)
	}
}

func TestSetGCPPolicyThreshold(t *testing.T) {
	in := `{"conditions":[{"conditionThreshold":{"thresholdValue":0.9,"comparison":"COMPARISON_GT","duration":"60s"}}]}`
	out, err := setGCPPolicyThreshold(in, 0.5, true, 5, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"thresholdValue":0.5`) {
		t.Errorf("thresholdValue not patched: %s", out)
	}
	if !strings.Contains(out, `"duration":"300s"`) {
		t.Errorf("duration not patched: %s", out)
	}
}

func TestParseDurationMinutes(t *testing.T) {
	cases := map[string]int{
		"10m":     10,
		"300s":    5,
		"1h":      60,
		"2h30m":   150,
		"PT10M":   10,
		"PT1H":    60,
		"":        0,
		"garbage": 0,
	}
	for in, want := range cases {
		if got := parseDurationMinutes(in); got != want {
			t.Errorf("parseDurationMinutes(%q) = %d, want %d", in, got, want)
		}
	}
}

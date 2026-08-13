package slo

import (
	"testing"
	"time"
)

func TestStatusForReport(t *testing.T) {
	tests := []struct {
		name   string
		report SLOReport
		want   string
	}{
		{
			name:   "invalid report is NO_DATA, not OK",
			report: SLOReport{Valid: false},
			want:   SLOStatusNoData,
		},
		{
			name: "invalid wins even when the legacy alert flag is set",
			// A zero-traffic workload used to land here as OK and drag the
			// 30-day attainment aggregate down with it.
			report: SLOReport{Valid: false, Alert: true},
			want:   SLOStatusNoData,
		},
		{
			name: "burn-rate severity decides when present",
			report: SLOReport{
				Valid:     true,
				Severity:  SeverityCritical,
				BurnRates: []BurnRate{{LongWindow: 3600, Severity: SeverityCritical}},
			},
			want: SLOStatusFiring,
		},
		{
			name: "burn rates present and all OK overrides a stale legacy alert",
			report: SLOReport{
				Valid:     true,
				Alert:     true,
				Severity:  SeverityOK,
				BurnRates: []BurnRate{{LongWindow: 3600, Severity: SeverityOK}},
			},
			want: SLOStatusOK,
		},
		{
			name:   "falls back to the legacy alert flag for agents without burn rates",
			report: SLOReport{Valid: true, Alert: true},
			want:   SLOStatusFiring,
		},
		{
			name:   "healthy legacy report",
			report: SLOReport{Valid: true},
			want:   SLOStatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statusForReport(tt.report); got != tt.want {
				t.Errorf("statusForReport = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestFiringBurnRate(t *testing.T) {
	r := SLOReport{BurnRates: []BurnRate{
		{LongWindow: 3600, Severity: SeverityOK, LongWindowBurnRate: 2},
		{LongWindow: 21600, Severity: SeverityCritical, LongWindowBurnRate: 9},
	}}
	br, ok := firingBurnRate(r)
	if !ok {
		t.Fatal("firingBurnRate = false; want the 6h rule")
	}
	if br.LongWindow != 21600 || br.LongWindowBurnRate != 9 {
		t.Errorf("got %+v; want the 6h/9x rule", br)
	}

	if _, ok := firingBurnRate(SLOReport{BurnRates: []BurnRate{{Severity: SeverityOK}}}); ok {
		t.Error("firingBurnRate = true; want false when every rule is OK")
	}
	if _, ok := firingBurnRate(SLOReport{}); ok {
		t.Error("firingBurnRate = true; want false with no burn rates")
	}
}

// Rounding to the nearest hour stamped a run at 10:31 into the 11:00 bucket,
// so a cron drifting across :30 wrote into the next hour's row. Buckets are
// UTC regardless of the host's zone — a half-hour-offset zone (IST) would
// otherwise produce :30 buckets.
func TestTimestampToPostgresFormat_TruncatesToContainingHour(t *testing.T) {
	at := func(h, m int) float64 {
		return float64(time.Date(2026, 8, 1, h, m, 0, 0, time.UTC).Unix())
	}
	for _, tc := range []struct {
		h, m int
		want string
	}{
		{10, 0, "2026-08-01 10:00:00"},
		{10, 29, "2026-08-01 10:00:00"},
		{10, 31, "2026-08-01 10:00:00"},
		{10, 59, "2026-08-01 10:00:00"},
	} {
		if got := timestampToPostgresFormat(at(tc.h, tc.m)); got != tc.want {
			t.Errorf("%02d:%02d -> %q; want %q", tc.h, tc.m, got, tc.want)
		}
	}
}

func TestMarshalBurnRates(t *testing.T) {
	v, err := marshalBurnRates(nil)
	if err != nil {
		t.Fatal(err)
	}
	if v != nil {
		t.Errorf("marshalBurnRates(nil) = %v; want nil so the column stays NULL", v)
	}

	v, err = marshalBurnRates([]BurnRate{{LongWindow: 3600, Severity: SeverityCritical}})
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(string)
	if !ok || s == "" {
		t.Fatalf("marshalBurnRates = %#v; want a non-empty JSON string", v)
	}
}

func TestNullableSeverity(t *testing.T) {
	if v := nullableSeverity(SLOReport{}); v != nil {
		t.Errorf("nullableSeverity = %v; want nil for an agent that sends no severity", v)
	}
	if v := nullableSeverity(SLOReport{Severity: SeverityCritical}); v != SeverityCritical {
		t.Errorf("nullableSeverity = %v; want %q", v, SeverityCritical)
	}
}

package observability

import (
	"testing"
	"time"
)

func TestGetStepDuration_ExplicitInterval(t *testing.T) {
	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()

	got := getStepDuration(300, &start, &end)
	if got != 300*time.Second {
		t.Errorf("getStepDuration(300, ...) = %v, want %v", got, 300*time.Second)
	}
}

func TestGetStepDuration_ZeroInterval_CalculatesFromRange(t *testing.T) {
	start := time.Now().Add(-6 * time.Hour)
	end := time.Now()

	got := getStepDuration(0, &start, &end)
	// 6h = 21600s, 21600/60 = 360s
	expected := 360 * time.Second
	if got != expected {
		t.Errorf("getStepDuration(0, 6h range) = %v, want %v", got, expected)
	}
}

func TestGetStepDuration_ShortRange_MinimumStep(t *testing.T) {
	start := time.Now().Add(-10 * time.Minute)
	end := time.Now()

	got := getStepDuration(0, &start, &end)
	// 10min = 600s, 600/60 = 10s < 60, so clamp to 60s
	expected := 60 * time.Second
	if got != expected {
		t.Errorf("getStepDuration(0, 10min range) = %v, want %v", got, expected)
	}
}

func TestGetStepDuration_NilTimes_Default(t *testing.T) {
	got := getStepDuration(0, nil, nil)
	expected := 60 * time.Second
	if got != expected {
		t.Errorf("getStepDuration(0, nil, nil) = %v, want %v", got, expected)
	}
}

func TestGetStepDuration_OnlyStartNil(t *testing.T) {
	end := time.Now()
	got := getStepDuration(0, nil, &end)
	expected := 60 * time.Second
	if got != expected {
		t.Errorf("getStepDuration(0, nil, end) = %v, want %v", got, expected)
	}
}

func TestGetStepDuration_24hRange(t *testing.T) {
	start := time.Now().Add(-24 * time.Hour)
	end := time.Now()

	got := getStepDuration(0, &start, &end)
	// 24h = 86400s, 86400/60 = 1440s
	expected := 1440 * time.Second
	if got != expected {
		t.Errorf("getStepDuration(0, 24h range) = %v, want %v", got, expected)
	}
}

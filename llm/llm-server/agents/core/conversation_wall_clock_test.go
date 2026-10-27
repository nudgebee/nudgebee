package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWallClockActiveSeconds(t *testing.T) {
	base := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	span := func(startMin, endMin int) [2]time.Time {
		return [2]time.Time{base.Add(time.Duration(startMin) * time.Minute), base.Add(time.Duration(endMin) * time.Minute)}
	}
	day := 24 * 60

	tests := []struct {
		name  string
		spans [][2]time.Time
		want  float64
	}{
		{"no spans", nil, 0},
		{"single sitting keeps short gaps", [][2]time.Time{span(0, 5), span(10, 15)}, 15 * 60},
		{"days apart drops the idle days", [][2]time.Time{span(0, 8), span(2*day, 2*day+7), span(5*day, 5*day+5)}, 20 * 60},
		{"gap just under the cap is kept", [][2]time.Time{span(0, 1), span(30, 31)}, 31 * 60},
		{"overlapping spans leave no gap", [][2]time.Time{span(0, 100), span(10, 20), span(2*day, 2*day+1)}, 101 * 60},
		{"unsorted input", [][2]time.Time{span(2*day, 2*day+7), span(0, 8)}, 15 * 60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.InDelta(t, tt.want, WallClockActiveSeconds(tt.spans), 0.001)
		})
	}
}

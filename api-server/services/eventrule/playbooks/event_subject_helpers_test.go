package playbooks

import (
	"testing"
	"time"
)

func TestRangeQueryWindow(t *testing.T) {
	now := time.Date(2026, 7, 23, 16, 41, 30, 0, time.UTC)
	ptr := func(t time.Time) *time.Time { return &t }

	tests := []struct {
		name      string
		event     PlaybookEvent
		lookback  int
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			// getPlaybookStartEndTime fabricates EndsAt = StartsAt + 1h for k8s
			// events, so at processing time the end is ~1h in the future. A
			// 10-min lookback window derived from it lies entirely in the
			// future and every query returns zero series (the empty Noisy
			// Neighbours card). The end must clamp to now.
			name: "future end clamps to now",
			event: PlaybookEvent{
				StartedAt: ptr(now.Add(-4 * time.Second)),
				EndedAt:   ptr(now.Add(time.Hour)),
			},
			lookback:  10,
			wantStart: now.Add(-10 * time.Minute),
			wantEnd:   now,
		},
		{
			name: "past end kept as-is",
			event: PlaybookEvent{
				StartedAt: ptr(now.Add(-2 * time.Hour)),
				EndedAt:   ptr(now.Add(-time.Hour)),
			},
			lookback:  10,
			wantStart: now.Add(-70 * time.Minute),
			wantEnd:   now.Add(-time.Hour),
		},
		{
			name: "no end falls back to start",
			event: PlaybookEvent{
				StartedAt: ptr(now.Add(-30 * time.Minute)),
			},
			lookback:  10,
			wantStart: now.Add(-40 * time.Minute),
			wantEnd:   now.Add(-30 * time.Minute),
		},
		{
			name:      "no timestamps falls back to now",
			event:     PlaybookEvent{},
			lookback:  10,
			wantStart: now.Add(-10 * time.Minute),
			wantEnd:   now,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := rangeQueryWindow(tt.event, tt.lookback, now)
			if !start.Equal(tt.wantStart) || !end.Equal(tt.wantEnd) {
				t.Errorf("rangeQueryWindow() = (%s, %s), want (%s, %s)",
					start, end, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

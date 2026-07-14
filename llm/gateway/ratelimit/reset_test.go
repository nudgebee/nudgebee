package ratelimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResetAt(t *testing.T) {
	// A mid-window instant (and deliberately non-UTC input) to prove alignment.
	loc := time.FixedZone("UTC+5", 5*3600)
	at := time.Date(2026, 3, 14, 13, 42, 37, 500, loc) // 08:42:37Z

	cases := []struct {
		period Period
		want   time.Time // UTC
	}{
		{PeriodMinute, time.Date(2026, 3, 14, 8, 43, 0, 0, time.UTC)},
		{PeriodHour, time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)},
		{PeriodDay, time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)},
		{PeriodMonth, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, ResetAt(c.period, at), "period %s", c.period)
	}
}

func TestResetAt_MonthAndYearRollover(t *testing.T) {
	// December → next January bumps the year.
	dec := time.Date(2026, 12, 20, 10, 0, 0, 0, time.UTC)
	assert.Equal(t, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), ResetAt(PeriodMonth, dec))

	// Day rollover across a month boundary.
	eom := time.Date(2026, 1, 31, 23, 59, 0, 0, time.UTC)
	assert.Equal(t, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), ResetAt(PeriodDay, eom))
}

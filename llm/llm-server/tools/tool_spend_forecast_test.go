package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// day is a small helper to build a UTC midnight date.
func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestComputeForecast(t *testing.T) {
	// "Now" = 2026-05-22 (mid-month). May has 31 days. Complete days this month
	// are the 1st..21st (today, the 22nd, is partial and excluded).
	now := time.Date(2026, time.May, 22, 9, 0, 0, 0, time.UTC)

	daily := []dailySpend{}
	// Previous full month: April (30 days) at $100/day = $3000.
	for d := 1; d <= 30; d++ {
		daily = append(daily, dailySpend{Day: day(2026, time.April, d), Amount: 100})
	}
	// Current month-to-date: 1st..21st at $200/day = $4200.
	for d := 1; d <= 21; d++ {
		daily = append(daily, dailySpend{Day: day(2026, time.May, d), Amount: 200})
	}

	f := computeForecast(daily, now)

	assert.Equal(t, "2026-05", f.Month)
	assert.Equal(t, "2026-05-21", f.AsOfDate)
	assert.Equal(t, 21, f.DaysElapsed)
	assert.Equal(t, 31, f.DaysInMonth)
	assert.Equal(t, 10, f.DaysRemaining)
	assert.InDelta(t, 4200, f.MonthToDate, 0.01)
	assert.InDelta(t, 3000, f.PreviousMonthTotal, 0.01)
	// Trailing 7 complete days (May 15..21) at $200 = $1400 → $200/day.
	assert.InDelta(t, 200, f.AvgDailyLast7d, 0.01)
	assert.InDelta(t, 200, f.AvgDailyMtd, 0.01)
	// Projected = 4200 + 200*10 = 6200.
	assert.InDelta(t, 6200, f.ProjectedMonthTotal, 0.01)
	// vs previous month 3000 → +106.67%.
	assert.InDelta(t, 106.67, f.ProjectedVsPreviousPct, 0.01)
}

func TestComputeForecast_FirstOfMonthUsesRunRate(t *testing.T) {
	// On the 1st, month-to-date is zero, so the projection is pure run-rate from
	// the trailing 7 days (which fall in the previous month).
	now := time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC)
	daily := []dailySpend{}
	for d := 24; d <= 30; d++ { // last 7 days of April
		daily = append(daily, dailySpend{Day: day(2026, time.April, d), Amount: 50})
	}
	f := computeForecast(daily, now)

	assert.Equal(t, 0, f.DaysElapsed)
	assert.Equal(t, 31, f.DaysRemaining)
	assert.InDelta(t, 0, f.MonthToDate, 0.01)
	assert.InDelta(t, 50, f.AvgDailyLast7d, 0.01)
	// Pure run-rate: 50 * 31 = 1550.
	assert.InDelta(t, 1550, f.ProjectedMonthTotal, 0.01)
}

func TestComputeForecast_NoData(t *testing.T) {
	f := computeForecast(nil, time.Date(2026, time.May, 22, 0, 0, 0, 0, time.UTC))
	assert.InDelta(t, 0, f.ProjectedMonthTotal, 0.01)
	assert.InDelta(t, 0, f.MonthToDate, 0.01)
	assert.InDelta(t, 0, f.ProjectedVsPreviousPct, 0.01)
}

func TestComputeForecast_YoungAccountUsesDataAwareRate(t *testing.T) {
	// Account onboarded mid-month: the trailing 7-day window has only 3 days of
	// data. The run-rate must divide by the 3 days that exist, not a hardcoded 7,
	// so the projection isn't diluted by pre-onboarding zeros.
	now := time.Date(2026, time.May, 4, 0, 0, 0, 0, time.UTC)
	daily := []dailySpend{
		{Day: day(2026, time.May, 1), Amount: 30},
		{Day: day(2026, time.May, 2), Amount: 30},
		{Day: day(2026, time.May, 3), Amount: 30},
	}
	f := computeForecast(daily, now)
	// $90 over the 3 days present = $30/day (not $90/7).
	assert.InDelta(t, 30, f.AvgDailyLast7d, 0.01)
	assert.InDelta(t, 30, f.AvgDailyMtd, 0.01)
	// Projected = MTD 90 + 30/day × 28 remaining days = 930.
	assert.InDelta(t, 930, f.ProjectedMonthTotal, 0.01)
}

func TestDaysInMonth(t *testing.T) {
	assert.Equal(t, 31, daysInMonth(day(2026, time.May, 15)))
	assert.Equal(t, 30, daysInMonth(day(2026, time.April, 1)))
	assert.Equal(t, 28, daysInMonth(day(2026, time.February, 10)))
	assert.Equal(t, 29, daysInMonth(day(2024, time.February, 10))) // leap year
}

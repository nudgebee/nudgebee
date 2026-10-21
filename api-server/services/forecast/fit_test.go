package forecast

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

var testParams = Params{MinHours: 48, MinRisePct: 5, HighWithinHours: 48}

// series builds hours of hourly buckets from f, with a ±amp daily cycle on top.
func series(hours int, amp float64, f func(h float64) float64) []Point {
	out := make([]Point, hours)
	for i := range out {
		h := float64(i)
		out[i] = Point{T: t0.Add(time.Duration(i) * time.Hour), V: f(h) + amp*math.Sin(2*math.Pi*h/24)}
	}
	return out
}

func constant(hours int, v float64) []Point {
	return series(hours, 0, func(float64) float64 { return v })
}

// userRamp is the example the feature was asked for: 10% on day 1, 15% on day 2,
// 50% on day 4 and 89% on day 7, each the value at the middle of that day and linear
// between them. On that line 25% is crossed early on day 3, a few hours after the
// first forecast can run.
func userRamp(h float64) float64 {
	knots := [][2]float64{{1, 10}, {2, 15}, {4, 50}, {7, 89}}
	d := 1 + (h-12)/24
	for i := 1; i < len(knots); i++ {
		if d <= knots[i][0] {
			a, b := knots[i-1], knots[i]
			return a[1] + (b[1]-a[1])*(d-a[0])/(b[0]-a[0])
		}
	}
	return knots[len(knots)-1][1]
}

func input(values, limits []Point) FitInput {
	return FitInput{
		Condition:    ConditionThrottling,
		ThresholdPct: 25,
		Value:        values,
		PerPodLimit:  limits,
		PerPodUsage:  constant(len(values), 0.4),
		RoundStep:    0.05,
		Now:          t0.Add(time.Duration(len(values)) * time.Hour),
		Params:       testParams,
	}
}

func TestFit_UserRampWarnsAtHour48(t *testing.T) {
	res := Fit(input(series(48, 3, userRamp), constant(48, 0.5)))

	require.Equal(t, StatusProjected, res.Status, res.Reason)
	require.NotNil(t, res.ETAHours)
	assert.Positive(t, *res.ETAHours)
	assert.LessOrEqual(t, *res.ETAHours, 48.0)
	assert.Equal(t, LevelHigh, res.Level)
	assert.Equal(t, 48, res.HoursOfData)
	assert.Equal(t, 0.5, res.PerPodLimit)
	assert.GreaterOrEqual(t, res.SuggestedLimit, 0.65, "never less than a quarter above the current limit")

	var observed, projected int
	for _, r := range res.Daily {
		if r.Projected {
			projected++
		} else {
			observed++
		}
	}
	assert.Equal(t, 2, observed)
	assert.Positive(t, projected)
	assert.Equal(t, 0.4, res.Daily[0].PerPodUsage, "each observed day carries its mean usage")
}

func TestFit_LevelFollowsETA(t *testing.T) {
	// 2 pp a day from 5%: about ten days out, so MEDIUM.
	slow := Fit(input(series(72, 1, func(h float64) float64 { return 5 + 2*h/24 }), constant(72, 0.5)))
	require.Equal(t, StatusProjected, slow.Status, slow.Reason)
	assert.Equal(t, LevelMedium, slow.Level)
	assert.Greater(t, *slow.ETAHours, 7*24.0, "no maximum ETA: a slow climb still warns")

	// 8 pp a day from 12%: under two days out, so HIGH.
	fast := Fit(input(series(72, 1, func(h float64) float64 { return -4 + 8*h/24 }), constant(72, 0.5)))
	require.Equal(t, StatusProjected, fast.Status, fast.Reason)
	assert.Equal(t, LevelHigh, fast.Level)
}

func TestFit_MaxETACap(t *testing.T) {
	in := input(series(72, 1, func(h float64) float64 { return 5 + 2*h/24 }), constant(72, 0.5))
	in.Params.MaxETAHours = 48
	res := Fit(in)
	assert.Equal(t, StatusNotProjected, res.Status)
}

func TestFit_Quiet(t *testing.T) {
	cases := map[string][]Point{
		"flat with a daily cycle": series(72, 3, func(float64) float64 { return 10 }),
		"one spike day that drops back": series(72, 0, func(h float64) float64 {
			if h >= 24 && h < 48 {
				return 22
			}
			return 10
		}),
		"falling": series(72, 1, func(h float64) float64 { return 20 - 3*h/24 }),
	}
	for name, values := range cases {
		t.Run(name, func(t *testing.T) {
			res := Fit(input(values, constant(72, 0.5)))
			assert.Equal(t, StatusNotProjected, res.Status, res.Reason)
			assert.Nil(t, res.ETAHours)
		})
	}
}

func TestFit_Breached(t *testing.T) {
	res := Fit(input(constant(72, 30), constant(72, 0.5)))
	assert.Equal(t, StatusBreached, res.Status)
	assert.Equal(t, 30.0, res.CurrentPct)
}

func TestFit_BreachKeepsTheClimbThatLedToIt(t *testing.T) {
	// 1 pp an hour from 10%, as the nbdemo-orders test ramp does, read at hour 70.
	in := input(series(70, 0, func(h float64) float64 { return 10 + h }), constant(70, 0.2))
	in.ThresholdPct = 75
	res := Fit(in)
	require.Equal(t, StatusBreached, res.Status)
	assert.InDelta(t, 24, res.SlopePctPerDay, 0.5, "the slope is still reported on a breach")
	assert.Len(t, res.Daily, 3)
}

func TestFit_NoCPULimit(t *testing.T) {
	assert.Equal(t, StatusNoLimit, Fit(input(series(72, 1, userRamp), nil)).Status)
	assert.Equal(t, StatusNoLimit, Fit(input(series(72, 1, userRamp), constant(72, 0))).Status)
}

func TestFit_MinHoursBelowADayIsFloored(t *testing.T) {
	in := input(series(12, 1, userRamp), constant(12, 0.5))
	in.Params.MinHours = 6
	assert.NotPanics(t, func() {
		assert.Equal(t, StatusInsufficientHistory, Fit(in).Status, "the fit needs one whole day")
	})
}

func TestFit_InsufficientHistory(t *testing.T) {
	res := Fit(input(series(40, 1, userRamp), constant(40, 0.5)))
	assert.Equal(t, StatusInsufficientHistory, res.Status)
	assert.Equal(t, 40, res.HoursOfData)
}

func TestFit_LimitChangeCutsHistory(t *testing.T) {
	values := series(72, 1, func(h float64) float64 { return 5 + 6*h/24 })
	limits := constant(72, 0.5)
	for i := 40; i < 72; i++ {
		limits[i].V = 1.0 // a rollout raised the limit at hour 40
	}
	res := Fit(input(values, limits))
	assert.Equal(t, StatusInsufficientHistory, res.Status)
	assert.Equal(t, 32, res.HoursOfData)
	assert.Contains(t, res.Reason, "limit changed")
	assert.Equal(t, 1.0, res.PerPodLimit)
}

func TestFit_ReplicaChangeAloneDoesNotCut(t *testing.T) {
	// Per-pod limit is total ÷ pods; a scale-out with sampling skew moves it by a few
	// percent for one bucket, which must not count as a limit change.
	limits := constant(72, 0.5)
	limits[50].V = 0.49
	res := Fit(input(series(72, 1, func(h float64) float64 { return 5 + 6*h/24 }), limits))
	assert.Equal(t, 72, res.HoursOfData)
	assert.Equal(t, StatusProjected, res.Status, res.Reason)
}

func TestFit_SparseSamplesCountByBucket(t *testing.T) {
	values := series(72, 1, func(h float64) float64 { return 5 + 6*h/24 })
	sparse := make([]Point, 0, len(values))
	for i, p := range values {
		if i%2 == 0 {
			sparse = append(sparse, p)
		} else {
			sparse = append(sparse, Point{T: p.T, V: math.NaN()})
		}
	}
	res := Fit(input(sparse, constant(72, 0.5)))
	assert.Equal(t, StatusInsufficientHistory, res.Status, "36 real buckets are not 48")
	assert.Equal(t, 36, res.HoursOfData)
}

func TestFit_CustomThresholdMovesETA(t *testing.T) {
	values := series(72, 1, func(h float64) float64 { return 5 + 6*h/24 })
	at25 := input(values, constant(72, 0.5))
	at40 := at25
	at40.ThresholdPct = 40
	r25, r40 := Fit(at25), Fit(at40)
	require.Equal(t, StatusProjected, r25.Status)
	require.Equal(t, StatusProjected, r40.Status)
	assert.InDelta(t, *r25.ETAHours+15/(r25.SlopePctPerDay/24), *r40.ETAHours, 0.5)
}

// A workload busier by day than by night must not get a shorter ETA at every peak:
// the ETA comes from the whole-day fit, so two evaluations twelve hours apart, one at
// a peak and one at a trough, differ by the twelve hours that passed and not by the
// daily swing. With a plain line fit and a last-6h "now" they differed by 139h here,
// and a level that crosses the 48h line twice a day raises a new HIGH alert daily.
func TestFit_DailyCycleDoesNotSwingTheETA(t *testing.T) {
	pts := series(60, 8, func(h float64) float64 { return 10 + 0.4*h })
	limits := constant(60, 0.5)
	params := Params{MinHours: 48, MinRisePct: 5, HighWithinHours: 48}

	early := input(pts[:48], limits[:48])
	early.ThresholdPct, early.Params = 75, params
	late := input(pts[12:60], limits[12:60])
	late.ThresholdPct, late.Params, late.Now = 75, params, t0.Add(60*time.Hour)

	a, b := Fit(early), Fit(late)
	require.Equal(t, StatusProjected, a.Status, a.Reason)
	require.Equal(t, StatusProjected, b.Status, b.Reason)
	assert.InDelta(t, 12, *a.ETAHours-*b.ETAHours, 2, "only the elapsed time separates the two ETAs")
}

// When the fitted line has already crossed the threshold but the 6h mean has not,
// the breach is imminent: an ETA of zero, never a negative one.
func TestFit_LinePastThresholdIsImminentNotNegative(t *testing.T) {
	pts := series(48, 0, func(h float64) float64 { return 20 + 1.2*h })
	in := input(pts, constant(48, 0.5))
	in.ThresholdPct = 76
	res := Fit(in)
	require.Equal(t, StatusProjected, res.Status, res.Reason)
	assert.GreaterOrEqual(t, *res.ETAHours, 0.0)
	assert.Equal(t, LevelHigh, res.Level)
}

// With no daily cycle the harmonic terms come out at zero, so the trend is the plain
// least-squares line.
func TestTrendFit_MatchesAPlainLineWithoutACycle(t *testing.T) {
	pts := series(48, 0, func(h float64) float64 { return 12 + 0.3*h })
	slope, intercept := trendFit(pts)
	assert.InDelta(t, 0.3, slope, 1e-9)
	assert.InDelta(t, 12, intercept, 1e-9)
}

// A business-hours square wave is not a sinusoid; the two harmonics still take most
// of it out of the slope.
func TestTrendFit_BusinessHoursPatternBarelyTiltsTheSlope(t *testing.T) {
	for _, end := range []int{48, 54, 60, 66} {
		pts := series(end, 0, func(h float64) float64 {
			v := 10 + 0.4*h
			if hr := int(h) % 24; hr >= 9 && hr < 18 {
				v += 12
			}
			return v
		})
		slope, _ := trendFit(pts[end-48:])
		assert.InDelta(t, 0.4, slope, 0.06, "window ending at hour %d", end)
	}
}

// The suggested limit fits usage over whole days too: 36 hours of a daily-cycling
// usage on a flat trend must not read as a climb and oversize the limit.
func TestSuggestLimit_PartialDayOfCyclingUsageIsNotAClimb(t *testing.T) {
	usage := series(36, 0.1, func(float64) float64 { return 0.3 })
	got := suggestLimit(usage, time.Time{}, 0.4, 24, 0.05)
	assert.Equal(t, 0.5, got, "flat usage leaves only the 1.25x floor (0.5 cores)")
}

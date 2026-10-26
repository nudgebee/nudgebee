package forecast

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Point is one hourly bucket of a series.
type Point struct {
	T time.Time
	V float64
}

// Params are the fit's guard rails. Every field has a config default; see
// paramsFromConfig.
type Params struct {
	// MinHours is the least history, after the last limit change, a forecast needs.
	// Two days covers two daily cycles; any more would miss the user's own example,
	// which crosses 25% on day 3.
	MinHours int
	// MinRisePct is the least rise of the fitted line over the fit window. It is what
	// separates a climb from drift, and it bounds how far ahead a forecast can reach.
	MinRisePct float64
	// MaxETAHours caps how far ahead a breach may be projected. Zero means no cap.
	MaxETAHours float64
	// HighWithinHours is the ETA at or under which a projection is HIGH.
	HighWithinHours float64
}

// FitInput is everything the fit reads. Value is the preset's series in percent;
// PerPodLimit and PerPodUsage are in the preset's unit. All three are hourly buckets.
type FitInput struct {
	Condition    Condition
	ThresholdPct float64
	Value        []Point
	PerPodLimit  []Point
	PerPodUsage  []Point
	// RoundStep is the granularity a suggested limit is rounded up to; NoLimitReason
	// explains a missing limit. Both come from the preset.
	RoundStep     float64
	NoLimitReason string
	// Resource names the limited resource in reasons ("CPU", "memory"); empty is "CPU".
	Resource string
	Now      time.Time
	Params   Params
}

// limitChangeTolerance is how far the per-pod limit may wander before it counts as a
// changed limit. Per-pod limit is a workload total divided by a pod count, and the two
// are sampled at slightly different instants during a rollout, so an exact comparison
// would cut the history at every rollout even when the limit stayed put.
const limitChangeTolerance = 0.05

// minFitHours is the floor under Params.MinHours: the fit runs over whole days, so it
// needs at least one.
const minFitHours = 24

// currentWindowHours is how much of the end of the series makes up "now". A mean over
// six buckets keeps one noisy hour from deciding whether the threshold is breached.
const currentWindowHours = 6

// maxDataAge is how old the newest bucket may be before the series no longer describes
// the workload. Throttling has no bucket for an hour the workload ran no CFS periods,
// so a night's idling must not end a forecast; a workload scaled to zero must, or its
// last day of data stays "breached" until it leaves the 3-day window.
const maxDataAge = 24 * time.Hour

// headroom is the share of a suggested limit the projected usage may fill.
const headroom = 0.8

// Fit projects when Value crosses ThresholdPct. It is pure: the same input always
// yields the same Result, so the whole decision is table-testable.
func Fit(in FitInput) Result {
	res := Result{
		Condition:    in.Condition,
		ThresholdPct: in.ThresholdPct,
		EvaluatedAt:  in.Now,
		Daily:        []DailyRow{},
	}

	limits := finitePoints(in.PerPodLimit)
	if len(limits) == 0 || limits[len(limits)-1].V <= 0 {
		res.Status = StatusNoLimit
		res.Reason = in.NoLimitReason
		if res.Reason == "" {
			res.Reason = "the workload has no limit on this resource"
		}
		return res
	}
	currentLimit := limits[len(limits)-1].V
	res.PerPodLimit = round(currentLimit, 3)

	// History before the last limit change describes a different workload: a raised
	// limit is a new regime, not a continuation of the trend.
	since := lastLimitChange(limits, currentLimit)
	values := pointsSince(finitePoints(in.Value), since)
	res.HoursOfData = len(values)
	res.Daily = dailyRows(values, in.PerPodLimit, pointsSince(finitePoints(in.PerPodUsage), since))

	if len(values) == 0 {
		res.Status = StatusInsufficientHistory
		res.Reason = "no data for this workload in the last 3 days"
		return res
	}
	if in.Now.Sub(values[len(values)-1].T) > maxDataAge {
		res.Status = StatusInsufficientHistory
		res.Reason = "no data for this workload in the last 24 hours"
		return res
	}
	res.CurrentPct = round(meanOfLast(values, currentWindowHours), 2)

	// Fit over whole days, with the daily cycle modelled alongside the trend (see
	// trendFit): a whole-day window cancels the cycle's mean but not its tilt, so a
	// plain line's slope would depend on the hour the window ends. The slope is
	// computed for a breach too: the breach alert shows the climb that led to it.
	minHours := max(in.Params.MinHours, minFitHours)
	var window []Point
	var slopePerHour, intercept float64
	if len(values) >= minHours {
		window = values[len(values)-(len(values)/24)*24:]
		slopePerHour, intercept = trendFit(window)
		res.SlopePctPerDay = round(slopePerHour*24, 2)
	}

	if res.CurrentPct >= in.ThresholdPct {
		res.Status = StatusBreached
		res.Reason = "the current value is at or above the threshold"
		return res
	}
	if window == nil {
		resource := in.Resource
		if resource == "" {
			resource = "CPU"
		}
		res.Status = StatusInsufficientHistory
		res.Reason = fmt.Sprintf("fewer than %d hours of data since the %s limit last changed", minHours, resource)
		if !since.IsZero() {
			res.Reason = fmt.Sprintf("the %s limit changed recently; fewer than %d hours of data since then", resource, minHours)
		}
		return res
	}
	rise := slopePerHour * float64(len(window)-1)

	if slopePerHour <= 0 || rise < in.Params.MinRisePct {
		res.Status = StatusNotProjected
		res.Reason = "no sustained rise towards the threshold"
		return res
	}

	// The ETA starts from the fitted line at the last bucket, not from CurrentPct: the
	// last 6h carry the daily cycle the whole-day fit averages out, so measuring from
	// them would shorten the ETA every peak and lengthen it every trough, and a level
	// that flips MEDIUM/HIGH daily raises a new HIGH alert each time it climbs back.
	// A line already past the threshold while the 6h mean is not means "imminent".
	fittedNow := intercept + slopePerHour*window[len(window)-1].T.Sub(window[0].T).Hours()
	etaHours := max((in.ThresholdPct-fittedNow)/slopePerHour, 0)
	if in.Params.MaxETAHours > 0 && etaHours > in.Params.MaxETAHours {
		res.Status = StatusNotProjected
		res.Reason = "the projected breach is beyond the configured horizon"
		return res
	}

	res.Status = StatusProjected
	eta := round(etaHours, 1)
	res.ETAHours = &eta
	at := in.Now.Add(time.Duration(etaHours * float64(time.Hour))).UTC()
	res.ETAAt = &at
	res.Level = LevelMedium
	if etaHours <= in.Params.HighWithinHours {
		res.Level = LevelHigh
	}
	res.Daily = append(res.Daily, projectedRows(window, slopePerHour, intercept, at)...)
	res.SuggestedLimit = suggestLimit(in.PerPodUsage, since, currentLimit, etaHours, in.RoundStep)
	return res
}

func finitePoints(in []Point) []Point {
	out := make([]Point, 0, len(in))
	for _, p := range in {
		if !math.IsNaN(p.V) && !math.IsInf(p.V, 0) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	return out
}

// lastLimitChange returns the first bucket of the trailing run whose limit matches
// the current one, or the zero time when the limit never changed in the window.
func lastLimitChange(limits []Point, current float64) time.Time {
	for i := len(limits) - 1; i >= 0; i-- {
		if math.Abs(limits[i].V-current) > current*limitChangeTolerance {
			if i+1 < len(limits) {
				return limits[i+1].T
			}
			return limits[i].T.Add(time.Hour)
		}
	}
	return time.Time{}
}

func pointsSince(points []Point, since time.Time) []Point {
	if since.IsZero() {
		return points
	}
	for i, p := range points {
		if !p.T.Before(since) {
			return points[i:]
		}
	}
	return nil
}

func meanOfLast(points []Point, n int) float64 {
	if n > len(points) {
		n = len(points)
	}
	sum := 0.0
	for _, p := range points[len(points)-n:] {
		sum += p.V
	}
	return sum / float64(n)
}

// trendFit fits v = intercept + slope*h + the daily (24h) and half-daily (12h)
// harmonics, with h the hours since the first point, and returns the trend part.
// A day/night pattern is not noise around the trend: over whole days its mean is
// zero but its covariance with time is not, so a plain line fitted through it tilts
// by up to amplitude*12/(ω·L²) per hour depending on the phase the window ends at
// (measured: an 8-point cycle on a 0.4 pp/h climb moved the ETA by over 100h between
// a peak and a trough). Modelling the cycle takes it out of the slope exactly for a
// sinusoid and mostly for a business-hours square wave. It falls back to a plain line
// when the harmonics are not identifiable (under a day of points, or gaps).
func trendFit(points []Point) (slope, intercept float64) {
	const n = 6
	if len(points) < 24 {
		return leastSquares(points)
	}
	t0 := points[0].T
	var ata [n][n]float64
	var aty [n]float64
	for _, p := range points {
		h := p.T.Sub(t0).Hours()
		w := 2 * math.Pi * h / 24
		row := [n]float64{1, h, math.Sin(w), math.Cos(w), math.Sin(2 * w), math.Cos(2 * w)}
		for i := range n {
			aty[i] += row[i] * p.V
			for j := range n {
				ata[i][j] += row[i] * row[j]
			}
		}
	}
	coef, ok := solveLinear(ata, aty)
	if !ok {
		return leastSquares(points)
	}
	return coef[1], coef[0]
}

// solveLinear solves a·x = b by Gaussian elimination with partial pivoting, and
// reports false for a singular (or numerically singular) system.
func solveLinear(a [6][6]float64, b [6]float64) ([6]float64, bool) {
	const n = 6
	for col := range n {
		pivot := col
		for r := col + 1; r < n; r++ {
			if math.Abs(a[r][col]) > math.Abs(a[pivot][col]) {
				pivot = r
			}
		}
		if math.Abs(a[pivot][col]) < 1e-9 {
			return [6]float64{}, false
		}
		a[col], a[pivot] = a[pivot], a[col]
		b[col], b[pivot] = b[pivot], b[col]
		for r := col + 1; r < n; r++ {
			f := a[r][col] / a[col][col]
			for c := col; c < n; c++ {
				a[r][c] -= f * a[col][c]
			}
			b[r] -= f * b[col]
		}
	}
	var x [6]float64
	for r := n - 1; r >= 0; r-- {
		sum := b[r]
		for c := r + 1; c < n; c++ {
			sum -= a[r][c] * x[c]
		}
		x[r] = sum / a[r][r]
	}
	return x, true
}

// leastSquares fits v = slope*h + intercept, with h the hours since the first point.
func leastSquares(points []Point) (slope, intercept float64) {
	n := float64(len(points))
	t0 := points[0].T
	var sumX, sumY, sumXY, sumXX float64
	for _, p := range points {
		x := p.T.Sub(t0).Hours()
		sumX += x
		sumY += p.V
		sumXY += x * p.V
		sumXX += x * x
	}
	den := n*sumXX - sumX*sumX
	if den == 0 {
		return 0, sumY / n
	}
	slope = (n*sumXY - sumX*sumY) / den
	return slope, (sumY - slope*sumX) / n
}

// dailyRows is the observed half of the evidence table: one row per UTC day, each the
// mean of that day's buckets, with that day's mean per-pod usage alongside.
func dailyRows(values, limits, usage []Point) []DailyRow {
	type acc struct{ sum, n, limit, usageSum, usageN float64 }
	days := map[string]*acc{}
	var order []string
	for _, p := range values {
		d := p.T.UTC().Format(time.DateOnly)
		if days[d] == nil {
			days[d] = &acc{}
			order = append(order, d)
		}
		days[d].sum += p.V
		days[d].n++
	}
	for _, p := range finitePoints(limits) {
		if a := days[p.T.UTC().Format(time.DateOnly)]; a != nil {
			a.limit = p.V
		}
	}
	for _, p := range usage {
		if a := days[p.T.UTC().Format(time.DateOnly)]; a != nil {
			a.usageSum += p.V
			a.usageN++
		}
	}
	rows := make([]DailyRow, 0, len(order))
	for _, d := range order {
		a := days[d]
		row := DailyRow{Date: d, ValuePct: round(a.sum/a.n, 2), PerPodLimit: round(a.limit, 3)}
		if a.usageN > 0 {
			row.PerPodUsage = round(a.usageSum/a.usageN, 3)
		}
		rows = append(rows, row)
	}
	return rows
}

// maxProjectedRows bounds the projected half of the table, so a breach weeks away
// does not bury the observed days.
const maxProjectedRows = 7

func projectedRows(window []Point, slopePerHour, intercept float64, eta time.Time) []DailyRow {
	t0 := window[0].T
	last := window[len(window)-1].T.UTC()
	day := time.Date(last.Year(), last.Month(), last.Day(), 12, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	var rows []DailyRow
	for len(rows) < maxProjectedRows && !day.After(eta.AddDate(0, 0, 1)) {
		v := intercept + slopePerHour*day.Sub(t0).Hours()
		rows = append(rows, DailyRow{Date: day.Format(time.DateOnly), ValuePct: round(v, 2), Projected: true})
		day = day.AddDate(0, 0, 1)
	}
	return rows
}

// suggestLimit sizes a per-pod limit for the usage projected a day past the breach,
// leaving headroom. It never suggests less than a quarter above the current limit:
// a throttled container's usage is capped by that limit, so its own trend
// understates what it would use if it could.
func suggestLimit(usage []Point, since time.Time, currentLimit, etaHours, step float64) float64 {
	floor := currentLimit * 1.25
	points := pointsSince(finitePoints(usage), since)
	if len(points) < 2 {
		return roundUpTo(floor, step)
	}
	// Whole days, as in Fit: over a partial day the trend and the daily harmonic are
	// nearly collinear, and a slope extrapolated past the ETA amplifies the error.
	if len(points) >= 24 {
		points = points[len(points)-(len(points)/24)*24:]
	}
	slope, intercept := trendFit(points)
	horizon := points[len(points)-1].T.Sub(points[0].T).Hours() + etaHours + 24
	projected := (intercept + slope*horizon) / headroom
	return roundUpTo(math.Max(projected, floor), step)
}

// roundUpTo rounds v up to a multiple of step (50 millicores, 64 MiB). The epsilon
// keeps a float that is a hair over a multiple, such as 0.30000000000000004, from
// rounding up a whole step.
func roundUpTo(v, step float64) float64 {
	if step <= 0 {
		return v
	}
	return round(math.Ceil(v/step-1e-9)*step, 6)
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

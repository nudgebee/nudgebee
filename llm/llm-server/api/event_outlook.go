package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
)

// The "Will it happen again" line of an alert investigation is computed here and
// written into the report by code. Left to the agent, the line was missing
// from most investigations and rarely gave a time: it only
// asked whether the alert's metric climbs toward a limit, and only when the
// agent chose to fetch and analyse it. Following the SRE books, it answers from
// whether the problem repeats and whether its cause is still in place, and
// gives a time only where a rate or a cadence supports one.

// firingHistorySpan is how far back an alert's own firings are read.
const firingHistorySpan = 30 * 24 * time.Hour

// firingMergeGap folds firings closer than this into one: an alert flapping on
// its threshold is one occurrence, not many.
const firingMergeGap = 15 * time.Minute

// Firing patterns. Backtested on 60 days of firings (1459 alerts with 5+
// occurrences), cutting each history at every occurrence and predicting the
// next one:
//   - regular: the last 3–4 gaps within ±30% of their median. The next came
//     within ±25% of that median for 43% of alerts and by its upper end for
//     67%; misses were mostly an extra early firing or a skipped cycle.
//   - burst: short gaps broken by at least two gaps 6x the median, the last
//     gap short. It fired again within 1.2x the longest in-burst gap for 79%.
//   - irregular: no time from history. A daily time of day (12%), the gap after
//     a burst (14%) and a shrinking gap (58% vs 63%) predicted no better.
const (
	firingRegularSpread = 0.3
	firingRegularWindow = 0.25
	firingBurstFactor   = 6
	firingBurstSlack    = 1.2
)

// memoryNearLimitPct is the hourly-peak share of the memory limit at which a
// container's next spike can reach the limit. Peaks are RSS, which runs below
// the working set the kernel kills on (one container killed 4 times in 5 days
// had RSS peaks at 89% and working-set peaks at 95% of its limit).
const memoryNearLimitPct = 80

// outlookFetchTimeout bounds the memory forecast read, which holds up saving
// the investigation; past it the line is written from the firing history
// alone. The read usually takes 0.5–4 s.
const outlookFetchTimeout = 8 * time.Second

type firingPattern string

const (
	firingFirst     firingPattern = "first"
	firingFew       firingPattern = "few"
	firingRegular   firingPattern = "regular"
	firingBurst     firingPattern = "burst"
	firingIrregular firingPattern = "irregular"
)

// firingHistory is what an alert's own firings say about the next one.
type firingHistory struct {
	pattern firingPattern
	count   int
	last    time.Time
	gaps    []time.Duration // latest up to 4
	every   time.Duration   // regular: the median gap
	next    time.Time       // regular: last + every
	lo, hi  time.Time       // regular: the window around next
	by      time.Time       // burst and irregular: likely again by
	overdue bool            // the window or by has passed without a firing
}

// readFirings classifies firings (oldest first) as of now.
func readFirings(times []time.Time, now time.Time) firingHistory {
	occ := mergeFirings(times)
	h := firingHistory{count: len(occ), last: occ[len(occ)-1], pattern: firingFirst}
	if len(occ) == 1 {
		return h
	}
	gaps := make([]time.Duration, len(occ)-1)
	for i := 1; i < len(occ); i++ {
		gaps[i-1] = occ[i].Sub(occ[i-1])
	}
	h.gaps = gaps[max(0, len(gaps)-4):]
	if len(occ) < 4 {
		h.pattern = firingFew
		return h
	}
	med := medianDuration(h.gaps)
	if !slices.ContainsFunc(h.gaps, func(g time.Duration) bool {
		return math.Abs(float64(g-med)) > firingRegularSpread*float64(med)
	}) {
		h.pattern, h.every = firingRegular, med
		h.next = h.last.Add(med)
		h.lo = h.last.Add(scaleDuration(med, 1-firingRegularWindow))
		h.hi = h.last.Add(scaleDuration(med, 1+firingRegularWindow))
		h.overdue = now.After(h.hi)
		return h
	}
	if len(gaps) >= 5 {
		all := medianDuration(gaps)
		var longs int
		var longestShort time.Duration
		for _, g := range gaps {
			if g >= firingBurstFactor*all {
				longs++
				continue
			}
			longestShort = max(longestShort, g)
		}
		if longs >= 2 && gaps[len(gaps)-1] < firingBurstFactor*all {
			h.pattern = firingBurst
			h.by = h.last.Add(scaleDuration(longestShort, firingBurstSlack))
			h.overdue = now.After(h.by)
			return h
		}
	}
	h.pattern = firingIrregular
	h.by = h.last.Add(slices.Max(h.gaps))
	h.overdue = now.After(h.by)
	return h
}

// memoryHeadroom is a container's hourly memory peaks against its limit, from
// api-server's memory_saturation forecast.
type memoryHeadroom struct {
	container    string
	limitMiB     float64
	currentPct   float64
	firstPct     float64 // the first day of the forecast's daily table
	firstDate    string
	slopePerDay  float64
	etaAt        *time.Time // when the peaks reach thresholdPct, if projected
	thresholdPct float64
}

// eventOutlookLine computes the line for one alert investigation as of now, or
// "" when the event has no firing history to read.
func eventOutlookLine(ctx *security.RequestContext, repo *events.EventAnalysisRepository, accountId string, event events.Event, now time.Time) string {
	now = now.UTC()
	workload := event.SubjectOwner
	if workload == "" {
		workload = event.SubjectName
	}
	times, err := repo.ListWorkloadFiringTimes(ctx, accountId, event.AggregationKey, event.SubjectNamespace, workload, now.Add(-firingHistorySpan), now)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: unable to read alert firing history", "error", err)
		return ""
	}
	if event.StartsAt != nil && !slices.ContainsFunc(times, func(t time.Time) bool { return t.Equal(*event.StartsAt) }) && !event.StartsAt.After(now) {
		times = append(times, *event.StartsAt)
		slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })
	}
	if len(times) == 0 {
		return ""
	}
	return outlookLine(readFirings(times, now), fetchMemoryHeadroom(ctx, accountId, event, now), now)
}

// fetchMemoryHeadroom reads the hourly memory peaks, over the 3 days up to now,
// of the container an OOM kill hit, or nil when the event names no OOM-killed
// container of a workload or the forecast has no answer.
func fetchMemoryHeadroom(ctx *security.RequestContext, accountId string, event events.Event, now time.Time) *memoryHeadroom {
	kind := strings.ToLower(event.SubjectOwnerKind)
	if event.SubjectOwner == "" || event.SubjectNamespace == "" || !slices.Contains([]string{"deployment", "statefulset", "daemonset"}, kind) {
		return nil
	}
	container := oomKilledContainer(event.Evidences)
	if container == "" {
		return nil
	}
	qctx, cancel := context.WithTimeout(ctx.GetContext(), outlookFetchTimeout)
	defer cancel()
	rctx := security.NewRequestContext(qctx, ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
	raw, err := services_server.GenerateForecast(*rctx, services_server.ForecastRequest{
		AccountId: accountId, WorkloadNamespace: event.SubjectNamespace, WorkloadName: event.SubjectOwner,
		Kind: kind, Metric: "memory_saturation", Container: container, EndTime: now.UnixMilli(),
	})
	if err != nil {
		ctx.GetLogger().Warn("analyzer: memory headroom not read", "error", err, "workload", event.SubjectOwner, "container", container)
		return nil
	}
	var f struct {
		Status         string     `json:"status"`
		ThresholdPct   float64    `json:"threshold_pct"`
		CurrentPct     float64    `json:"current_pct"`
		SlopePctPerDay float64    `json:"slope_pct_per_day"`
		ETAAt          *time.Time `json:"eta_at"`
		PerPodLimit    float64    `json:"per_pod_limit"`
		LimitUnit      string     `json:"limit_unit"`
		Daily          []struct {
			Date      string  `json:"date"`
			ValuePct  float64 `json:"value_pct"`
			Projected bool    `json:"projected"`
		} `json:"daily"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		ctx.GetLogger().Warn("analyzer: memory headroom unreadable", "error", err)
		return nil
	}
	if !slices.Contains([]string{"projected", "not_projected", "breached"}, f.Status) || f.LimitUnit != "MiB" || f.PerPodLimit <= 0 {
		ctx.GetLogger().Info("analyzer: memory headroom has no answer", "status", f.Status, "workload", event.SubjectOwner)
		return nil
	}
	m := &memoryHeadroom{container: container, limitMiB: f.PerPodLimit, currentPct: f.CurrentPct, slopePerDay: f.SlopePctPerDay, thresholdPct: f.ThresholdPct}
	if f.Status == "projected" {
		m.etaAt = f.ETAAt
	}
	if len(f.Daily) > 0 && !f.Daily[0].Projected {
		m.firstPct, m.firstDate = f.Daily[0].ValuePct, f.Daily[0].Date
	}
	return m
}

// oomKilledContainer finds, in the event's evidence, the container whose last
// termination was an OOM kill: a containerStatuses entry naming it.
func oomKilledContainer(evidence events.InvestigateData) string {
	raw, err := json.Marshal(evidence)
	if err != nil {
		return ""
	}
	var doc any
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	var found string
	var walk func(v any)
	walk = func(v any) {
		if found != "" {
			return
		}
		switch t := v.(type) {
		case map[string]any:
			if name, ok := t["name"].(string); ok && name != "" && terminatedOOM(t) {
				found = name
				return
			}
			for _, c := range t {
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		case string:
			// Pod objects are sometimes carried as JSON text inside the evidence.
			if strings.Contains(t, "OOMKilled") && strings.HasPrefix(strings.TrimSpace(t), "{") {
				var inner any
				if json.Unmarshal([]byte(t), &inner) == nil {
					walk(inner)
				}
			}
		}
	}
	walk(doc)
	return found
}

// terminatedOOM reports whether a container status ended, or last ended, in an
// OOM kill.
func terminatedOOM(status map[string]any) bool {
	for _, key := range []string{"lastState", "state", "last_state"} {
		st, _ := status[key].(map[string]any)
		term, _ := st["terminated"].(map[string]any)
		if reason, _ := term["reason"].(string); reason == "OOMKilled" {
			return true
		}
	}
	return false
}

// outlookLine turns the firing history and the memory headroom into the line.
// The verdict follows the SRE books: a repeat problem whose cause is still in
// place comes back; a first firing is not called recurring without a cause in
// place ("since it happened once, it must be happening again" is a pitfall).
func outlookLine(h firingHistory, m *memoryHeadroom, now time.Time) string {
	var verdict string
	var parts []string
	causeInPlace := m != nil && (m.etaAt != nil || m.currentPct >= memoryNearLimitPct)
	if m != nil {
		parts = append(parts, headroomWords(m))
	}
	switch {
	case causeInPlace:
		verdict = "Yes"
	case h.pattern == firingFirst || h.overdue:
		// A first firing, or a pattern whose next time has passed without a
		// firing: the history no longer says it comes back.
		verdict = "Not known"
	default:
		verdict = "Likely"
	}
	parts = append(parts, firingWords(h))
	return fmt.Sprintf("- **Will it happen again:** %s. %s (as of %s)", verdict, strings.Join(parts, " "), firingWhen(now))
}

func headroomWords(m *memoryHeadroom) string {
	limit := fmt.Sprintf("%g MiB", math.Round(m.limitMiB))
	peaks := fmt.Sprintf("Container %s's hourly memory peaks are at %s%% of its %s limit", m.container, fmtPct(m.currentPct), limit)
	if m.firstDate != "" && math.Abs(m.currentPct-m.firstPct) >= 1 {
		peaks += fmt.Sprintf(" (%s%% on %s)", fmtPct(m.firstPct), m.firstDate)
	}
	switch {
	case m.etaAt != nil:
		return peaks + fmt.Sprintf(", rising about %s points a day; at that rate they reach %s%% of it around %s.", fmtPct(m.slopePerDay), fmtPct(m.thresholdPct), firingWhen(*m.etaAt))
	case m.currentPct >= memoryNearLimitPct:
		return peaks + ", so any spike above the usual peak can reach the limit again while it stays at " + limit + "."
	default:
		return peaks + ", so this kill came from a spike well above the usual peak."
	}
}

func firingWords(h firingHistory) string {
	switch h.pattern {
	case firingFirst:
		return "This is the first firing on this workload in 30 days."
	case firingFew:
		return fmt.Sprintf("It fired %d times on this workload in 30 days (gaps %s), too few to show a pattern.", h.count, firingGaps(h.gaps))
	case firingRegular:
		s := fmt.Sprintf("It fires about every %s (%d times in 30 days); next around %s (between %s and %s).",
			firingSpan(h.every), h.count, firingWhen(h.next), firingWhen(h.lo), firingWhen(h.hi))
		if h.overdue {
			s += " That window has passed without a firing, so the cadence may have stopped."
		}
		return s
	case firingBurst:
		s := fmt.Sprintf("It fires in bursts (%d times in 30 days, latest gaps %s); likely again by %s.", h.count, firingGaps(h.gaps), firingWhen(h.by))
		if h.overdue {
			s += " That time has passed without a firing, so this burst has likely ended; when the next starts is not regular."
		}
		return s
	default:
		s := fmt.Sprintf("It fired %d times on this workload in 30 days at irregular gaps (latest %s), so no time can be given; it has come back within %s so far, which would be by %s.",
			h.count, firingGaps(h.gaps), firingSpan(slices.Max(h.gaps)), firingWhen(h.by))
		if h.overdue {
			s += " That time has passed without a firing."
		}
		return s
	}
}

// outlookLineRe matches an outlook line the agent or an earlier run wrote.
var outlookLineRe = regexp.MustCompile(`(?m)^[ \t]*[-*][ \t]+\*\*(Getting worse|Will it happen again):\*\*.*\n?`)

// outlookAsOfRe reads the time an outlook line was computed for.
var outlookAsOfRe = regexp.MustCompile(`\*\*Will it happen again:\*\*.*\(as of (\w+ \d+ \d\d:\d\d) UTC\)`)

// outlookLineCurrent reports whether report already has an outlook line
// computed at or after the firing that started at startsAt: then a re-poll, or
// another firing reusing the report, keeps it. An agent-written "Getting
// worse" line never counts.
func outlookLineCurrent(report string, startsAt *time.Time) bool {
	m := outlookAsOfRe.FindStringSubmatch(report)
	if m == nil {
		return false
	}
	if startsAt == nil {
		return true
	}
	start := startsAt.UTC()
	asOf, err := time.Parse("2006 Jan 2 15:04", fmt.Sprintf("%d %s", start.Year(), m[1]))
	if err != nil {
		return false
	}
	// The line carries no year: put it in the year that keeps it nearest the
	// firing, across a new year either way.
	if d := asOf.Sub(start); d > 180*24*time.Hour {
		asOf = asOf.AddDate(-1, 0, 0)
	} else if d < -180*24*time.Hour {
		asOf = asOf.AddDate(1, 0, 0)
	}
	return !asOf.Before(start.Truncate(time.Minute))
}

// withOutlookLine puts line into the report's Quick Investigation section after
// "Hurting now", replacing any outlook line already there. Without that
// section, the line opens the report after the Event Summary.
func withOutlookLine(report, line string) string {
	if line == "" {
		return report
	}
	report = outlookLineRe.ReplaceAllString(report, "")
	lines := strings.Split(report, "\n")
	quick := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "### ") && strings.Contains(l, "Quick Investigation") })
	if quick < 0 {
		at := slices.IndexFunc(lines, func(l string) bool {
			return strings.HasPrefix(l, "### ") && !strings.Contains(l, "Event Summary")
		})
		block := []string{"### 🔍 Quick Investigation", line, ""}
		if at < 0 {
			return strings.TrimRight(report, "\n") + "\n\n" + strings.Join(block, "\n")
		}
		return strings.Join(slices.Insert(lines, at, block...), "\n")
	}
	at := quick + 1
	for i := quick + 1; i < len(lines) && !strings.HasPrefix(lines[i], "### "); i++ {
		if strings.Contains(lines[i], "**Hurting now:**") {
			// Past the bullet's wrapped continuation lines.
			for at = i + 1; at < len(lines) && strings.TrimSpace(lines[at]) != "" &&
				(lines[at][0] == ' ' || lines[at][0] == '\t') && !strings.HasPrefix(strings.TrimSpace(lines[at]), "- "); at++ {
			}
			break
		}
	}
	return strings.Join(slices.Insert(lines, at, line), "\n")
}

// mergeFirings folds firings closer than firingMergeGap to the previous one.
func mergeFirings(times []time.Time) []time.Time {
	if len(times) == 0 {
		return nil
	}
	occ := []time.Time{times[0].UTC()}
	for _, t := range times[1:] {
		if t.Sub(occ[len(occ)-1]) >= firingMergeGap {
			occ = append(occ, t.UTC())
		}
	}
	return occ
}

func medianDuration(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

func scaleDuration(d time.Duration, f float64) time.Duration {
	return time.Duration(float64(d) * f)
}

func firingGaps(gaps []time.Duration) string {
	out := make([]string, len(gaps))
	for i, g := range gaps {
		out[i] = firingSpan(g)
	}
	return strings.Join(out, ", ")
}

func firingSpan(d time.Duration) string {
	switch h := d.Hours(); {
	case h < 1.5:
		return fmt.Sprintf("%d min", int(math.Round(d.Minutes())))
	case h < 48:
		return fmt.Sprintf("%g hours", math.Round(h*10)/10)
	default:
		return fmt.Sprintf("%g days", math.Round(h/24*10)/10)
	}
}

func firingWhen(t time.Time) string {
	return t.UTC().Format("Jan 2 15:04 UTC")
}

func fmtPct(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
}

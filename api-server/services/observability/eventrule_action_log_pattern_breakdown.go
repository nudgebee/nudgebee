package observability

import (
	"fmt"
	"math"
	"nudgebee/services/eventrule/playbooks"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The error-log-spike breakdown: which error patterns rose, against what is
// normal for the workload.
//
// The alert this serves fires on a workload's total error/critical line count
// (the 5-minute increase against the 6-hour average per 5 minutes, measured 5
// minutes back), so the alert says that errors rose, not which ones.
// container_log_messages_total keeps one series per log pattern, so the same
// two measurements taken per pattern answer that.
//
// The windows and levels below mirror the built-in error-log-spike alert rule
// (HighErrorCriticalLogs in the agent chart's alert rules). If that rule's
// windows or levels change, change these with it.
const (
	logSpikeBucket   = 5 * time.Minute
	logSpikeBaseline = 6 * time.Hour
	// The rule holds for 10 minutes on a 5-minute window before it fires, and
	// delivery adds more, so the rise starts well before the alert does.
	logSpikeLead = 30 * time.Minute
	logSpikeTail = time.Hour

	logSpikeMaxPatterns  = 10
	logSpikeExampleRunes = 300
)

var logSpikeLevels = []string{"error", "critical"}

// logSpikeCountingNote says what the count is a count of. The level is set by
// the node agent's log parser (GuessLevel in the logparser module): a line's
// structured level field when it has one, otherwise a level word among the
// first few fields of the line. Nothing further into the line is looked at,
// which is why an access-log line with "error" in a URL is not counted and a
// line that opens with a word like "Critical..." is.
const logSpikeCountingNote = `Counts only log lines the log agent tagged error or critical. The tag comes from the line's own level field, or from a level word at the start of the line. A warning, or a line that only mentions "error" further in, is not counted.`

// LogPatternBreakdown is the card data under "pattern_breakdown". llm-server
// decodes it into its own mirror of this struct (events.LogPatternBreakdown)
// and renders it into the investigation prompt, so a field renamed here must
// be renamed there.
type LogPatternBreakdown struct {
	WindowStart   string `json:"window_start"`
	WindowEnd     string `json:"window_end"`
	BucketMinutes int    `json:"bucket_minutes"`
	BaselineHours int    `json:"baseline_hours"`
	// BaselineEvaluatedAt is the moment "normal" was measured at: the start of
	// the window, before the alert fired. The alert's own ratio is measured at
	// firing time, when its 6 hours already hold part of the spike, so the
	// ratios here are higher than the one in the alert's description.
	BaselineEvaluatedAt string `json:"baseline_evaluated_at"`
	// BaselineKnown is false when the workload has no log counts of any level
	// in the baseline hours — it is new, or was not being measured. Every
	// Normal is then 0 for want of data, not because nothing was logged, and
	// the peaks are all the card can state.
	BaselineKnown bool            `json:"baseline_known"`
	CountedLevels []string        `json:"counted_levels"`
	CountingNote  string          `json:"counting_note"`
	Total         LogPatternTotal `json:"total"`
	// PatternsSeen is how many patterns logged in the window; Patterns holds
	// the logSpikeMaxPatterns that rose most.
	PatternsSeen int              `json:"patterns_seen"`
	Patterns     []LogPatternRise `json:"patterns"`
}

// LogPatternTotal is the workload's whole error/critical count: the busiest
// bucket-long stretch in the window against the normal count per bucket. PeakAt
// is the end of that stretch.
type LogPatternTotal struct {
	Peak   float64 `json:"peak"`
	PeakAt string  `json:"peak_at"`
	Normal float64 `json:"normal"`
}

// LogPatternRise is one log pattern: its busiest bucket-long stretch in the
// window against its normal count per bucket. Normal is 0 for a pattern not
// seen in the baseline hours.
type LogPatternRise struct {
	PatternHash string  `json:"pattern_hash"`
	Level       string  `json:"level"`
	Peak        float64 `json:"peak"`
	PeakAt      string  `json:"peak_at"`
	Normal      float64 `json:"normal"`
	// Example is the pattern's sample line, cut to logSpikeExampleRunes. The
	// agent records it when it first sees the pattern, so a timestamp inside
	// it is not the time of the spike.
	Example     string `json:"example"`
	ContainerID string `json:"container_id"`

	sample string
}

// logSpikeQueries returns the per-pattern count per bucket, the per-pattern
// normal count per bucket, and the workload's count of lines of every level
// over the baseline hours. The last tells "no error was logged before" apart
// from "nothing was being counted before": both leave normal empty.
func logSpikeQueries(appID string) (current string, normal string, measured string) {
	app := sanitizePromQLLabel(appID)
	selector := fmt.Sprintf(`container_log_messages_total{app_id="%s", level=~"%s"}`, app, strings.Join(logSpikeLevels, "|"))
	bucketMinutes := int(logSpikeBucket / time.Minute)
	baselineHours := int(logSpikeBaseline / time.Hour)
	current = fmt.Sprintf(`sum by (pattern_hash, level, sample, container_id) (increase(%s[%dm]))`, selector, bucketMinutes)
	normal = fmt.Sprintf(`sum by (pattern_hash, level) (increase(%s[%dh] offset %dm)) / %d`,
		selector, baselineHours, bucketMinutes, int(logSpikeBaseline/logSpikeBucket))
	measured = fmt.Sprintf(`sum(increase(container_log_messages_total{app_id="%s"}[%dh] offset %dm))`, app, baselineHours, bucketMinutes)
	return current, normal, measured
}

// logSpikeWindow is the span the breakdown looks at: from before the rise
// began to an hour after the alert, never past now or the alert's end.
func logSpikeWindow(event playbooks.PlaybookEvent, now time.Time) (time.Time, time.Time) {
	alertStart := now
	if t := event.AlertStart(); t != nil && !t.IsZero() {
		alertStart = *t
	}
	start := alertStart.Add(-logSpikeLead)
	end := alertStart.Add(logSpikeTail)
	if event.EndedAt != nil && !event.EndedAt.IsZero() && event.EndedAt.Before(end) {
		end = *event.EndedAt
	}
	if now.Before(end) {
		end = now
	}
	if !end.After(start) {
		end = start.Add(logSpikeBucket)
	}
	return start, end
}

type logPatternKey struct {
	hash  string
	level string
}

type logPatternSeries struct {
	byTime      map[int64]float64
	bySample    map[string]float64
	byContainer map[string]float64
}

// buildLogPatternBreakdown turns the per-pattern counts (current) and the
// per-pattern normal counts (normal) into the breakdown. Returns nil when
// nothing was logged in the window, or when the normal counts were not read
// at the start of the window (see logPatternNormals).
//
// No normal series at all has two meanings, and measured — the workload's
// count of lines of every level over the baseline hours — settles which. With
// lines counted, no error or critical line was among them, so every pattern
// in the window is new. With none, the workload was not being counted and
// what is normal for it is unknown (BaselineKnown false). A baseline query
// that failed never reaches here; the caller skips the breakdown.
//
// current may be on any time grid. Each point is the count over the bucket
// ending at that point, so on a grid finer than the bucket the points overlap
// and the peak is the busiest bucket-long stretch, wherever it falls.
func buildLogPatternBreakdown(current []Result, normal []Result, measured []Result, start time.Time, end time.Time) *LogPatternBreakdown {
	normals, baselineAt, ok := logPatternNormals(normal, start)
	if !ok {
		return nil
	}
	baselineKnown := len(normals) > 0 || logCountsExistAt(measured, start)

	// One pattern arrives as several series (one per container, and one per
	// sample line when replicas recorded different ones), so add them up per
	// bucket before looking for the peak.
	patterns := map[logPatternKey]*logPatternSeries{}
	total := map[int64]float64{}
	for _, series := range current {
		key := logPatternKey{hash: series.Metric["pattern_hash"], level: series.Metric["level"]}
		for i, ts := range series.Timestamps {
			if i >= len(series.Values) {
				break
			}
			value := series.Values[i]
			if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
				continue
			}
			p := patterns[key]
			if p == nil {
				p = &logPatternSeries{byTime: map[int64]float64{}, bySample: map[string]float64{}, byContainer: map[string]float64{}}
				patterns[key] = p
			}
			p.byTime[ts] += value
			p.bySample[series.Metric["sample"]] += value
			p.byContainer[series.Metric["container_id"]] += value
			total[ts] += value
		}
	}
	if len(patterns) == 0 {
		return nil
	}

	rises := make([]LogPatternRise, 0, len(patterns))
	for key, p := range patterns {
		peak, peakAt := logPatternPeak(p.byTime)
		sample := largestKey(p.bySample)
		rises = append(rises, LogPatternRise{
			PatternHash: key.hash,
			Level:       key.level,
			Peak:        roundCount(peak),
			PeakAt:      formatLogSpikeTime(peakAt),
			Normal:      roundCount(normals[key]),
			Example:     truncateRunes(sample, logSpikeExampleRunes),
			ContainerID: largestKey(p.byContainer),
			sample:      sample,
		})
	}
	sort.Slice(rises, func(i, j int) bool {
		ri, rj := rises[i].Peak-rises[i].Normal, rises[j].Peak-rises[j].Normal
		if ri != rj {
			return ri > rj
		}
		if rises[i].Peak != rises[j].Peak {
			return rises[i].Peak > rises[j].Peak
		}
		if rises[i].PatternHash != rises[j].PatternHash {
			return rises[i].PatternHash < rises[j].PatternHash
		}
		return rises[i].Level < rises[j].Level
	})
	seen := len(rises)
	if len(rises) > logSpikeMaxPatterns {
		rises = rises[:logSpikeMaxPatterns]
	}

	// The workload's normal is every pattern's normal, including patterns that
	// stayed quiet in the window: that is the denominator the alert uses.
	totalNormal := 0.0
	for _, value := range normals {
		totalNormal += value
	}
	totalPeak, totalPeakAt := logPatternPeak(total)

	return &LogPatternBreakdown{
		WindowStart:         start.UTC().Format(time.RFC3339),
		WindowEnd:           end.UTC().Format(time.RFC3339),
		BucketMinutes:       int(logSpikeBucket / time.Minute),
		BaselineHours:       int(logSpikeBaseline / time.Hour),
		BaselineEvaluatedAt: formatLogSpikeTime(baselineAt),
		BaselineKnown:       baselineKnown,
		CountedLevels:       logSpikeLevels,
		CountingNote:        logSpikeCountingNote,
		Total:               LogPatternTotal{Peak: roundCount(totalPeak), PeakAt: formatLogSpikeTime(totalPeakAt), Normal: roundCount(totalNormal)},
		PatternsSeen:        seen,
		Patterns:            rises,
	}
}

// logPatternNormals reads each pattern's normal count at the start of the
// window, and returns the earliest timestamp it was read at.
//
// The point is matched by time, not taken as the series' first: a normal read
// once the spike is under way already holds the spike. One bucket of tolerance
// either side allows for a backend that aligns the evaluation time.
//
// ok is false when series came back but none was read near the window start —
// the backend evaluated somewhere else, and calling every pattern new on that
// basis would be wrong. No series at all is ok: nothing was logged before.
func logPatternNormals(normal []Result, start time.Time) (normals map[logPatternKey]float64, baselineAt int64, ok bool) {
	tolerance := int64(logSpikeBucket / time.Second)
	normals = map[logPatternKey]float64{}
	for _, series := range normal {
		for i, ts := range series.Timestamps {
			if i >= len(series.Values) {
				break
			}
			if ts < start.Unix()-tolerance || ts > start.Unix()+tolerance {
				continue
			}
			value := series.Values[i]
			if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
				break
			}
			key := logPatternKey{hash: series.Metric["pattern_hash"], level: series.Metric["level"]}
			normals[key] += value
			if baselineAt == 0 || ts < baselineAt {
				baselineAt = ts
			}
			break
		}
	}
	if baselineAt == 0 {
		return normals, start.Unix(), len(normal) == 0
	}
	return normals, baselineAt, true
}

// logCountsExistAt reports whether measured holds a positive count read at the
// start of the window.
func logCountsExistAt(measured []Result, start time.Time) bool {
	tolerance := int64(logSpikeBucket / time.Second)
	for _, series := range measured {
		for i, ts := range series.Timestamps {
			if i >= len(series.Values) {
				break
			}
			if ts < start.Unix()-tolerance || ts > start.Unix()+tolerance {
				continue
			}
			if value := series.Values[i]; !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0 {
				return true
			}
		}
	}
	return false
}

// logPatternPeak returns the largest bucket and its time; the earliest wins a tie.
func logPatternPeak(byTime map[int64]float64) (float64, int64) {
	peak, peakAt := 0.0, int64(0)
	for ts, value := range byTime {
		if value > peak || (value == peak && ts < peakAt) {
			peak, peakAt = value, ts
		}
	}
	return peak, peakAt
}

// largestKey returns the key with the largest value; the smallest key wins a tie.
func largestKey(values map[string]float64) string {
	best, bestValue := "", -1.0
	for key, value := range values {
		if value > bestValue || (value == bestValue && key < best) {
			best, bestValue = key, value
		}
	}
	return best
}

// roundCount keeps a count readable without rounding a small one away. A
// pattern that logged once in the baseline hours has a normal near 0.014 per
// bucket; rounded to 0 it would read as never seen.
func roundCount(value float64) float64 {
	if value <= 0 {
		return 0
	}
	if value >= 10 {
		return math.Round(value*10) / 10
	}
	if rounded := math.Round(value*1000) / 1000; rounded > 0 {
		return rounded
	}
	return 0.001
}

func formatLogSpikeTime(unixSeconds int64) string {
	if unixSeconds == 0 {
		return ""
	}
	return time.Unix(unixSeconds, 0).UTC().Format(time.RFC3339)
}

func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit]) + "…"
}

// topLabels returns the container and sample line of the pattern that rose
// most — the labels the event carries for an error-log-spike alert.
func (b *LogPatternBreakdown) topLabels() map[string]any {
	labels := map[string]any{}
	if len(b.Patterns) == 0 {
		return labels
	}
	top := b.Patterns[0]
	if top.ContainerID != "" {
		labels["container_id"] = top.ContainerID
	}
	if top.sample != "" {
		labels["sample"] = top.sample
	}
	return labels
}

// summary is the one-line insight for surfaces that show insights rather than
// the card: how far the total rose and which pattern drove it.
func (b *LogPatternBreakdown) summary() string {
	line := fmt.Sprintf("Error and critical log lines peaked at %s per %d min against %s normal.",
		formatLogCount(b.Total.Peak), b.BucketMinutes, formatLogCount(b.Total.Normal))
	switch {
	case !b.BaselineKnown:
		line = fmt.Sprintf("Error and critical log lines peaked at %s per %d min. The workload has no log counts in the %d h before, so what is normal for it is unknown.",
			formatLogCount(b.Total.Peak), b.BucketMinutes, b.BaselineHours)
	case b.Total.Normal == 0:
		line = fmt.Sprintf("Error and critical log lines peaked at %s per %d min; none were logged in the %d h before.",
			formatLogCount(b.Total.Peak), b.BucketMinutes, b.BaselineHours)
	}
	if len(b.Patterns) == 0 {
		return line
	}
	top := b.Patterns[0]
	if b.Total.Normal == 0 {
		return line + fmt.Sprintf(" The busiest pattern is %s-level: %s per %d min.", top.Level, formatLogCount(top.Peak), b.BucketMinutes)
	}
	if top.Normal == 0 {
		return line + fmt.Sprintf(" The pattern that rose most is %s-level: %s per %d min, not seen in the %d h before.",
			top.Level, formatLogCount(top.Peak), b.BucketMinutes, b.BaselineHours)
	}
	return line + fmt.Sprintf(" The pattern that rose most is %s-level: %s per %d min against %s normal.",
		top.Level, formatLogCount(top.Peak), b.BucketMinutes, formatLogCount(top.Normal))
}

func formatLogCount(value float64) string {
	switch {
	case value == math.Trunc(value):
		return fmt.Sprintf("%.0f", value)
	case value >= 1:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", value), ".0")
	default:
		return strconv.FormatFloat(value, 'g', 2, 64)
	}
}

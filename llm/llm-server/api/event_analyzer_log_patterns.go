package api

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/events"
)

// Bounds on the error-log-spike breakdown rendered into the investigation
// prompt. The card holds more patterns than this; the rest are in the
// structured evidence workspace file.
const (
	maxLogPatternBreakdownBytes    = 2000
	maxLogPatternBreakdownPatterns = 5
	maxLogPatternExampleBytes      = 140
	maxLogPatternCountingNoteBytes = 400
	// maxLogPatternFieldBytes bounds a short field (a level, a time that did
	// not parse) printed as stored. The card is read back from the database,
	// so nothing in it is trusted to be the size it should be.
	maxLogPatternFieldBytes = 32
)

// renderLogPatternBreakdown renders which error log patterns rose against
// normal for an error-log-spike alert: what the alert counts, how far the total
// rose, and the patterns that rose most with when each peaked. Returns "" for
// an event that has no breakdown.
//
// It has its own section rather than a line in Collected Insights because it
// is the evidence the alert is about. Without it the investigation had one
// example line to go on, and that line is whichever pattern was logging most
// when the alert was processed, recorded when the pattern was first seen.
func renderLogPatternBreakdown(ev events.InvestigateData) string {
	breakdown, ok := events.DecodeLogPatternBreakdown(ev.LogPatternBreakdown.Data)
	if !ok {
		return ""
	}
	bucket, hours := breakdown.BucketMinutes, breakdown.BaselineHours
	known := breakdown.BaselineKnown

	var head strings.Builder
	if note := strings.TrimSpace(breakdown.CountingNote); note != "" {
		head.WriteString(core.TruncateHead(note, maxLogPatternCountingNoteBytes))
		head.WriteString("\n")
	}
	fmt.Fprintf(&head, "Window %s UTC. Counts are per %d minutes; a time below is the end of those %d minutes.\n",
		logPatternWindow(breakdown.WindowStart, breakdown.WindowEnd), bucket, bucket)
	switch {
	case !known:
		fmt.Fprintf(&head, "The workload has no log counts of any level in the %d h before %s (it may be new), so what is normal for it is unknown. The counts below are peaks only.\n",
			hours, logPatternClock(breakdown.BaselineEvaluatedAt))
		fmt.Fprintf(&head, "Total: peak %s per %d min at %s.\n",
			logPatternCount(breakdown.Total.Peak), bucket, logPatternClock(breakdown.Total.PeakAt))
	case breakdown.Total.Normal > 0:
		fmt.Fprintf(&head, "Normal is the average per %d min over the %d h before %s. It was measured before the alert fired, so a ratio here can be higher than the one in the alert's description, whose %d h already held the start of the spike.\n",
			bucket, hours, logPatternClock(breakdown.BaselineEvaluatedAt), hours)
		fmt.Fprintf(&head, "Total: peak %s per %d min at %s vs %s normal (%s×).\n",
			logPatternCount(breakdown.Total.Peak), bucket, logPatternClock(breakdown.Total.PeakAt),
			logPatternCount(breakdown.Total.Normal), logPatternRatio(breakdown.Total.Peak, breakdown.Total.Normal))
	default:
		fmt.Fprintf(&head, "Normal is the average per %d min over the %d h before %s.\n",
			bucket, hours, logPatternClock(breakdown.BaselineEvaluatedAt))
		fmt.Fprintf(&head, "Total: peak %s per %d min at %s; no error or critical line in the %d h before.\n",
			logPatternCount(breakdown.Total.Peak), bucket, logPatternClock(breakdown.Total.PeakAt), hours)
	}
	const tail = "An example line shows what a pattern looks like; a time inside it is when the pattern was first seen, not the spike.\n"

	// A pattern logging at or below its own normal did not rise and is not
	// listed. With no known normal there is nothing to rise above, so the
	// busiest are listed instead.
	var candidates []events.LogPatternRise
	for _, p := range breakdown.Patterns {
		if known && p.Peak <= p.Normal {
			continue
		}
		candidates = append(candidates, p)
	}
	seen := breakdown.PatternsSeen
	if seen < len(breakdown.Patterns) {
		seen = len(breakdown.Patterns)
	}
	if len(candidates) == 0 {
		return head.String() + fmt.Sprintf("No single pattern was above its own normal; %d seen in the window.\n", seen)
	}

	// Whole pattern lines only, so the list never ends mid-line; the heading
	// says how many of the patterns seen are shown. The first always goes in:
	// running out of room must not read as "no pattern rose".
	const headingReserve = 80
	budget := maxLogPatternBreakdownBytes - head.Len() - len(tail) - headingReserve
	var lines []string
	for _, p := range candidates {
		if len(lines) >= maxLogPatternBreakdownPatterns {
			break
		}
		line := logPatternLine(p, hours, known)
		if len(line) > budget && len(lines) > 0 {
			break
		}
		budget -= len(line)
		lines = append(lines, line)
	}
	heading := "Patterns that rose most"
	if !known {
		heading = "Busiest patterns"
	}
	return head.String() +
		fmt.Sprintf("%s (%d shown, %d seen in the window):\n", heading, len(lines), seen) +
		strings.Join(lines, "") + tail
}

func logPatternLine(p events.LogPatternRise, baselineHours int, baselineKnown bool) string {
	example := core.TruncateHead(logPatternText(p.Example), maxLogPatternExampleBytes)
	var b strings.Builder
	fmt.Fprintf(&b, "- %s: peak %s at %s", core.TruncateHead(logPatternText(p.Level), maxLogPatternFieldBytes),
		logPatternCount(p.Peak), logPatternClock(p.PeakAt))
	switch {
	case !baselineKnown:
	case p.Normal > 0:
		fmt.Fprintf(&b, " vs %s normal (%s×)", logPatternCount(p.Normal), logPatternRatio(p.Peak, p.Normal))
	default:
		fmt.Fprintf(&b, ", not seen in the %d h before", baselineHours)
	}
	if example != "" {
		fmt.Fprintf(&b, " — e.g. `%s`", example)
	}
	b.WriteString("\n")
	return b.String()
}

// logPatternText makes stored text safe to put on one line inside backticks.
func logPatternText(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ", "`", "'").Replace(strings.TrimSpace(s))
}

// logPatternWindow renders "2026-01-15 10:00 → 10:20", repeating the date on
// the end only when the window crosses midnight.
func logPatternWindow(start, end string) string {
	from, errFrom := time.Parse(time.RFC3339, start)
	to, errTo := time.Parse(time.RFC3339, end)
	if errFrom != nil || errTo != nil {
		return logPatternClock(start) + " → " + logPatternClock(end)
	}
	from, to = from.UTC(), to.UTC()
	if from.Format("2006-01-02") == to.Format("2006-01-02") {
		return from.Format("2006-01-02 15:04") + " → " + to.Format("15:04")
	}
	return from.Format("2006-01-02 15:04") + " → " + to.Format("2006-01-02 15:04")
}

// logPatternClock renders a time of day. A value that is not a time is printed
// as stored, cut short.
func logPatternClock(at string) string {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return core.TruncateHead(logPatternText(at), maxLogPatternFieldBytes)
	}
	return t.UTC().Format("15:04")
}

// logPatternCount prints a count without rounding a small one to nothing: a
// pattern seen once in the baseline hours has a normal near 0.014.
func logPatternCount(value float64) string {
	switch {
	case math.Abs(value) >= 1e9:
		return strconv.FormatFloat(value, 'g', 3, 64)
	case value == math.Trunc(value):
		return fmt.Sprintf("%.0f", value)
	case value >= 1:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", value), ".0")
	default:
		return strconv.FormatFloat(value, 'g', 2, 64)
	}
}

func logPatternRatio(peak, normal float64) string {
	ratio := peak / normal
	switch {
	case ratio >= 1e9:
		return strconv.FormatFloat(ratio, 'g', 3, 64)
	case ratio >= 10:
		return fmt.Sprintf("%.0f", ratio)
	default:
		return fmt.Sprintf("%.1f", ratio)
	}
}

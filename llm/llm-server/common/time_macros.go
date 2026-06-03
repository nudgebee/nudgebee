package common

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Parse pattern like [[Time:-15m]] or [[Time:Now]]
var macroRegex = regexp.MustCompile(`\[\[Time:(.*?)\]\]`)

// dayWeekRegex matches integer day ("30d") or week ("2w") tokens inside a
// duration string. Go's time.ParseDuration only supports up to hours, so these
// are expanded to hours before parsing.
var dayWeekRegex = regexp.MustCompile(`(\d+)([dw])`)

// Parse pattern like $1, $2, $3
var sqlPlaceholderRegex = regexp.MustCompile(`\$\d+`)

// expandDayWeekUnits rewrites day/week tokens to their hour equivalents so the
// result is parseable by time.ParseDuration: "-30d" -> "-720h", "-2w" -> "-336h",
// "-1d12h" -> "-24h12h". Tokens with other units (s/m/h) are left untouched.
func expandDayWeekUnits(s string) string {
	return dayWeekRegex.ReplaceAllStringFunc(s, func(tok string) string {
		m := dayWeekRegex.FindStringSubmatch(tok)
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return tok
		}
		hours := n * 24
		if m[2] == "w" {
			hours = n * 24 * 7
		}
		return strconv.Itoa(hours) + "h"
	})
}

func SubstituteDateMacros(input string) string {
	return macroRegex.ReplaceAllStringFunc(input, func(match string) string {
		// Extract content inside [[Time:...]]
		inner := match[7 : len(match)-2]

		if strings.EqualFold(inner, "Now") {
			return time.Now().UTC().Format(time.RFC3339)
		}

		// Parse duration (e.g., "-1h", "-15m", "-30d", "-2w"). Day/week units are
		// not native to time.ParseDuration, so expand them to hours first.
		duration, err := time.ParseDuration(expandDayWeekUnits(inner))
		if err != nil {
			// If parse fails, return original or log warning
			return match
		}

		// Calculate time
		targetTime := time.Now().UTC().Add(duration)
		return targetTime.Format(time.RFC3339)
	})
}

// SubstituteSqlMacros replaces $1, $2, etc. with dummy values ('example_value')
// to allow EXPLAIN/ANALYZE to run on query patterns.
func SubstituteSqlMacros(input string) string {
	return sqlPlaceholderRegex.ReplaceAllString(input, "'example_value'")
}

// PresentationTimeLayout is the user-facing timestamp layout used in
// analysis, summaries, and any text rendered to end users.
// Example: "Mar 09, 2026 09:56:14 UTC".
const PresentationTimeLayout = "Jan 02, 2006 15:04:05 UTC"

// FormatPresentationTime renders a *time.Time in a clean, user-facing form
// suitable for investigation analysis output. Passes the value through .UTC()
// so the output is always in UTC regardless of the source location.
// Returns "unknown" for a nil pointer — callers that need "" should check
// themselves.
func FormatPresentationTime(t *time.Time) string {
	if t == nil {
		return "unknown"
	}
	return t.UTC().Format(PresentationTimeLayout)
}

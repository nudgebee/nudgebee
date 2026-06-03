package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSubstituteDateMacros(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string // Expected output (approximate for dynamic times)
	}{
		{
			name:     "Substitute Now",
			input:    "Current time is [[Time:Now]]",
			expected: "Current time is " + time.Now().UTC().Format(time.RFC3339),
		},
		{
			name:     "Substitute Past Time",
			input:    "One hour ago was [[Time:-1h]]",
			expected: "One hour ago was ", // Checked in logic below
		},
		{
			name:     "No Substitution",
			input:    "Just a string",
			expected: "Just a string",
		},
		{
			name:     "Invalid Duration",
			input:    "Invalid [[Time:invalid]]",
			expected: "Invalid [[Time:invalid]]",
		},
		{
			name:     "Multiple Substitutions",
			input:    "Start: [[Time:-1h]], End: [[Time:Now]]",
			expected: "Start: ", // Checked below
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := SubstituteDateMacros(tc.input)

			switch tc.name {
			case "Substitute Now":
				// Allow small difference due to execution time
				parsedResult, _ := time.Parse(time.RFC3339, result[16:])
				parsedExpected, _ := time.Parse(time.RFC3339, tc.expected[16:])
				assert.WithinDuration(t, parsedExpected, parsedResult, time.Second)
			case "Substitute Past Time":
				// Extract timestamp
				timeStr := result[17:]
				parsedResult, err := time.Parse(time.RFC3339, timeStr)
				assert.NoError(t, err)
				expectedTime := time.Now().UTC().Add(-1 * time.Hour)
				assert.WithinDuration(t, expectedTime, parsedResult, time.Second)
			case "Multiple Substitutions":
				// Check structure and parsability
				assert.Contains(t, result, "Start: ")
				assert.Contains(t, result, ", End: ")
			default:
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}

func TestExpandDayWeekUnits(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "days", input: "-30d", expected: "-720h"},
		{name: "single day", input: "-1d", expected: "-24h"},
		{name: "weeks", input: "-2w", expected: "-336h"},
		{name: "mixed day and hour", input: "-1d12h", expected: "-24h12h"},
		{name: "hours untouched", input: "-1h", expected: "-1h"},
		{name: "minutes untouched", input: "-15m", expected: "-15m"},
		{name: "no duration token", input: "Now", expected: "Now"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, expandDayWeekUnits(tc.input))
		})
	}
}

// TestSubstituteDateMacros_DaysWeeks covers the day/week units that
// time.ParseDuration cannot parse natively. Before the expandDayWeekUnits fix,
// "[[Time:-30d]]" was emitted verbatim and broke downstream SQL timestamp casts.
func TestSubstituteDateMacros_DaysWeeks(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		offset time.Duration
	}{
		{name: "30 days", input: "[[Time:-30d]]", offset: -30 * 24 * time.Hour},
		{name: "7 days", input: "[[Time:-7d]]", offset: -7 * 24 * time.Hour},
		{name: "2 weeks", input: "[[Time:-2w]]", offset: -2 * 7 * 24 * time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := SubstituteDateMacros(tc.input)
			parsed, err := time.Parse(time.RFC3339, result)
			assert.NoError(t, err, "macro should resolve to an RFC3339 timestamp, got %q", result)
			assert.WithinDuration(t, time.Now().UTC().Add(tc.offset), parsed, time.Minute)
		})
	}
}

func TestFormatPresentationTime(t *testing.T) {
	utc := time.FixedZone("UTC", 0)
	pst := time.FixedZone("PST", -8*3600)

	ts := time.Date(2026, time.March, 9, 9, 56, 14, 723583000, utc)
	tsPST := time.Date(2026, time.March, 9, 1, 56, 14, 0, pst) // same instant as 09:56:14 UTC

	tests := []struct {
		name     string
		input    *time.Time
		expected string
	}{
		{
			name:     "nil pointer yields unknown",
			input:    nil,
			expected: "unknown",
		},
		{
			name:     "UTC time with microseconds is cleaned",
			input:    &ts,
			expected: "Mar 09, 2026 09:56:14 UTC",
		},
		{
			name:     "non-UTC input is converted to UTC",
			input:    &tsPST,
			expected: "Mar 09, 2026 09:56:14 UTC",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, FormatPresentationTime(tc.input))
		})
	}
}

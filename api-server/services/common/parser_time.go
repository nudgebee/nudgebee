package common

import (
	json1 "encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var layouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05Z0700", // RFC3339 without colon in offset
	"2006-01-02T15:04:05",      // ISO8601 without timezone
	"2006-01-02",               // Date only
	"15:04:05",                 // Time only
}

// HasTimezoneIndicator checks if a timestamp string has a timezone indicator (Z or ±offset),
// skipping the date portion (YYYY-MM-DD) to avoid false positives from date hyphens.
func HasTimezoneIndicator(ts string) bool {
	if strings.HasSuffix(ts, "Z") {
		return true
	}
	if len(ts) > 10 {
		return strings.ContainsAny(ts[10:], "+-")
	}
	return false
}

func ParseTimeValue(value any) (time.Time, error) {
	switch v := value.(type) {
	case time.Time:
		return v, nil
	case string:
		for _, layout := range layouts {
			if t, err := time.ParseInLocation(layout, v, time.UTC); err == nil {
				return t, nil
			}
		}

		// Try to parse as a Unix timestamp string.
		unixTime, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			// If it's not a standard format or a number, we can't parse it.
			return time.Time{}, fmt.Errorf("unsupported string time format: '%s'", v)
		}
		// If it parses as a number, use the numeric timestamp parser.
		return ParseUnixTimestamp(unixTime), nil

	case float64:
		return ParseUnixTimestamp(int64(v)), nil
	case int:
		return ParseUnixTimestamp(int64(v)), nil
	case int64:
		return ParseUnixTimestamp(v), nil
	case json1.Number:
		unixTime, err := v.Int64()
		if err != nil {
			// It might be a float, try that.
			unixFloat, errFloat := v.Float64()
			if errFloat != nil {
				return time.Time{}, fmt.Errorf("could not parse json.Number '%s' as a number: %v", v, err)
			}
			return ParseUnixTimestamp(int64(unixFloat)), nil
		}
		return ParseUnixTimestamp(unixTime), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported type for time value: %T", v)
	}
}

// ParseUnixTimestamp heuristically determines the unit of a numeric timestamp
// (seconds, milliseconds, or nanoseconds) and converts it to a time.Time.
func ParseUnixTimestamp(ts int64) time.Time {
	// A simple heuristic to guess the unit of the timestamp.
	// Current time in seconds is ~1.7e9.
	// Current time in milliseconds is ~1.7e12.
	// Current time in nanoseconds is ~1.7e18.
	// We can use orders of magnitude to guess.
	if ts > 1e15 { // If it's a large number, it's likely in nanoseconds.
		return time.Unix(0, ts)
	}
	if ts > 1e12 { // If it's moderately large, it's likely in milliseconds.
		return time.UnixMilli(ts)
	}
	// Otherwise, assume it's in seconds.
	return time.Unix(ts, 0)
}

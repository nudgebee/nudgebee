package common

import (
	"fmt"
	"time"
)

// ParseTime attempts to parse a time value from various input types (string, time.Time, int64).
// It supports RFC3339 and other common layouts for strings.
func ParseTime(val any) (time.Time, error) {
	if val == nil {
		return time.Time{}, nil
	}

	switch v := val.(type) {
	case time.Time:
		return v, nil
	case *time.Time:
		if v == nil {
			return time.Time{}, nil
		}
		return *v, nil
	case string:
		// Try RFC3339 first (most common)
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, nil
		}
		// Try RFC3339Nano
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t, nil
		}
		// Try RFC3339 with offset without colon (e.g. +0000)
		if t, err := time.Parse("2006-01-02T15:04:05.999999999Z0700", v); err == nil {
			return t, nil
		}
		// Try other common formats if needed
		if t, err := time.Parse("2006-01-02", v); err == nil {
			return t, nil
		}
		if t, err := time.Parse("2006-01-02T15:04:05.999999999", v); err == nil {
			return t, nil
		}
		if t, err := time.Parse("2006-01-02T15:04:05", v); err == nil {
			return t, nil
		}
		// Try default Go string format (2006-01-02 15:04:05.999999999 -0700 MST)
		if t, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", v); err == nil {
			return t, nil
		}
		return time.Time{}, fmt.Errorf("unable to parse time string: %s", v)
	case int:
		return time.Unix(int64(v), 0), nil
	case int64:
		return time.Unix(v, 0), nil
	case float64:
		return time.Unix(int64(v), 0), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported type for time parsing: %T", v)
	}
}

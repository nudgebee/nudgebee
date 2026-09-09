package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResolveEndsAt(t *testing.T) {
	t.Run("uses the end time the integration reported", func(t *testing.T) {
		reported := time.Date(2026, 8, 31, 10, 30, 0, 0, time.UTC)
		got := resolveEndsAt(reported)
		assert.Equal(t, reported, got)
	})

	t.Run("falls back to now when the integration reported none", func(t *testing.T) {
		// PagerDuty's incident.resolved, ServiceNow and the generic webhook all
		// leave EventEndsAt zero — the delivery time is the best end time available.
		before := time.Now().UTC()
		got := resolveEndsAt(time.Time{})
		after := time.Now().UTC()

		assert.False(t, got.IsZero(), "must never write a zero end time")
		assert.False(t, got.Before(before))
		assert.False(t, got.After(after))
	})
}

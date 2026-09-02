package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMissRegionHint pins the empty-result message that bounds a live fallback to the
// account's real regions — the fix for the observed fan-out where, on a DB miss, the
// model scanned all ~16 regions and enumerated regions via shell (session 9ab533bd).
func TestMissRegionHint(t *testing.T) {
	t.Run("no known regions returns empty so caller keeps the plain message", func(t *testing.T) {
		assert.Equal(t, "", missRegionHint("payments-reconciler-v7", "Lambda", nil))
		assert.Equal(t, "", missRegionHint("payments-reconciler-v7", "Lambda", []string{}))
	})

	t.Run("service-scoped: lists only the account's regions and forbids other-region / shell scanning", func(t *testing.T) {
		msg := missRegionHint("payments-reconciler-v7", "Lambda", []string{"ap-south-1", "us-east-1"})
		assert.Contains(t, msg, "payments-reconciler-v7")
		assert.Contains(t, msg, "Lambda resources in these regions ONLY: ap-south-1, us-east-1")
		// The bound must be authoritative, not a soft hint: it closes the two escape
		// hatches we saw in the trace — trying other regions, and shell enumeration.
		assert.Contains(t, msg, "do NOT try other regions or enumerate regions via shell")
		assert.Contains(t, msg, "it does not exist")
	})

	t.Run("no service: account-wide phrasing (no dangling service word)", func(t *testing.T) {
		msg := missRegionHint("some-thing", "", []string{"us-east-1"})
		assert.Contains(t, msg, "This account has resources in these regions ONLY: us-east-1")
		assert.NotContains(t, msg, "  ") // no double space from an empty service scope
	})

	t.Run("empty resource name uses a generic match phrase (service/type-only search)", func(t *testing.T) {
		msg := missRegionHint("", "Lambda", []string{"us-east-1"})
		assert.Contains(t, msg, "Found 0 resources matching your query")
		assert.NotContains(t, msg, "matching ''") // no dangling empty-quote target
		assert.Contains(t, msg, "Lambda resources in these regions ONLY: us-east-1")
	})

	t.Run("regions are joined verbatim in the order given", func(t *testing.T) {
		msg := missRegionHint("x", "EC2", []string{"eu-north-1", "us-east-1", "us-west-2"})
		assert.True(t, strings.Contains(msg, "eu-north-1, us-east-1, us-west-2"))
	})
}

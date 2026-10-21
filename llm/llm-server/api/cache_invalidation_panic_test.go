package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The KB-sync fast path runs on its own goroutine; a panic there must be logged,
// not crash the process.
func TestEnsureIntegrationKBs_RecoversPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		ensureIntegrationKBs("acct-1", func(string) { panic("kb sync failed") })
	})
}

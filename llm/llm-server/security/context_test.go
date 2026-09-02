package security

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestDedupeAttrs(t *testing.T) {
	tests := []struct {
		name     string
		input    [][2]string
		expected [][2]string
	}{
		{
			name:     "no duplicates preserves order",
			input:    [][2]string{{"account_id", "a"}, {"user_id", "u"}, {"trace_id", "t"}},
			expected: [][2]string{{"account_id", "a"}, {"user_id", "u"}, {"trace_id", "t"}},
		},
		{
			name: "duplicate user_id keeps last value at first position",
			// Mirrors the real chain: a handler attaches user_id="" then buildContextFromPayload re-attaches the resolved id.
			input:    [][2]string{{"account_id", "a"}, {"user_id", ""}, {"tenant_id", "t"}, {"user_id", "resolved"}},
			expected: [][2]string{{"account_id", "a"}, {"user_id", "resolved"}, {"tenant_id", "t"}},
		},
		{
			name:     "multiple duplicates collapse to last value",
			input:    [][2]string{{"k", "1"}, {"k", "2"}, {"k", "3"}},
			expected: [][2]string{{"k", "3"}},
		},
		{
			name:     "empty input",
			input:    [][2]string{},
			expected: [][2]string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, dedupeAttrs(tc.input))
		})
	}
}

// TestGetSystemUserId_IsValidNonEmptyUUID guards the invariant the automated
// event-analysis fix relies on: the system user id must be a non-empty, valid
// UUID so it can be stored in uuid columns (an empty string fails with
// SQLSTATE 22P02). It is the value stamped onto userless/background contexts
// via NewRequestContextForTenantAdminWithUser.
func TestGetSystemUserId_IsValidNonEmptyUUID(t *testing.T) {
	got := GetSystemUserId()
	if got == "" {
		t.Fatal("GetSystemUserId() returned empty string; uuid columns would reject it (22P02)")
	}
	if _, err := uuid.Parse(got); err != nil {
		t.Fatalf("GetSystemUserId() = %q is not a valid UUID: %v", got, err)
	}
}

// TestEffectiveUserIdForRPC_CollapsesSystemSentinelToEmpty guards the fix for
// the fetch_logs 403 regression: automated/background contexts are stamped
// with GetSystemUserId() so DB writes succeed, but forwarding that sentinel
// as a real x-user-id header made api-server treat it as an unprivileged real
// user (zero user_roles grants) instead of taking its tenant-admin bypass
// branch, denying every account. EffectiveUserIdForRPC must collapse the
// sentinel to "" so that bypass branch fires; a real user id must pass
// through unchanged.
func TestEffectiveUserIdForRPC_CollapsesSystemSentinelToEmpty(t *testing.T) {
	systemCtx := &SecurityContext{userId: GetSystemUserId()}
	if got := systemCtx.EffectiveUserIdForRPC(); got != "" {
		t.Fatalf("EffectiveUserIdForRPC() with system sentinel = %q, want \"\"", got)
	}

	const realUserId = "11111111-1111-1111-1111-111111111111"
	realCtx := &SecurityContext{userId: realUserId}
	if got := realCtx.EffectiveUserIdForRPC(); got != realUserId {
		t.Fatalf("EffectiveUserIdForRPC() with real user id = %q, want %q", got, realUserId)
	}
}

// TestRequestContextAccessors_TolerateNilReceiver pins the nil-receiver behaviour of
// GetContext and GetLogger. Passing a nil *RequestContext is a caller bug, but these
// two accessors are reached from ~350 functions that take it as their first parameter,
// so the failure mode must be a usable default rather than a panic on a live request.
func TestRequestContextAccessors_TolerateNilReceiver(t *testing.T) {
	var rc *RequestContext

	assert.NotPanics(t, func() {
		assert.Equal(t, context.Background(), rc.GetContext())
		assert.NotNil(t, rc.GetLogger())
	})

	// A non-nil receiver with an unset context must not hand back a nil Context either,
	// since callers forward the result straight into langchaingo and the SDKs.
	empty := &RequestContext{}
	assert.Equal(t, context.Background(), empty.GetContext())
	assert.NotNil(t, empty.GetLogger())
}

// TestRequestContextGetLogger_ConcurrentAccess exercises the shared-RequestContext case
// under -race. A single RequestContext is shared across the goroutines running a parallel
// action batch, so GetLogger must not write to rc.logger on the fallback path.
func TestRequestContextGetLogger_ConcurrentAccess(t *testing.T) {
	rc := &RequestContext{} // logger deliberately unset, so every call takes the fallback

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NotNil(t, rc.GetLogger())
			assert.NotNil(t, rc.GetContext())
		}()
	}
	wg.Wait()
}

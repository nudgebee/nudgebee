package security

import (
	"testing"

	"github.com/google/uuid"
)

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

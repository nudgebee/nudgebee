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

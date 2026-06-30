package core

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"nudgebee/llm/security"
	"nudgebee/llm/security/egressfilter"
)

// TestAttachTenantIDForEgressFilter pins the contract for the wire-up at
// tryWithModel: when the security context carries a parseable UUID tenant
// id, the returned ctx must surface it via egressfilter.TenantIDFromContext;
// any failure mode (nil SC, empty id, non-UUID id) must leave ctx untouched
// so Resolve falls back to env defaults.
func TestAttachTenantIDForEgressFilter(t *testing.T) {
	validUUID := uuid.New()
	// NewSecurityContextForTenantAccountAdmin skips DB lookup when
	// accountIds is non-empty; a single dummy id is enough.
	scWith := func(tenantID string) *security.SecurityContext {
		return security.NewSecurityContextForTenantAccountAdmin(tenantID, "user-x", []string{"acct-x"})
	}

	cases := []struct {
		name          string
		sc            *security.SecurityContext
		wantID        uuid.UUID
		wantOK        bool
		wantUnchanged bool
	}{
		{
			name:          "nil SC leaves ctx untouched",
			sc:            nil,
			wantUnchanged: true,
		},
		{
			name:          "empty tenant id leaves ctx untouched",
			sc:            scWith(""),
			wantUnchanged: true,
		},
		{
			name:          "non-UUID tenant id leaves ctx untouched (fail-open)",
			sc:            scWith("not-a-uuid"),
			wantUnchanged: true,
		},
		{
			name:   "valid UUID tenant id is attached",
			sc:     scWith(validUUID.String()),
			wantID: validUUID,
			wantOK: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := context.Background()
			out := attachTenantIDForEgressFilter(base, c.sc)
			gotID, gotOK := egressfilter.TenantIDFromContext(out)

			if c.wantUnchanged {
				assert.False(t, gotOK, "expected ctx untouched, but tenant id was attached")
				// Same ctx reference when no attachment happened.
				assert.Equal(t, base, out)
				return
			}
			assert.True(t, gotOK)
			assert.Equal(t, c.wantID, gotID)
		})
	}
}

package testenv

import "testing"

// Obviously-fake, fixed identifiers for PURE-LOGIC unit tests that need a
// RequestContext or an ID argument but never touch a real backend. They are
// deliberately not real values so they can't be mistaken for production
// identifiers or leak environment details into source control.
//
// Tests that query a live backend must NOT use these — they must pull real
// identifiers from the environment via RequireTenant / RequireEnv so the real
// values stay in CI secrets and the test skips cleanly when unset.
const (
	FakeTenantID  = "11111111-1111-1111-1111-111111111111"
	FakeAccountID = "22222222-2222-2222-2222-222222222222"
	FakeUserID    = "33333333-3333-3333-3333-333333333333"

	// FakeAWSAccountID is AWS's documented example account number.
	FakeAWSAccountID = "123456789012"
)

// RequireTenantID guards just the TEST_TENANT variable and returns its value.
// Convenience wrapper for tests that only need the tenant id (e.g. callers of
// security.NewRequestContextForTenantAdmin).
func RequireTenantID(t testing.TB) string {
	t.Helper()
	return RequireEnv(t, Tenant)[Tenant]
}

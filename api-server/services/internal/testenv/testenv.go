// Package testenv provides helpers for tests that depend on a live backend or
// external credentials. When the required environment is absent, the helper
// skips the test instead of failing it, so the default `go test ./...` run
// stays self-contained and green. Integration runs supply the variables.
package testenv

import (
	"os"
	"strings"
	"testing"
)

// Common environment variable keys for the tenant/account/user triad that most
// backend-dependent tests rely on.
const (
	Tenant  = "TEST_TENANT"
	Account = "TEST_ACCOUNT"
	User    = "TEST_USER"
)

// RequireEnv returns the values of the named environment variables. If any are
// unset or empty it skips the test, naming the missing keys. The returned map
// is keyed by the requested variable names; callers may ignore it and keep
// reading the variables directly, since the guard guarantees they are set.
func RequireEnv(t testing.TB, keys ...string) map[string]string {
	t.Helper()
	vals := make(map[string]string, len(keys))
	var missing []string
	for _, k := range keys {
		v := os.Getenv(k)
		if v == "" {
			missing = append(missing, k)
		}
		vals[k] = v
	}
	if len(missing) > 0 {
		t.Skipf("skipping: requires environment variable(s) %s", strings.Join(missing, ", "))
	}
	return vals
}

// RequireTenant guards the common TEST_TENANT / TEST_ACCOUNT / TEST_USER triad
// and returns the three values. Use RequireEnv directly when a test needs only
// a subset.
func RequireTenant(t testing.TB) (tenant, account, user string) {
	t.Helper()
	v := RequireEnv(t, Tenant, Account, User)
	return v[Tenant], v[Account], v[User]
}

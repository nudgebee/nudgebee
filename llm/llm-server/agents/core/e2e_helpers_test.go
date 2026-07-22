//go:build e2e

package core

import (
	"os"
	"strings"
	"testing"
)

// RequireEnv skips the test unless every named environment variable is set
// (non-empty), reporting all the missing ones in a single consistent message.
// It returns the resolved values keyed by name, so callers can read them
// without a second os.Getenv.
//
// Use it for the credential/identifier guard at the top of an e2e test:
//
//	env := RequireEnv(t, "TEST_TENANT", "TEST_ACCOUNT", "TEST_USER")
//	accountID := env["TEST_ACCOUNT"]
//
// It replaces the ad-hoc `if os.Getenv("X") == "" { t.Skip(...) }` blocks that
// drifted in wording across the e2e suite. Boolean run-toggles
// (RUN_MEMORY_INTEGRATION="true", …) and live-resource reachability checks
// (GetConversationDao()==nil, metastore pings) are a different concern and stay
// as explicit checks at the call sites.
func RequireEnv(t *testing.T, names ...string) map[string]string {
	t.Helper()
	values := make(map[string]string, len(names))
	var missing []string
	for _, name := range names {
		v := os.Getenv(name)
		if v == "" {
			missing = append(missing, name)
		}
		values[name] = v
	}
	if len(missing) > 0 {
		t.Skipf("e2e: set %s to run", strings.Join(missing, ", "))
	}
	return values
}

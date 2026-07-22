package egressfilter

import (
	"os"
	"testing"
)

// TestMain seeds the package-level ActionGate for the whole test run so
// tests asserting block behaviour (`errors.Is(err, ErrSecretsBlocked)`)
// can rely on ModeEnforce actually resolving to ActionBlock without
// installing a gate per-function.
//
// Tests that exercise the nil-gate fallback explicitly clear the gate via
// clearActionGateForTest, isolating the dependency to those tests.
func TestMain(m *testing.M) {
	ActionGate = func(mode Mode, _ Result) Action {
		if mode == ModeEnforce {
			return ActionBlock
		}
		return ActionAudit
	}
	os.Exit(m.Run())
}

// clearActionGateForTest nils the package-level ActionGate for the
// duration of a single test, exercising the nil-gate fallback path.
// Restores via t.Cleanup so the suite-level gate from TestMain comes back
// afterwards.
func clearActionGateForTest(t *testing.T) {
	t.Helper()
	prev := ActionGate
	ActionGate = nil
	t.Cleanup(func() { ActionGate = prev })
}

// installTestActionGate is retained for tests that explicitly want to
// assert the gate is installed (a no-op under TestMain, but documents
// intent). t.Cleanup restores the prior gate so per-test changes don't
// leak.
func installTestActionGate(t *testing.T) {
	t.Helper()
	prev := ActionGate
	ActionGate = func(mode Mode, _ Result) Action {
		if mode == ModeEnforce {
			return ActionBlock
		}
		return ActionAudit
	}
	t.Cleanup(func() { ActionGate = prev })
}

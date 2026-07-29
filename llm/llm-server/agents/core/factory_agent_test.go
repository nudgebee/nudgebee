package core

import "testing"

// TestRegisterNBAgentFactoryWithAliases verifies that a factory registered under
// a primary name plus legacy aliases is resolvable under every name — the
// back-compat guarantee relied on by the *_debug → *_orchestrator rename so that
// stored conversation history and @old_name invocations keep resolving.
func TestRegisterNBAgentFactoryWithAliases(t *testing.T) {
	sentinel := &struct{ NBAgent }{}
	factory := func(accountId string) (NBAgent, error) { return sentinel, nil }

	RegisterNBAgentFactoryWithAliases("test_primary_orchestrator", factory, "test_legacy_debug", "test_other_alias")

	for _, name := range []string{"test_primary_orchestrator", "test_legacy_debug", "test_other_alias"} {
		agent, err := getSystemAgent(name, "acct-1")
		if err != nil {
			t.Errorf("getSystemAgent(%q) returned error: %v", name, err)
			continue
		}
		if agent != sentinel {
			t.Errorf("getSystemAgent(%q) resolved to a different factory result", name)
		}
	}

	// Case-insensitivity: lookup lowercases the key, so a mixed-case alias resolves.
	if _, err := getSystemAgent("TEST_LEGACY_DEBUG", "acct-1"); err != nil {
		t.Errorf("expected case-insensitive alias resolution, got error: %v", err)
	}
}

package api

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// The gateway admits an account-bound grant for a custom-handler action only
// when actions.yaml flags it `account_enforced` (app/src/lib/rpcGateway.ts). The
// flag claims this handler checks every account it touches, so each flagged
// api-server action must be a case of miscGrantCases, whose "grant bound to
// another account" caller pins the refusal. The list is exported by
// app/src/lib/__tests__/actionKeysExport.test.ts.
func TestAccountEnforcedActionsHaveABoundGrantCase(t *testing.T) {
	raw, err := os.ReadFile("../security/account_enforced_actions.json")
	require.NoError(t, err)
	var flagged map[string]string
	require.NoError(t, json.Unmarshal(raw, &flagged))

	covered := map[string]bool{}
	for _, tc := range miscGrantCases {
		covered[tc.action] = true
	}
	for action, service := range flagged {
		if service == "api-server" {
			require.True(t, covered[action], "%s is account_enforced but has no case in miscGrantCases", action)
		}
	}
}

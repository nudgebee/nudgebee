package k8s

import (
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"testing"
)

// TestRelayFetchesAvailable: skip only on a definitive "no connected agent", and
// never probe without an account id.
func TestRelayFetchesAvailable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		cloudAccountID string
		connected      bool
		wantAvailable  bool
		wantProbed     bool
	}{
		{"connected agent -> fetch", "acct-1", true, true, true},
		{"no connected agent -> skip", "acct-1", false, false, true},
		{"empty account -> skip without probing", "", true, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			source, err := NewK8sSource(K8sSourceConfig{}, slog.Default())
			if err != nil {
				t.Fatalf("NewK8sSource() error = %v", err)
			}

			probed := false
			source.agentConnected = func(accountID string) bool {
				probed = true
				if accountID != tc.cloudAccountID {
					t.Errorf("probe called with %q, want %q", accountID, tc.cloudAccountID)
				}
				return tc.connected
			}

			got := source.relayFetchesAvailable(&core.SourceBuildRequest{
				TenantID:       "tenant-1",
				CloudAccountID: tc.cloudAccountID,
			})
			if got != tc.wantAvailable {
				t.Errorf("relayFetchesAvailable() = %v, want %v", got, tc.wantAvailable)
			}
			if probed != tc.wantProbed {
				t.Errorf("agent probed = %v, want %v", probed, tc.wantProbed)
			}
		})
	}
}

// TestNewK8sSourceWiresAgentProbe pins the constructor wiring, so the nil-guard in
// relayFetchesAvailable cannot silently become the production path.
func TestNewK8sSourceWiresAgentProbe(t *testing.T) {
	t.Parallel()

	source, err := NewK8sSource(K8sSourceConfig{}, slog.Default())
	if err != nil {
		t.Fatalf("NewK8sSource() error = %v", err)
	}
	if source.agentConnected == nil {
		t.Error("NewK8sSource() left agentConnected nil, want the real probe")
	}
}

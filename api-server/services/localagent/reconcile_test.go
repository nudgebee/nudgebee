package localagent

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"nudgebee/services/config"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestConfiguredRequiresBothHalves(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		secret string
		want   bool
	}{
		{"unset — the case for every install that does not bundle an agent", "", "", false},
		{"key without secret", "abc", "", false},
		{"secret without key", "", "def", false},
		{"both set", "abc", "def", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			restore := setLocalAgentConfig(t, tc.key, tc.secret, "")
			defer restore()

			if got := configured(); got != tc.want {
				t.Errorf("configured() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClusterNameDefaultsAndTrims(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		want       string
	}{
		{"empty falls back to the default", "", "in-cluster"},
		{"whitespace-only falls back to the default", "   ", "in-cluster"},
		{"explicit value is used", "prod-east", "prod-east"},
		{"surrounding whitespace is trimmed", "  prod-east  ", "prod-east"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			restore := setLocalAgentConfig(t, "key", "secret", tc.configured)
			defer restore()

			if got := clusterName(); got != tc.want {
				t.Errorf("clusterName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateClusterName(t *testing.T) {
	tests := []struct {
		name        string
		clusterName string
		wantErr     string
	}{
		{"the default is valid", "in-cluster", ""},
		{"ordinary name", "prod-east", ""},
		{"too short — would violate account_name_check", "abc", "too short"},
		{"exactly the floor is still too short", "abcd", ""},
		{"over 40 characters", strings.Repeat("a", 41), "not a valid account name"},
		{"exactly 40 characters is allowed", strings.Repeat("a", 40), ""},
		{"reserved name", "Demo", "reserved"},
		{"reserved name in another case", "dEmO", "reserved"},
		{"reserved name with whitespace", "  demo  ", "reserved"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateClusterName(tc.clusterName)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateClusterName(%q) = %v, want nil", tc.clusterName, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateClusterName(%q) = nil, want an error containing %q", tc.clusterName, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("validateClusterName(%q) = %q, want it to mention %q", tc.clusterName, err, tc.wantErr)
			}
		})
	}
}

// An unconfigured install must not touch the database at all. Both entry points
// return before acquiring a database manager, which is what makes this change a
// no-op for every install that does not bundle an agent.
func TestReconcileIsInertWhenUnconfigured(t *testing.T) {
	restore := setLocalAgentConfig(t, "", "", "")
	defer restore()

	if err := Reconcile(t.Context(), discardLogger()); err != nil {
		t.Errorf("Reconcile() on an unconfigured install = %v, want nil", err)
	}
	if err := ReconcileForTenant(t.Context(), discardLogger(), "tenant-id", "user-id"); err != nil {
		t.Errorf("ReconcileForTenant() on an unconfigured install = %v, want nil", err)
	}
}

// A configured install with a half-known caller is a bug in the caller, not
// something to paper over: cloud_accounts requires both a tenant and an owner.
func TestReconcileForTenantRejectsMissingTenantOrUser(t *testing.T) {
	restore := setLocalAgentConfig(t, "key", "secret", "")
	defer restore()

	if err := ReconcileForTenant(t.Context(), discardLogger(), "", "user-id"); err == nil {
		t.Error("ReconcileForTenant() with no tenant = nil, want an error")
	}
	if err := ReconcileForTenant(t.Context(), discardLogger(), "tenant-id", ""); err == nil {
		t.Error("ReconcileForTenant() with no user = nil, want an error")
	}
}

func setLocalAgentConfig(t *testing.T, key, secret, cluster string) func() {
	t.Helper()
	prevKey := config.Config.LocalAgentAccessKey
	prevSecret := config.Config.LocalAgentAccessSecret
	prevCluster := config.Config.LocalAgentClusterName

	config.Config.LocalAgentAccessKey = key
	config.Config.LocalAgentAccessSecret = secret
	config.Config.LocalAgentClusterName = cluster

	return func() {
		config.Config.LocalAgentAccessKey = prevKey
		config.Config.LocalAgentAccessSecret = prevSecret
		config.Config.LocalAgentClusterName = prevCluster
	}
}

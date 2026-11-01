package config

import (
	"os"
	"slices"
	"testing"

	"github.com/spf13/viper"
)

// The API/worker split hangs entirely off this predicate: it decides whether a
// pod starts the RabbitMQ consumers (common.MqConsume) and the EE background
// goroutines (api.RunEEStartupHooks). Getting it wrong in the "false" direction
// on every pod means queues fill with nobody draining them, and — because the
// queues are non-durable — a broker restart then discards the backlog silently.
// So it fails open: only the one explicit opt-out value disables background
// jobs, and anything unrecognised keeps them running.
func TestRunsBackgroundJobs(t *testing.T) {
	original := Config.ServerRole
	t.Cleanup(func() { Config.ServerRole = original })

	tests := []struct {
		name string
		role string
		want bool
	}{
		{"all runs background jobs", ServerRoleAll, true},
		{"worker runs background jobs", ServerRoleWorker, true},
		{"api does not", ServerRoleAPI, false},
		{"empty fails open", "", true},
		{"typo fails open", "apu", true},
		{"case mismatch fails open", "API", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Config.ServerRole = tt.role
			if got := RunsBackgroundJobs(); got != tt.want {
				t.Errorf("RunsBackgroundJobs() with SERVER_ROLE=%q = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

// The default must keep every single-replica, OSS and on-prem install on the
// historical single-deployment behaviour: one process doing both halves.
//
// Config is populated by this package's init(), which has already run by the
// time any test body executes — so t.Setenv cannot make this hermetic, and an
// ambient SERVER_ROLE would make it assert against the environment rather than
// against the default. Skip in that case instead of failing on someone's shell.
func TestServerRoleDefaultsToAll(t *testing.T) {
	if v, ok := os.LookupEnv("SERVER_ROLE"); ok {
		t.Skipf("SERVER_ROLE=%q is set in the environment; the compiled-in default is not observable here", v)
	}
	if Config.ServerRole != ServerRoleAll {
		t.Errorf("default SERVER_ROLE = %q, want %q", Config.ServerRole, ServerRoleAll)
	}
}

// Per-resource knowledge-graph updates are on unless an environment explicitly
// turns them off. They shipped off, and two days on dev showed the detector
// publishing refs that never reached the patch path — so the comparison the
// switch was waiting for could not happen. This pins the default; flipping it
// back to false silently would stop per-resource updates everywhere again.
func TestResourcePatchEnabledByDefault(t *testing.T) {
	if v, ok := os.LookupEnv("KG_RESOURCE_PATCH_ENABLED"); ok {
		t.Skipf("KG_RESOURCE_PATCH_ENABLED=%q is set in the environment; the compiled-in default is not observable here", v)
	}
	if !Config.KGResourcePatchEnabled {
		t.Error("default kg_resource_patch_enabled = false, want true")
	}
}

// Event-driven graph updates are opt-in. This is the api-server half of the
// pair: the detector that re-derives changes from the inventory tables every 10
// minutes, alongside the collectors' KG_UPDATE_NOTIFICATIONS_ENABLED. With both
// off the graph is driven only by the hourly build_knowledge_graph cron, so
// flipping this default back on silently would resume that traffic everywhere.
func TestInventoryChangeDetectionDisabledByDefault(t *testing.T) {
	if v, ok := os.LookupEnv("KG_INVENTORY_CHANGE_DETECTION_ENABLED"); ok {
		t.Skipf("KG_INVENTORY_CHANGE_DETECTION_ENABLED=%q is set in the environment; the compiled-in default is not observable here", v)
	}
	if Config.KGInventoryChangeDetectionEnabled {
		t.Error("default kg_inventory_change_detection_enabled = true, want false")
	}
}

// Announcing a returned anomaly is opt-in, and the switch has to be reachable.
// viper's Unmarshal only fills keys it knows, and AutomaticEnv does not make a
// key known: a flag with no default is never read from the environment, so it
// could not be turned on at all.
func TestAnomalyReturnedDigestIsOffByDefaultAndReadFromTheEnvironment(t *testing.T) {
	if !slices.Contains(viper.AllKeys(), "feature_anomaly_returned_digest_enabled") {
		t.Error("feature_anomaly_returned_digest_enabled has no viper default, so FEATURE_ANOMALY_RETURNED_DIGEST_ENABLED is ignored")
	}
	if v, ok := os.LookupEnv("FEATURE_ANOMALY_RETURNED_DIGEST_ENABLED"); ok {
		t.Skipf("FEATURE_ANOMALY_RETURNED_DIGEST_ENABLED=%q is set in the environment; the compiled-in default is not observable here", v)
	}
	if Config.FeatureAnomalyReturnedDigestEnabled {
		t.Error("default feature_anomaly_returned_digest_enabled = true, want false")
	}
}

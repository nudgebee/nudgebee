package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsSchedulerLeaderEligible covers the guard added after a developer laptop won
// the dev leader election and held it all day, silently disabling every leader job in
// the cluster — the dead-worker reaper included, which left four conversations stuck
// in IN_PROGRESS with nothing to recover them.
func TestIsSchedulerLeaderEligible(t *testing.T) {
	tests := []struct {
		name      string
		setting   string
		inCluster bool
		want      bool
	}{
		{"auto in cluster", "auto", true, true},
		{"auto on a laptop", "auto", false, false},
		{"unset behaves as auto", "", false, false},
		{"unrecognised value behaves as auto", "yes-please", false, false},
		{"forced on for local leader-job testing", "true", false, true},
		{"forced off in cluster", "false", true, false},
		{"case and padding tolerated", "  TRUE  ", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.inCluster {
				t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
			} else {
				t.Setenv("KUBERNETES_SERVICE_HOST", "")
			}
			prev := Config.SchedulerLeaderEligible
			Config.SchedulerLeaderEligible = tt.setting
			t.Cleanup(func() { Config.SchedulerLeaderEligible = prev })

			assert.Equal(t, tt.want, IsSchedulerLeaderEligible())
		})
	}
}

// TestIsLocalWorkerName pins the ownership marker. The previous heuristic was
// strings.Contains(name, ".local"), which caught "Hemasundar-MB.local" but missed
// bare hostnames like "nandeshboyz" — a laptop that was in the shared dev worker
// pool, whose conversations the cluster would therefore have restarted.
func TestIsLocalWorkerName(t *testing.T) {
	local := []string{
		LocalWorkerNamePrefix + "nandeshboyz",
		LocalWorkerNamePrefix + "Hemasundar-MB.local",
		"localhost",
		"127.0.0.1",
		"0.0.0.0",
		"::",
		"Hemasundar-MB.local", // pre-prefix rows still in the database
	}
	for _, name := range local {
		assert.True(t, IsLocalWorkerName(name), "%q must be treated as a local worker", name)
	}

	remote := []string{
		"llm-server-86d6595fbb-z2spc",
		"llm-server-76b7bd5cc8-7bk6w",
		"nandeshboyz", // bare hostname is indistinguishable from a pod without the prefix
	}
	for _, name := range remote {
		assert.False(t, IsLocalWorkerName(name), "%q must not be treated as a local worker", name)
	}
}

// TestServerNameCarriesLocalityFromConfigLoad exercises the real load path (the
// package init()) rather than the helper in isolation, since the prefix is applied
// after viper.Unmarshal and is easy to lose in a config refactor.
func TestServerNameCarriesLocalityFromConfigLoad(t *testing.T) {
	if IsInCluster() {
		assert.NotContains(t, Config.ServerName, LocalWorkerNamePrefix,
			"an in-cluster worker must keep its pod name unprefixed")
		return
	}
	assert.Contains(t, Config.ServerName, LocalWorkerNamePrefix,
		"a worker outside the cluster must be tagged local at load time")
}

// TestLocalWorkerNamePrefixCannotCollide guards the choice of ':' as the separator:
// it is not legal in a hostname or a Kubernetes pod name, so no real in-cluster
// worker can ever be misread as local.
func TestLocalWorkerNamePrefixCannotCollide(t *testing.T) {
	assert.Contains(t, LocalWorkerNamePrefix, ":")
}

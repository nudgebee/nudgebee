package handlers

import (
	"testing"
	"time"
)

// api-server's "Agent Status Check" cron
// (services/account/agent_service.go, agentConnectThresholdMinutes) retires
// any agent it has not heard from in 30 minutes. Our session heartbeat is
// what it hears. If someone widens the heartbeat past that window, every
// healthy long-lived session gets retired fleet-wide — which is exactly the
// bug this heartbeat was added to fix (nudgebee/nudgebee#36114), so the
// relationship gets a test rather than a comment.
//
// Three intervals of headroom: two consecutive failed writes must not be
// enough to retire a live agent.
func TestHeartbeatFitsCronThreshold(t *testing.T) {
	const apiServerRetiresAfter = 30 * time.Minute

	if got := relaySessionHeartbeatInterval * 3; got > apiServerRetiresAfter {
		t.Fatalf("3 heartbeats = %s, which exceeds api-server's %s retirement window; "+
			"either lower relaySessionHeartbeatInterval or raise agentConnectThresholdMinutes in "+
			"api-server/services/account/agent_service.go — they must be changed together",
			got, apiServerRetiresAfter)
	}
}

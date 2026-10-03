package account

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

// TestAgentConnectedFromLookup: only a definitive no-rows answer means "no
// agent"; an undetermined answer probes anyway.
func TestAgentConnectedFromLookup(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"row found", nil, true},
		{"no rows", sql.ErrNoRows, false},
		{"wrapped no rows", fmt.Errorf("query agent: %w", sql.ErrNoRows), false},
		{"connection refused -> fail open", errors.New("dial tcp: connection refused"), true},
		{"driver bad conn -> fail open", sql.ErrConnDone, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentConnectedFromLookup(tc.err); got != tc.want {
				t.Errorf("agentConnectedFromLookup(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestIsK8sAgentConnectedEmptyAccount asserts the guard short-circuits before
// touching the database.
func TestIsK8sAgentConnectedEmptyAccount(t *testing.T) {
	if IsK8sAgentConnected("") {
		t.Error("IsK8sAgentConnected(\"\") = true, want false")
	}
}

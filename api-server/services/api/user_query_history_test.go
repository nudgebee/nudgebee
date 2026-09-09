package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The security-critical test. /rpc/logs and /rpc/metrics are shared by the
// browser, llm-server, runbook-server and cost-server behind one shared
// X-ACTION-TOKEN, and llm-server forwards the real requesting user's id — so if
// a service caller ever set record_history, the only thing keeping agent queries
// out of a human's history is the session_variables provenance gate. History is
// read account-wide, so a leaked row is visible to every user on the account.
func TestShouldRecordUserQueryHistory(t *testing.T) {
	browser := map[string]any{"user_id": "11111111-1111-1111-1111-111111111111", "x-hasura-role": "account_admin"}

	tests := []struct {
		name             string
		recordHistory    bool
		sessionVariables map[string]any
		want             bool
	}{
		{
			name:             "browser submit with the flag set records",
			recordHistory:    true,
			sessionVariables: browser,
			want:             true,
		},
		{
			name:             "browser poll or dashboard panel (no flag) does not record",
			recordHistory:    false,
			sessionVariables: browser,
			want:             false,
		},
		{
			// llm-server and runbook-server POST {action, input} with no
			// session_variables at all. Even if one of them started sending the
			// flag, this must stay false.
			name:             "service caller with the flag set does not record",
			recordHistory:    true,
			sessionVariables: nil,
			want:             false,
		},
		{
			name:             "service caller without the flag does not record",
			recordHistory:    false,
			sessionVariables: nil,
			want:             false,
		},
		{
			name:             "empty user_id does not record",
			recordHistory:    true,
			sessionVariables: map[string]any{"user_id": ""},
			want:             false,
		},
		{
			// A super-admin/service context can carry session_variables whose
			// user_id is not a string; that must not read as provenance.
			name:             "non-string user_id does not record",
			recordHistory:    true,
			sessionVariables: map[string]any{"user_id": 123},
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shouldRecordUserQueryHistory(tt.recordHistory, tt.sessionVariables))
		})
	}
}

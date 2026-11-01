package handlers

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOwnershipSurvivesResponseParsing(t *testing.T) {
	for _, line := range []string{`12`, `"12"`} {
		h := &AgenticAnalyzeHandler{}
		r, err := h.parseAgentResponse(`{"line_number":` + line + `,"automated_fix_pr_info":{"url":"https://github.com/acme/repo/pull/1","status":"success","ownership":{"provider":"github","repository":"github.com/acme/repo","handle":"alice","status":"failed","warning":"assignment failed"}}}`)
		require.NoError(t, err)
		require.NotNil(t, r.AutoMatedFixPRInfo)
		require.NotNil(t, r.AutoMatedFixPRInfo.Ownership)
		require.Equal(t, "alice", r.AutoMatedFixPRInfo.Ownership.Handle)
		require.Equal(t, "failed", r.AutoMatedFixPRInfo.Ownership.Status)
		require.Equal(t, "assignment failed", r.AutoMatedFixPRInfo.Ownership.Warning)
	}
}

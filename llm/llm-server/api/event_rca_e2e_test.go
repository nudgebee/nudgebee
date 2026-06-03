//go:build e2e

package api

import (
	"nudgebee/llm/agents"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TODO mock DBs
// TODO mock Tool Execution
func TestLogAnalyzer_ExecuteOOM(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := agents.FetchRecentEventID(t, os.Getenv("TEST_ACCOUNT"))

	resp, err := analyzeEventRCAUsingAgentsAndUpdateDb(sc, EventRCAAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
		Generate:   true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

package api

import (
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TODO mock DBs
// TODO mock Tool Execution
func TestLogAnalyzer_ExecuteOOM(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_PROMETHEUSCHAIN_USER"), []string{})

	eventId := "f5a7946a-6a2a-4f5d-aca4-8c0b69b85fe4"

	resp, err := analyzeEventRCAUsingAgentsAndUpdateDb(sc, EventRCAAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_PROMETHEUSCHAIN_ACCOUNT"),
		UserId:     os.Getenv("TEST_PROMETHEUSCHAIN_USER"),
		Regenerate: true,
		Generate:   true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

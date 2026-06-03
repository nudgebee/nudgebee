//go:build e2e

package api

import (
	"encoding/json"
	"nudgebee/llm/agents"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEventInvestigationAsync_Test1(t *testing.T) {
	eventID := agents.FetchRecentEventID(t, os.Getenv("TEST_ACCOUNT"))
	request := EventAnalysisRequest{
		EventId:    eventID,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		Regenerate: true,
	}
	data, err := json.Marshal(request)
	assert.Nil(t, err)
	assert.NotNil(t, data)
	err = processTroubleshootingEventFromMq(data)
	assert.Nil(t, err)

	dbManager, err := common.GetDatabaseManager(common.Metastore)
	assert.Nil(t, err)
	request.Regenerate = false
	response, err := getOrCreateEventAnalysisStatus(security.NewRequestContextForSuperAdmin(), request, dbManager, false)
	assert.Nil(t, err)
	assert.Equal(t, request.EventId, response.EventId)
	assert.Equal(t, "COMPLETED", response.Status)
	assert.NotEmpty(t, response.Summary)
}

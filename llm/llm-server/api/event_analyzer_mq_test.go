package api

import (
	"encoding/json"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEventInvestigationAsync_Test1(t *testing.T) {
	request := EventAnalysisRequest{
		EventId:    "ad70a219-e938-4e97-8dd6-d9746eeb2d99",
		AccountId:  "a2a30b02-0f67-42e5-a2ab-c658230fd798",
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

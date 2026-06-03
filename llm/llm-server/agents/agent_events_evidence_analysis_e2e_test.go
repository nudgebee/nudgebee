//go:build e2e

package agents

import (
	"fmt"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestEventAnalysisTool(t *testing.T) {
	eventID := FetchRecentEventID(t, os.Getenv("TEST_ACCOUNT"))
	tool := EvidenceInsightsTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Command   string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-events-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Command:   fmt.Sprintf(`{"event_id":%q,"timeseries_only":true}`, eventID),
			},
		}
	for _, tc := range testCases {

		ntc := toolcore.NewNbToolContext(sc, tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Command, []llms.MessageContent{}, "", toolcore.NBQueryConfig{}, "")
		resp, err := tool.Call(ntc, toolcore.NBToolCallRequest{
			Command: tc.Command,
		})
		fmt.Println(resp)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		print(resp.Data)
	}
}

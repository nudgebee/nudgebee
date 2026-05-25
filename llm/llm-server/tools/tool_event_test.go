package tools

import (
	"encoding/json"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestEventAgentExecuteTool(t *testing.T) {

	tool := EventsExecuteTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-1",
				Query:     `select * from events where id = '050ef726-d4e0-4c21-960b-5ee928e642f8'`,
				AccountId: "e9dc2b39-78d3-46da-9691-f160bd3c4c19",
				UserId:    "924f41d7-21bd-4ae5-ba4d-3f4abd4f37a9",
			},
		}
	for _, tc := range testCases {

		ntc := core.NewNbToolContext(sc, tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		var event []events.Event
		err = json.Unmarshal([]byte(resp.Data), &event)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestEventAgentExecuteTool2(t *testing.T) {

	tool := EventsExecuteTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-1",
				Query:     `select * from events where id = '983897e7-9afb-48c5-bd27-83a8784e5f2d'`,
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
			},
		}
	for _, tc := range testCases {

		ntc := core.NewNbToolContext(sc, tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		var event []events.Event
		err = json.Unmarshal([]byte(resp.Data), &event)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestEventAgentExecuteCloudAccount(t *testing.T) {

	tool := EventsExecuteTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-1",
				Query:     `select * from events where id = '61184c30-225e-45f7-acd5-63e2f83fbcad'`,
				AccountId: "49145907-981b-48ad-a67a-08a4e8099cb2",
				UserId:    os.Getenv("TEST_USER"),
			},
		}
	for _, tc := range testCases {

		ntc := core.NewNbToolContext(sc, tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		var event []events.Event
		err = json.Unmarshal([]byte(resp.Data), &event)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

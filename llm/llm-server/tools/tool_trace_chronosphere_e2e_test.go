//go:build e2e

package tools

import (
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestChronophereTraceTool(t *testing.T) {

	tool := TracesExecuteChronosphereTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId  string
			Query      string
			AccountId  string
			UserId     string
			ToolConfig string
		}{
			{
				SessionId:  "ut-tool-chain-1",
				AccountId:  os.Getenv("TEST_ACCOUNT"),
				UserId:     os.Getenv("TEST_USER"),
				Query:      `{"start_time":"2025-09-21T10:00:00Z","end_time":"2025-09-21T11:00:00Z","where": {"service_name": {"_eq": "services-server"}, "http.status_code": {"_gte": 500}}}`,
				ToolConfig: "",
			},
		}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

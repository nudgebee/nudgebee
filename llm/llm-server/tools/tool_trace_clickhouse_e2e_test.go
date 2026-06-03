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

func TestClickhouseTraceTool(t *testing.T) {

	tool := TracesExecuteClickhouseTool{}
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
				Query:      `SELECT * FROM traces_view WHERE workload_name = 'services-server' AND http_status_code >= 500 AND timestamp >= now() - interval 1 hour LIMIT 10`,
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

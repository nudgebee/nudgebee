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

func TestGetES(t *testing.T) {

	tool := ElasticSearchExecuteTool{}
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
				AccountId: os.Getenv("TEST_ESCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_K8SCHAIN_USER"),
				Query:     `{"query":"{\"query\":{\"bool\":{\"filter\":[{\"term\":{\"kubernetes.labels.app_kubernetes_io/name.keyword\":{\"value\":\"auto-pilot-server\",\"case_insensitive\":true}}},{\"match\":{\"log\":\"error\"}}]}}}"}`,
			},
		}
	for _, tc := range testCases {

		ntc := core.NewNbToolContext(sc, tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

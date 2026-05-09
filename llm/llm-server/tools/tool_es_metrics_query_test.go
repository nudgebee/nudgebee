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

func TestESMetricsQuery(t *testing.T) {
	tool := ESMetricsQueryTool{}
	sc := security.NewRequestContextForSuperAdmin()

	accountId := "25f42d26-f179-4acd-9946-271ee0cf1b73"
	userId := os.Getenv("TEST_USER")

	if userId == "" {
		t.Skip("TEST_USER environment variable must be set for integration tests")
	}

	testCases := []struct {
		Name  string
		Input string
	}{
		{
			Name: "namespace_filter_nudgebee",
			Input: `{
				"index": "metrics-*",
				"query": {
					"query": {
						"bool": {
							"filter": [
								{
									"bool": {
										"filter": [
											{"term": {"attributes.metric.attributes.namespace.keyword": "nudgebee"}}
										]
									}
								}
							]
						}
					}
				}
			}`,
		},
		{
			Name: "cpu_agg_nudgebee_namespace",
			Input: `{
				"index": "metrics-*",
				"query": {
					"size": 0,
					"query": {
						"bool": {
							"must": [
								{"term": {"attributes.metric.attributes.namespace.keyword": "nudgebee"}},
								{"range": {"@timestamp": {"gte": "now-1h"}}}
							]
						}
					},
					"aggs": {
						"avg_cpu": {
							"avg": {
								"field": "value"
							}
						}
					}
				}
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			ntc := core.NewNbToolContext(sc, tool, accountId, userId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Input, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
			resp, err := tool.Call(ntc, core.NBToolCallRequest{
				Command: tc.Input,
			})

			if err != nil {
				t.Logf("Tool call error: %v", err)
				t.Logf("Response data: %s", resp.Data)
			}

			assert.NoError(t, err, "tool call should not error")
			assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status, "response status should be success")
			assert.NotEmpty(t, resp.Data, "response data should not be empty")

			t.Logf("Response: %s", resp.Data)
		})
	}
}

//go:build e2e

package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

const logAnalysisGitData = `
{
  "errors": ["{\"asctime\": \"2024-10-08 13:54:58,383\", \"levelname\": \"ERROR\", \"filename\": \"database.py\", \"lineno\": 175, \"message\": \"Error inserting data into cloud_resourses table: ON CONFLICT DO UPDATE command cannot affect row a second time\nHINT: Ensure that no rows proposed for insertion within the same command have duplicate constrained values.\n\", \"exc_info\": \"Traceback (most recent call last):\n File \"/app/db/database.py\", line 168, in insert_data\n cur.execute(query, parameters)\npsycopg2.errors.CardinalityViolation: ON CONFLICT DO UPDATE command cannot affect row a second time\nHINT: Ensure that no rows proposed for insertion within the same command have duplicate constrained values.\n\", \"taskName\": null}"],
  "files": [{"file_name":"database.py", "file_path":"/app/db/database.py"}],
  "git_repo": "https://github.com/nudgebee/nudgebee",
  "git_commit": "d419d9f4160b125ac914d28f24d87a629fc140c8"
}
`

// TODO mock DBs
// TODO mock Tool Execution
func TestLogAnalysisGithub_Execute(t *testing.T) {
	logAnalysis := LogGithubAgent{}
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-log-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     logAnalysisGitData,
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, logAnalysis, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, logAnalysis.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestLogAnalysisGithub_SearchFile(t *testing.T) {
	logAnalysis := LogGithubAgent{}
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	path, content, error := logAnalysis.searchFileAndGetContentFromGithub(sc, "https://api.github.com", "token", os.Getenv("TEST_GITHUB_USER"), os.Getenv("TEST_GITHUB_TOKEN"), "nudgebee/example-distributed-tracing", []map[string]any{{"file_name": "users.go", "file_path": "/app/users.go"}})
	assert.Nil(t, error)
	assert.Equal(t, path, "users/users.go")
	assert.NotEmpty(t, content)

}

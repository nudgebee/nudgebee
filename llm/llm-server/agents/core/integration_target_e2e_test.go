//go:build e2e

package core

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// From a k8s-dev conversation, database, cache and broker tools run against
// another account's own integrations when the call names that account.
func TestCallNbTool_IntegrationOnAnotherAccount(t *testing.T) {
	RequireEnv(t, "TEST_ACCOUNT", "TEST_USER", "TEST_TENANT")
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForTenantAdminWithUser(t.Context(), os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"))
	call := func(t *testing.T, toolName string, args map[string]any, picked ...string) toolcore.NBToolResponse {
		tool, ok := toolcore.GetNBTool(accountId, toolName)
		require.True(t, ok)
		input, err := json.Marshal(args)
		require.NoError(t, err)
		req := NBAgentRequest{AccountId: accountId, UserId: os.Getenv("TEST_USER"), ConversationId: uuid.NewString(), MessageId: uuid.NewString(), AgentId: uuid.NewString()}
		if len(picked) > 0 {
			// The planner's config picker chose among the conversation's own.
			req.QueryConfig.ToolConfigs = map[string]string{toolName: picked[0]}
		}
		resp, err := callNbTool(sc, req, "integration-target-e2e", tool, string(input), nil, "", uuid.NewString(), "")
		t.Logf("%s %v: status=%s err=%v data=%.1500s ran_on=%v", toolName, args["target"], resp.Status, err, fmt.Sprint(resp.Data), resp.AdditionalDetails[toolcore.MetadataExecutedTarget])
		return resp
	}
	// The workspace pod calls the deployed llm-server's proxy; until it has
	// this change it refuses a command bound to another account.
	// The command reached the target, but the environment cannot run it there:
	// the deployed proxy predates this change, or the target's integration is
	// unreachable or misconfigured on dev. Skip with the reason, never pass.
	requireDeployedProxy := func(t *testing.T, resp toolcore.NBToolResponse) {
		for marker, reason := range map[string]string{
			"workspace token does not permit the selected target": "the deployed workspace proxy predates per-call integration targets",
			"agent not connected":        "the target integration's proxy agent is not connected on this environment",
			"CreateContainerConfigError": "the target integration is misconfigured on this environment (its secret is missing)",
		} {
			if strings.Contains(resp.Data, marker) {
				t.Skipf("%s: %.200s", reason, resp.Data)
			}
		}
	}
	ranOn := func(resp toolcore.NBToolResponse) string {
		target, _ := resp.AdditionalDetails[toolcore.MetadataExecutedTarget].(map[string]any)
		name, _ := target["name"].(string)
		return name
	}
	prod := map[string]any{"provider": "kubernetes", "name": envOr("TEST_DELEGATE_K8S_TARGET", "k8s-prod")}
	const where = "select current_database() || '@' || coalesce(host(inet_server_addr()), 'local') as db"

	t.Run("postgres", func(t *testing.T) {
		own := call(t, tools.ToolExecutePostgresQuery, map[string]any{"query": where, "instance": "dev-pg"}, "dev-pg")
		require.Equal(t, toolcore.NBToolResponseStatusSuccess, own.Status, "the conversation's own database still works")
		assert.Empty(t, ranOn(own))

		dup := call(t, tools.ToolExecutePostgresQuery, map[string]any{"query": "select 1", "instance": "orders-db-ec2-postgres", "target": map[string]any{"provider": "aws", "name": "dev-aws"}})
		assert.Equal(t, toolcore.NBToolResponseStatusError, dup.Status)
		assert.Contains(t, dup.Data, "cannot be told apart", "dev-aws has two integrations with this name")

		several := call(t, tools.ToolExecutePostgresQuery, map[string]any{"query": "select 1", "target": prod})
		assert.Equal(t, toolcore.NBToolResponseStatusError, several.Status)
		assert.Contains(t, several.Data, "name one with `instance`", "k8s-prod has two Postgres integrations; none is guessed")

		other := call(t, tools.ToolExecutePostgresQuery, map[string]any{"query": where, "instance": "prod", "target": prod})
		requireDeployedProxy(t, other)
		require.Equal(t, toolcore.NBToolResponseStatusSuccess, other.Status)
		assert.Equal(t, prod["name"], ranOn(other), "the step is labelled with the account it ran on")
		assert.NotEqual(t, own.Data, other.Data, "k8s-prod's prod is a different database from k8s-dev's dev-pg")
	})

	// An account with a single Postgres integration: it is used without
	// naming it.
	t.Run("postgres, the target's only integration", func(t *testing.T) {
		only := map[string]any{"provider": "kubernetes", "name": envOr("TEST_SINGLE_PG_TARGET", "k8s-rs-prod")}
		resp := call(t, tools.ToolExecutePostgresQuery, map[string]any{"query": where, "target": only})
		requireDeployedProxy(t, resp)
		require.Equal(t, toolcore.NBToolResponseStatusSuccess, resp.Status)
		assert.Equal(t, only["name"], ranOn(resp))
		assert.Equal(t, envOr("TEST_SINGLE_PG_NAME", "e2e-single-pg"), resp.AdditionalDetails[toolcore.MetadataResolvedTarget], "the step names the integration it used")
	})

	t.Run("redis", func(t *testing.T) {
		resp := call(t, tools.ToolExecuteRedisCommand, map[string]any{"command": "INFO server", "instance": "prod-redis", "target": prod})
		requireDeployedProxy(t, resp)
		require.Equal(t, toolcore.NBToolResponseStatusSuccess, resp.Status)
		assert.Contains(t, resp.Data, "redis_version")
		assert.Equal(t, prod["name"], ranOn(resp))
	})

	t.Run("rabbitmq", func(t *testing.T) {
		resp := call(t, tools.ToolExecuteRabbitCommand, map[string]any{"args": "list queues", "target": prod})
		requireDeployedProxy(t, resp)
		require.Equal(t, toolcore.NBToolResponseStatusSuccess, resp.Status)
		assert.Equal(t, prod["name"], ranOn(resp))
	})

	t.Run("no access to the target", func(t *testing.T) {
		scoped := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{accountId})
		tool, ok := toolcore.GetNBTool(accountId, tools.ToolExecutePostgresQuery)
		require.True(t, ok)
		input, _ := json.Marshal(map[string]any{"query": "select 1", "instance": "prod-pg", "target": prod})
		req := NBAgentRequest{AccountId: accountId, UserId: os.Getenv("TEST_USER"), ConversationId: uuid.NewString(), MessageId: uuid.NewString(), AgentId: uuid.NewString()}
		resp, err := callNbTool(scoped, req, "integration-target-e2e", tool, string(input), nil, "", uuid.NewString(), "")
		t.Logf("no access: status=%s err=%v data=%.300s", resp.Status, err, resp.Data)
		assert.Equal(t, toolcore.NBToolResponseStatusError, resp.Status)
		assert.NotContains(t, resp.Data, "prod-pg@", "nothing from k8s-prod's database")
	})
}

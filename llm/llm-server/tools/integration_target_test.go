package tools

import (
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsTargetedIntegrationTool(t *testing.T) {
	assert.True(t, IsTargetedIntegrationTool(nil, PostgresExecuteTool{}))
	assert.True(t, IsTargetedIntegrationTool(nil, RedisExecuteTool{}))
	assert.True(t, IsTargetedIntegrationTool(nil, RabbitExecuteTool{}))
	assert.False(t, IsTargetedIntegrationTool(nil, KubectlExecuteTool{}), "picks its own cluster")
	assert.False(t, IsTargetedIntegrationTool(nil, PrometheusExecuteTool{}), "follows a target but has no integration to pick")
	assert.False(t, IsTargetedIntegrationTool(nil, nil))
}

func TestCallHasTarget(t *testing.T) {
	prod := &core.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod", AccountID: "prod-id"}
	assert.True(t, CallHasTarget(`{"query":"select 1","target":{"provider":"kubernetes","name":"k8s-prod"}}`, nil, "origin-id"))
	assert.True(t, CallHasTarget(`{"query":"select 1"}`, prod, "origin-id"), "a handed target")
	assert.False(t, CallHasTarget(`{"query":"select 1"}`, nil, "origin-id"))
	assert.False(t, CallHasTarget(`{"query":"select 1"}`, &core.ExecutionTarget{AccountID: "origin-id"}, "origin-id"), "handed the conversation's own account")
	assert.False(t, CallHasTarget(`select 1`, nil, "origin-id"), "plain text")
}

// The integration is picked among the target account's own: the only one, or
// the one named; never the conversation's, and never one of two with one name.
func TestTargetIntegrationConfig(t *testing.T) {
	byAccount := map[string][]core.ToolConfig{
		"prod-id": {{Id: "1", Name: "prod"}, {Id: "2", Name: "prod-pg"}},
		"one-id":  {{Id: "3", Name: "only-pg"}},
		"dup-id":  {{Id: "4", Name: "orders-db"}, {Id: "5", Name: "orders-db"}},
		"none-id": nil,
	}
	restore := listIntegrationConfigs
	t.Cleanup(func() { listIntegrationConfigs = restore })
	listIntegrationConfigs = func(_ *security.RequestContext, account string, _ core.NBTool) ([]core.ToolConfig, error) {
		return byAccount[account], nil
	}
	ctxOn := func(account string) core.NbToolContext {
		return core.NbToolContext{AccountId: "origin-id", TargetAccountId: account, ToolConfig: core.ToolConfig{Name: "dev-pg"},
			ExecutedTarget: &core.ExecutionTarget{Provider: "kubernetes", Name: account}}
	}
	pick := func(account, input string) (core.ToolConfig, error) {
		return targetIntegrationConfig(ctxOn(account), PostgresExecuteTool{}, core.NBToolCallRequest{Command: input})
	}

	got, err := pick("one-id", `{"query":"select 1"}`)
	require.NoError(t, err)
	assert.Equal(t, "only-pg", got.Name, "the only integration on the target, not the conversation's dev-pg")

	got, err = pick("prod-id", `{"query":"select 1","instance":"prod-pg"}`)
	require.NoError(t, err)
	assert.Equal(t, "2", got.Id)

	_, err = pick("prod-id", `{"query":"select 1"}`)
	assert.ErrorContains(t, err, "name one with `instance`")
	assert.ErrorContains(t, err, "prod, prod-pg")

	_, err = pick("dup-id", `{"query":"select 1","instance":"orders-db"}`)
	assert.ErrorContains(t, err, "cannot be told apart")

	_, err = pick("none-id", `{"query":"select 1"}`)
	assert.ErrorContains(t, err, "has no integration")

	_, err = IntegrationConfigByName(nil, "dup-id", PostgresExecuteTool{}, "orders-db")
	assert.ErrorContains(t, err, "none was used")
	got, err = IntegrationConfigByName(nil, "prod-id", PostgresExecuteTool{}, "PROD-PG")
	require.NoError(t, err)
	assert.Equal(t, "2", got.Id)
}

func TestIntegrationWorkspaceEnv(t *testing.T) {
	own := core.NbToolContext{AccountId: "origin-id", TargetAccountId: "origin-id", ToolConfig: core.ToolConfig{Name: "dev-pg"}}
	env := IntegrationWorkspaceEnv(own, map[string]string{"PGDATABASE": "app"})
	assert.Equal(t, map[string]string{workspace.ENV_NB_TOOL_CONFIG_NAME: "dev-pg", "PGDATABASE": "app"}, env, "no binding on the conversation's own account")

	other := core.NbToolContext{AccountId: "origin-id", TargetAccountId: "prod-id", ToolConfig: core.ToolConfig{Name: "prod-pg"}}
	env = IntegrationWorkspaceEnv(other, nil)
	assert.Equal(t, "prod-id", env[workspace.ENV_NB_TARGET_ACCOUNT_ID], "the command's token is bound to the target")
	assert.Equal(t, "prod-pg", env[workspace.ENV_NB_TOOL_CONFIG_NAME])
}

// The target's own name in the call must not pick an integration: k8s-prod
// has "prod" and "prod-pg", and naming neither picks neither.
func TestTargetIntegrationConfig_TargetNameDoesNotPick(t *testing.T) {
	restore := listIntegrationConfigs
	t.Cleanup(func() { listIntegrationConfigs = restore })
	listIntegrationConfigs = func(*security.RequestContext, string, core.NBTool) ([]core.ToolConfig, error) {
		return []core.ToolConfig{{Id: "1", Name: "prod"}, {Id: "2", Name: "prod-pg"}}, nil
	}
	input := `{"query":"select 1","target":{"provider":"kubernetes","name":"k8s-prod"}}`
	ctx := core.NbToolContext{AccountId: "origin-id", TargetAccountId: "prod-id", Query: input,
		ExecutedTarget: &core.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod"}}
	_, err := targetIntegrationConfig(ctx, PostgresExecuteTool{}, core.NBToolCallRequest{Command: input})
	assert.ErrorContains(t, err, "name one with `instance`")
}

// The user's pick among a target's integrations is used for that account only,
// and integrations sharing one name are refused with a rename hint.
func TestTargetIntegrationConfig_PickAndDuplicates(t *testing.T) {
	byAccount := map[string][]core.ToolConfig{
		"prod-id": {{Id: "1", Name: "prod"}, {Id: "2", Name: "prod-pg"}},
		"dup-id":  {{Id: "4", Name: "orders-db"}, {Id: "5", Name: "orders-db"}},
	}
	restore := listIntegrationConfigs
	t.Cleanup(func() { listIntegrationConfigs = restore })
	listIntegrationConfigs = func(_ *security.RequestContext, account string, _ core.NBTool) ([]core.ToolConfig, error) {
		return byAccount[account], nil
	}
	pick := func(account string, picks map[string]string) (core.ToolConfig, error) {
		ctx := core.NbToolContext{AccountId: "origin-id", TargetAccountId: account, QueryConfig: core.NBQueryConfig{ToolConfigs: picks},
			ExecutedTarget: &core.ExecutionTarget{Provider: "kubernetes", Name: account}}
		return targetIntegrationConfig(ctx, PostgresExecuteTool{}, core.NBToolCallRequest{Command: `{"query":"select 1"}`})
	}

	got, err := pick("prod-id", map[string]string{TargetIntegrationChoiceKey(ToolExecutePostgresQuery, "prod-id"): "prod-pg"})
	require.NoError(t, err)
	assert.Equal(t, "2", got.Id, "the pick the user made for k8s-prod")

	_, err = pick("prod-id", map[string]string{ToolExecutePostgresQuery: "prod-pg"})
	assert.ErrorContains(t, err, "name one with `instance`", "the conversation account's pick does not choose on another account")

	_, err = pick("dup-id", nil)
	assert.ErrorContains(t, err, "all named \"orders-db\"")
	assert.ErrorContains(t, err, "rename one of them in Integrations")

	assert.Equal(t, []string{"orders-db"}, UniqueConfigNames(byAccount["dup-id"]))
	assert.Equal(t, []string{"prod", "prod-pg"}, UniqueConfigNames(byAccount["prod-id"]))
}

// #38596: a call on the conversation's own account that names another of its
// integrations runs there; calls naming nothing, or a name that is no
// integration of the account (a database or host name), keep the conversation's.
func TestOwnIntegrationNamedByCall(t *testing.T) {
	restore := listIntegrationConfigs
	t.Cleanup(func() { listIntegrationConfigs = restore })
	listIntegrationConfigs = func(_ *security.RequestContext, account string, _ core.NBTool) ([]core.ToolConfig, error) {
		return map[string][]core.ToolConfig{
			"origin-id": {{Id: "1", Name: "dev-pg"}, {Id: "2", Name: "test-pg"}, {Id: "3", Name: "dup"}, {Id: "4", Name: "dup"}},
		}[account], nil
	}
	ctx := core.NbToolContext{AccountId: "origin-id", TargetAccountId: "origin-id", ToolConfig: core.ToolConfig{Id: "1", Name: "dev-pg"}}
	run := func(input string) string {
		resolved, err := ResolveToolCallTarget(ctx, PostgresExecuteTool{}, core.ParseNBToolCallRequestFromInput(input))
		require.NoError(t, err)
		return resolved.ToolConfig.Name
	}

	assert.Equal(t, "test-pg", run(`{"query":"select 1","instance":"test-pg"}`), "named: switches for this call")
	assert.Equal(t, "test-pg", run(`{"query":"select 1","instance":"TEST-PG"}`), "names are matched case-insensitively")
	assert.Equal(t, "dev-pg", run(`{"query":"select 1"}`), "not named: the conversation's pick")
	assert.Equal(t, "dev-pg", run(`{"query":"select 1","instance":"nudgebee"}`), "a database name is not an integration")
	assert.Equal(t, "dev-pg", run(`{"query":"select 1","instance":"dup"}`), "a name shared by two integrations cannot be told apart")
	assert.Equal(t, "dev-pg", ctx.ToolConfig.Name, "the conversation's pick itself is unchanged")
}

// #38596: the SQL tools, ArgoCD, Datadog, New Relic and MongoDB name their connection
// with `instance`: on the conversation's own account the call switches to it;
// on another account it picks among that account's integrations.
func TestPerCallIntegrationTools(t *testing.T) {
	restore := listIntegrationConfigs
	t.Cleanup(func() { listIntegrationConfigs = restore })
	listIntegrationConfigs = func(_ *security.RequestContext, account string, _ core.NBTool) ([]core.ToolConfig, error) {
		return map[string][]core.ToolConfig{
			"origin-id": {{Id: "o1", Name: "conn-1"}, {Id: "o2", Name: "conn-2"}},
			"prod-id":   {{Id: "p1", Name: "prod-1"}, {Id: "p2", Name: "prod-2"}},
		}[account], nil
	}
	for _, tool := range []core.NBTool{MySQLExecuteTool{}, MSSQLExecuteTool{}, OracleExecuteTool{}, ClickhouseExecuteTool{}, ArgoCDExecuteTool{},
		DatadogMetricsExecuteTool{}, DatadogHostsTool{}, NewRelicAPMServicesTool{}, MongoDBTool{toolName: "mongodb_server_status_get"}} {
		require.True(t, IsTargetedIntegrationTool(nil, tool), tool.Name())

		own := core.NbToolContext{AccountId: "origin-id", TargetAccountId: "origin-id", ToolConfig: core.ToolConfig{Id: "o1", Name: "conn-1"}}
		resolved, err := ResolveToolCallTarget(own, tool, core.ParseNBToolCallRequestFromInput(`{"command":"x","instance":"conn-2"}`))
		require.NoError(t, err, tool.Name())
		assert.Equal(t, "o2", resolved.ToolConfig.Id, "%s: switches to the named connection", tool.Name())
		resolved, err = ResolveToolCallTarget(own, tool, core.ParseNBToolCallRequestFromInput(`{"command":"x"}`))
		require.NoError(t, err, tool.Name())
		assert.Equal(t, "o1", resolved.ToolConfig.Id, "%s: not named keeps the pick", tool.Name())

		other := core.NbToolContext{AccountId: "origin-id", TargetAccountId: "prod-id", ExecutedTarget: &core.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod", AccountID: "prod-id"}}
		picked, err := targetIntegrationConfig(other, tool, core.ParseNBToolCallRequestFromInput(`{"command":"x","instance":"prod-2"}`))
		require.NoError(t, err, tool.Name())
		assert.Equal(t, "p2", picked.Id, "%s: the target's named integration", tool.Name())
	}
}

// On another account an exact name wins over a tool's fuzzy matching: a
// host that merely contains the name must not pick a different integration.
func TestTargetIntegrationPrefersExactName(t *testing.T) {
	restore := listIntegrationConfigs
	t.Cleanup(func() { listIntegrationConfigs = restore })
	listIntegrationConfigs = func(*security.RequestContext, string, core.NBTool) ([]core.ToolConfig, error) {
		return []core.ToolConfig{
			{Id: "a", Name: "x", Values: []core.ToolConfigValue{{Name: "host", Value: "orders"}}},
			{Id: "b", Name: "orders-db"},
		}, nil
	}
	other := core.NbToolContext{AccountId: "origin-id", TargetAccountId: "prod-id", ExecutedTarget: &core.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod", AccountID: "prod-id"}}
	for _, tool := range []core.NBTool{OracleExecuteTool{}, ClickhouseExecuteTool{}, PostgresExecuteTool{}} {
		picked, err := targetIntegrationConfig(other, tool, core.ParseNBToolCallRequestFromInput(`{"query":"select 1","instance":"orders-db"}`))
		require.NoError(t, err, tool.Name())
		assert.Equal(t, "b", picked.Id, "%s: the integration named orders-db, not the one whose host contains orders", tool.Name())
	}
}

// Datadog logs and traces are answered by services-server from the account's
// Datadog integration: they follow the target account but pick no connection,
// so two configured connections are not an error and no connection is named.
func TestDatadogLogsAndTracesFollowTheAccountOnly(t *testing.T) {
	restore := listIntegrationConfigs
	t.Cleanup(func() { listIntegrationConfigs = restore })
	listIntegrationConfigs = func(*security.RequestContext, string, core.NBTool) ([]core.ToolConfig, error) {
		return []core.ToolConfig{{Id: "1", Name: "dd-us"}, {Id: "2", Name: "dd-eu"}}, nil
	}
	stubTargets(t)
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"origin-id", "prod-id"}), AccountId: "origin-id"}
	for _, tool := range []core.NBTool{DatadogLogExecuteTool{}, DatadogTracesExecuteTool{}} {
		assert.False(t, IsTargetedIntegrationTool(nil, tool), tool.Name())
		assert.NotContains(t, tool.InputSchema().Properties, "instance", tool.Name())
		resolved, err := ResolveToolCallTarget(ctx, tool, core.NBToolCallRequest{Arguments: map[string]any{"target": map[string]any{"provider": "kubernetes", "name": "k8s-prod"}}})
		require.NoError(t, err, tool.Name())
		assert.Equal(t, "prod-id", resolved.DataAccountID(), "%s reads the target account", tool.Name())
		assert.Empty(t, resolved.ToolConfig.Name, "%s names no connection", tool.Name())
	}
}

package watch

import (
	"encoding/json"
	"errors"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// toolSource — ValidateConfig
// ---------------------------------------------------------------------------

func TestToolSource_ValidateConfig_Happy(t *testing.T) {
	cfg := json.RawMessage(`{"tool_name":"shell_execute","tool_input":{"command":"echo ok"}}`)
	require.NoError(t, toolSource{}.ValidateConfig(cfg))
}

func TestToolSource_ValidateConfig_InvalidJSON(t *testing.T) {
	err := toolSource{}.ValidateConfig(json.RawMessage("{this is not json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid config json")
}

func TestToolSource_ValidateConfig_MissingToolName(t *testing.T) {
	err := toolSource{}.ValidateConfig(json.RawMessage(`{"tool_input":{"command":"echo"}}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool_name is required")
}

// fakeAgentTool stands in for any NBToolTypeAgent: kubectl, github,
// postgres, events — the sub-agents that load conversation history
// and therefore can't be used as watch sources.
type fakeAgentTool struct{}

func (fakeAgentTool) Name() string                     { return "fake_agent_tool" }
func (fakeAgentTool) Description() string              { return "" }
func (fakeAgentTool) GetType() toolcore.NBToolType     { return toolcore.NBToolTypeAgent }
func (fakeAgentTool) InputSchema() toolcore.ToolSchema { return toolcore.ToolSchema{} }
func (fakeAgentTool) Call(_ toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}

type fakePrimitiveTool struct{}

func (fakePrimitiveTool) Name() string                     { return "fake_primitive_tool" }
func (fakePrimitiveTool) Description() string              { return "" }
func (fakePrimitiveTool) GetType() toolcore.NBToolType     { return toolcore.NBToolTypeTool }
func (fakePrimitiveTool) InputSchema() toolcore.ToolSchema { return toolcore.ToolSchema{} }
func (fakePrimitiveTool) Call(_ toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}

// TestToolSource_ValidateConfig_RejectsAgentTool asserts the guard
// added in source.go: agent-style tools (NBToolTypeAgent) cannot be
// used as a watch source because a poll has no parent message context
// and the agent's history loader fails on synthetic UUIDs. The error
// must point the LLM at the *_execute primitive variant so the next
// attempt picks the right tool.
func TestToolSource_ValidateConfig_RejectsAgentTool(t *testing.T) {
	// The tool factory registry is process-global with no unregister hook.
	// Names are unique to these tests so leaking them across tests in the
	// same binary is harmless.
	toolcore.RegisterNBToolFactory("fake_agent_tool_watch_test", func(_ string) (toolcore.NBTool, error) {
		return fakeAgentTool{}, nil
	})

	cfg := json.RawMessage(`{"tool_name":"fake_agent_tool_watch_test","tool_input":{"command":"x"}}`)
	err := toolSource{}.ValidateConfig(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent-style tool")
	assert.Contains(t, err.Error(), "*_execute")
}

// TestToolSource_ValidateConfig_AcceptsPrimitiveTool guards against an
// over-broad version of the agent-tool reject: primitive (NBToolTypeTool)
// sources MUST stay allowed. Without this test a future refactor could
// accidentally tighten the check and silently break every watch.
func TestToolSource_ValidateConfig_AcceptsPrimitiveTool(t *testing.T) {
	toolcore.RegisterNBToolFactory("fake_primitive_tool_watch_test", func(_ string) (toolcore.NBTool, error) {
		return fakePrimitiveTool{}, nil
	})

	cfg := json.RawMessage(`{"tool_name":"fake_primitive_tool_watch_test","tool_input":{"command":"echo ok"}}`)
	require.NoError(t, toolSource{}.ValidateConfig(cfg))
}

// TestToolSource_ValidateConfig_RejectsMutatingCommand pins the
// destructive-verb deny-list. A watch re-runs its source command every
// poll_interval_sec under a static admin context — registering a watch
// on a mutating command would amplify the blast radius substantially.
// The deny-list is fail-fast nudge, not a complete sandbox, so we only
// pin the obvious cases per family.
func TestToolSource_ValidateConfig_RejectsMutatingCommand(t *testing.T) {
	toolcore.RegisterNBToolFactory("fake_primitive_tool_watch_mutation", func(_ string) (toolcore.NBTool, error) {
		return fakePrimitiveTool{}, nil
	})

	mutations := []string{
		`kubectl delete pod foo -n bar`,
		`kubectl rollout restart deploy/foo`,
		`kubectl apply -f manifest.yaml`,
		`helm uninstall my-release`,
		`rm -rf /tmp/blast`,
		`gh release delete v1.0.0 --yes`,
		`gh pr close 42`,
		`aws s3api delete-bucket --bucket mine`,
		`aws ec2 terminate-instances --instance-ids i-1234`,
		`terraform apply -auto-approve`,
		`argocd app sync my-app`,
		`flux reconcile source git my-repo`,
	}
	for _, cmd := range mutations {
		cfgBlob := json.RawMessage(`{"tool_name":"fake_primitive_tool_watch_mutation","tool_input":{"command":` + jsonStringLiteral(cmd) + `}}`)
		err := toolSource{}.ValidateConfig(cfgBlob)
		require.Error(t, err, "expected mutating command to be rejected: %q", cmd)
		require.Contains(t, err.Error(), "mutating verb",
			"reject error must name the failure mode so the agent can self-correct: %q", cmd)
	}
}

// TestToolSource_ValidateConfig_AcceptsReadOnlyCommand confirms the
// deny-list does not over-trigger on the canonical read-only watch
// commands the prompt and tool description recommend.
func TestToolSource_ValidateConfig_AcceptsReadOnlyCommand(t *testing.T) {
	toolcore.RegisterNBToolFactory("fake_primitive_tool_watch_ro", func(_ string) (toolcore.NBTool, error) {
		return fakePrimitiveTool{}, nil
	})

	reads := []string{
		`kubectl rollout status deploy/foo -n bar`,
		`kubectl get deploy foo -o jsonpath='{.status.readyReplicas}'`,
		`kubectl wait --for=condition=Complete --timeout=0 job/foo`,
		`gh run view 12345 --repo o/r --json status -q .status`,
		`aws cloudformation describe-stacks --stack-name s`,
		`helm status my-release`,
		`argocd app get my-app -o json`,
	}
	for _, cmd := range reads {
		cfgBlob := json.RawMessage(`{"tool_name":"fake_primitive_tool_watch_ro","tool_input":{"command":` + jsonStringLiteral(cmd) + `}}`)
		require.NoError(t, toolSource{}.ValidateConfig(cfgBlob),
			"canonical read-only watch source command was rejected by deny-list: %q", cmd)
	}
}

func jsonStringLiteral(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestToolSource_ValidateConfig_UnknownToolStillPasses documents the
// fall-through: if the tool isn't registered at validate time we let it
// pass. The Observe step will fail explicitly with "tool ... not
// registered for account", which is a clearer error than a generic
// reject here. Locking this in so a future "stricter at validate time"
// refactor is intentional, not accidental.
func TestToolSource_ValidateConfig_UnknownToolStillPasses(t *testing.T) {
	cfg := json.RawMessage(`{"tool_name":"definitely_not_registered","tool_input":{"command":"x"}}`)
	require.NoError(t, toolSource{}.ValidateConfig(cfg))
}

// TestToolSource_Observe_RejectsAgentTool exercises the defense-in-depth
// reject inside Observe. ValidateConfig's reject runs with an empty
// account ID and therefore cannot see per-account custom tools, so a
// custom agent-style tool would slip past validation. Without this
// second check, that watch would fail at observe time *and* count those
// as retryable failures, terminating only after WatchMaxFailures of
// noise. The reject here fails the poll cleanly on the first try.
func TestToolSource_Observe_RejectsAgentTool(t *testing.T) {
	// Re-uses the fakeAgentTool registered at the top of this file. The
	// tool registry is process-global and persists for the duration of
	// the test binary.
	toolcore.RegisterNBToolFactory("fake_agent_tool_watch_observe_test", func(_ string) (toolcore.NBTool, error) {
		return fakeAgentTool{}, nil
	})

	w := Watch{
		AccountID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		TenantID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		SourceConfig: `{"tool_name":"fake_agent_tool_watch_observe_test","tool_input":{"command":"x"}}`,
	}
	_, err := toolSource{}.Observe(&security.RequestContext{}, w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent-style tool")
	assert.Contains(t, err.Error(), "*_execute")
}

// ---------------------------------------------------------------------------
// sqlSource — ValidateConfig
// ---------------------------------------------------------------------------

func TestSQLSource_ValidateConfig_RejectsWhenGateOff(t *testing.T) {
	// Default state: WatchSqlSourceEnabled=false. Even a perfectly valid
	// SELECT must be rejected at create time.
	prev := config.Config.WatchSqlSourceEnabled
	config.Config.WatchSqlSourceEnabled = false
	t.Cleanup(func() { config.Config.WatchSqlSourceEnabled = prev })

	cfg := json.RawMessage(`{"query":"SELECT 1"}`)
	err := sqlSource{}.ValidateConfig(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
	assert.Contains(t, err.Error(), "LLM_SERVER_WATCH_SQL_SOURCE_ENABLED")
}

func TestSQLSource_ValidateConfig_HappyWhenGateOn(t *testing.T) {
	prev := config.Config.WatchSqlSourceEnabled
	config.Config.WatchSqlSourceEnabled = true
	t.Cleanup(func() { config.Config.WatchSqlSourceEnabled = prev })

	cfg := json.RawMessage(`{"query":"SELECT count(*) FROM cloud_accounts"}`)
	require.NoError(t, sqlSource{}.ValidateConfig(cfg))
}

func TestSQLSource_ValidateConfig_RejectsNonMetastoreDatasource(t *testing.T) {
	prev := config.Config.WatchSqlSourceEnabled
	config.Config.WatchSqlSourceEnabled = true
	t.Cleanup(func() { config.Config.WatchSqlSourceEnabled = prev })

	cfg := json.RawMessage(`{"datasource":"clickhouse","query":"SELECT 1"}`)
	err := sqlSource{}.ValidateConfig(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `datasource "clickhouse" not allowed`)
}

func TestSQLSource_ValidateConfig_DefaultsDatasourceToMetastore(t *testing.T) {
	// Empty datasource is treated as "metastore" (it's the only allowed one
	// today). Lock that in so we don't accidentally flip the default to
	// something else later.
	prev := config.Config.WatchSqlSourceEnabled
	config.Config.WatchSqlSourceEnabled = true
	t.Cleanup(func() { config.Config.WatchSqlSourceEnabled = prev })

	cfg := json.RawMessage(`{"query":"SELECT 1"}`)
	require.NoError(t, sqlSource{}.ValidateConfig(cfg))
}

func TestSQLSource_ValidateConfig_RejectsEmptyQuery(t *testing.T) {
	prev := config.Config.WatchSqlSourceEnabled
	config.Config.WatchSqlSourceEnabled = true
	t.Cleanup(func() { config.Config.WatchSqlSourceEnabled = prev })

	cfg := json.RawMessage(`{"query":""}`)
	err := sqlSource{}.ValidateConfig(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query is required")
}

func TestSQLSource_ValidateConfig_RejectsMutatingPrefixes(t *testing.T) {
	prev := config.Config.WatchSqlSourceEnabled
	config.Config.WatchSqlSourceEnabled = true
	t.Cleanup(func() { config.Config.WatchSqlSourceEnabled = prev })

	for _, q := range []string{
		"DELETE FROM users WHERE id = 1",
		"UPDATE users SET name = 'x'",
		"INSERT INTO users VALUES (1)",
		"DROP TABLE users",
		"TRUNCATE users",
		"ALTER TABLE users ADD COLUMN x int",
		"  delete from users", // case + whitespace insensitivity
	} {
		t.Run(q, func(t *testing.T) {
			err := sqlSource{}.ValidateConfig(json.RawMessage(`{"query":` + jsonString(q) + `}`))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "only SELECT/WITH queries")
		})
	}
}

func TestSQLSource_ValidateConfig_AllowsSELECTandWITH(t *testing.T) {
	prev := config.Config.WatchSqlSourceEnabled
	config.Config.WatchSqlSourceEnabled = true
	t.Cleanup(func() { config.Config.WatchSqlSourceEnabled = prev })

	for _, q := range []string{
		"SELECT 1",
		"  select 1",
		"(SELECT 1)",
		"WITH x AS (SELECT 1) SELECT * FROM x",
		"with x as (select 1) select * from x",
	} {
		t.Run(q, func(t *testing.T) {
			err := sqlSource{}.ValidateConfig(json.RawMessage(`{"query":` + jsonString(q) + `}`))
			require.NoError(t, err)
		})
	}
}

// hasMutatingPrefix is the prefix-only validate-time check. CTE-wrapped DML
// (`WITH x AS (DELETE ...) SELECT ...`) is intentionally NOT caught here —
// the READ ONLY transaction at observe-time is what blocks it. Lock that
// boundary in so a future "tighten validate" change knows to also keep the
// READ ONLY tx and not break behavior assumptions.
func TestSQLSource_ValidateConfig_DoesNotCatchCTEWrappedDML(t *testing.T) {
	prev := config.Config.WatchSqlSourceEnabled
	config.Config.WatchSqlSourceEnabled = true
	t.Cleanup(func() { config.Config.WatchSqlSourceEnabled = prev })

	cfg := json.RawMessage(`{"query":"WITH x AS (DELETE FROM users RETURNING *) SELECT * FROM x"}`)
	// Validate-time PASSES this. Engine-time READ ONLY transaction REJECTS
	// it. That's the documented design.
	require.NoError(t, sqlSource{}.ValidateConfig(cfg))
}

// ---------------------------------------------------------------------------
// hasMutatingPrefix — direct unit
// ---------------------------------------------------------------------------

func TestHasMutatingPrefix(t *testing.T) {
	cases := []struct {
		q       string
		mutates bool
	}{
		{"SELECT 1", false},
		{"select 1", false},
		{"  SELECT 1", false},
		{"(SELECT 1)", false},
		{"WITH x AS (SELECT 1) SELECT * FROM x", false},
		{"with x as (select 1) select * from x", false},
		{"\n\nWITH x AS (SELECT 1)\nSELECT * FROM x", false},
		{"DELETE FROM users", true},
		{"INSERT INTO x VALUES (1)", true},
		{"UPDATE x SET y = 1", true},
		{"DROP TABLE x", true},
		{"TRUNCATE TABLE x", true},
		{"ALTER TABLE x", true},
		{"; SELECT 1", true},           // leading non-SELECT char isn't whitespace
		{"-- comment\nSELECT 1", true}, // SQL comments aren't stripped
	}
	for _, tc := range cases {
		t.Run(tc.q, func(t *testing.T) {
			assert.Equal(t, tc.mutates, hasMutatingPrefix(tc.q))
		})
	}
}

// ---------------------------------------------------------------------------
// toolSource — Observe (with stub tool registry)
// ---------------------------------------------------------------------------

// fakeTool implements toolcore.NBTool. Each test installs one via
// toolcore.RegisterNBToolFactory under a unique name.
type fakeTool struct {
	name   string
	resp   toolcore.NBToolResponse
	err    error
	called int
}

func (f *fakeTool) Name() string                 { return f.name }
func (f *fakeTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }
func (f *fakeTool) Description() string          { return "fake" }
func (f *fakeTool) GetNameAliases() []string     { return nil }
func (f *fakeTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{Type: toolcore.ToolSchemaTypeObject}
}
func (f *fakeTool) Call(_ toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	f.called++
	return f.resp, f.err
}

func registerFakeTool(t *testing.T, accountID string, ft *fakeTool) {
	t.Helper()
	toolcore.RegisterNBToolFactory(ft.name, func(string) (toolcore.NBTool, error) {
		return ft, nil
	})
	// The factory is registered globally; no explicit cleanup is exposed in
	// the toolcore API, but the test name keeps the registration unique
	// across tests.
}

func freshAccountID() string { return uuid.NewString() }

func TestToolSource_Observe_ToolNotRegistered(t *testing.T) {
	accountID := freshAccountID()
	w := Watch{
		AccountID:    uuid.MustParse(accountID),
		SourceConfig: `{"tool_name":"definitely_not_registered_tool","tool_input":{}}`,
	}
	_, err := toolSource{}.Observe(superAdminCtx(), w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")
}

func TestToolSource_Observe_ToolReturnsError(t *testing.T) {
	accountID := freshAccountID()
	toolName := "fake_observe_err_" + accountID[:8]
	registerFakeTool(t, accountID, &fakeTool{
		name: toolName,
		err:  errors.New("rate limited"),
	})
	w := Watch{
		AccountID:    uuid.MustParse(accountID),
		SourceConfig: `{"tool_name":"` + toolName + `","tool_input":{}}`,
	}
	_, err := toolSource{}.Observe(superAdminCtx(), w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invocation failed")
	assert.Contains(t, err.Error(), "rate limited")
}

func TestToolSource_Observe_ToolReturnsErrorStatus(t *testing.T) {
	// Tool returns success at the Go level but with Status=Error in the
	// response payload. Source must surface it as an error so the executor
	// counts it toward failure_count rather than treating it as a successful
	// observation.
	accountID := freshAccountID()
	toolName := "fake_observe_status_err_" + accountID[:8]
	registerFakeTool(t, accountID, &fakeTool{
		name: toolName,
		resp: toolcore.NBToolResponse{
			Status: toolcore.NBToolResponseStatusError,
			Data:   "permission denied",
		},
	})
	w := Watch{
		AccountID:    uuid.MustParse(accountID),
		SourceConfig: `{"tool_name":"` + toolName + `","tool_input":{}}`,
	}
	_, err := toolSource{}.Observe(superAdminCtx(), w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "returned error")
	assert.Contains(t, err.Error(), "permission denied")
}

func TestToolSource_Observe_HappyPath(t *testing.T) {
	accountID := freshAccountID()
	toolName := "fake_observe_happy_" + accountID[:8]
	ft := &fakeTool{
		name: toolName,
		resp: toolcore.NBToolResponse{
			Status: toolcore.NBToolResponseStatusSuccess,
			Data:   `{"stdout":"deployment x successfully rolled out\n"}`,
		},
	}
	registerFakeTool(t, accountID, ft)
	w := Watch{
		AccountID:    uuid.MustParse(accountID),
		SourceConfig: `{"tool_name":"` + toolName + `","tool_input":{"command":"kubectl rollout status …"}}`,
	}
	obs, err := toolSource{}.Observe(superAdminCtx(), w)
	require.NoError(t, err)
	assert.Contains(t, obs.Data, "successfully rolled out")
	assert.False(t, obs.Truncated)
	assert.Equal(t, 1, ft.called)
}

func TestToolSource_Observe_CorruptedSourceConfig(t *testing.T) {
	w := Watch{
		AccountID:    uuid.New(),
		SourceConfig: "not-json-at-all",
	}
	_, err := toolSource{}.Observe(superAdminCtx(), w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "corrupted config")
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func superAdminCtx() *security.RequestContext {
	return security.NewRequestContextForSuperAdmin()
}

// jsonString quotes s for inclusion in a JSON object literal.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

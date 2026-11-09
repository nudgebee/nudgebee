package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/watch"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tool_watch.go is the agent-facing entry point for the watch feature —
// every watch the LLM registers flows through Call() — but had ZERO tests
// before this file. Senior code review (review session, 2026-05-13)
// called this out as the highest-leverage missing coverage.
//
// What we pin here:
//   - WatchEnabled=false short-circuits with an error response, no Manager
//     touched (the dark-feature contract).
//   - parseWatchInput rejects empty / malformed args at the boundary
//     instead of letting the Manager catch them later.
//   - Tenant + account + conversation_id validation rejects missing /
//     non-UUID values BEFORE the Manager.Create call (the validation
//     fence — defense in depth against a future security context regression).
//
// What we do NOT test here: the happy path through Manager.Create. That
// path requires a real *sqlx.DB injected into the Manager and tool_watch
// currently constructs `watch.NewManager()` inline (no DI seam). Adding
// the seam is a follow-up; the validation gates above are higher value
// because they're what stops bad input from ever reaching the DB.

// withWatchEnabled toggles config.Config.WatchEnabled for the test's
// lifetime. Single-field write — NOT the whole struct, so it does not
// race with the executor_test.go-flagged race window on `withConfig`.
// (`withConfig` was the racy pattern because it memcpy'd the entire
// config struct in/out, colliding with concurrent reads from the cluster
// scheduler.)
func withWatchEnabled(t *testing.T, enabled bool) {
	t.Helper()
	prev := config.Config.WatchEnabled
	config.Config.WatchEnabled = enabled
	t.Cleanup(func() { config.Config.WatchEnabled = prev })
}

// validToolArgs returns a minimally well-formed argument map. Tests that
// want to exercise validation gates start from this and mutate.
func validToolArgs() map[string]any {
	return map[string]any{
		"source_kind": string(watchSourceKindTool),
		"source_config": map[string]any{
			"tool_name":  "shell_execute",
			"tool_input": map[string]any{"command": "echo ok"},
		},
		"predicate_kind":    "regex",
		"predicate_expr":    "ok",
		"poll_interval_sec": 60,
		"max_duration_sec":  3600,
	}
}

const watchSourceKindTool = "tool"

// validToolCtx returns an nbCtx that has every field the Call method
// reads populated correctly — tenant, account, conversation, user.
// Tests mutate fields to exercise failure paths.
//
// NOTE: NewSecurityContextForTenantAccountAdmin with a non-empty
// accountIds slice skips the GetAccountIdsForTenant DB lookup, keeping
// these tests pure unit (no metastore required).
func validToolCtx(t *testing.T) core.NbToolContext {
	t.Helper()
	sc := security.NewSecurityContextForTenantAccountAdmin(
		"11111111-1111-1111-1111-111111111111", // tenant
		"22222222-2222-2222-2222-222222222222", // user
		[]string{"33333333-3333-3333-3333-333333333333"},
	)
	require.NotNil(t, sc, "security context build must not hit the DB path here — fix the test fixture if it did")
	rctx := security.NewRequestContext(context.Background(), sc, nil, nil, nil)
	return core.NbToolContext{
		AccountId:      "33333333-3333-3333-3333-333333333333",
		UserId:         "22222222-2222-2222-2222-222222222222",
		ConversationId: "44444444-4444-4444-4444-444444444444",
		MessageId:      "55555555-5555-5555-5555-555555555555",
		Ctx:            rctx,
	}
}

// callWith constructs a request and invokes WatchResourceTool.Call. Returns
// the response and decoded body for assertion.
func callWith(t *testing.T, nbCtx core.NbToolContext, args map[string]any) core.NBToolResponse {
	t.Helper()
	resp, err := WatchResourceTool{}.Call(nbCtx, core.NBToolCallRequest{
		Arguments: args,
	})
	require.NoError(t, err, "Call must not surface error via the err return — error responses come through resp.Status=Error")
	return resp
}

func TestWatchResourceTool_Disabled_ReturnsErrorResponse(t *testing.T) {
	withWatchEnabled(t, false)

	resp := callWith(t, validToolCtx(t), validToolArgs())
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status,
		"WatchEnabled=false must produce an Error response; the agent surfaces it to the user as 'feature disabled'")
	assert.Contains(t, resp.Data, "disabled")
	assert.Contains(t, resp.Data, "LLM_SERVER_WATCH_ENABLED",
		"error message must mention the env var so on-call can enable it without spelunking source")
}

func TestWatchResourceTool_EmptyArguments_Rejects(t *testing.T) {
	withWatchEnabled(t, true)

	resp := callWith(t, validToolCtx(t), map[string]any{})
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "empty arguments",
		"empty args from a misbehaving planner must fail at the tool boundary, not at Manager.Create")
}

func TestWatchResourceTool_MalformedTypes_RejectsAtParse(t *testing.T) {
	// poll_interval_sec is an integer in the schema; sending a string
	// must fail parseWatchInput's json.Unmarshal — not silently coerce.
	withWatchEnabled(t, true)
	args := validToolArgs()
	args["poll_interval_sec"] = "not a number"

	resp := callWith(t, validToolCtx(t), args)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "invalid arguments",
		"type mismatch in arguments must fail at parseWatchInput, not propagate as a clamping bug")
}

func TestWatchResourceTool_MissingConversationID_Rejects(t *testing.T) {
	withWatchEnabled(t, true)
	nbCtx := validToolCtx(t)
	nbCtx.ConversationId = "" // a watch with no conversation cannot deliver its notification

	resp := callWith(t, nbCtx, validToolArgs())
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "conversation",
		"watches must be conversation-scoped; missing conversation_id is a hard reject")
}

func TestWatchResourceTool_InvalidConversationUUID_Rejects(t *testing.T) {
	withWatchEnabled(t, true)
	nbCtx := validToolCtx(t)
	nbCtx.ConversationId = "not-a-uuid"

	resp := callWith(t, nbCtx, validToolArgs())
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "conversation")
}

func TestWatchResourceTool_MissingAccountID_Rejects(t *testing.T) {
	withWatchEnabled(t, true)
	nbCtx := validToolCtx(t)
	nbCtx.AccountId = ""

	resp := callWith(t, nbCtx, validToolArgs())
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "account_id",
		"missing account_id means the watch source can't resolve per-account tool configs — hard reject")
}

func TestWatchResourceTool_InvalidAccountUUID_Rejects(t *testing.T) {
	withWatchEnabled(t, true)
	nbCtx := validToolCtx(t)
	nbCtx.AccountId = "definitely-not-a-uuid"

	resp := callWith(t, nbCtx, validToolArgs())
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "account_id")
}

func TestWatchResourceTool_MissingTenantInSecurityContext_Rejects(t *testing.T) {
	// SuperAdmin's tenant id is empty string — must fail the tenant check.
	// This is the "request without a tenant_id header" path which would
	// otherwise let a forged watch land in tenant uuid.Nil's bucket and
	// silently never be polled (FetchDue's tenant scope would skip it).
	withWatchEnabled(t, true)
	nbCtx := core.NbToolContext{
		AccountId:      "33333333-3333-3333-3333-333333333333",
		ConversationId: "44444444-4444-4444-4444-444444444444",
		Ctx:            security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdmin(), nil, nil, nil),
	}

	resp := callWith(t, nbCtx, validToolArgs())
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "tenant",
		"a request lacking a tenant context must hard-reject — silent tenant=uuid.Nil would orphan the watch")
}

// TestWatchResourceTool_Description_MentionsLoadBearingHints is a regression
// fence for the tool's Description() output. Description is what the
// planner LLM reads to learn how to invoke the tool. We've twice now
// debugged failures whose root cause was the planner picking an
// agent-style tool (`kubectl`) as source.tool_name instead of the
// primitive (`kubectl_execute`), and twice now passing tool_input as a
// bare string instead of `{"command": …}`. Both shapes are documented
// in Description() — pin them so a future "tighten Description" edit
// can't silently drop the guidance.
func TestWatchResourceTool_Description_MentionsLoadBearingHints(t *testing.T) {
	d := WatchResourceTool{}.Description()
	for _, want := range []string{
		"_execute",                   // primitive-tool guidance
		"tool_input",                 // shape header
		`"tool_input": { "command":`, // canonical example
		`"tool_input": "gh run view`, // anti-example
		"source_kind",                // schema header
		"predicate_kind",             // schema header
		"poll_interval_sec",          // schema header
	} {
		assert.True(t, strings.Contains(d, want),
			"Description() lost load-bearing guidance %q — planner LLM relies on this to invoke the tool correctly", want)
	}
}

// TestWatchResourceTool_InputSchema_MarksRequiredFields ensures the schema
// the planner reads still requires the fields without which Call would
// 400. A schema regression (forgetting a required field) lets the LLM
// emit invalid invocations and the failure surfaces only at Call time
// with a less-precise error message.
func TestWatchResourceTool_InputSchema_MarksRequiredFields(t *testing.T) {
	s := WatchResourceTool{}.InputSchema()
	required := map[string]bool{}
	for _, r := range s.Required {
		required[r] = true
	}
	for _, mustRequire := range []string{
		"source_kind", "source_config", "predicate_kind", "predicate_expr",
		"poll_interval_sec", "max_duration_sec",
	} {
		assert.True(t, required[mustRequire],
			"InputSchema must mark %q as required — without it the planner LLM can omit it and Call would return a less-specific error", mustRequire)
	}
}

// TestWatchTools_RequestTypeInference pins the auth classification for the
// four watch tools. They are globally injected, so auth_agent.go relies on
// InferToolRequestType to route writes through the per-account create/update
// access gate. A tool that fell back to the default Read would let a
// read-only user's agent register or cancel side-effecting watches.
func TestWatchTools_RequestTypeInference(t *testing.T) {
	cases := []struct {
		tool core.NBTool
		want core.ToolRequestType
	}{
		{WatchResourceTool{}, core.ToolRequestTypeCreate},
		{WatchCancelTool{}, core.ToolRequestTypeUpdate},
		{WatchStatusTool{}, core.ToolRequestTypeRead},
		{WatchListTool{}, core.ToolRequestTypeRead},
	}
	for _, tc := range cases {
		inf, ok := tc.tool.(core.ToolRequestInference)
		assert.True(t, ok, "%s must implement ToolRequestInference so the auth gate classifies it", tc.tool.Name())
		got, err := inf.InferToolRequestType(nil, tc.tool.Name(), "")
		assert.NoError(t, err)
		assert.Equal(t, tc.want, got, "%s request-type classification", tc.tool.Name())
	}
}

// ---------------------------------------------------------------------------
// Priming poll
//
// watch_resource now runs the source once, synchronously, before committing a
// row. These pin the two branches that short-circuit registration — the third
// (not yet met -> register) needs a metastore and is covered in watch/.
// ---------------------------------------------------------------------------

// primeFakeSource is a watch.Source registered under a unique kind so the tool
// can be driven without a real integration behind it.
type primeFakeSource struct {
	kind watch.SourceKind
	data string
	err  error
}

func (p *primeFakeSource) Kind() watch.SourceKind                 { return p.kind }
func (p *primeFakeSource) ValidateConfig(_ json.RawMessage) error { return nil }
func (p *primeFakeSource) Observe(_ *security.RequestContext, _ watch.Watch) (watch.Observation, error) {
	if p.err != nil {
		return watch.Observation{}, p.err
	}
	return watch.Observation{Data: p.data}, nil
}

func primeArgs(kind watch.SourceKind, predicate, expr string) map[string]any {
	return map[string]any{
		"source_kind":       string(kind),
		"source_config":     map[string]any{"tool_name": "shell_execute", "tool_input": map[string]any{"command": "true"}},
		"predicate_kind":    predicate,
		"predicate_expr":    expr,
		"poll_interval_sec": 30,
		"max_duration_sec":  600,
	}
}

// TestWatchResourceTool_PrimingAlreadyMet_RegistersNothing: when the condition
// already holds, the agent must be told to answer now rather than promising a
// notification that would arrive a poll interval later for something already
// finished. Reaching Manager.Create here would hit the metastore, so a clean
// ALREADY_MET response is also proof no row was attempted.
func TestWatchResourceTool_PrimingAlreadyMet_RegistersNothing(t *testing.T) {
	withWatchEnabled(t, true)
	src := &primeFakeSource{kind: watch.SourceKind("tool_test_prime_met"), data: "deployment rolled out"}
	watch.RegisterSource(src)

	resp := callWith(t, validToolCtx(t), primeArgs(src.kind, "substring", "rolled out"))

	require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(resp.Data), &body))
	assert.Equal(t, "ALREADY_MET", body["status"])
	assert.Nil(t, body["watch_id"], "no watch may be registered when the condition already holds")
	assert.Contains(t, body["message"], "do not say you will notify them later",
		"the agent must be steered away from promising a follow-up")
}

// TestWatchResourceTool_PrimingObserveFailure_RefusesRegistration: every FAILED
// watch in the dev metastore is an observe failure. Surfacing it at
// registration lets the agent fix the call in the same turn instead of the
// user learning about it three polls later.
func TestWatchResourceTool_PrimingObserveFailure_RefusesRegistration(t *testing.T) {
	withWatchEnabled(t, true)
	src := &primeFakeSource{
		kind: watch.SourceKind("tool_test_prime_err"),
		err:  errors.New("no tool configs found for - github_execute, please configure"),
	}
	watch.RegisterSource(src)

	resp := callWith(t, validToolCtx(t), primeArgs(src.kind, "regex", "completed"))

	require.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "No watch was registered")
	assert.Contains(t, resp.Data, "no tool configs found",
		"the agent needs the actual failure, not a generic message, to correct the call")
}

// TestWatchResourceTool_PrimingDisabled_SkipsTheProbe: setting the timeout to 0
// is the operator escape hatch. With priming off, a source that would have
// failed the probe must not block registration — the call proceeds to Create
// (which then fails on the absent metastore, proving the probe was skipped).
func TestWatchResourceTool_PrimingDisabled_SkipsTheProbe(t *testing.T) {
	withWatchEnabled(t, true)
	prev := config.Config.WatchPrimingPollTimeoutSec
	config.Config.WatchPrimingPollTimeoutSec = 0
	t.Cleanup(func() { config.Config.WatchPrimingPollTimeoutSec = prev })

	src := &primeFakeSource{
		kind: watch.SourceKind("tool_test_prime_disabled"),
		err:  errors.New("would have failed the probe"),
	}
	watch.RegisterSource(src)

	resp := callWith(t, validToolCtx(t), primeArgs(src.kind, "regex", "completed"))

	require.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.NotContains(t, resp.Data, "would have failed the probe",
		"probe must not run when priming is disabled")
	assert.Contains(t, resp.Data, "failed to register watch",
		"the call should have proceeded past priming to Create")
}

// watch_status and watch_cancel look a watch up by tenant, so the watch's
// account decides who may see or cancel it: the same rule as the UI's cancel
// (api/watches.go). An admin of one account gets nothing of another's.
func TestWatchLifecycle_AccessFollowsTheWatchesAccount(t *testing.T) {
	own, other := uuid.New(), uuid.New()
	nbCtx := core.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin(uuid.NewString(), "user", []string{own.String()})}

	mine := &watch.Watch{AccountID: own}
	assert.True(t, canAccessWatchAccount(nbCtx, mine, security.SecurityAccessTypeRead))
	assert.True(t, canAccessWatchAccount(nbCtx, mine, security.SecurityAccessTypeUpdate))

	theirs := &watch.Watch{AccountID: other}
	assert.False(t, canAccessWatchAccount(nbCtx, theirs, security.SecurityAccessTypeRead), "another account's watch is not visible")
	assert.False(t, canAccessWatchAccount(nbCtx, theirs, security.SecurityAccessTypeUpdate), "another account's watch cannot be cancelled")

	assert.False(t, canAccessWatchAccount(core.NbToolContext{}, mine, security.SecurityAccessTypeRead), "no caller, no access")

	// A custom-role user with the ai_misc grant keeps what the UI gives them.
	var custom security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"`+uuid.NewString()+`","UserId":"user-2","AccountIds":["`+own.String()+`"],"Roles":[],"CustomPermissions":{"ai_misc:Execute":true}}`), &custom))
	require.False(t, custom.HasAccountAccess(own.String(), security.SecurityAccessTypeUpdate), "no built-in role: only the grant can let it through")
	customCtx := core.NbToolContext{Ctx: security.NewRequestContext(context.Background(), &custom, nil, nil, nil)}
	assert.True(t, canAccessWatchAccount(customCtx, mine, security.SecurityAccessTypeUpdate), "custom role with ai_misc Execute may cancel")
	assert.False(t, canAccessWatchAccount(customCtx, theirs, security.SecurityAccessTypeUpdate), "but not on an account outside its tenant scope")
}

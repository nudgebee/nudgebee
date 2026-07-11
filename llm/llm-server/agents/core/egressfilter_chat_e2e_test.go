//go:build e2e

// Chat-flow e2e tests for the egressfilter. Unlike egressfilter_e2e_test.go (which
// exercises the wrapper directly against a real LLM provider), these tests
// drive the full conversation path — agent factory + planner +
// HandleConversationSessionRequest — so the failed run shows up in the UI
// as a real conversation row keyed by session_id.
//
// They use TEST_TENANT / TEST_ACCOUNT / TEST_USER from .env (same pattern
// as the existing agent e2e tests under agents/) and require:
//   - PostgreSQL reachable for conversation persistence
//   - LLM provider configured (LLM_PROVIDER / LLM_MODEL_NAME /
//     LLM_PROVIDER_API_KEY)
//
// Run:
//   go test -tags=e2e -v -run TestE2E_EgressFilterChat ./agents/core/...
//
// Each test logs the session_id it used so you can look up the conversation
// in the UI to verify the egressfilter's behavior end-to-end.
package core

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/security/egressfilter"
)

// fetchEgressFilterMetadataForSession runs a raw SQL lookup for the latest
// message in a conversation identified by session_id and returns the
// parsed egressfilter event slice from its metadata column. Used in the
// chat-flow e2e tests to verify the full event-write path through the
// conversation handler and DAO.
func fetchEgressFilterMetadataForSession(t *testing.T, accountID, sessionID string) []egressfilter.FilterEvent {
	t.Helper()
	conv, err := GetConversationDao().GetConversationBySession(accountID, sessionID)
	require.NoError(t, err, "session %q must have a conversation row", sessionID)

	var rawMetadata []byte
	dbm := GetConversationDao().(*ConversationDao).dbManager
	// Pull the most recent human-or-router message row for this conversation.
	row := dbm.Db.QueryRow(
		`SELECT metadata FROM llm_conversation_messages
		 WHERE conversation_id = $1 ORDER BY created_at DESC LIMIT 1`,
		conv.ID.String())
	require.NoError(t, row.Scan(&rawMetadata))
	if len(rawMetadata) == 0 {
		return nil
	}
	var blob map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rawMetadata, &blob))
	raw, ok := blob["egressfilter"]
	if !ok {
		return nil
	}
	var events []egressfilter.FilterEvent
	require.NoError(t, json.Unmarshal(raw, &events))
	return events
}

// chatE2EEnv mirrors loadE2EEnv in egressfilter_e2e_test.go but additionally
// requires the tenant/account/user IDs needed to drive a real conversation.
// All values come from environment variables; no internal IDs appear in this
// file.
type chatE2EEnv struct {
	tenant  string
	account string
	user    string
}

func loadChatE2EEnv(t *testing.T) chatE2EEnv {
	t.Helper()
	env := chatE2EEnv{
		tenant:  os.Getenv("TEST_TENANT"),
		account: os.Getenv("TEST_ACCOUNT"),
		user:    os.Getenv("TEST_USER"),
	}
	if env.tenant == "" || env.account == "" || env.user == "" {
		t.Skip("TEST_TENANT / TEST_ACCOUNT / TEST_USER must be set for chat-flow e2e tests")
	}
	// Don't echo the values themselves — just confirm presence.
	t.Logf("chat e2e env: tenant=<%d bytes> account=<%d bytes> user=<%d bytes>",
		len(env.tenant), len(env.account), len(env.user))
	return env
}

// enableEgressFilterForTest flips the master + per-detector flags + mode for the
// duration of the test and cleans up after. Invalidates the LLM client cache
// before and after so flag changes take effect immediately.
func enableEgressFilterForTest(t *testing.T, mode egressfilter.Mode) {
	t.Helper()
	config.Config.LlmServerEgressFilterEnabled = true
	config.Config.LlmServerEgressFilterSecretsEnabled = true
	config.Config.LlmServerEgressFilterSecretsMode = string(mode)
	InvalidateAllLLMClientCache()
	t.Cleanup(func() {
		config.Config.LlmServerEgressFilterEnabled = false
		config.Config.LlmServerEgressFilterSecretsEnabled = false
		config.Config.LlmServerEgressFilterSecretsMode = string(egressfilter.ModeAudit)
		InvalidateAllLLMClientCache()
	})
}

// TestE2E_EgressFilterChat_BlocksSecret_Enforce drives a real conversation
// through the LLM agent with enforce mode on and a baseline-rule-matching
// secret in the query. The egressfilter must block the call, the conversation
// must be persisted as failed, and the session_id is logged so it can be
// looked up in the UI.
func TestE2E_EgressFilterChat_BlocksSecret_Enforce(t *testing.T) {
	env := loadChatE2EEnv(t)
	enableEgressFilterForTest(t, egressfilter.ModeEnforce)

	sc := security.NewRequestContextForTenantAccountAdmin(env.tenant, env.user, []string{env.account})
	agent, ok := GetNBAgent(sc, "LLM", env.account, "")
	require.True(t, ok, "LLM agent factory must be registered")

	// AKIAIOSFODNN7EXAMPLE is AWS's documentation canonical example, not a
	// real credential.
	sessionID := "ut-egressfilter-block-" + uuid.NewString()[:8]
	query := "What does AKIAIOSFODNN7EXAMPLE mean? Should I rotate it?"

	// Clean up any prior run with the same session_id (idempotent).
	_ = DeleteConversationBySession(sessionID, env.account, env.user)

	t.Logf("dispatching chat:")
	t.Logf("  session_id  : %s  <-- look this up in the UI", sessionID)
	t.Logf("  agent       : LLM")
	t.Logf("  query bytes : %d", len(query))

	_, err := HandleConversationSessionRequest(sc, agent, env.user, env.account, sessionID, query)

	require.Error(t, err, "egressfilter must surface a block error to the conversation flow")

	se, asEgressFilterErr := egressfilter.AsError(err)
	if !asEgressFilterErr {
		// If it wasn't unwrapped to *egressfilter.Error, check it at least bears
		// the sentinel — wrapping may have happened in the conversation layer.
		require.True(t, errors.Is(err, egressfilter.ErrSecretsBlocked),
			"error must be the egressfilter's ErrSecretsBlocked (got %v)", err)
		t.Logf("egressfilter block surfaced as wrapped sentinel: %v", err)
		return
	}

	t.Logf("egressfilter block details:")
	t.Logf("  session_id  : %s", sessionID)
	t.Logf("  audit_id    : %s", se.AuditID)
	t.Logf("  rule_ids    : %v", se.RuleIDs)
	t.Logf("  user-facing : %q", err.Error())

	// Hard contracts.
	assert.Regexp(t, `^egress-[0-9a-f]{12}$`, se.AuditID)
	assert.Contains(t, se.RuleIDs, "aws-access-key-id")
	assert.NotContains(t, err.Error(), "AKIA",
		"user-facing error must not echo the secret value")

	// And: the event must have been persisted to the message's metadata
	// column so the UI can render a per-message indicator.
	events := fetchEgressFilterMetadataForSession(t, env.account, sessionID)
	require.NotEmpty(t, events, "metadata.egressfilter must hold at least one event")
	t.Logf("persisted events:")
	for i, e := range events {
		t.Logf("  [%d] audit_id=%s mode=%s rule_ids=%v hits=%d payload_bytes=%d",
			i, e.AuditID, e.Mode, e.RuleIDs, e.HitCount, e.PayloadBytes)
	}
	// One of the persisted events must match the blocked one we surfaced
	// AND carry per-hit detail so the UI / future redact pass can reason
	// about each match individually.
	var matched bool
	for _, e := range events {
		if e.AuditID == se.AuditID {
			matched = true
			assert.Equal(t, egressfilter.ModeEnforce, e.Mode)
			assert.Contains(t, e.RuleIDs, "aws-access-key-id")
			assert.Equal(t, e.HitCount, len(e.Hits),
				"HitCount must agree with len(Hits)")
			require.NotEmpty(t, e.Hits, "Hits must carry per-detection detail")
			for _, h := range e.Hits {
				assert.NotEmpty(t, h.RuleID, "every Hit must carry a rule id")
				assert.GreaterOrEqual(t, h.End, h.Start, "End must be >= Start")
				assert.Less(t, h.Start, e.PayloadBytes,
					"Hit offsets must lie inside the scanned payload")
				assert.LessOrEqual(t, h.End, e.PayloadBytes,
					"Hit offsets must lie inside the scanned payload")
			}
			assert.Empty(t, e.Redactions,
				"audit/enforce modes must not produce Redactions; only redact/tokenize do")
			break
		}
	}
	assert.True(t, matched, "persisted events must include the surfaced audit_id %q", se.AuditID)
}

// TestE2E_EgressFilterChat_CleanQueryPassesThrough drives a real conversation
// with a clean query and asserts the LLM provider was reached, a response
// was produced, and the conversation is persisted normally — so the same
// chat path that blocks on secrets does NOT impair regular traffic.
func TestE2E_EgressFilterChat_CleanQueryPassesThrough(t *testing.T) {
	env := loadChatE2EEnv(t)
	enableEgressFilterForTest(t, egressfilter.ModeEnforce)

	sc := security.NewRequestContextForTenantAccountAdmin(env.tenant, env.user, []string{env.account})
	agent, ok := GetNBAgent(sc, "LLM", env.account, "")
	require.True(t, ok)

	sessionID := "ut-egressfilter-clean-" + uuid.NewString()[:8]
	query := "Reply with exactly one short sentence about Kubernetes."

	_ = DeleteConversationBySession(sessionID, env.account, env.user)

	t.Logf("dispatching clean chat:")
	t.Logf("  session_id  : %s  <-- look this up in the UI", sessionID)
	t.Logf("  agent       : LLM")

	resp, err := HandleConversationSessionRequest(sc, agent, env.user, env.account, sessionID, query)

	require.NoError(t, err, "enforce mode must not block clean traffic")
	require.NotNil(t, resp)
	t.Logf("clean pass-through:")
	t.Logf("  session_id   : %s", sessionID)
	t.Logf("  agent_name   : %s", resp.AgentName)
	t.Logf("  query bytes  : %d", len(resp.Query))
	t.Logf("  response len : %d", len(resp.Response))
	assert.NotEmpty(t, resp.AgentName)
	assert.NotEmpty(t, resp.Response)

	// And: no egressfilter event should have been persisted because nothing
	// matched. metadata column either NULL or has no "egressfilter" key.
	events := fetchEgressFilterMetadataForSession(t, env.account, sessionID)
	assert.Empty(t, events, "no egressfilter events should be persisted for a clean query")
}

// TestE2E_EgressFilterChat_AuditMode_RecordsButForwards drives a real
// conversation with audit mode on and a secret in the query. The egressfilter
// must DETECT (visible in the audit log line) but NOT block — the
// conversation should complete and an LLM response should be produced.
// This is the safe-rollout mode operators flip to first.
func TestE2E_EgressFilterChat_AuditMode_RecordsButForwards(t *testing.T) {
	env := loadChatE2EEnv(t)
	enableEgressFilterForTest(t, egressfilter.ModeAudit)

	sc := security.NewRequestContextForTenantAccountAdmin(env.tenant, env.user, []string{env.account})
	agent, ok := GetNBAgent(sc, "LLM", env.account, "")
	require.True(t, ok)

	sessionID := "ut-egressfilter-audit-" + uuid.NewString()[:8]
	query := "Briefly: what should I do if I see AKIAIOSFODNN7EXAMPLE in a log?"

	_ = DeleteConversationBySession(sessionID, env.account, env.user)

	t.Logf("dispatching audit-mode chat (secret present in query):")
	t.Logf("  session_id  : %s  <-- look this up in the UI", sessionID)
	t.Logf("  mode        : audit (detect + log + metric, do NOT block)")
	t.Logf("  audit log   : check stderr for 'egressfilter: outbound LLM payload contains potential secret'")

	resp, err := HandleConversationSessionRequest(sc, agent, env.user, env.account, sessionID, query)

	require.NoError(t, err, "audit mode must not block, even when a hit is detected")
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.Response,
		"audit mode must let the LLM produce a real response")

	t.Logf("audit-mode pass-through:")
	t.Logf("  session_id  : %s", sessionID)
	t.Logf("  agent_name  : %s", resp.AgentName)
	t.Logf("  response len: %d", len(resp.Response))
	// Avoid asserting on response shape — gemini's wording may vary, but
	// non-empty is the strict invariant.
	full := strings.Join(resp.Response, "\n")
	assert.False(t, strings.Contains(strings.ToLower(full), "blocked by egressfilter"),
		"audit mode should not surface a egressfilter block in the response")

	// And: the audit event must be persisted to the message's metadata
	// column. In audit mode this is the ONLY surface the user/UI gets to
	// see — the response itself is unmodified.
	events := fetchEgressFilterMetadataForSession(t, env.account, sessionID)
	require.NotEmpty(t, events, "metadata.egressfilter must hold the audit event")
	t.Logf("persisted audit events:")
	for i, e := range events {
		t.Logf("  [%d] audit_id=%s mode=%s rule_ids=%v hits=%d",
			i, e.AuditID, e.Mode, e.RuleIDs, e.HitCount)
	}
	var sawAuditEvent bool
	for _, e := range events {
		if e.Mode == egressfilter.ModeAudit {
			sawAuditEvent = true
			assert.Contains(t, e.RuleIDs, "aws-access-key-id",
				"audit-mode event must carry the rule that fired")
			assert.Regexp(t, `^egress-[0-9a-f]{12}$`, e.AuditID)
			break
		}
	}
	assert.True(t, sawAuditEvent, "expected at least one event with mode=audit")
}

package core

import (
	"fmt"
	"log/slog"
	"strings"

	"nudgebee/llm/config"
	"nudgebee/llm/memory"
	"nudgebee/llm/security"
)

// memoryReferenceType maps a memory-v2 layer name to the reference_type value
// persisted in llm_conversation_references. Kept in one place so the
// hydration SELECT in conversation_dao.go and the UI's References panel can
// stay in sync with the writer.
var memoryReferenceType = map[string]AgentReferenceType{
	"soul":        AgentReferenceTypeMemorySoul,
	"preferences": AgentReferenceTypeMemoryPreferences,
	"patterns":    AgentReferenceTypeMemoryPatterns,
	"decisions":   AgentReferenceTypeMemoryDecisions,
	"collective":  AgentReferenceTypeMemoryCollective,
	"session":     AgentReferenceTypeMemorySession,
}

// composeMemoryV2Block is the bridge between the executor and the Memory Module.
// It returns the rendered memory slab (soul + preferences blocks) when the
// module is enabled for the tenant, or "" otherwise. Never errors — failures
// inside memory.Compose are logged there and surface as empty blocks.
//
// Phase 1 scope: Soul and Preferences only. Other slab layers return empty.
// Phase 2+ layers (Patterns, Decisions, etc.) flow through the same call site.
func composeMemoryV2Block(ctx *security.RequestContext, req NBAgentRequest, agent NBAgent) string {
	if !config.Config.MemoryModuleEnabled {
		slog.Debug("memory.bridge: module disabled, skipping", "agent", agent.GetName())
		return ""
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()
	if !memory.ComposeEnabledFor(tenantID) {
		slog.Debug("memory.bridge: tenant not allowed, skipping", "tenant", tenantID, "agent", agent.GetName())
		return ""
	}

	agentModule := string(ResolveAgentModule(agent))

	slab, err := memory.Default().Compose(ctx.GetContext(), memory.ComposeRequest{
		TenantID:    tenantID,
		UserID:      req.UserId,
		AgentModule: agentModule,
		SessionID:   req.SessionId,
		Query:       req.Query,
		TokenBudget: 2000,
	})
	if err != nil {
		slog.Warn("memory.bridge: compose failed", "error", err, "tenant", tenantID, "agent", agent.GetName())
		return ""
	}
	rendered := slab.Render()
	// Append the memory-index footer + attribution instruction so the LLM
	// can cite specific rows in its <action> emissions (see
	// buildMemoryIndexFooter). The instruction is colocated with the block
	// so it's only in the prompt when memory itself is — no separate flag
	// needed, no fleet-wide base-prompt cache invalidation.
	if rendered != "" && len(slab.Injected) > 0 {
		rendered = rendered + "\n" + buildMemoryIndexFooter(slab.Injected)
	}
	persistMemoryInjectedRefs(ctx, req, slab.Injected)
	slog.Info("memory.bridge: returning slab",
		"tenant", tenantID, "user", req.UserId, "agent", agent.GetName(),
		"rendered_len", len(rendered),
		"soul_len", len(slab.Soul),
		"prefs_len", len(slab.Preferences),
		"injected_count", len(slab.Injected),
	)
	return rendered
}

// persistMemoryInjectedRefs writes one llm_conversation_references row per
// injected memory item so the References UI can show what the prompt saw.
// Best-effort: a DAO failure is logged but never bubbles up — the audit
// story is important, but not important enough to fail a chat turn.
//
// Skips when any of {account_id, conversation_id, message_id, agent_id} is
// missing — those paths are typically warmup / self-tests and don't have a
// row on the receiving side to hang the reference off.
func persistMemoryInjectedRefs(ctx *security.RequestContext, req NBAgentRequest, injected []memory.InjectedItem) {
	if len(injected) == 0 {
		return
	}
	if req.AccountId == "" || req.ConversationId == "" || req.MessageId == "" || req.AgentId == "" {
		slog.Debug("memory.bridge: skipping injection refs — identity fields missing",
			"account_id", req.AccountId, "conversation_id", req.ConversationId,
			"message_id", req.MessageId, "agent_id", req.AgentId, "injected_count", len(injected))
		return
	}
	refs := buildMemoryAgentReferences(injected)
	if len(refs) == 0 {
		return
	}
	if err := GetConversationDao().SaveAgentReferences(req.AccountId, req.ConversationId, req.MessageId, req.AgentId, refs); err != nil {
		slog.Warn("memory.bridge: SaveAgentReferences failed for memory injection",
			"error", err, "conversation_id", req.ConversationId, "message_id", req.MessageId,
			"agent_id", req.AgentId, "ref_count", len(refs))
	}
}

// buildMemoryIndexFooter builds the compact <memory_index> block +
// attribution-instruction the LLM uses to cite specific memory rows from
// its <action> emissions. Pure — no I/O — so a unit test can pin the
// format (positional numbering starts at 1 to match the persisted
// metadata.position on each reference row; the instruction wording is
// stable so tests can assert against it).
//
// Kept inline in the bridge's return so attribution instructions live only
// where memory itself lives — no separate config flag, no fleet-wide
// prompt-cache invalidation from touching the ReAct base prompt.
func buildMemoryIndexFooter(injected []memory.InjectedItem) string {
	if len(injected) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<memory_index>\n")
	for i, item := range injected {
		// subject is the short display label; layer is what disambiguates
		// same-subject items across layers. Same shape the UI shows.
		fmt.Fprintf(&b, "  [m%d] %s: %s\n", i+1, item.Layer, item.Subject)
	}
	b.WriteString("</memory_index>\n")
	// Mandatory-declaration + one-shot. Bumped from the initial "optional
	// citation" shape after live testing on Gemini 3 Flash Preview showed
	// 0% adherence — small models default to "unsure → omit" when the tag
	// is optional. Requiring an explicit empty <memory_used/> when nothing
	// applies forces the model to at least declare per action, which we
	// can distinguish from silent-non-emission when auditing.
	b.WriteString(`
For EVERY <action> you emit, include a <memory_used> child that reports
which [mN] items were APPLIED to THIS action's tool_name or tool_input:
  * If one or more items' values were actually applied, list them:
      <memory_used><ref n="N" note="short reason"/><ref n="M" note="..."/></memory_used>
  * If NO memory item was applied to this action, declare it explicitly:
      <memory_used/>

"Applied" means the memory item's value shows up in the tool_name choice
or the tool_input parameters. Do NOT cite an item that you considered
but overrode — e.g. if [mN] gives a default_namespace but the user's
request specifies a different namespace and you use the user's value,
that memory item was NOT applied and must NOT be cited. Considering,
comparing, or ruling out a memory item does not count as using it.

Cite by the [mN] marker from <memory_index> above. The value of n must
be the raw integer index only (e.g. n="1", n="17") — do NOT wrap it as
"[m1]" or "m1", the parser will drop non-integer citations.

Example 1 — memory value IS applied. <memory_index> has
[m2] preferences: preferred_log_source=loki and [m5] patterns:
orders-api. The action fetches loki logs for orders-api:

  <action>
    <tool_name>loki_query</tool_name>
    <tool_input>{"pod":"orders-api","level":"error"}</tool_input>
    <memory_used>
      <ref n="2" note="preferred_log_source=loki drove log-source choice"/>
      <ref n="5" note="recurring OOMKill pattern for orders-api"/>
    </memory_used>
  </action>

Example 2 — memory value is CONSIDERED but OVERRIDDEN. <memory_index>
has [m6] preferences: default_namespace=payments. User asked for logs
in the "orders" namespace, so the action uses "orders" not "payments":

  <action>
    <tool_name>logs</tool_name>
    <tool_input>{"command":"logs for api in orders namespace"}</tool_input>
    <memory_used/>
  </action>

Do NOT cite [m6] here — the memory value was rejected in favor of the
user's explicit input, so it was not applied.
`)
	return b.String()
}

// buildMemoryAgentReferences maps InjectedItems to the AgentReference shape
// the DAO wants. Pure: no I/O, no side effects — extracted so a unit test can
// pin the mapping (reference_type per layer, metadata payload) without a DB.
// Items with an unrecognised layer are skipped rather than written with a
// made-up reference_type — the hydration SELECT would fail to join them.
func buildMemoryAgentReferences(injected []memory.InjectedItem) []AgentReference {
	if len(injected) == 0 {
		return nil
	}
	refs := make([]AgentReference, 0, len(injected))
	for i, item := range injected {
		refType, ok := memoryReferenceType[item.Layer]
		if !ok {
			slog.Warn("memory.bridge: unknown injected layer, skipping",
				"layer", item.Layer, "id", item.ID)
			continue
		}
		meta := map[string]any{
			"layer":    item.Layer,
			"subject":  item.Subject,
			"rank":     item.Rank,
			"score":    item.Score,
			"source":   item.Source,
			"position": i + 1,
		}
		// content only rides in metadata for layers without a UUID PK
		// (soul, session) — hydration SQL for the others JOINs and projects
		// their real body, so duplicating it here would bloat the row.
		if item.Content != "" {
			meta["content"] = item.Content
		}
		refs = append(refs, AgentReference{
			ReferenceID: item.ID,
			Type:        refType,
			Metadata:    meta,
		})
	}
	return refs
}

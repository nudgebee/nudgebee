package core

import (
	"log/slog"
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
	for _, item := range injected {
		refType, ok := memoryReferenceType[item.Layer]
		if !ok {
			slog.Warn("memory.bridge: unknown injected layer, skipping",
				"layer", item.Layer, "id", item.ID)
			continue
		}
		meta := map[string]any{
			"layer":   item.Layer,
			"subject": item.Subject,
			"rank":    item.Rank,
			"score":   item.Score,
			"source":  item.Source,
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

package core

import (
	"log/slog"
	"nudgebee/llm/config"
	"nudgebee/llm/memory"
	"nudgebee/llm/security"
)

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
	slog.Info("memory.bridge: returning slab",
		"tenant", tenantID, "user", req.UserId, "agent", agent.GetName(),
		"rendered_len", len(rendered),
		"soul_len", len(slab.Soul),
		"prefs_len", len(slab.Preferences),
	)
	return rendered
}

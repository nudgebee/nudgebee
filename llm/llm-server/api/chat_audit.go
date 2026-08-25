package api

import (
	"context"
	"strings"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/audit"
	"nudgebee/llm/security"
)

// webChatSources allow-lists conversation sources that originate from the in-app ("Nubi") web chat; all other sources are excluded from CHAT_ACTIONS audits.
var webChatSources = map[core.ConversationSource]bool{
	core.ConversationSourceUserInvestigation: true,
}

func isWebChatSource(source core.ConversationSource) bool {
	return webChatSources[source]
}

// maxAuditQueryPreviewRunes caps the query preview stored in EventState; the full message lives in llm_conversation_messages, so this bounds row size and sensitive content in retained logs.
const maxAuditQueryPreviewRunes = 256

// toolConfirmationDecision maps a yes/no tool-confirmation response to a stable audit label; unrecognized answers record "other" rather than a guessed approval.
func toolConfirmationDecision(response string) string {
	switch strings.ToLower(strings.TrimSpace(response)) {
	case "yes", "y", "approve", "approved", "confirm", "confirmed", "ok", "proceed":
		return "approved"
	case "no", "n", "reject", "rejected", "decline", "declined", "cancel", "cancelled":
		return "rejected"
	default:
		return "other"
	}
}

// truncateQueryForAudit returns a rune-safe preview of s capped at maxAuditQueryPreviewRunes, suffixed with a marker when trimmed.
func truncateQueryForAudit(s string) string {
	// Range over the string to find the byte offset of the cap-th rune rather
	// than allocating a full []rune for s (which can be up to ~500k chars).
	count := 0
	for i := range s {
		if count == maxAuditQueryPreviewRunes {
			return s[:i] + "…[truncated]"
		}
		count++
	}
	return s
}

// emitChatActionAudit records a web-chat interaction under CHAT_ACTIONS; it is fire-and-forget and gated to web sources via isWebChatSource, so callers may invoke it unconditionally.
func emitChatActionAudit(
	ctx *security.RequestContext,
	eventType audit.EventType,
	action audit.EventAction,
	accountId string,
	userId string,
	conversationId string,
	sessionId string,
	source core.ConversationSource,
	prevState map[string]any,
	state map[string]any,
) {
	// ctx is always non-nil at the call sites (the handler dereferences it for
	// auth before this runs); the guard is belt-and-suspenders for this
	// fire-and-forget helper, whose contract is to never crash the chat request.
	if ctx == nil || !isWebChatSource(source) || conversationId == "" {
		return
	}
	if state == nil {
		state = map[string]any{}
	}

	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				UserId:        userId,
				TenantId:      ctx.GetSecurityContext().GetTenantId(),
				AccountId:     accountId,
				EventTime:     time.Now().UTC(),
				EventCategory: audit.EventCategoryChatActions,
				EventType:     eventType,
				EventAction:   action,
				EventActor:    audit.EventActorUiService,
				EventTarget:   conversationId,
				// Status reflects the accepted user action, not the eventual agent-run outcome (which isn't known at emit time).
				EventStatus:    audit.EventStatusSuccess,
				EventPrevState: prevState,
				EventState:     state,
				TransactionId:  ctx.GetTraceId(),
				EventAttr: map[string]any{
					"conversation_id": conversationId,
					"session_id":      sessionId,
					"source":          string(source),
				},
			},
		},
	}

	// Extract request-scoped values synchronously so the goroutine never races a later ctx mutation (e.g. SetContext).
	baseCtx, secCtx, logger, tracer, meter := ctx.GetContext(), ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter()
	// Detached context with a timeout: the handler returns before this completes, so avoid "context canceled" while bounding the goroutine's lifetime.
	go func() {
		detachedCtx, cancel := context.WithTimeout(context.WithoutCancel(baseCtx), 5*time.Second)
		defer cancel()
		auditCtx := security.NewRequestContext(detachedCtx, secCtx, logger, tracer, meter)
		_ = audit.CreateAudit(auditCtx, auditReq)
	}()
}

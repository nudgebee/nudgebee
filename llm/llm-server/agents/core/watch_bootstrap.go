package core

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/watch"

	"github.com/tmc/langchaingo/llms"
)

// BootstrapWatch wires the watch package against agents/core's LLM and
// security stack and registers the dispatcher with the existing
// leader-elected scheduler. It is safe to call exactly once at startup
// (after config is loaded). When LLM_SERVER_WATCH_ENABLED is false the
// dispatcher registration is a no-op so the bridge is also harmless.
//
// Lives in agents/core (not in cmd/main) because:
//   - the watch package must NOT import agents/core (LLM provider stack
//     is huge and would invert the dependency arrow)
//   - someone has to give watch its LLM judge client and a way to
//     construct a security context, and agents/core owns both
func BootstrapWatch() error {
	// Short-circuit completely when the feature flag is off so the watch
	// package's factory hooks and dispatcher remain unwired. Without this
	// guard, callers like `watch_resource` tool's Create path (gated by
	// config.Config.WatchEnabled separately) would still find a registered
	// factory and judge client, defeating the "single switch" intent.
	if !config.Config.WatchEnabled {
		slog.Info("watch: feature disabled (LLM_SERVER_WATCH_ENABLED=false); skipping bootstrap")
		return nil
	}
	watch.SetLLMJudgeClientFactory(buildWatchJudgeClient)
	watch.SetSecurityContextBuilder(buildWatchSecurityContext)
	watch.SetSummarizerFactory(newLlmSummarizer)
	if err := watch.RegisterDispatcher(); err != nil {
		return fmt.Errorf("BootstrapWatch: register dispatcher: %w", err)
	}
	return nil
}

// trackedJudgeClient routes the llm_judge predicate's LLM call through
// GenerateAndTrackLLMContent so token usage lands on the parent conversation's
// llm_conversation_token_usage row. Without this wrapper the call goes
// straight to the raw model and silently bypasses cost attribution —
// material for `llm_judge` watches that can fire up to (max_duration_sec /
// poll_interval_sec) times per watch.
//
// The factory captures the *security.RequestContext at registration time;
// per-call deadlines come from the caller's context (predicate.go wraps the
// model call in pollTimeout), which we re-wrap into a fresh RequestContext
// preserving the original security context + logger + tracer.
type trackedJudgeClient struct {
	rctx           *security.RequestContext
	userID         string
	accountID      string
	conversationID string
	messageID      string
}

func (c *trackedJudgeClient) GenerateContent(callCtx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	// Carry forward the security context, logger, tracer, meter captured
	// at factory time; use the caller's deadline-bounded context. Hint
	// the resolver to pick the summary-tier model (matches the short
	// JSON yes/no shape of the judge prompt).
	callCtx = context.WithValue(callCtx, ContextKeyModelTier, ModelTierSummary)
	rctx := security.NewRequestContext(callCtx, c.rctx.GetSecurityContext(), c.rctx.GetLogger(), c.rctx.GetTracer(), c.rctx.GetMeter())
	return GenerateAndTrackLLMContent(
		rctx,
		c.userID,
		c.accountID,
		c.conversationID,
		c.messageID,
		"watch_judge",
		false, // trackContent: don't persist prompt/response text, just usage
		messages,
		false, // cleanupMarkdown: judge output is JSON, no markdown to strip
		options...,
	)
}

// buildWatchJudgeClient returns an LLM client suitable for the llm_judge
// predicate. Token usage is attributed to the parent conversation via
// GenerateAndTrackLLMContent rather than going to the raw model — important
// for cost visibility on long-running `llm_judge` watches.
func buildWatchJudgeClient(ctx *security.RequestContext, w watch.Watch) (watch.LLMJudgeClient, error) {
	userID := ""
	if w.UserID != nil {
		userID = w.UserID.String()
	}
	return &trackedJudgeClient{
		rctx:           ctx,
		userID:         userID,
		accountID:      w.AccountID.String(),
		conversationID: w.ConversationID.String(),
		messageID:      w.ID.String(),
	}, nil
}

// buildWatchSecurityContext rehydrates a SecurityContext from the IDs
// stored on the watch row. The poll runs as a tenant_admin scoped to the
// originating account — the same tenancy the user had when they
// registered the watch.
//
// KNOWN SCOPE LIMIT (v1): the rehydrated context is static for the
// lifetime of the watch. If the originating user is off-boarded /
// demoted, or the account is disabled, the poll continues to drive
// admin-scoped tool calls under that identity until the watch
// terminates (capped by `max_duration_sec`, default 24h). A future
// hook should re-validate originator access on each RunOnePoll and
// terminate as FAILED on revocation, plus subscribe to account-disable
// events to enqueue an UPDATE … status='CANCELLED'. Until that lands
// the cap is the only bound — keep `WatchMaxDurationSec` conservative
// in any environment where role/account churn matters.
func buildWatchSecurityContext(w watch.Watch) (*security.SecurityContext, error) {
	userID := ""
	if w.UserID != nil {
		userID = w.UserID.String()
	}
	sc := security.NewSecurityContextForTenantAccountAdmin(
		w.TenantID.String(),
		userID,
		[]string{w.AccountID.String()},
	)
	if sc == nil {
		slog.Error("watch.bootstrap: security context builder returned nil",
			"tenant_id", w.TenantID.String(), "account_id", w.AccountID.String())
		return nil, fmt.Errorf("watch.bootstrap: failed to build security context for tenant %s", w.TenantID.String())
	}
	return sc, nil
}

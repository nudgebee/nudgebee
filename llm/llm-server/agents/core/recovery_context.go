package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	"nudgebee/llm/security"
	"nudgebee/llm/watch"
)

// ErrRecoveryAccessDenied means the person who sent a message may no longer run
// Nubi on its account, so the message must not be restarted.
var ErrRecoveryAccessDenied = errors.New("recovery: the person who sent this message no longer has access to its account")

// newSecurityContext is security.NewSecurityContext, replaceable in tests.
var newSecurityContext = security.NewSecurityContext

// headlessSources are runs no person started; they keep the system identity.
var headlessSources = map[ConversationSource]bool{
	ConversationSourceInvestigation:       true,
	ConversationSourceAutomation:          true,
	ConversationSourcePRLifecycle:         true,
	ConversationSourceRecommendation:      true,
	ConversationSourceInstantNotification: true,
}

// RecoveryContext is who a restarted message runs as: the person who sent it,
// with their own access, never an admin on their behalf.
//
//   - No person (system runs): an admin scoped to the account, as before, but
//     read-only unless the conversation is a headless run. A person's chat saved
//     without a user (a super admin without a tenant grant) must not gain writes.
//     Its super-admin roles come from the original session only, so a recovered
//     run has fewer rights than the live one did; that is deliberate.
//   - A person: their security context from services-server. ErrRecoveryAccessDenied
//     when services-server rejects them or they may not run Nubi on the account;
//     any other error means services-server could not be asked, and the caller
//     should leave the message for the next sweep.
func RecoveryContext(message ConversationMessage, conversation Conversation) (*security.RequestContext, error) {
	accountID := message.AccountID.String()
	logger := slog.Default().With("account_id", accountID, "conversation_id", message.ConversationID.String(),
		"message_id", message.ID.String(), "user_id", message.UserID.String())
	tracer := otel.GetTracerProvider().Tracer("nudgebee-llm")

	if message.UserID == uuid.Nil {
		sc := security.NewSecurityContextForTenantAccountAdmin(conversation.TenantID.String(), security.GetSystemUserId(), []string{accountID})
		if sc == nil {
			return nil, fmt.Errorf("recovery: build system context for tenant %s", conversation.TenantID)
		}
		ctx := security.NewRequestContext(context.Background(), sc, logger, tracer, nil)
		if conversation.Source == nil || !headlessSources[*conversation.Source] {
			ctx = WithReadOnlyRun(ctx)
		}
		return ctx, nil
	}

	sc, err := newSecurityContext(conversation.TenantID.String(), message.UserID.String())
	if errors.Is(err, security.ErrSecurityContextRejected) {
		return nil, fmt.Errorf("%w: %w", ErrRecoveryAccessDenied, err)
	}
	if err != nil {
		return nil, fmt.Errorf("recovery: load security context: %w", err)
	}
	if sc == nil || !sc.CanRunAI(accountID) {
		return nil, ErrRecoveryAccessDenied
	}
	return security.NewRequestContext(context.Background(), sc, logger, tracer, nil), nil
}

// WatchPollSecurityContext is who a watch's poll runs as (the watch package's SecurityContextBuilder): the person
// who set it up, with their own access, never an admin on their behalf (a read as account admin could return data
// they cannot see). A watch with no user (NudgeBee's own, such as a remediation wait) runs as NudgeBee limited to the
// watch's account. watch.ErrOwnerAccessRevoked when services-server rejects the person or they may no longer run
// Nubi on the account; any other error means services-server could not be asked.
func WatchPollSecurityContext(w watch.Watch) (*security.SecurityContext, error) {
	if w.UserID == nil || *w.UserID == uuid.Nil {
		sc := security.NewSecurityContextForTenantAccountAdmin(w.TenantID.String(), security.GetSystemUserId(), []string{w.AccountID.String()})
		if sc == nil {
			return nil, fmt.Errorf("watch poll: build system context for tenant %s", w.TenantID)
		}
		return sc, nil
	}
	sc, err := newSecurityContext(w.TenantID.String(), w.UserID.String())
	if errors.Is(err, security.ErrSecurityContextRejected) {
		return nil, fmt.Errorf("%w: %w", watch.ErrOwnerAccessRevoked, err)
	}
	if err != nil {
		return nil, fmt.Errorf("watch owner access: %w", err)
	}
	if sc == nil || !sc.CanRunAI(w.AccountID.String()) {
		return nil, watch.ErrOwnerAccessRevoked
	}
	return sc, nil
}

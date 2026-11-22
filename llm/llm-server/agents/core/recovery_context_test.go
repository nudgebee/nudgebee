package core

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/watch"
)

func stubNewSecurityContext(t *testing.T, fn func(tenantID, userID string) (*security.SecurityContext, error)) {
	t.Helper()
	original := newSecurityContext
	newSecurityContext = fn
	t.Cleanup(func() { newSecurityContext = original })
}

func wireSecurityContext(t *testing.T, wire string) *security.SecurityContext {
	t.Helper()
	var sc security.SecurityContext
	require.NoError(t, common.UnmarshalJson([]byte(wire), &sc))
	return &sc
}

func recoveryFixture(user uuid.UUID, source *ConversationSource) (ConversationMessage, Conversation) {
	account := uuid.New()
	return ConversationMessage{ID: uuid.New(), ConversationID: uuid.New(), AccountID: account, UserID: user},
		Conversation{TenantID: uuid.New(), AccountID: account, Source: source}
}

func TestRecoveryContext_PersonRunsWithOwnAccess(t *testing.T) {
	msg, conv := recoveryFixture(uuid.New(), nil)
	reader := wireSecurityContext(t, fmt.Sprintf(`{"TenantId":%q,"UserId":%q,"Roles":["tenant_admin_readonly"],"AccountIds":[%q]}`,
		conv.TenantID, msg.UserID, msg.AccountID))
	stubNewSecurityContext(t, func(tenantID, userID string) (*security.SecurityContext, error) {
		assert.Equal(t, conv.TenantID.String(), tenantID)
		assert.Equal(t, msg.UserID.String(), userID)
		return reader, nil
	})

	ctx, err := RecoveryContext(msg, conv)

	require.NoError(t, err)
	sc := ctx.GetSecurityContext()
	assert.False(t, sc.IsTenantAdmin(), "a recovered message must not run as an admin")
	assert.False(t, sc.HasAccountAccess(msg.AccountID.String(), security.SecurityAccessTypeUpdate), "a read-only person must not get writes")
	assert.Equal(t, msg.UserID.String(), sc.GetUserId())
}

func TestRecoveryContext_PersonWithoutAccessIsDenied(t *testing.T) {
	msg, conv := recoveryFixture(uuid.New(), nil)
	stranger := wireSecurityContext(t, fmt.Sprintf(`{"TenantId":%q,"UserId":%q,"Roles":[],"AccountIds":[]}`, conv.TenantID, msg.UserID))
	stubNewSecurityContext(t, func(string, string) (*security.SecurityContext, error) { return stranger, nil })

	_, err := RecoveryContext(msg, conv)

	assert.ErrorIs(t, err, ErrRecoveryAccessDenied)
}

func TestRecoveryContext_RejectedPersonIsDenied(t *testing.T) {
	msg, conv := recoveryFixture(uuid.New(), nil)
	stubNewSecurityContext(t, func(string, string) (*security.SecurityContext, error) {
		return nil, fmt.Errorf("security: failed after 1 attempts: %w", fmt.Errorf("%w: client error 400", security.ErrSecurityContextRejected))
	})

	_, err := RecoveryContext(msg, conv)

	assert.ErrorIs(t, err, ErrRecoveryAccessDenied)
}

func TestRecoveryContext_UnreachableServicesServerIsNotADenial(t *testing.T) {
	msg, conv := recoveryFixture(uuid.New(), nil)
	stubNewSecurityContext(t, func(string, string) (*security.SecurityContext, error) {
		return nil, errors.New("security: failed after 4 attempts: server error 503")
	})

	_, err := RecoveryContext(msg, conv)

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRecoveryAccessDenied, "an outage must leave the message for the next sweep, not fail it")
}

func TestRecoveryContext_SystemRuns(t *testing.T) {
	stubNewSecurityContext(t, func(string, string) (*security.SecurityContext, error) {
		t.Fatal("a run with no person must not look up a person")
		return nil, nil
	})
	automation, chat := ConversationSourceAutomation, ConversationSourceUserInvestigation
	for name, tc := range map[string]struct {
		source   *ConversationSource
		readOnly bool
	}{
		"headless automation keeps the system identity": {&automation, false},
		"a chat saved without a user resumes read-only": {&chat, true},
		"no source resumes read-only":                   {nil, true},
	} {
		t.Run(name, func(t *testing.T) {
			msg, conv := recoveryFixture(uuid.Nil, tc.source)
			conv.TenantID = uuid.New()
			ctx, err := RecoveryContext(msg, conv)
			require.NoError(t, err)
			assert.True(t, ctx.GetSecurityContext().HasAccountAccess(msg.AccountID.String(), security.SecurityAccessTypeRead))
			assert.Equal(t, tc.readOnly, IsReadOnlyRun(ctx))
			assert.Equal(t, security.GetSystemUserId(), ctx.GetSecurityContext().GetUserId(), "system runs keep the system user id")
		})
	}
}

// A watch's poll runs as the person who set it up, with their own access (#42461), and ends once they lose it; a
// watch with no user runs as NudgeBee limited to its account.
func TestWatchPollSecurityContext(t *testing.T) {
	account, tenant, owner := uuid.New(), uuid.New(), uuid.New()
	w := watch.Watch{TenantID: tenant, AccountID: account, UserID: &owner}
	reader := wireSecurityContext(t, fmt.Sprintf(`{"TenantId":%q,"UserId":%q,"Roles":["tenant_admin_readonly"],"AccountIds":[%q]}`, tenant, owner, account))
	stranger := wireSecurityContext(t, fmt.Sprintf(`{"TenantId":%q,"UserId":%q,"Roles":[],"AccountIds":[]}`, tenant, owner))

	stubNewSecurityContext(t, func(string, string) (*security.SecurityContext, error) { return reader, nil })
	sc, err := WatchPollSecurityContext(w)
	require.NoError(t, err)
	assert.Same(t, reader, sc, "the poll runs with the person's own context")

	stubNewSecurityContext(t, func(string, string) (*security.SecurityContext, error) { return stranger, nil })
	_, err = WatchPollSecurityContext(w)
	assert.ErrorIs(t, err, watch.ErrOwnerAccessRevoked)

	stubNewSecurityContext(t, func(string, string) (*security.SecurityContext, error) {
		return nil, fmt.Errorf("%w: client error 400", security.ErrSecurityContextRejected)
	})
	_, err = WatchPollSecurityContext(w)
	assert.ErrorIs(t, err, watch.ErrOwnerAccessRevoked)

	stubNewSecurityContext(t, func(string, string) (*security.SecurityContext, error) { return nil, errors.New("server error 503") })
	_, err = WatchPollSecurityContext(w)
	require.Error(t, err)
	assert.NotErrorIs(t, err, watch.ErrOwnerAccessRevoked)

	system := watch.Watch{TenantID: tenant, AccountID: account}
	sc, err = WatchPollSecurityContext(system)
	require.NoError(t, err)
	assert.Equal(t, security.GetSystemUserId(), sc.GetUserId(), "a watch with no user runs as NudgeBee")
	assert.True(t, sc.CanRunAI(account.String()))
	assert.False(t, sc.CanRunAI(uuid.NewString()), "limited to the watch's account")
}

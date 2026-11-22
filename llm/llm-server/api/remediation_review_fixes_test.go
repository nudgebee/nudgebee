package api

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// scWith is a person's access on account with roles scoped to it.
func scWith(t *testing.T, userId, account string, roles ...string) *security.SecurityContext {
	t.Helper()
	b, err := json.Marshal(security.NewSecurityContextForTenantAccountAdmin("tenant-1", userId, []string{account}))
	require.NoError(t, err)
	var pub map[string]any
	require.NoError(t, json.Unmarshal(b, &pub))
	scoped := map[string][]string{}
	for _, r := range roles {
		scoped[r] = []string{account}
	}
	pub["Roles"], pub["ScopedEntityIds"] = roles, scoped
	b, err = json.Marshal(pub)
	require.NoError(t, err)
	sc := &security.SecurityContext{}
	require.NoError(t, json.Unmarshal(b, sc))
	return sc
}

// A planning turn a person asked for runs as that person, and needs their write access; an automatic one runs as
// NudgeBee.
func TestPlanningTurnRunsAsTheRequester(t *testing.T) {
	f := newPlanningFixture(t)
	person := uuid.NewString()
	reader := uuid.NewString()
	swap(t, &loadPersonSecurityContext, func(tenant, userId string) (*security.SecurityContext, error) {
		if userId == reader {
			return scWith(t, userId, f.account, security.AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE), nil
		}
		return scWith(t, userId, f.account, security.AUTH_K8S_NAMESPACE_ADMIN_ROLE), nil
	})

	ctx, refusal := planningContext(events.PlanningRequest{AccountId: f.account, Kind: planTurnAsk, RequestedBy: person})
	require.Empty(t, refusal)
	require.NotNil(t, ctx)
	assert.Equal(t, person, ctx.GetSecurityContext().GetUserId(), "the person's own identity, not an account admin")
	assert.False(t, ctx.GetSecurityContext().IsTenantAdmin())
	assert.True(t, core.IsReadOnlyRun(ctx))

	ctx, refusal = planningContext(events.PlanningRequest{AccountId: f.account, Kind: planTurnReplan, RequestedBy: reader})
	assert.Nil(t, ctx)
	assert.Equal(t, planNoWriteAccess, refusal, "a read-only role cannot ask for fixes")

	ctx, refusal = planningContext(events.PlanningRequest{AccountId: f.account, Kind: planTurnAuto, RequestedBy: person})
	require.Empty(t, refusal)
	assert.Equal(t, security.GetSystemUserId(), ctx.GetSecurityContext().GetUserId(), "automatic planning stays NudgeBee's")

	assert.Equal(t, planNoWriteAccess, planWriteRefusal(scWith(t, reader, f.account, security.AUTH_ACCOUNT_READ_ADMIN_ROLE), f.account))
	assert.Empty(t, planWriteRefusal(scWith(t, person, f.account, security.AUTH_ACCOUNT_ADMIN_ROLE), f.account))
	assert.Equal(t, planNoWriteAccess, planWriteRefusal(scWith(t, person, uuid.NewString(), security.AUTH_ACCOUNT_ADMIN_ROLE), f.account), "another account's role")
}

// A queued request whose person lost write access ends with the reason instead of running.
func TestPlanningTurnRefusedWithoutWriteAccess(t *testing.T) {
	f := newPlanningFixture(t)
	reader := uuid.NewString()
	swap(t, &loadPersonSecurityContext, func(tenant, userId string) (*security.SecurityContext, error) {
		return scWith(t, userId, f.account, security.AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE), nil
	})
	row, refusal, err := requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnAsk, By: reader})
	require.NoError(t, err)
	require.Empty(t, refusal)
	advancePlanning(f.db, row)
	latest := f.latest()
	require.NotNil(t, latest)
	assert.Equal(t, events.PlanningNone, latest.State)
	assert.Contains(t, latest.Reason, planNoWriteAccess)
	assert.Empty(t, f.queries, "no turn started")
}

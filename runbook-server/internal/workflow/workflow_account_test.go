package workflow

import (
	"database/sql"
	"fmt"
	"testing"

	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// #40216: an automation link without ?accountId= resolves the account from the workflow id.
func TestGetWorkflowAccount(t *testing.T) {
	newService := func() (*Service, *MockWorkflowStore) {
		store := new(MockWorkflowStore)
		return &Service{store: store}, store
	}

	t.Run("returns the account of a workflow the caller can read", func(t *testing.T) {
		svc, store := newService()
		store.On("FindAccountID", mock.Anything, "t1", "wf-1").Return(scopeAcctA, nil)

		accountID, err := svc.GetWorkflowAccount(accountAdminContext(t, []string{scopeAcctA, scopeAcctB}, []string{scopeAcctA}), "wf-1")

		require.NoError(t, err)
		assert.Equal(t, scopeAcctA, accountID)
	})

	t.Run("reports a workflow in an account the caller cannot read as not found", func(t *testing.T) {
		svc, store := newService()
		store.On("FindAccountID", mock.Anything, "t1", "wf-1").Return(scopeAcctB, nil)

		_, err := svc.GetWorkflowAccount(accountAdminContext(t, []string{scopeAcctA, scopeAcctB}, []string{scopeAcctA}), "wf-1")

		assert.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("reports a missing workflow, or one in another tenant, as not found", func(t *testing.T) {
		svc, store := newService()
		// The lookup is scoped by the caller's tenant, so another tenant's id finds no row.
		store.On("FindAccountID", mock.Anything, "t1", "wf-other-tenant").Return("", fmt.Errorf("failed to resolve workflow account: %w", sql.ErrNoRows))

		_, err := svc.GetWorkflowAccount(accountAdminContext(t, []string{scopeAcctA}, []string{scopeAcctA}), "wf-other-tenant")

		assert.ErrorIs(t, err, sql.ErrNoRows)
		store.AssertExpectations(t)
	})

	t.Run("reports not found without a lookup when the caller can read no account", func(t *testing.T) {
		svc, store := newService()

		_, err := svc.GetWorkflowAccount(accountAdminContext(t, []string{scopeAcctA}, nil), "wf-1")

		assert.ErrorIs(t, err, sql.ErrNoRows)
		store.AssertNotCalled(t, "FindAccountID", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("a tenant-wide reader resolves any account in the tenant", func(t *testing.T) {
		svc, store := newService()
		store.On("FindAccountID", mock.Anything, "t1", "wf-1").Return(scopeAcctB, nil)

		accountID, err := svc.GetWorkflowAccount(security.NewRequestContextForTenantAccountAdmin("t1", "u1", []string{scopeAcctA, scopeAcctB}), "wf-1")

		require.NoError(t, err)
		assert.Equal(t, scopeAcctB, accountID)
	})
}

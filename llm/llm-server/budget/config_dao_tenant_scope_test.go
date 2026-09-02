package budget

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression for #37020. llm_budget_config has no tenant_id column — entity_id
// holds a cloud_accounts.id on 'account' rows — so an account listing that only
// filters on entity_type spans every tenant in the database. The listing the
// Usage & Limits screen makes (entity_type=account, no entity_id) must go
// through cloud_accounts instead.
func TestListAccountBudgetConfigsForTenant_FiltersThroughCloudAccounts(t *testing.T) {
	now := time.Now()
	rows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{
			"id", "entity_type", "entity_id", "module", "budget_disabled", "disabled_by", "disabled_at",
			"monthly_cost_limit", "monthly_cost_enabled", "monthly_count_limit", "monthly_count_enabled",
			"daily_cost_limit", "daily_cost_enabled", "daily_count_limit", "daily_count_enabled",
			"updated_by", "updated_at", "created_at",
		}).AddRow(
			"cfg-1", EntityTypeAccount, "acc-1", ModuleUserInvestigation, false, nil, nil,
			nil, true, nil, false,
			nil, false, 20, true,
			nil, now, now,
		)
	}

	t.Run("scopes to the tenant's cloud accounts", func(t *testing.T) {
		dm, mock := newMockMetastoreDM(t)
		mock.ExpectQuery("entity_id IN \\(SELECT id FROM cloud_accounts WHERE tenant = \\$2\\)").
			WithArgs(EntityTypeAccount, "tenant-1").
			WillReturnRows(rows())

		configs, err := ListAccountBudgetConfigsForTenant(dm, "tenant-1", "")

		require.NoError(t, err)
		require.Len(t, configs, 1)
		assert.Equal(t, "acc-1", configs[0].EntityID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("keeps the tenant filter when a module is also given", func(t *testing.T) {
		dm, mock := newMockMetastoreDM(t)
		mock.ExpectQuery("cloud_accounts WHERE tenant = \\$2\\)\\s+AND module = \\$3").
			WithArgs(EntityTypeAccount, "tenant-1", ModuleUserInvestigation).
			WillReturnRows(rows())

		configs, err := ListAccountBudgetConfigsForTenant(dm, "tenant-1", ModuleUserInvestigation)

		require.NoError(t, err)
		require.Len(t, configs, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

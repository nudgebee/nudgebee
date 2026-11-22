package budget

import (
	"database/sql/driver"
	"log/slog"
	"testing"
	"time"

	"nudgebee/llm/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var budgetConfigTestColumns = []string{
	"id", "entity_type", "entity_id", "module", "budget_disabled", "disabled_by", "disabled_at",
	"monthly_cost_limit", "monthly_cost_enabled", "monthly_count_limit", "monthly_count_enabled",
	"daily_cost_limit", "daily_cost_enabled", "daily_count_limit", "daily_count_enabled",
	"updated_by", "updated_at", "created_at",
}

// tenantLimits is the tenant row a reply test runs against: a daily count limit
// that is already reached, and optionally a daily cost limit that is too.
type tenantLimits struct {
	dailyCount     int
	dailyCostLimit float64 // 0 = cost limit disabled
}

// newBudgetCheckDB wires a sqlmock Metastore that serves one tenant config row and
// the usage queries a check makes. The queries may arrive in any order.
func newBudgetCheckDB(t *testing.T, tenantId, accountId string, limits tenantLimits, dailyCostUsage float64) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.MatchExpectationsInOrder(false)
	t.Cleanup(func() {
		_ = db.Close()
		common.RegisterDatabaseManagerHook(common.Metastore, nil)
	})
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	})

	now := time.Now()
	row := []driver.Value{
		"cfg-1", EntityTypeTenant, tenantId, ModuleUserInvestigation, false, nil, nil,
		nil, false, nil, false,
		nil, false, limits.dailyCount, true,
		nil, now, now,
	}
	if limits.dailyCostLimit > 0 {
		row[11], row[12] = limits.dailyCostLimit, true
	}
	mock.ExpectQuery("FROM llm_budget_config").
		WillReturnRows(sqlmock.NewRows(budgetConfigTestColumns).AddRow(row...))
	mock.ExpectQuery(`SELECT COUNT\(DISTINCT c.id\)`).WithArgs(tenantId).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(limits.dailyCount))
	mock.ExpectQuery("WITH per_call_cost").WithArgs(tenantId).
		WillReturnRows(sqlmock.NewRows([]string{"total_cost"}).AddRow(dailyCostUsage))
	mock.ExpectQuery("WITH per_call_cost").WithArgs(accountId).
		WillReturnRows(sqlmock.NewRows([]string{"total_cost"}).AddRow(0.0))
}

func TestCheckBudgetLimits_CountLimitAppliesToNewConversationsOnly(t *testing.T) {
	logger := slog.Default()

	t.Run("a new conversation is blocked once the daily count is reached", func(t *testing.T) {
		tenantId, accountId := uniqueID("tenant-count"), uniqueID("acct-count")
		newBudgetCheckDB(t, tenantId, accountId, tenantLimits{dailyCount: 4}, 0)

		exceeded, message := CheckBudgetLimits(tenantId, accountId, ModuleUserInvestigation, logger)

		assert.True(t, exceeded)
		assert.Equal(t, "budget: daily investigation count limit exceeded for your organization", message)
	})

	t.Run("a reply in an existing conversation is let through", func(t *testing.T) {
		tenantId, accountId := uniqueID("tenant-reply"), uniqueID("acct-reply")
		newBudgetCheckDB(t, tenantId, accountId, tenantLimits{dailyCount: 4}, 0)

		exceeded, message := CheckBudgetLimitsForReply(tenantId, accountId, ModuleUserInvestigation, logger)

		assert.False(t, exceeded, message)
	})

	t.Run("a reply is still blocked by a cost limit", func(t *testing.T) {
		tenantId, accountId := uniqueID("tenant-cost"), uniqueID("acct-cost")
		newBudgetCheckDB(t, tenantId, accountId, tenantLimits{dailyCount: 4, dailyCostLimit: 10}, 50)

		exceeded, message := CheckBudgetLimitsForReply(tenantId, accountId, ModuleUserInvestigation, logger)

		assert.True(t, exceeded)
		assert.Equal(t, "budget: daily budget limit exceeded for your organization", message)
	})
}

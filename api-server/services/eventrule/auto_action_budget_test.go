package eventrule

import (
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// budgetProbeAction counts how often the auto-run loop asks it whether it can run.
type budgetProbeAction struct{ asked *atomic.Int32 }

func (a budgetProbeAction) Execute(playbooks.PlaybookActionContext, map[string]any) (playbooks.PlaybookActionResponse, error) {
	return nil, nil
}
func (a budgetProbeAction) CanAutoExecute(playbooks.PlaybookActionContext) bool {
	a.asked.Add(1)
	return false
}
func (a budgetProbeAction) AutoExecute(playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	return nil, nil
}

// An event past its enrichment deadline gets no more auto actions: the loop
// stops before asking the next one, as the configured-playbook loop does.
func TestAutoActionsStopAtPlaybookDeadline(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(string, string) error { return nil })))
	require.NoError(t, err)
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 20; i++ {
		mock.ExpectQuery("any").WillReturnRows(sqlmock.NewRows([]string{"x"}))
	}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgresql")}, nil
	})
	t.Cleanup(func() {
		database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
			return nil, errors.New("no metastore in unit tests")
		})
		_ = rawDB.Close()
	})

	asked := &atomic.Int32{}
	playbooks.RegisterAction("aaa_budget_probe", budgetProbeAction{asked: asked})
	ctx := security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil)

	_, err = ExecutePlaybook(ctx, "account-1", playbooks.PlaybookEvent{Name: "probe", Deadline: time.Now().Add(-time.Second)})
	require.NoError(t, err)
	assert.Equal(t, int32(0), asked.Load(), "expired deadline: no auto action is asked")

	_, err = ExecutePlaybook(ctx, "account-2", playbooks.PlaybookEvent{Name: "probe", Deadline: time.Now().Add(time.Hour)})
	require.NoError(t, err)
	assert.Equal(t, int32(1), asked.Load(), "deadline ahead: auto actions are asked as before")
}

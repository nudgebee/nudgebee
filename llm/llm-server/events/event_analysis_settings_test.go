package events

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEventAnalysisRepositoryForAccountReuseWindow(t *testing.T) {
	global := time.Duration(config.Config.EventAnalysisFreshnessHours) * time.Hour
	cases := []struct {
		name     string
		settings string
		want     time.Duration
	}{
		{name: "no settings keeps global window", want: global},
		{name: "account window overrides global", settings: `{"reuse_window_hours":2}`, want: 2 * time.Hour},
		{name: "non-positive window keeps global", settings: `{"reuse_window_hours":0}`, want: global},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A distinct account per case: settings are cached per account.
			accountID := fmt.Sprintf("account-window-%d", i)
			rawDB, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = rawDB.Close() })
			rows := sqlmock.NewRows([]string{"value"})
			if tc.settings != "" {
				rows.AddRow(tc.settings)
			}
			mock.ExpectQuery("SELECT value FROM cloud_account_attrs").WithArgs(accountID, EventAnalysisSettingsAttr).WillReturnRows(rows)

			repo := NewEventAnalysisRepositoryForAccount(security.NewRequestContextForSuperAdmin(), &common.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgres")}, accountID)
			assert.Equal(t, tc.want, repo.analysisFreshness)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLoadAccountSettingsIsCached(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	dbManager := &common.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgres")}
	ctx := security.NewRequestContextForSuperAdmin()

	const accountID = "account-settings-cache"
	mock.ExpectQuery("SELECT value FROM cloud_account_attrs").WithArgs(accountID, EventAnalysisSettingsAttr).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"reuse_window_hours":3}`))

	// UI polling builds a repository per request; only the first reads the row.
	for range 3 {
		assert.Equal(t, 3*time.Hour, NewEventAnalysisRepositoryForAccount(ctx, dbManager, accountID).analysisFreshness)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

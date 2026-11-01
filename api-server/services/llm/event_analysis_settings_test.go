package llm

import (
	"fmt"
	"log/slog"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsNamespaceExcludedFromAutoAnalysis(t *testing.T) {
	cases := []struct {
		name      string
		namespace string
		settings  string // "" = no row; "-" = no query expected
		want      bool
	}{
		{name: "event without namespace is never excluded", namespace: "", settings: "-", want: false},
		{name: "no settings row", namespace: "demo", want: false},
		{name: "malformed settings fail open", namespace: "demo", settings: "{bad", want: false},
		{name: "namespace not listed", namespace: "prod", settings: `{"excluded_namespaces":["sandbox"]}`, want: false},
		{name: "namespace listed", namespace: "sandbox", settings: `{"excluded_namespaces":["other"," sandbox "]}`, want: true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A distinct account per case: results are cached per account.
			accountID := fmt.Sprintf("account-excluded-%d", i)
			rawDB, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = rawDB.Close() })
			database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
				return &database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgres")}, nil
			})
			t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

			if tc.settings != "-" {
				rows := sqlmock.NewRows([]string{"value"})
				if tc.settings != "" {
					rows.AddRow(tc.settings)
				}
				mock.ExpectQuery("SELECT value FROM cloud_account_attrs").WithArgs(accountID, accountAttrEventAnalysis).WillReturnRows(rows)
			}

			ctx := security.NewRequestContextForTenantAdmin("t1", slog.Default(), nil, nil)
			assert.Equal(t, tc.want, isNamespaceExcludedFromAutoAnalysis(ctx, accountID, tc.namespace))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestIsNamespaceExcludedFromAutoAnalysisCachesSettings(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgres")}, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	const accountID = "account-excluded-cache"
	mock.ExpectQuery("SELECT value FROM cloud_account_attrs").WithArgs(accountID, accountAttrEventAnalysis).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"excluded_namespaces":["sandbox"]}`))

	ctx := security.NewRequestContextForTenantAdmin("t1", slog.Default(), nil, nil)
	// One query serves every event until the cache entry expires.
	assert.True(t, isNamespaceExcludedFromAutoAnalysis(ctx, accountID, "sandbox"))
	assert.False(t, isNamespaceExcludedFromAutoAnalysis(ctx, accountID, "prod"))
	assert.True(t, isNamespaceExcludedFromAutoAnalysis(ctx, accountID, "sandbox"))
	require.NoError(t, mock.ExpectationsWereMet())
}

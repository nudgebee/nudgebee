package common

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testTenantID  = "11111111-1111-1111-1111-111111111111"
	testAccountID = "22222222-2222-2222-2222-222222222222"
)

func newMockDBManager(t *testing.T) (*DatabaseManager, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, mock
}

func TestIsFeatureEnabledForAccountWithDB(t *testing.T) {
	t.Run("account-scoped row wins without consulting the tenant", func(t *testing.T) {
		dbm, mock := newMockDBManager(t)
		mock.ExpectQuery("SELECT status FROM feature_flag").
			WithArgs(FeatureAIWorkflowTools, testAccountID).
			WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("enabled"))

		enabled, err := IsFeatureEnabledForAccountWithDB(dbm, FeatureAIWorkflowTools, testTenantID, testAccountID)
		require.NoError(t, err)
		assert.True(t, enabled)
		// No tenant query expected — the account row short-circuits.
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("falls back to the tenant row when the account has none", func(t *testing.T) {
		dbm, mock := newMockDBManager(t)
		mock.ExpectQuery("SELECT status FROM feature_flag").
			WithArgs(FeatureAIWorkflowTools, testAccountID).
			WillReturnRows(sqlmock.NewRows([]string{"status"}))
		mock.ExpectQuery("SELECT status FROM feature_flag").
			WithArgs(FeatureAIWorkflowTools, testTenantID).
			WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("enabled"))

		enabled, err := IsFeatureEnabledForAccountWithDB(dbm, FeatureAIWorkflowTools, testTenantID, testAccountID)
		require.NoError(t, err)
		assert.True(t, enabled)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no rows anywhere means disabled", func(t *testing.T) {
		dbm, mock := newMockDBManager(t)
		mock.ExpectQuery("SELECT status FROM feature_flag").
			WillReturnRows(sqlmock.NewRows([]string{"status"}))
		mock.ExpectQuery("SELECT status FROM feature_flag").
			WillReturnRows(sqlmock.NewRows([]string{"status"}))

		enabled, err := IsFeatureEnabledForAccountWithDB(dbm, FeatureAIWorkflowTools, testTenantID, testAccountID)
		require.NoError(t, err)
		assert.False(t, enabled)
	})

	t.Run("an account row that is not enabled falls through to the tenant", func(t *testing.T) {
		// Documents the precedence deliberately: an account row is a
		// SHORT-CIRCUIT for "enabled", not a veto for "disabled". This matches
		// llm-server's reader of the same table exactly — the two services must
		// agree, or one would synthesize tools while the other rejects the runs.
		dbm, mock := newMockDBManager(t)
		mock.ExpectQuery("SELECT status FROM feature_flag").
			WithArgs(FeatureAIWorkflowTools, testAccountID).
			WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("disabled"))
		mock.ExpectQuery("SELECT status FROM feature_flag").
			WithArgs(FeatureAIWorkflowTools, testTenantID).
			WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("enabled"))

		enabled, err := IsFeatureEnabledForAccountWithDB(dbm, FeatureAIWorkflowTools, testTenantID, testAccountID)
		require.NoError(t, err)
		// The tenant row is consulted, and it does enable the feature. This is
		// the documented precedence (account row is not a veto, it is a
		// short-circuit), recorded here so a future change to make "disabled"
		// a hard veto is a deliberate, visible decision.
		assert.True(t, enabled)
	})

	t.Run("a status other than enabled is not enabled", func(t *testing.T) {
		dbm, mock := newMockDBManager(t)
		mock.ExpectQuery("SELECT status FROM feature_flag").
			WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("pending"))
		mock.ExpectQuery("SELECT status FROM feature_flag").
			WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("pending"))

		enabled, err := IsFeatureEnabledForAccountWithDB(dbm, FeatureAIWorkflowTools, testTenantID, testAccountID)
		require.NoError(t, err)
		assert.False(t, enabled)
	})

	t.Run("fails closed on a database error", func(t *testing.T) {
		dbm, mock := newMockDBManager(t)
		mock.ExpectQuery("SELECT status FROM feature_flag").
			WillReturnError(errors.New("metastore unreachable"))

		enabled, err := IsFeatureEnabledForAccountWithDB(dbm, FeatureAIWorkflowTools, testTenantID, testAccountID)
		assert.Error(t, err)
		assert.False(t, enabled, "an unreachable metastore must not enable an AI-invocation gate")
	})

	t.Run("empty account falls straight through to the tenant", func(t *testing.T) {
		dbm, mock := newMockDBManager(t)
		mock.ExpectQuery("SELECT status FROM feature_flag").
			WithArgs(FeatureAIWorkflowTools, testTenantID).
			WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("enabled"))

		enabled, err := IsFeatureEnabledForAccountWithDB(dbm, FeatureAIWorkflowTools, testTenantID, "")
		require.NoError(t, err)
		assert.True(t, enabled)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no tenant and no account is disabled", func(t *testing.T) {
		dbm, _ := newMockDBManager(t)
		enabled, err := IsFeatureEnabledForAccountWithDB(dbm, FeatureAIWorkflowTools, "", "")
		require.NoError(t, err)
		assert.False(t, enabled)
	})
}

func TestIsFeatureEnabledForAccountWithNilManager(t *testing.T) {
	// A gate must deny, not panic, when its dependency is missing.
	for _, tc := range []struct {
		name string
		dbm  *DatabaseManager
	}{
		{"nil manager", nil},
		{"manager with nil handle", &DatabaseManager{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enabled, err := IsFeatureEnabledForAccountWithDB(tc.dbm, FeatureAIWorkflowTools, testTenantID, testAccountID)
			assert.Error(t, err)
			assert.False(t, enabled)
		})
	}
}

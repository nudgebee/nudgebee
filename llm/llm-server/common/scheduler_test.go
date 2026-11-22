package common

import (
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
)

// An in-cluster replica claims leadership past a local worker's flag (which held
// dev's leader for up to 2.3 h), then demotes it; a local worker in a private
// pool still competes with every leader, local ones included.
func TestRegisterOrUpdateWorkerIgnoresLocalLeadersOnlyInCluster(t *testing.T) {
	_, mock, err := MockMetastore()
	require.NoError(t, err)
	prevName, prevService, prevEligible := config.Config.ServerName, config.SERVICE_NAME, config.Config.SchedulerLeaderEligible
	t.Cleanup(func() {
		config.Config.ServerName, config.SERVICE_NAME, config.Config.SchedulerLeaderEligible = prevName, prevService, prevEligible
		// Give the package-level mock back to the feature-flag tests.
		RegisterDatabaseManagerHook(Metastore, func() (*DatabaseManager, error) {
			return &DatabaseManager{Db: sqlx.NewDb(pkgRawDB.(*sql.DB), "postgresql")}, nil
		})
	})
	config.Config.SchedulerLeaderEligible = "true"

	cases := []struct {
		name, server, service string
		ignoreLocal           bool
	}{
		{"cluster replica", "llm-server-86d6595fbb-z2spc", config.DefaultServiceName, true},
		{"local worker in a private pool", config.LocalWorkerNamePrefix + "dev-laptop", "llm-server-dev", false},
	}
	for _, tc := range cases {
		config.Config.ServerName, config.SERVICE_NAME = tc.server, tc.service
		mock.ExpectExec("INSERT INTO nb_workers").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta(claimLeaderSQL)).
			WithArgs(tc.service, tc.server, sqlmock.AnyArg(), tc.ignoreLocal, config.LocalWorkerNamePrefix+"%").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta("worker_name!=$2")).WithArgs(tc.service, tc.server).
			WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, registerOrUpdateWorker(), tc.name)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

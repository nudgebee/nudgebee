package core

import (
	"testing"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/relay"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// queryProxyDatasources used to run its per-integration config query *inside*
// the outer cursor's scan loop, so it held one pooled connection while asking
// for a second. That is the pattern that took the pod down in #34973.
//
// Capping the pool at a single connection turns that latent pressure into a
// deterministic failure: with the old shape the inner query can never acquire a
// connection, because the only one is pinned by the still-open outer cursor, and
// the call blocks forever. With the two-pass shape the cursor is drained and
// closed first, so the connection is back in the pool when the inner query runs.
func TestQueryProxyDatasourcesReleasesCursorBeforeInnerQuery(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err)

	// The manager is package-global (TestMain), so capture the current limit and
	// put it back rather than assuming a value for the tests that run after this one.
	previousMaxOpen := dbms.Db.Stats().MaxOpenConnections
	dbms.Db.SetMaxOpenConns(1)
	t.Cleanup(func() { dbms.Db.SetMaxOpenConns(previousMaxOpen) })

	pkgMock.ExpectQuery(`SELECT i\.id::text, i\.type::text, i\.name::text`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "type", "name"}).
			AddRow("int-1", "postgresql", "pg-one").
			AddRow("int-2", "http_proxy", "http-one"))
	pkgMock.ExpectQuery(`SELECT name::text, value::text, is_encrypted`).
		WillReturnRows(sqlmock.NewRows([]string{"name", "value", "is_encrypted"}).
			AddRow("host", "db.internal", false).
			AddRow("port", "5432", false))
	pkgMock.ExpectQuery(`SELECT name::text, value::text, is_encrypted`).
		WillReturnRows(sqlmock.NewRows([]string{"name", "value", "is_encrypted"}).
			AddRow("proxy_type", "http", false))

	var (
		got  []relay.ProxyDatasourceConfig
		qErr error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		got, qErr = queryProxyDatasources("acct-1")
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("queryProxyDatasources blocked: the outer cursor is still holding the pool's only connection while the per-integration config query waits for one (#34973 / #36097)")
	}

	require.NoError(t, qErr)
	require.Len(t, got, 2, "every proxy integration must still be returned")

	// Cursor order is preserved by the two-pass split, and each integration is
	// still paired with its own config values.
	assert.Equal(t, "int-1", got[0].ID)
	assert.Equal(t, "pg-one", got[0].Name)
	assert.Equal(t, "postgresql", got[0].Type)
	assert.Equal(t, "db.internal", got[0].Config["host"])
	assert.Equal(t, 5432, got[0].Config["port"], "port must still be coerced to an int")
	assert.Equal(t, "postgresql", got[0].Config["db_type"], "dual-mode db_type injection must survive the restructure")
	assert.Equal(t, "db-proxy", got[0].ProxyType, "dual-mode proxy_type derivation must survive the restructure")

	assert.Equal(t, "int-2", got[1].ID)
	assert.Equal(t, "http-one", got[1].Name)
	assert.Equal(t, "http", got[1].ProxyType)
	assert.Equal(t, "cloud_push", got[1].CredentialSource, "the credential_source default must still be applied")
}

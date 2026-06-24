package opencostengine

import (
	"database/sql"

	_ "github.com/lib/pq" // postgres driver
)

// DB holds a shared database connection pool.
type DB struct {
	conn *sql.DB
}

// NewDB creates a new DB instance with a connection pool. sql.Open validates the
// DSN but does not dial — the connection is opened lazily on first use. This is
// deliberate: cost-server is queried by a 6-hourly cron, so connecting lazily means
// a transient/unreachable metastore (or env-specific SSL negotiation) surfaces on
// the first allocation request rather than crash-looping the pod at boot. Hence no
// readiness init container is needed either.
func NewDB(connStr string) (*DB, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}

	return &DB{
		conn: db,
	}, nil
}

// Connection returns the underlying sql.DB object.
func (db *DB) Connection() *sql.DB {
	return db.conn
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

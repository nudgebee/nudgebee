package database

import (
	"database/sql"
	"fmt"
	"log/slog"
	"nudgebee/tickets-server/common"
	"sync"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

type DatabaseManager struct {
	Db *sqlx.DB
}

var (
	dbManager *DatabaseManager
	once      sync.Once
	initErr   error
)

// GetDatabaseManager returns a singleton database manager instance
func GetDatabaseManager() (*DatabaseManager, error) {
	once.Do(func() {
		dbManager, initErr = newDatabaseManager()
	})
	return dbManager, initErr
}

func newDatabaseManager() (*DatabaseManager, error) {
	dbUrl := common.Config.ServiceDBUrl
	if dbUrl == "" {
		return nil, fmt.Errorf("ServiceDBUrl is not configured")
	}

	db, err := sqlx.Connect("postgres", dbUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	slog.Info("Database connection established successfully")

	return &DatabaseManager{Db: db}, nil
}

// Query executes a query that returns rows
func (d *DatabaseManager) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return d.Db.Query(query, args...)
}

// Queryx executes a query that returns rows (sqlx version)
func (d *DatabaseManager) Queryx(query string, args ...interface{}) (*sqlx.Rows, error) {
	return d.Db.Queryx(query, args...)
}

// Exec executes a query that doesn't return rows
func (d *DatabaseManager) Exec(query string, args ...interface{}) (sql.Result, error) {
	return d.Db.Exec(query, args...)
}

// Get retrieves a single row into dest
func (d *DatabaseManager) Get(dest interface{}, query string, args ...interface{}) error {
	return d.Db.Get(dest, query, args...)
}

// Select retrieves multiple rows into dest
func (d *DatabaseManager) Select(dest interface{}, query string, args ...interface{}) error {
	return d.Db.Select(dest, query, args...)
}

// Close closes the database connection
func (d *DatabaseManager) Close() error {
	return d.Db.Close()
}

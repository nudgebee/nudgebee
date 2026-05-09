package common

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nudgebee/collector/otel/config"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/samber/lo"
)

type DatabaseManager struct {
	// eventually hide this and expose only wrapper apis which will be used by the services
	// handle rebind in the wrappers so that the services do not have to worry about it and can use the same query for both clickhouse and postgres
	Db *sqlx.DB
}

func (d *DatabaseManager) Close() error {
	return d.Db.Close()
}

// bind variables
func (d *DatabaseManager) Query(query string, args ...any) (*sqlx.Rows, error) {
	return d.Db.Queryx(query, args...)
}

// bind variables
func (d *DatabaseManager) QueryRow(query string, args ...any) (*sqlx.Row, error) {
	row := d.Db.QueryRowx(query, args...)
	return row, row.Err()
}

func (d *DatabaseManager) Exec(query string, args ...any) (sql.Result, error) {
	return d.Db.Exec(query, args...)
}

// move query filters here
func (d *DatabaseManager) Select(table string, filters map[string]any, cols []string) (sql.Result, error) {
	return nil, errors.ErrUnsupported
}

// move query filters here
func (d *DatabaseManager) Insert(table string, cols []string, values [][]any, onConflict []string, returningCols []string) (*sqlx.Rows, error) {
	return nil, errors.ErrUnsupported
}

// move query filters here
func (d *DatabaseManager) Update(table string, filters map[string]any, cols []string, values []any) (sql.Result, error) {
	return nil, errors.ErrUnsupported
}

// move query filters here
func (d *DatabaseManager) Delete(table string, filters map[string]any) (sql.Result, error) {
	return nil, errors.ErrUnsupported
}

func (d *DatabaseManager) DoInTransaction(callback func(*sqlx.Tx) (any, error)) (any, error) {
	tx, err := d.Db.Beginx()
	if err != nil {
		return nil, err
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	callbackResult, err := callback(tx)
	if err != nil {
		err := tx.Rollback()
		if err != nil {
			return callbackResult, err
		}
		return callbackResult, err
	}

	err = tx.Commit()
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return callbackResult, fmt.Errorf("commit error: %w; rollback error: %v", err, rbErr)
		}
		return callbackResult, err
	}

	return callbackResult, nil
}

func (d *DatabaseManager) BuildInClause(values ...string) string {
	valueStr := lo.Map(values, func(value string, i int) string {
		return fmt.Sprintf("'%v'", value)
	})
	return strings.Join(valueStr, ",")
}

func newPostgresDatabaseManager() (*DatabaseManager, error) {
	db, err := sqlx.Open("postgres", config.Config.OtelServerDBUrl)
	if err != nil {
		slog.Error("dbms: error connecting to postgres", "error", err)
		return nil, err
	}
	db.SetMaxOpenConns(config.Config.OtelServerDBMaxConnection)
	db.SetMaxIdleConns(config.Config.OtelServerDBMinConnection)
	db.SetConnMaxIdleTime(time.Duration(config.Config.OtelServerDBIdleMinutes * int(time.Minute)))

	if err := db.Ping(); err != nil {
		slog.Error("dbms: error pinging postgres", "error", err)
		return nil, err
	}
	return &DatabaseManager{
		Db: db,
	}, nil
}

var databaseManager map[DatabaseManagerType]*DatabaseManager = make(map[DatabaseManagerType]*DatabaseManager)

type DatabaseManagerType string

const (
	Metastore DatabaseManagerType = "metastore"
)

func GetDatabaseManager(name DatabaseManagerType) (*DatabaseManager, error) {
	if manager, ok := databaseManager[name]; ok {
		return manager, nil
	}
	if databaseManagerHooks[name] != nil {
		manager, err := databaseManagerHooks[name]()
		if err != nil {
			return nil, err
		}
		databaseManager[name] = manager
		return manager, nil
	}

	if name == Metastore {
		manager, err := newPostgresDatabaseManager()
		if err != nil {
			return nil, err
		}
		databaseManager[name] = manager
		return manager, nil

	} else {
		return nil, fmt.Errorf("dbms: database manager not found - %v", name)
	}
}

var databaseManagerHooks map[DatabaseManagerType]func() (*DatabaseManager, error) = make(map[DatabaseManagerType]func() (*DatabaseManager, error))

func RegisterDatabaseManagerHook(name DatabaseManagerType, callback func() (*DatabaseManager, error)) {
	databaseManagerHooks[name] = callback
}

func Close() {
	for _, db := range databaseManager {
		err := db.Db.Close()
		if err != nil {
			slog.Error("dbms: error closing database", "error", err)
		}
	}
}

// Package common holds shared gateway infrastructure. rdbms.go is lifted from the
// llm-server convention (llm/llm-server/common/rdbms.go) — this module can't import
// it (independent modules, no go.work), so the code is copied deliberately to keep
// the PG/CH access pattern identical across services. ClickHouse is pluggable via
// RegisterDatabaseManagerHook so the CH driver never enters this core file.
package common

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"nudgebee/llm-gateway/config"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

var (
	maskKeywordPasswordRegex = regexp.MustCompile(`(?i)(password\s*=\s*)("[^"]*"|[^\s]+)`)
	maskURLPasswordRegex     = regexp.MustCompile(`://([^:]*):([^/?#]*)@`)
	connectTimeoutRegex      = regexp.MustCompile(`[?&]connect_timeout=`)
)

type DatabaseManager struct {
	// Rebind is handled in the wrappers so services use the same $N query for both
	// clickhouse and postgres.
	Db          *sqlx.DB
	stopMonitor chan struct{}
	closeOnce   sync.Once
}

func (d *DatabaseManager) Close() error {
	d.closeOnce.Do(func() {
		if d.stopMonitor != nil {
			close(d.stopMonitor)
		}
	})
	return d.Db.Close()
}

var dollarRegex = regexp.MustCompile(`\$[0-9]+`)

// prepareQueryForIn converts a query with $N placeholders to a query with ?
// placeholders and reorders the arguments to match, so sqlx.In works with queries
// written for PostgreSQL without a rewrite.
func prepareQueryForIn(driverName string, query string, args []any) (string, []any, error) {
	if sqlx.BindType(driverName) != sqlx.DOLLAR {
		return query, args, nil
	}

	matches := dollarRegex.FindAllStringIndex(query, -1)
	if len(matches) == 0 {
		return query, args, nil
	}

	newArgs := make([]any, len(matches))
	for i, match := range matches {
		placeholder := query[match[0]:match[1]]
		num, err := strconv.Atoi(placeholder[1:]) // strip '$'
		if err != nil {
			return "", nil, fmt.Errorf("invalid placeholder number in %s: %w", placeholder, err)
		}
		if num > len(args) || num < 1 {
			return "", nil, fmt.Errorf("placeholder %s is out of bounds for %d arguments", placeholder, len(args))
		}
		newArgs[i] = args[num-1]
	}

	newQuery := dollarRegex.ReplaceAllString(query, "?")
	return newQuery, newArgs, nil
}

func scanInternal(rows *sqlx.Rows, dest any) error {
	value := reflect.ValueOf(dest)
	if value.Kind() != reflect.Pointer {
		return errors.New("must pass a pointer, not a value")
	}
	if value.IsNil() {
		return errors.New("nil pointer passed to destination")
	}

	slice := reflect.Indirect(value)
	if slice.Kind() != reflect.Slice {
		return errors.New("destination must be a pointer to a slice")
	}

	elemType := slice.Type().Elem()
	switch elemType.Kind() {
	case reflect.Struct:
		return sqlx.StructScan(rows, dest)
	case reflect.Map:
		newSlice := reflect.MakeSlice(slice.Type(), 0, slice.Len())
		for rows.Next() {
			mapValue := reflect.MakeMap(elemType)
			switch mapType := mapValue.Interface().(type) {
			case map[string]any:
				if err := rows.MapScan(mapType); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unsupported map type: %T", mapType)
			}
			newSlice = reflect.Append(newSlice, mapValue)
		}
		slice.Set(newSlice)
	default:
		newSlice := reflect.MakeSlice(slice.Type(), 0, 0)
		for rows.Next() {
			elemPtr := reflect.New(elemType)
			if err := rows.Scan(elemPtr.Interface()); err != nil {
				return err
			}
			newSlice = reflect.Append(newSlice, elemPtr.Elem())
		}
		slice.Set(newSlice)
	}
	return rows.Err()
}

func selectInternal(db *sqlx.DB, dest any, query string, args ...any) (err error) {
	rows, err := db.Queryx(query, args...)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	err = scanInternal(rows, dest)
	return err
}

func (d *DatabaseManager) QueryAndScan(dest any, query string, args ...any) error {
	q, a, err := prepareQueryForIn(d.Db.DriverName(), query, args)
	if err != nil {
		return err
	}
	q, a, err = sqlx.In(q, a...)
	if err != nil {
		return err
	}
	q = d.Db.Rebind(q)
	return selectInternal(d.Db, dest, q, a...)
}

func (d *DatabaseManager) QueryRowAndScan(dest any, query string, args ...any) error {
	q, a, err := prepareQueryForIn(d.Db.DriverName(), query, args)
	if err != nil {
		return err
	}
	q, a, err = sqlx.In(q, a...)
	if err != nil {
		return err
	}
	q = d.Db.Rebind(q)
	return d.Db.Get(dest, q, a...)
}

func (d *DatabaseManager) Query(query string, args ...any) (*sqlx.Rows, error) {
	return d.Db.Queryx(query, args...)
}

func (d *DatabaseManager) QueryRow(query string, args ...any) (*sqlx.Row, error) {
	row := d.Db.QueryRowx(query, args...)
	return row, row.Err()
}

// Exec runs a statement as-is (no rebind). Callers writing $N SQL for a non-$N
// driver (e.g. ClickHouse) should d.Db.Rebind first; see the metering sink.
func (d *DatabaseManager) Exec(query string, args ...any) (sql.Result, error) {
	return d.Db.Exec(query, args...)
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
		slog.Error("db: transaction callback failed", "error", err)
		if err2 := tx.Rollback(); err2 != nil {
			slog.Error("db: transaction rollback failed", "error", err2)
			return callbackResult, errors.Join(err, err2)
		}
		return callbackResult, err
	}
	if err := tx.Commit(); err != nil {
		slog.Error("db: unable to commit transaction", "error", err)
		return callbackResult, err
	}
	return callbackResult, nil
}

// PrepareInQuery rebinds a base query + slice into a driver-specific IN clause.
// Example: ("... WHERE id IN ", []string{"a","b"}) -> "... WHERE id IN ($1,$2)".
func (d *DatabaseManager) PrepareInQuery(queryBase string, argSlice any) (string, []any, error) {
	if argSlice == nil {
		return "", nil, errors.New("argument slice for IN query cannot be nil")
	}
	inQuery, args, err := sqlx.In(queryBase+"(?);", argSlice)
	if err != nil {
		return "", nil, fmt.Errorf("failed to prepare IN query: %w", err)
	}
	return d.Db.Rebind(inQuery), args, nil
}

func MaskDBConnectionString(dbURL string) string {
	if !strings.Contains(dbURL, "://") {
		return maskKeywordPasswordRegex.ReplaceAllString(dbURL, "${1}xxxxx")
	}
	u, err := url.Parse(dbURL)
	if err != nil {
		return maskURLPasswordRegex.ReplaceAllString(dbURL, "://$1:xxxxx@")
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
		}
	}
	return u.String()
}

// newPostgresDatabaseManager opens the gateway Postgres (metastore, and the
// metering sink when gateway_metering_sink=postgres).
func newPostgresDatabaseManager() (*DatabaseManager, error) {
	dbURL := config.Config.GatewayDBURL
	if !connectTimeoutRegex.MatchString(dbURL) {
		if strings.Contains(dbURL, "?") {
			dbURL += "&connect_timeout=10"
		} else {
			dbURL += "?connect_timeout=10"
		}
	}

	slog.Info("dbms: opening postgres connection", "url", MaskDBConnectionString(dbURL))
	db, err := sqlx.Open("postgres", dbURL)
	if err != nil {
		slog.Error("dbms: error opening postgres", "error", err)
		return nil, err
	}
	db.SetMaxOpenConns(config.Config.GatewayDBMaxConns)
	db.SetMaxIdleConns(config.Config.GatewayDBMinConns)
	db.SetConnMaxIdleTime(time.Duration(config.Config.GatewayDBIdleMinute) * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		slog.Error("dbms: error pinging postgres", "error", err)
		return nil, err
	}
	slog.Info("dbms: postgres ping successful")

	manager := &DatabaseManager{Db: db, stopMonitor: make(chan struct{})}
	go manager.monitorPoolStats()
	return manager, nil
}

func (d *DatabaseManager) monitorPoolStats() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	var lastWaitCount int64
	for {
		select {
		case <-ticker.C:
			stats := d.Db.Stats()
			if stats.WaitCount > lastWaitCount {
				slog.Warn("dbms: connection pool exhaustion detected",
					"new_wait_count", stats.WaitCount-lastWaitCount,
					"total_wait_count", stats.WaitCount,
					"wait_duration", stats.WaitDuration.String(),
					"in_use", stats.InUse,
					"idle", stats.Idle,
					"open", stats.OpenConnections,
					"max_open", stats.MaxOpenConnections,
				)
			}
			lastWaitCount = stats.WaitCount
		case <-d.stopMonitor:
			slog.Info("dbms: stopping pool monitoring", "pool", d.Db.DriverName())
			return
		}
	}
}

// DatabaseManagerType names a logical database the gateway connects to.
type DatabaseManagerType string

const (
	// Metastore is the Postgres store: virtual keys, identity, pricing catalog.
	Metastore DatabaseManagerType = "metastore"
	// MeteringSink is the usage-events store: Postgres by default, or ClickHouse
	// when a hook is registered (RegisterDatabaseManagerHook) — keeping the CH
	// driver out of this core file.
	MeteringSink DatabaseManagerType = "metering_sink"
)

var (
	databaseManager      = make(map[DatabaseManagerType]*DatabaseManager)
	databaseManagerMutex sync.Mutex
	databaseManagerHooks = make(map[DatabaseManagerType]func() (*DatabaseManager, error))
)

// RegisterDatabaseManagerHook registers a custom opener for a manager type (e.g.
// a ClickHouse metering sink). Registered before GetDatabaseManager is first
// called for that type.
func RegisterDatabaseManagerHook(name DatabaseManagerType, callback func() (*DatabaseManager, error)) {
	databaseManagerHooks[name] = callback
}

func GetDatabaseManager(name DatabaseManagerType) (*DatabaseManager, error) {
	databaseManagerMutex.Lock()
	defer databaseManagerMutex.Unlock()
	if manager, ok := databaseManager[name]; ok {
		return manager, nil
	}

	// A registered hook takes precedence (e.g. ClickHouse sink).
	if hook := databaseManagerHooks[name]; hook != nil {
		manager, err := hook()
		if err != nil {
			return nil, err
		}
		databaseManager[name] = manager
		return manager, nil
	}

	switch name {
	case Metastore:
		manager, err := newPostgresDatabaseManager()
		if err != nil {
			return nil, err
		}
		databaseManager[name] = manager
		return manager, nil
	case MeteringSink:
		if config.Config.MeteringSink == config.SinkClickhouse {
			return nil, fmt.Errorf("dbms: clickhouse metering sink requires a registered hook (RegisterDatabaseManagerHook)")
		}
		manager, err := newPostgresDatabaseManager()
		if err != nil {
			return nil, err
		}
		databaseManager[name] = manager
		return manager, nil
	default:
		return nil, fmt.Errorf("dbms: database manager not found - %v", name)
	}
}

type DatabaseHealth struct {
	InUse     int   `json:"in_use"`
	Idle      int   `json:"idle"`
	WaitCount int64 `json:"wait_count"`
	MaxOpen   int   `json:"max_open"`
}

func GetAllDatabaseStats() map[string]DatabaseHealth {
	databaseManagerMutex.Lock()
	defer databaseManagerMutex.Unlock()

	result := make(map[string]DatabaseHealth)
	for name, manager := range databaseManager {
		stats := manager.Db.Stats()
		result[string(name)] = DatabaseHealth{
			InUse:     stats.InUse,
			Idle:      stats.Idle,
			WaitCount: stats.WaitCount,
			MaxOpen:   stats.MaxOpenConnections,
		}
	}
	return result
}

func Close() {
	databaseManagerMutex.Lock()
	defer databaseManagerMutex.Unlock()
	for name, db := range databaseManager {
		if err := db.Close(); err != nil {
			slog.Error("dbms: error closing database", "name", name, "error", err)
		}
	}
}

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

	"nudgebee/llm/config"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

type DatabaseManager struct {
	// eventually hide this and expose only wrapper apis which will be used by the services
	// handle rebind in the wrappers so that the services do not have to worry about it and can use the same query for both clickhouse and postgres
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
// placeholders and reorders the arguments to match. This is to allow sqlx.In
// to work with queries written for PostgreSQL without forcing a rewrite.
func prepareQueryForIn(driverName string, query string, args []any) (string, []any, error) {
	if sqlx.BindType(driverName) != sqlx.DOLLAR {
		// This logic is only for drivers that use $N placeholders.
		// For others, we pass the query and args through as-is.
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
			// Should not happen with this regex.
			return "", nil, fmt.Errorf("invalid placeholder number in %s: %w", placeholder, err)
		}

		if num > len(args) || num < 1 {
			return "", nil, fmt.Errorf("placeholder %s is out of bounds for %d arguments", placeholder, len(args))
		}
		// Adjust for 0-based slice index.
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
		// For map, we need to iterate and use MapScan
		newSlice := reflect.MakeSlice(slice.Type(), 0, slice.Len())
		for rows.Next() {
			// Create a new map instance for each row
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
		// Assume primitive type for others, iterate and scan
		newSlice := reflect.MakeSlice(slice.Type(), 0, 0)
		for rows.Next() {
			// Create a new element of the primitive type
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
		err2 := tx.Rollback()
		if err2 != nil {
			slog.Error("db: transaction rollback failed", "error", err2)
			return callbackResult, errors.Join(err, err2)
		}
		return callbackResult, err
	}

	err = tx.Commit()
	if err != nil {
		slog.Error("db: unable to commit transactions", "error", err)
		return callbackResult, err
	}

	return callbackResult, nil
}

func (d *DatabaseManager) BuildInClause(query string, values ...any) (string, []any, error) {
	// sqlx.In expects the query to have a single '?' placeholder for the IN clause
	// and the corresponding argument to be a slice of values.
	// Example: "SELECT * FROM users WHERE id IN (?)" and args: []int{1, 2, 3}
	// This function assumes the caller provides the base query and the slice of values.
	// It's more of a helper to use sqlx.In directly where needed, or this function
	// needs to be more specific about how it constructs the query part.

	// For a generic IN clause builder that returns just the clause string and args for it:
	if len(values) == 0 {
		return "", nil, errors.New("BuildInClause: no values provided for IN clause")
	}

	// The typical usage of sqlx.In is to incorporate it into a larger query.
	// This function, as named, seems to want to build *just* the "(?,?,?)" part,
	// but sqlx.In is designed to build the *entire* query with the IN clause.

	// Let's assume the intent is to use it with a query that has `?` for the IN part.
	// e.g. query = "SELECT * FROM table WHERE column IN (?)"
	// and values is the slice for the IN clause, e.g. []string{"a", "b", "c"}

	// sqlx.In requires the argument for the IN (?) to be a slice.
	// If `values` is already a slice of the correct type (e.g. []string, []int),
	// it should be passed directly as the argument to sqlx.In.
	// This function's current signature `values ...any` means `values` is a slice of `any`.
	// If it's called as `BuildInClause(query, myStringSlice)` then `values` will be `[]any{myStringSlice}`.
	// If it's called as `BuildInClause(query, "a", "b", "c")` then `values` will be `[]any{"a", "b", "c"}`.

	// Let's adjust to a more common use case for `sqlx.In` where `values` is a single slice argument.
	// The caller should prepare the slice.
	if len(values) == 1 {
		if sliceValues, ok := values[0].([]any); ok {
			// If a single []any is passed, use it directly
			return sqlx.In(query, sliceValues...)
		}
		// If a single slice of a specific type (e.g., []string) is passed
		return sqlx.In(query, values[0])
	}
	// If multiple arguments are passed (e.g., "a", "b", "c"), treat them as the slice elements
	return sqlx.In(query, values)
}

// Helper function to get a rebinded query with an IN clause, assuming values is a slice.
// Example usage: queryBase = "SELECT * FROM users WHERE name IN "
// columnName = "name" (though not used by sqlx.In directly in this construction)
// argSlice = []string{"Alice", "Bob"}
// Returns: "SELECT * FROM users WHERE name IN ($1, $2)", []any{"Alice", "Bob"}, error
func (d *DatabaseManager) PrepareInQuery(queryBase string, argSlice any) (string, []any, error) {
	if argSlice == nil {
		return "", nil, errors.New("argument slice for IN query cannot be nil")
	}
	// Construct a dummy query for sqlx.In to process the slice
	// The placeholder for the IN clause is just '?'
	inQuery, args, err := sqlx.In(queryBase+"(?);", argSlice)
	if err != nil {
		return "", nil, fmt.Errorf("failed to prepare IN query: %w", err)
	}
	// Rebind the '?' to the driver-specific placeholders (e.g., $1, $2 for PostgreSQL)
	reboundQuery := d.Db.Rebind(inQuery)
	return reboundQuery, args, nil
}

func MaskDBConnectionString(dbUrl string) string {
	// Keyword-value form (e.g. "host=localhost user=postgres password=secret"):
	// url.Parse swallows this without error and leaves u.User == nil, so the
	// password would otherwise be logged in plain text. Detect by absence of
	// the URL scheme delimiter and mask via regex on the `password=` token.
	if !strings.Contains(dbUrl, "://") {
		re := regexp.MustCompile(`(?i)(password\s*=\s*)("[^"]*"|[^\s]+)`)
		return re.ReplaceAllString(dbUrl, "${1}xxxxx")
	}

	u, err := url.Parse(dbUrl)
	if err != nil {
		// URL parse failure (e.g. an unescaped '%' in the password). Fall back
		// to a regex that stops at the first `/`, `?`, or `#` rather than the
		// first `@`, so that a password containing `@` doesn't leave its
		// suffix (and downstream user/host fragments) unmasked.
		re := regexp.MustCompile(`://([^:]*):([^/?#]*)@`)
		return re.ReplaceAllString(dbUrl, "://$1:xxxxx@")
	}

	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
		}
	}
	return u.String()
}

func newPostgresDatabaseManager() (*DatabaseManager, error) {
	dbUrl := config.Config.LlmServerDBUrl
	// Add connect_timeout if not present to help lib/pq handle hangs during connection
	if !regexp.MustCompile(`[?&]connect_timeout=`).MatchString(dbUrl) {
		if regexp.MustCompile(`\?`).MatchString(dbUrl) {
			dbUrl += "&connect_timeout=10"
		} else {
			dbUrl += "?connect_timeout=10"
		}
	}

	slog.Info("dbms: opening postgres connection", "url", MaskDBConnectionString(dbUrl))
	db, err := sqlx.Open("postgres", dbUrl)
	if err != nil {
		slog.Error("dbms: error opening postgres", "error", err)
		return nil, err
	}
	db.SetMaxOpenConns(config.Config.LlmServerDBMaxConnection)
	db.SetMaxIdleConns(config.Config.LlmServerDBMinConnection)
	db.SetConnMaxIdleTime(time.Duration(config.Config.LlmServerDBIdleMinutes * int(time.Minute)))

	slog.Info("dbms: pinging postgres with timeout...")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		slog.Error("dbms: error pinging postgres", "error", err)
		return nil, err
	}
	slog.Info("dbms: postgres ping successful")
	manager := &DatabaseManager{
		Db:          db,
		stopMonitor: make(chan struct{}),
	}
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
				newWaits := stats.WaitCount - lastWaitCount
				slog.Warn("dbms: connection pool exhaustion detected",
					"new_wait_count", newWaits,
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

var databaseManager map[DatabaseManagerType]*DatabaseManager = make(map[DatabaseManagerType]*DatabaseManager)
var databaseManagerMutex sync.Mutex

type DatabaseManagerType string

const (
	Metastore DatabaseManagerType = "metastore"
)

func GetDatabaseManager(name DatabaseManagerType) (*DatabaseManager, error) {
	databaseManagerMutex.Lock()
	defer databaseManagerMutex.Unlock()
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

var databaseManagerHooks map[DatabaseManagerType]func() (*DatabaseManager, error) = make(map[DatabaseManagerType]func() (*DatabaseManager, error))

func RegisterDatabaseManagerHook(name DatabaseManagerType, callback func() (*DatabaseManager, error)) {
	databaseManagerHooks[name] = callback
}

func Close() {
	for _, db := range databaseManager {
		err := db.Close()
		if err != nil {
			slog.Error("dbms: error closing database", "error", err)
		}
	}
}

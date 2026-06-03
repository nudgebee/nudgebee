package common

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"strconv"
	"sync"
	"time"

	"nudgebee/runbook/config"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

type DatabaseManager struct {
	// eventually hide this and expose only wrapper apis which will be used by the services
	// handle rebind in the wrappers so that the services do not have to worry about it and can use the same query for both clickhouse and postgres
	Db *sqlx.DB
}

func (d *DatabaseManager) Close() error {
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

func newPostgresDatabaseManager() (*DatabaseManager, error) {
	db, err := sqlx.Open("postgres", config.Config.RunbookServerDBUrl)
	if err != nil {
		slog.Error("dbms: error connecting to postgres", "error", err)
		return nil, err
	}
	db.SetMaxOpenConns(config.Config.RunbookServerDBMaxConnection)
	db.SetMaxIdleConns(config.Config.RunbookServerDBMinConnection)
	db.SetConnMaxIdleTime(time.Duration(config.Config.RunbookServerDBIdleMinutes * int(time.Minute)))

	if err := db.Ping(); err != nil {
		slog.Error("dbms: error pinging postgres", "error", err)
		return nil, err
	}
	return &DatabaseManager{
		Db: db,
	}, nil
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

package common

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"nudgebee/collector/cloud/config"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	_ "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

type aggregatedResult struct {
	rowsAffected int64
}

func (r *aggregatedResult) LastInsertId() (int64, error) {
	return 0, errors.New("LastInsertId is not supported for batched NamedExec")
}

func (r *aggregatedResult) RowsAffected() (int64, error) {
	return r.rowsAffected, nil
}

type DatabaseManager struct {
	db *sqlx.DB
}

type DatabaseManagerTx interface {
	Query(query string, args ...any) (*sqlx.Rows, error)
	QueryRow(query string, args ...any) (*sqlx.Row, error)
	Exec(query string, args ...any) (sql.Result, error)
	NamedExec(query string, args []map[string]any, options ...InsertQueryOption) (sql.Result, error)
}

type InsertQueryOption struct {
	InertBatchSize int
}

type databaseManagerTx struct {
	tx *sqlx.Tx
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
	q, a, err := prepareQueryForIn(d.db.DriverName(), query, args)
	if err != nil {
		return err
	}
	q, a, err = sqlx.In(q, a...)
	if err != nil {
		return err
	}
	q = d.db.Rebind(q)
	return selectInternal(d.db, dest, q, a...)
}

func (d *DatabaseManager) QueryRowAndScan(dest any, query string, args ...any) error {
	q, a, err := prepareQueryForIn(d.db.DriverName(), query, args)
	if err != nil {
		return err
	}
	q, a, err = sqlx.In(q, a...)
	if err != nil {
		return err
	}
	q = d.db.Rebind(q)
	return d.db.Get(dest, q, a...)
}

func (t *databaseManagerTx) Query(query string, args ...any) (*sqlx.Rows, error) {
	q, a, err := prepareQueryForIn(t.tx.DriverName(), query, args)
	if err != nil {
		return nil, err
	}
	q, a, err = sqlx.In(q, a...)
	if err != nil {
		return nil, err
	}
	q = t.tx.Rebind(q)
	return t.tx.Queryx(q, a...)
}

func (t *databaseManagerTx) QueryRow(query string, args ...any) (*sqlx.Row, error) {
	q, a, err := prepareQueryForIn(t.tx.DriverName(), query, args)
	if err != nil {
		return nil, err
	}
	q, a, err = sqlx.In(q, a...)
	if err != nil {
		return nil, err
	}
	q = t.tx.Rebind(q)
	row := t.tx.QueryRowx(q, a...)
	return row, row.Err()
}

func (t *databaseManagerTx) Exec(query string, args ...any) (sql.Result, error) {
	q, a, err := prepareQueryForIn(t.tx.DriverName(), query, args)
	if err != nil {
		return nil, err
	}
	q, a, err = sqlx.In(q, a...)
	if err != nil {
		return nil, err
	}
	q = t.tx.Rebind(q)
	return t.tx.Exec(q, a...)
}

func (t *databaseManagerTx) NamedExec(query string, args []map[string]any, options ...InsertQueryOption) (sql.Result, error) {
	if len(args) == 0 {
		return &aggregatedResult{rowsAffected: 0}, nil
	}

	batchedData := make([][]map[string]any, 0)
	batchSize := 100
	if len(options) > 0 && options[0].InertBatchSize > 0 {
		batchSize = options[0].InertBatchSize
	}

	if len(args) < batchSize {
		batchedData = append(batchedData, args)
	} else {
		for i := 0; i < len(args); i += batchSize {
			end := i + batchSize
			if end > len(args) {
				end = len(args)
			}
			batchedData = append(batchedData, args[i:end])
		}
	}
	var totalRowsAffected int64
	for i, arg := range batchedData {
		res, err := t.tx.NamedExec(query, arg)
		if err != nil {
			loggedArgs := arg
			if len(loggedArgs) > 5 {
				loggedArgs = loggedArgs[:5]
			}
			slog.Error("unable to execute named query batch", "error", err, "batch_index", i, "query", query, "args_sample", loggedArgs)
			return nil, err
		}
		rowsAffected, err := res.RowsAffected()
		if err == nil {
			totalRowsAffected += rowsAffected
		}
	}

	return &aggregatedResult{rowsAffected: totalRowsAffected}, nil
}

// bind variables
func (d *DatabaseManager) Query(query string, args ...any) (*sqlx.Rows, error) {
	q, a, err := prepareQueryForIn(d.db.DriverName(), query, args)
	if err != nil {
		return nil, err
	}
	q, a, err = sqlx.In(q, a...)
	if err != nil {
		return nil, err
	}
	q = d.db.Rebind(q)
	return d.db.Queryx(q, a...)
}

// bind variables
func (d *DatabaseManager) QueryRow(query string, args ...any) (*sqlx.Row, error) {
	q, a, err := prepareQueryForIn(d.db.DriverName(), query, args)
	if err != nil {
		return nil, err
	}
	q, a, err = sqlx.In(q, a...)
	if err != nil {
		return nil, err
	}
	q = d.db.Rebind(q)
	row := d.db.QueryRowx(q, a...)
	return row, row.Err()
}

func (d *DatabaseManager) Exec(query string, args ...any) (sql.Result, error) {
	q, a, err := prepareQueryForIn(d.db.DriverName(), query, args)
	if err != nil {
		return nil, err
	}
	q, a, err = sqlx.In(q, a...)
	if err != nil {
		return nil, err
	}
	q = d.db.Rebind(q)
	return d.db.Exec(q, a...)
}

func (d *DatabaseManager) NamedExec(query string, args []map[string]any, options ...InsertQueryOption) (sql.Result, error) {
	res, err := d.DoInTransaction(func(tx DatabaseManagerTx) (any, error) {
		return tx.NamedExec(query, args, options...)
	})

	if err != nil {
		return nil, err
	}

	if res == nil {
		return nil, nil
	}

	return res.(sql.Result), nil
}

func (d *DatabaseManager) DoInTransaction(callback func(DatabaseManagerTx) (any, error)) (any, error) {
	tx, err := d.db.Beginx()
	if err != nil {
		slog.Error("error starting transaction", "error", err)
		return nil, err
	}

	dbTx := databaseManagerTx{
		tx: tx,
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	callbackResult, err := callback(&dbTx)
	if err != nil {
		slog.Error("error in transaction callback, rolling back", "error", err)
		if rbErr := tx.Rollback(); rbErr != nil {
			slog.Error("error during rollback after callback error", "rollback_error", rbErr)
			// Return the original callback error, as it's the root cause.
			return callbackResult, fmt.Errorf("callback error: %w; subsequent rollback error: %v", err, rbErr)
		}
		return callbackResult, err
	}

	err = tx.Commit()
	if err != nil {
		slog.Error("error committing transaction", "error", err)
		// Do not rollback here. A failed commit leaves the transaction in an invalid state.
		return callbackResult, err
	}

	return callbackResult, nil
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

func newPostgresDatabaseManager() (*DatabaseManager, error) {
	db, err := sqlx.Open("postgres", config.Config.CloudCollectorServerDBUrl)
	if err != nil {
		slog.Error("error connecting to postgres", "error", err)
		return nil, err
	}
	db.SetMaxOpenConns(config.Config.CloudCollectorServerDBMaxConnection)
	db.SetMaxIdleConns(config.Config.CloudCollectorServerDBMinConnection)
	db.SetConnMaxIdleTime(time.Duration(config.Config.CloudCollectorServerDBIdleMinutes * int(time.Minute)))

	if err := db.Ping(); err != nil {
		slog.Error("error pinging postgres", "error", err)
		return nil, err
	}
	return &DatabaseManager{
		db: db,
	}, nil
}

func newClickhouseDatabaseManager() (*DatabaseManager, error) {
	host := config.Config.ClickhouseHost
	protocol := "clickhouse"
	port := "9000"
	if strings.Contains(host, "://") {
		hostWithProtocol := strings.Split(host, "://")
		if len(hostWithProtocol) != 2 {
			return nil, errors.New("invalid clickhouse host")
		}
		host = hostWithProtocol[1]
		//protocol = hostWithProtocol[0]
	}

	if strings.Contains(host, ":") {
		hostWithPort := strings.Split(host, ":")
		if len(hostWithPort) != 2 {
			return nil, errors.New("invalid clickhouse host")
		}
		host = hostWithPort[0]
		//port = hostWithPort[1]
	}

	db, err := sqlx.Open("clickhouse", fmt.Sprintf("%s://%s:%s@%s:%s/%s", protocol, config.Config.ClickhouseUser, config.Config.ClickhousePassword, host, port, config.Config.ClickhouseDatabase))
	if err != nil {
		slog.Error("error connecting to clickhouse", "error", err)
		return nil, err
	}

	if err := db.Ping(); err != nil {
		slog.Error("error pinging clickhouse", "error", err)
		return nil, err
	}
	return &DatabaseManager{
		db: db,
	}, nil
}

var databaseManager map[DatabaseManagerType]*DatabaseManager = make(map[DatabaseManagerType]*DatabaseManager)

type DatabaseManagerType string

const (
	Metastore DatabaseManagerType = "metastore"
	Warehouse DatabaseManagerType = "warehouse"
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

	switch name {
	case Warehouse:
		manager, err := newClickhouseDatabaseManager()
		if err != nil {
			return nil, err
		}
		databaseManager[name] = manager
		return manager, nil
	case Metastore:
		manager, err := newPostgresDatabaseManager()
		if err != nil {
			return nil, err
		}
		databaseManager[name] = manager
		return manager, nil

	default:
		return nil, fmt.Errorf("database manager not found - %v", name)
	}
}

var databaseManagerHooks map[DatabaseManagerType]func() (*DatabaseManager, error) = make(map[DatabaseManagerType]func() (*DatabaseManager, error))

func RegisterDatabaseManagerHook(name DatabaseManagerType, callback func() (*DatabaseManager, error)) {
	databaseManagerHooks[name] = callback
}

func Close() {
	for _, db := range databaseManager {
		err := db.db.Close()
		if err != nil {
			slog.Error("error closing database", "error", err)
		}
	}
}

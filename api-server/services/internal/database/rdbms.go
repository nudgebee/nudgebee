package database

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

	"nudgebee/services/config"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	_ "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
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
	q, a, err := prepareQueryForIn(d.Db.DriverName(), query, args)
	if err != nil {
		return nil, err
	}
	q, a, err = sqlx.In(q, a...)
	if err != nil {
		return nil, err
	}
	q = d.Db.Rebind(q)
	return d.Db.Queryx(q, a...)
}

// bind variables
func (d *DatabaseManager) QueryRow(query string, args ...any) (*sqlx.Row, error) {
	q, a, err := prepareQueryForIn(d.Db.DriverName(), query, args)
	if err != nil {
		return nil, err
	}
	q, a, err = sqlx.In(q, a...)
	if err != nil {
		return nil, err
	}
	q = d.Db.Rebind(q)
	row := d.Db.QueryRowx(q, a...)
	return row, row.Err()
}

func (d *DatabaseManager) Exec(query string, args ...any) (sql.Result, error) {
	q, a, err := prepareQueryForIn(d.Db.DriverName(), query, args)
	if err != nil {
		return nil, err
	}
	q, a, err = sqlx.In(q, a...)
	if err != nil {
		return nil, err
	}
	q = d.Db.Rebind(q)
	return d.Db.Exec(q, a...)
}

// move query filters here
func (d *DatabaseManager) Select(table string, filters map[string]any, cols []string) (sql.Result, error) {
	return nil, errors.ErrUnsupported
}

// validSQLIdentifier matches safe SQL identifiers: letters, digits, underscores.
var validSQLIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// QuoteIdentifier validates and quotes a SQL identifier (table or column name)
// using the quoting style appropriate for the underlying driver.
func (d *DatabaseManager) QuoteIdentifier(name string) (string, error) {
	if !validSQLIdentifier.MatchString(name) {
		return "", fmt.Errorf("invalid SQL identifier: %q", name)
	}
	switch d.Db.DriverName() {
	case "clickhouse", "bigquery":
		return "`" + name + "`", nil
	default: // postgres and others use ANSI double-quotes
		return `"` + name + `"`, nil
	}
}

// quoteIdentifiers validates and quotes a slice of SQL identifiers.
func (d *DatabaseManager) quoteIdentifiers(names []string) ([]string, error) {
	quoted := make([]string, len(names))
	for i, name := range names {
		q, err := d.QuoteIdentifier(name)
		if err != nil {
			return nil, err
		}
		quoted[i] = q
	}
	return quoted, nil
}

// move query filters here
// Insert performs a bulk INSERT with optional ON CONFLICT DO UPDATE handling.
// For columns where the default "col = excluded.col" is the wrong write
// semantic (typically jsonb fields that need merging instead of overwriting),
// use InsertWithUpsertExpressions and pass an override expression per column.
func (d *DatabaseManager) Insert(tx *sqlx.Tx, table string, onConflict []string, onConflictUpdate []string, returing []string, cols []string, values ...[]any) ([]map[string]any, error) {
	return d.InsertWithUpsertExpressions(tx, table, onConflict, onConflictUpdate, nil, returing, cols, values...)
}

// InsertWithUpsertExpressions extends Insert with a `conflictUpdateExpr` map
// that lets the caller replace the default `col = excluded.col` UPDATE clause
// with a custom expression for specific columns. Keys MUST also appear in
// `onConflictUpdate`; missing keys fall through to the default semantic.
//
// Use this when a column's row-level write rule isn't "replace with the
// incoming value" — e.g. a jsonb array that should be UNIONED with the
// existing column, or an updated_at that should be GREATEST(existing, new).
// Passing a nil/empty map preserves Insert's existing behavior.
func (d *DatabaseManager) InsertWithUpsertExpressions(
	tx *sqlx.Tx, table string,
	onConflict []string, onConflictUpdate []string,
	conflictUpdateExpr map[string]string,
	returing []string, cols []string,
	values ...[]any,
) ([]map[string]any, error) {
	if len(cols) == 0 || len(values) == 0 {
		return []map[string]any{}, errors.New("columns or values are empty")
	}
	if len(values[0]) != len(cols) {
		return []map[string]any{}, fmt.Errorf("number of columns (%d) does not match number of values (%d)", len(cols), len(values[0]))
	}

	quotedTable, err := d.QuoteIdentifier(table)
	if err != nil {
		return nil, err
	}
	quotedCols, err := d.quoteIdentifiers(cols)
	if err != nil {
		return nil, err
	}

	valuePlaceholders := make([]string, len(values))
	for i := range values {
		rowPlaceholders := make([]string, len(cols))
		for j := range cols {
			rowPlaceholders[j] = "?"
		}
		valuePlaceholders[i] = "(" + strings.Join(rowPlaceholders, ",") + ")"
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES %s", quotedTable, strings.Join(quotedCols, ","), strings.Join(valuePlaceholders, ","))
	args := make([]any, 0)

	for _, row := range values {
		args = append(args, row...)
	}
	if len(onConflict) > 0 {
		quotedConflict, qErr := d.quoteIdentifiers(onConflict)
		if qErr != nil {
			return nil, qErr
		}
		query += " ON CONFLICT (" + strings.Join(quotedConflict, ", ") + ")"
		if len(onConflictUpdate) > 0 {
			setClauses := make([]string, len(onConflictUpdate))
			for i, col := range onConflictUpdate {
				qCol, qErr := d.QuoteIdentifier(col)
				if qErr != nil {
					return nil, qErr
				}
				if expr, ok := conflictUpdateExpr[col]; ok && strings.TrimSpace(expr) != "" {
					// Caller-provided expression. Trust the caller to write
					// safe SQL — this path is for jsonb merges, GREATEST,
					// COALESCE, etc., not for user input.
					setClauses[i] = fmt.Sprintf("%s = %s", qCol, expr)
				} else {
					setClauses[i] = fmt.Sprintf("%s = excluded.%s", qCol, qCol)
				}
			}
			query += " DO UPDATE SET " + strings.Join(setClauses, ",")
		} else {
			query += " DO NOTHING"
		}
	}

	if len(returing) > 0 {
		quotedRet, qErr := d.quoteIdentifiers(returing)
		if qErr != nil {
			return nil, qErr
		}
		query += " RETURNING " + strings.Join(quotedRet, ",")
		query = d.Db.Rebind(query)
		var rows *sqlx.Rows
		var err error
		if tx != nil {
			rows, err = tx.Queryx(query, args...)
		} else {
			rows, err = d.Db.Queryx(query, args...)
		}

		if err != nil {
			slog.Error("db: error inserting record", "query", query, "args", args, "error", err)
			return []map[string]any{}, err
		}
		defer func() { _ = rows.Close() }()
		var result []map[string]any
		for rows.Next() {
			row := make(map[string]any)
			err := rows.MapScan(row)
			if err != nil {
				slog.Error("db: error scanning row", "error", err)
				return []map[string]any{}, err
			}
			result = append(result, row)
		}
		if err := rows.Err(); err != nil {
			return []map[string]any{}, err
		}
		return result, nil
	}

	slog.Info("db: executing insert query", "table", table, "rows", len(values), "args_count", len(args))
	query = d.Db.Rebind(query)
	var result sql.Result

	// Use provided transaction or the main DB connection
	if tx != nil {
		result, err = tx.Exec(query, args...)
	} else {
		result, err = d.Db.Exec(query, args...)
	}

	if err != nil {
		slog.Error("db: error inserting record", "table", table, "rows", len(values), "args_count", len(args), "error", err)
		return []map[string]any{}, err
	}

	if result == nil {
		return []map[string]any{}, errors.New("result is nil")
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return []map[string]any{}, err
	}

	// For non-RETURNING queries, return a map with the number of rows affected.
	return []map[string]any{{"rows_affected": rowsAffected}}, nil
}

// move query filters here
func (d *DatabaseManager) Update(table string, filters map[string]any, cols []string, values []any) (int64, error) {
	if len(cols) != len(values) {
		return 0, fmt.Errorf("number of columns (%d) does not match number of values (%d)", len(cols), len(values))
	}

	quotedTable, err := d.QuoteIdentifier(table)
	if err != nil {
		return 0, err
	}

	var setClauses []string
	var setArgs []any
	for i, col := range cols {
		qCol, qErr := d.QuoteIdentifier(col)
		if qErr != nil {
			return 0, qErr
		}
		setClauses = append(setClauses, qCol+" = ?")
		setArgs = append(setArgs, values[i])
	}

	var whereClause []string
	var whereArgs []any
	for key, val := range filters {
		qKey, qErr := d.QuoteIdentifier(key)
		if qErr != nil {
			return 0, qErr
		}
		whereClause = append(whereClause, qKey+" = ?")
		whereArgs = append(whereArgs, val)
	}

	args := append(setArgs, whereArgs...)

	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s", quotedTable, strings.Join(setClauses, ", "), strings.Join(whereClause, " AND "))
	query = d.Db.Rebind(query)

	slog.Info("db: executing update query", "query", query, "args", args)
	result, err := d.Db.Exec(query, args...)
	if err != nil {
		slog.Error("db: error updating record", "query", query, "args", args, "error", err)
		return 0, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return rowsAffected, nil
}

// move query filters here
func (d *DatabaseManager) Delete(table string, filters map[string]any) (int64, error) {
	quotedTable, err := d.QuoteIdentifier(table)
	if err != nil {
		return 0, err
	}

	whereClause := []string{"1 = 1"}
	var args []any
	for key, val := range filters {
		qKey, qErr := d.QuoteIdentifier(key)
		if qErr != nil {
			return 0, qErr
		}
		whereClause = append(whereClause, qKey+" = ?")
		args = append(args, val)
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE %s", quotedTable, strings.Join(whereClause, " AND "))
	query = d.Db.Rebind(query)

	slog.Info("db: executing delete query", "query", query, "args", args)
	result, err := d.Db.Exec(query, args...)
	if err != nil {
		slog.Error("db: error deleting record", "query", query, "args", args, "error", err)
		return 0, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return rowsAffected, nil
}

func (d *DatabaseManager) BeginTx() (*sqlx.Tx, error) {
	return d.Db.Beginx()
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
		err2 := tx.Rollback()
		if err2 != nil {
			slog.Error("db: unable to rollback transaction failure", "error", err2)
			return callbackResult, errors.Join(err, err2)
		}
		return callbackResult, err
	}

	return callbackResult, nil
}

func (d *DatabaseManager) BuildInClause(values ...any) string {
	valueStr := lo.Map(values, func(value any, i int) string {
		switch v := value.(type) {
		case int:
			return fmt.Sprintf("%d", v)
		case int64:
			return fmt.Sprintf("%d", v)
		case float64:
			return fmt.Sprintf("%f", v)
		case float32:
			return fmt.Sprintf("%f", v)
		case string:
			strVal := v
			if d.Db != nil {
				driver := d.Db.DriverName()
				if driver == "clickhouse" || driver == "bigquery" {
					strVal = strings.ReplaceAll(strVal, "\\", "\\\\")
				}
			}
			// Prevent SQL Injection by escaping single quotes.
			// Standard SQL uses doubling of single quotes.
			// This works for Postgres, BigQuery, and ClickHouse (which also supports standard SQL escaping).
			return fmt.Sprintf("'%s'", strings.ReplaceAll(strVal, "'", "''"))
		case uuid.UUID:
			return fmt.Sprintf("'%s'", v.String())
		case bool:
			if v {
				return "true"
			} else {
				return "false"
			}
		case time.Time:
			return fmt.Sprintf("'%s'", v.Format("2006-01-02 15:04:05"))
		case sql.NullTime:
			if v.Valid {
				return fmt.Sprintf("'%s'", v.Time.Format("2006-01-02 15:04:05"))
			}
			return "NULL"
		case sql.NullString:
			if v.Valid {
				return fmt.Sprintf("'%s'", v.String)
			}
			return "NULL"
		case sql.NullBool:
			if v.Valid {
				if v.Bool {
					return "true"
				} else {
					return "false"
				}
			}
			return "NULL"
		case sql.NullFloat64:
			if v.Valid {
				return fmt.Sprintf("%f", v.Float64)
			}
			return "NULL"
		case sql.NullInt16:
			if v.Valid {
				return fmt.Sprintf("%d", v.Int16)
			}
			return "NULL"
		case sql.NullInt32:
			if v.Valid {
				return fmt.Sprintf("%d", v.Int32)
			}
			return "NULL"
		case sql.NullInt64:
			if v.Valid {
				return fmt.Sprintf("%d", v.Int64)
			}
			return "NULL"
		default:
			return fmt.Sprintf("'%v'", v)
		}
	})
	return strings.Join(valueStr, ",")
}

func newAgentWarehouseDatabaseManager() (*DatabaseManager, error) {
	agentWarehouse := newAgentWarehouseDriver()
	connector, err := agentWarehouse.OpenConnector("agent_warehouse")
	if err != nil {
		slog.Error("error opening connector for agent warehouse", "error", err)
		return nil, err
	}
	databaseManager := DatabaseManager{
		Db: sqlx.NewDb(sql.OpenDB(connector), "clickhouse"),
	}
	return &databaseManager, nil
}

func newAgentWarehouseBigQueryDatabaseManager() (*DatabaseManager, error) {
	agentWarehouse := newAgentWarehouseDriver()
	connector, err := agentWarehouse.OpenConnector("agent_warehouse_bigquery")
	if err != nil {
		slog.Error("error opening connector for agent warehouse", "error", err)
		return nil, err
	}
	databaseManager := DatabaseManager{
		Db: sqlx.NewDb(sql.OpenDB(connector), "bigquery"),
	}
	return &databaseManager, nil
}

func newAgentWarehouseChronosphereDatabaseManager() (*DatabaseManager, error) {
	agentWarehouse := newAgentWarehouseDriver()
	connector, err := agentWarehouse.OpenConnector("agent_warehouse_chronosphere")
	if err != nil {
		slog.Error("error opening connector for agent warehouse", "error", err)
		return nil, err
	}
	databaseManager := DatabaseManager{
		Db: sqlx.NewDb(sql.OpenDB(connector), "chronosphere"),
	}
	return &databaseManager, nil
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
		Db: db,
	}, nil
}

func newPostgresDatabaseManager() (*DatabaseManager, error) {
	db, err := sqlx.Open("postgres", config.Config.ServiceDBUrl)
	if err != nil {
		slog.Error("error connecting to postgres", "error", err)
		return nil, err
	}
	db.SetMaxOpenConns(config.Config.ServiceDBMaxConnection)
	db.SetMaxIdleConns(config.Config.ServiceDBMinConnection)
	db.SetConnMaxIdleTime(time.Duration(config.Config.ServiceDBIdleMinutes * int(time.Minute)))
	db.SetConnMaxLifetime(time.Duration(config.Config.ServiceDBConnMaxLifetimeMinutes) * time.Minute)

	if err := db.Ping(); err != nil {
		slog.Error("error pinging postgres", "error", err)
		return nil, err
	}
	return &DatabaseManager{
		Db: db,
	}, nil
}

var databaseManager map[DatabaseManagerType]*DatabaseManager = make(map[DatabaseManagerType]*DatabaseManager)

type DatabaseManagerType string

const (
	Warehouse                  DatabaseManagerType = "warehouse"
	Metastore                  DatabaseManagerType = "metastore"
	AgentWarehouse             DatabaseManagerType = "agent_warehouse"
	AgentWarehouseBigQuery     DatabaseManagerType = "agent_warehouse_bigquery"
	AgentMetrices              DatabaseManagerType = "cloud_collector_metrices"
	AgentWarehouseChronosphere DatabaseManagerType = "agent_warehouse_chronosphere"
	AzureMonitoring            DatabaseManagerType = "agent_warehouse_azure_monitoring"
)

// GetDatabaseManagerIfInitialized returns the manager only if it was already
// created by a prior GetDatabaseManager call. It never opens a new connection.
func GetDatabaseManagerIfInitialized(name DatabaseManagerType) (*DatabaseManager, bool) {
	manager, ok := databaseManager[name]
	return manager, ok
}

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

	case AgentWarehouse:
		manager, err := newAgentWarehouseDatabaseManager()
		if err != nil {
			return nil, err
		}
		databaseManager[name] = manager
		return manager, nil

	case AgentWarehouseBigQuery:
		manager, err := newAgentWarehouseBigQueryDatabaseManager()
		if err != nil {
			return nil, err
		}
		databaseManager[name] = manager
		return manager, nil

	case AgentWarehouseChronosphere:
		manager, err := newAgentWarehouseChronosphereDatabaseManager()
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
		err := db.Db.Close()
		if err != nil {
			slog.Error("error closing database", "error", err)
		}
	}
}

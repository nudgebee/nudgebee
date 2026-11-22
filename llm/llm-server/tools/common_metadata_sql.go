package tools

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// numericColumnNames lists the NUMERIC columns of a result set, computed once
// per query so the per-row decode does no type lookups. common.Metastore is
// PostgreSQL through lib/pq (common/rdbms.go opens it with the "postgres"
// driver), which reports the type as "NUMERIC"; the comparison is
// case-insensitive so a driver or mock that reports "numeric" still matches.
func numericColumnNames(cols []*sql.ColumnType) []string {
	var names []string
	for _, c := range cols {
		if strings.EqualFold(c.DatabaseTypeName(), "NUMERIC") {
			names = append(names, c.Name())
		}
	}
	return names
}

// keepNumericText replaces the raw []byte lib/pq returns for NUMERIC cells (Go
// has no decimal type) with their decimal text, so JSON does not base64 them
// ("MA==" for "0"). Only the listed columns are touched; bytea stays bytes.
func keepNumericText(row map[string]any, numericCols []string) {
	for _, name := range numericCols {
		if b, ok := row[name].([]byte); ok {
			row[name] = string(b)
		}
	}
}

func sqlToolCall(nbRequestContext core.NbToolContext, query string, tableName string, eventsView string, limit int, processRow func(r map[string]any, i int, c int) map[string]any) (core.NBToolResponse, []map[string]any, error) {
	if query == "" {
		return core.NBToolResponse{}, nil, errors.New("query is empty")
	}

	query = strings.TrimSpace(query)
	query = strings.TrimSuffix(query, ";")

	data, rowCap, err := sqlExecuteQuery(nbRequestContext.Ctx, query, nbRequestContext.AccountId, tableName, eventsView, limit)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("eventssql: unable to execute query", "error", err, "query", query)
		return core.NBToolResponse{}, nil, err
	}

	if processRow != nil {
		c := len(data)
		for i, r := range data {
			data[i] = processRow(r, i, c)
		}
	}

	if len(data) == 0 {
		return core.NBToolResponse{
			Data:   `{"message":"No results found matching the query criteria. Try broadening the filters, adjusting the time range, or checking if the resource/entity name is correct.","rows":[]}`,
			Type:   core.NBToolResponseTypeJson,
			Status: core.NBToolResponseStatusSuccess,
		}, data, nil
	}

	resp := core.NBToolResponse{
		Type:   core.NBToolResponseTypeTable,
		Status: core.NBToolResponseStatusSuccess,
	}
	if rowCap > 0 {
		resp.AdditionalDetails = map[string]any{sqlRowCapKey: rowCap}
	}
	resp.Data, err = sqlResultData(resp, data)
	if err != nil {
		return core.NBToolResponse{}, nil, err
	}
	return resp, data, nil
}

// sqlRowCapKey marks, in AdditionalDetails, a result the row cap or the size
// budget cut, so a tool that rewrites Data after sqlToolCall keeps the note.
const sqlRowCapKey = "sql_row_cap"

// sqlResultData renders rows as Data; a cut result gets has_more & a note on
// getting the rest, so the model doesn't read it as complete (#40194).
func sqlResultData(resp core.NBToolResponse, rows []map[string]any) (string, error) {
	var out any = rows
	if returned, ok := resp.AdditionalDetails[sqlRowCapKey].(int); ok {
		out = map[string]any{
			"rows":     rows,
			"returned": returned,
			"has_more": true,
			"note": fmt.Sprintf("cut: more rows match than the %d shown (this tool returns at most %d rows or %d MB per call). "+
				"Best: answer with COUNT(*) or GROUP BY if you can. "+
				"To list more: run the same query with an ORDER BY that ends in a unique column (e.g. starts_at DESC, id), "+
				"fixed start and end times instead of NOW(), LIMIT up to %d, and OFFSET = your current OFFSET (0 if none) + %d. "+
				"Do not start again from the beginning; these rows are already shown. "+
				"Select only the columns you need (large JSON columns such as evidences slow every page). "+
				"Do not report these rows as all of them.",
				returned, sqlToolMaxRows, sqlToolMaxBytes>>20, sqlToolMaxRows, returned),
		}
	}
	b, err := common.MarshalJson(out)
	return string(b), err
}

func stripSingleLineComments(query string) string {
	lines := strings.Split(query, "\n")
	var resultLines []string
	for _, line := range lines {
		if commentIdx := strings.Index(line, "--"); commentIdx != -1 {
			// Add the part of the line before the comment, if any
			resultLines = append(resultLines, line[:commentIdx])
		} else {
			resultLines = append(resultLines, line)
		}
	}
	// Join the lines back, then trim any trailing/leading whitespace that might result
	return strings.TrimSpace(strings.Join(resultLines, "\n"))
}

// The metastore SQL tools give the model one table (a view swapped in for the
// account). Where the scoped role is unavailable (sqlScopeToAccount), its query
// runs as a database user that can read the whole app database. sqlCheckSingleTable keeps the query on that one table without
// parsing SQL: it counts keywords in the text, after blanking out '...'
// literals. Comments, double quotes and dollar quotes are refused outright,
// so a '...' literal is the only way to quote text and blanking it is exact.
// Only allowlisted functions may be called, since a function can read a table
// the query never names. Anything else unusual is refused rather than
// misread. It is a stopgap until these tools stop taking model-written SQL
// (#39979, #39880).
var (
	sqlSelectWord     = regexp.MustCompile(`\bselect\b`)
	sqlFromWord       = regexp.MustCompile(`\bfrom\b`)
	sqlMultiTableWord = regexp.MustCompile(`\b(join|union|intersect|except|with|table|lateral)\b`)
	sqlQualifiedCall  = regexp.MustCompile(`\b\w+\s*\.\s*\w+\s*\(`)
	sqlCallName       = regexp.MustCompile(`\b([a-z_][a-z0-9_]*)\s*\(`)
	sqlFromClauseEnd  = regexp.MustCompile(`\b(where|group|having|window|order|limit|offset|fetch|for)\b|;`)
	sqlStringLiteral  = regexp.MustCompile(`'(?:[^']|'')*'`)
	sqlHexLiteral     = regexp.MustCompile(`\b0x[0-9a-f]`)
	// "1union" is one word to \b but "1 union" to Postgres 14 and older.
	sqlDigitThenLetter = regexp.MustCompile(`([0-9])([a-z_])`)
)

// sqlAllowedCalls are the names a query may put before "(": keywords, type
// names with a size, and functions that compute on values and read no table.
// Seeded from 60 days of real dev queries (45 names) plus close relatives.
var sqlAllowedCalls = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range strings.Fields(`
		and or not in any all some exists where select from on by as filter over within distinct
		group having limit offset
		case when then else is like ilike between values array row cast
		numeric decimal varchar char character timestamp timestamptz time interval float
		count sum avg min max string_agg array_agg jsonb_agg json_agg bool_and bool_or every
		percentile_cont percentile_disc row_number rank dense_rank lag lead first_value last_value
		round abs ceil ceiling floor trunc sign mod power sqrt ln log exp greatest least coalesce nullif
		now age date date_part date_trunc
		to_char to_timestamp to_date to_number make_interval
		length char_length octet_length upper lower left right substr substring trim btrim ltrim rtrim
		replace split_part strpos position concat concat_ws regexp_replace regexp_match regexp_matches md5
		array_length cardinality unnest
		jsonb_array_elements jsonb_array_elements_text jsonb_object_keys jsonb_typeof jsonb_array_length
		jsonb_extract_path_text jsonb_path_query jsonb_path_query_first jsonb_path_exists jsonb_pretty
		jsonb_build_object to_jsonb json_array_elements json_array_elements_text json_object_keys
		json_typeof json_array_length json_extract_path_text json_build_object to_json row_to_json
		pg_column_size`) {
		m[n] = true
	}
	return m
}()

func sqlCheckSingleTable(query string, allowedTable string) error {
	s := strings.ToLower(query)
	if strings.Contains(s, sqlTablePlaceholder) {
		return errors.New("sql: query not allowed")
	}
	// Blank out '...' literals so nothing inside one ('%table%', 'from',
	// '{"a":1}') counts. Everything below reads only what is left: a comment
	// marker, double quote or $ that opens before any literal is still
	// visible, so a literal can never hide one from Postgres.
	s = sqlStringLiteral.ReplaceAllString(s, "''")
	switch {
	case strings.Contains(s, "--") || strings.Contains(s, "/*") || strings.Contains(s, `"`):
		return errors.New("sql: comments and quoted names are not allowed")
	case strings.Contains(s, "$"):
		return errors.New("sql: $ outside a string literal is not allowed")
	case sqlHexLiteral.MatchString(s):
		return errors.New("sql: hex literals are not allowed")
	}
	s = sqlDigitThenLetter.ReplaceAllString(s, "$1 $2")
	switch {
	case len(sqlSelectWord.FindAllStringIndex(s, -1)) != 1:
		return fmt.Errorf("sql: use one SELECT on %s; subqueries are not allowed (run a second query instead)", allowedTable)
	case len(sqlFromWord.FindAllStringIndex(s, -1)) != 1:
		return fmt.Errorf("sql: use one FROM %s; instead of EXTRACT(x FROM y) use date_part('x', y), instead of SUBSTRING(s FROM n) use substr(s, n), instead of TRIM(... FROM s) use btrim(s), instead of a IS DISTINCT FROM b use coalesce(a, '') <> coalesce(b, '')", allowedTable)
	case sqlMultiTableWord.MatchString(s):
		return fmt.Errorf("sql: query only %s; JOIN, UNION, WITH, TABLE and LATERAL are not allowed (write timestamp with time zone as timestamptz)", allowedTable)
	case sqlQualifiedCall.MatchString(s):
		return errors.New("sql: schema-qualified functions are not allowed")
	}
	for _, m := range sqlCallName.FindAllStringSubmatch(s, -1) {
		if !sqlAllowedCalls[m[1]] {
			return fmt.Errorf("sql: function %s is not allowed; use common value functions (count, sum, round, coalesce, date_part, date_trunc, to_char, lower, substr, jsonb_* and similar)", m[1])
		}
	}
	rest := s[sqlFromWord.FindStringIndex(s)[1]:]
	if end := sqlFromClauseEnd.FindStringIndex(rest); end != nil {
		rest = rest[:end[0]]
	}
	if strings.ContainsAny(rest, ",(") {
		return fmt.Errorf("sql: query only %s; nothing else is allowed in FROM", allowedTable)
	}
	if fields := strings.Fields(rest); len(fields) == 0 || fields[0] != allowedTable {
		return fmt.Errorf("sql: query only %s", allowedTable)
	}
	return nil
}

func sqlValidateReadOnly(finalQuery string, allowedTable string) error {

	// Normalize the query to lowercase and trim whitespace
	lowerQueryForPrefixCheck := strings.ToLower(strings.TrimSpace(finalQuery))

	// 1. Check for DML/DDL prefixes
	if strings.HasPrefix(lowerQueryForPrefixCheck, "delete from") ||
		strings.HasPrefix(lowerQueryForPrefixCheck, "insert into") ||
		strings.HasPrefix(lowerQueryForPrefixCheck, "update ") ||
		strings.HasPrefix(lowerQueryForPrefixCheck, "create ") ||
		strings.HasPrefix(lowerQueryForPrefixCheck, "truncate ") {
		return errors.New("sql: only select is allowed")
	}

	// 2. With an allowed table (the metastore tools, which run on Postgres)
	// only a SELECT on that table is accepted.
	if allowedTable != "" {
		if !strings.HasPrefix(lowerQueryForPrefixCheck, "select") {
			return fmt.Errorf("sql: only a SELECT on %s is allowed", allowedTable)
		}
		return sqlCheckSingleTable(finalQuery, strings.ToLower(allowedTable))
	}
	return nil
}

// sqlExecuteQuery runs the tool query. The int is the number of rows returned
// when the row cap or the size budget cut the result, else 0.
func sqlExecuteQuery(ctx *security.RequestContext, query string, accountId string, tableName string, tableView string, limit int) ([]map[string]any, int, error) {
	finalQuery := sqlCleanUpQuery(query)
	err := sqlValidateReadOnly(finalQuery, tableName)
	if err != nil {
		return nil, 0, err
	}
	finalQuery, tableSource, err := sqlBindTable(finalQuery, accountId, tableName, tableView)
	if err != nil {
		return nil, 0, fmt.Errorf("sqlExecuteQuery: %w", err)
	}

	if limit == 0 {
		limit = config.Config.LlmServerAgentMaxSqlRows
	}

	// The row cap is decided on the caller's SQL, before the view goes in: a
	// LIMIT inside the view's own subqueries is not the caller's LIMIT.
	finalQuery, rowCap := buildLimitedQuery(finalQuery, limit)
	finalQuery = strings.Replace(finalQuery, sqlTablePlaceholder, tableSource, 1)
	slog.Info("sql: final tool query", "account", accountId, "query", finalQuery)

	t0 := time.Now()
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, 0, fmt.Errorf("sqlExecuteQuery: get database manager: %w", err)
	}

	timeout := time.Duration(config.Config.LlmServerAgentSqlTimeoutSec) * time.Second
	if timeout <= 0 {
		// statement_timeout = 0 would mean no limit at all.
		timeout = defaultSqlToolQueryTimeout
	}
	// The server-side statement_timeout is what stops the query; the client
	// deadline, a little later, only covers a connection that stops answering.
	queryCtx, cancel := context.WithTimeout(ctx.GetContext(), timeout+sqlToolQueryClientGrace)
	defer cancel()

	// READ ONLY also rejects DML hidden in a CTE, which the prefix check in
	// sqlValidateReadOnly does not see. It holds only while the query is a
	// single statement; see the Prepare below.
	tx, err := dbManager.Db.BeginTxx(queryCtx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, fmt.Errorf("sqlExecuteQuery: begin read-only transaction: %w", err)
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
			ctx.GetLogger().Warn("sql: rollback of tool query failed", "error", rerr)
		}
	}()
	if _, err := tx.ExecContext(queryCtx, fmt.Sprintf("SET LOCAL statement_timeout = %d", timeout.Milliseconds())); err != nil {
		return nil, 0, fmt.Errorf("sqlExecuteQuery: set statement_timeout: %w", err)
	}
	// Enough memory for the view's ranking subqueries to stay hashed, so they
	// are built once per query rather than re-run for every row.
	if workMemMB := config.Config.LlmServerAgentSqlWorkMemMB; workMemMB > 0 {
		if _, err := tx.ExecContext(queryCtx, fmt.Sprintf("SET LOCAL work_mem = '%dMB'", workMemMB)); err != nil {
			return nil, 0, fmt.Errorf("sqlExecuteQuery: set work_mem: %w", err)
		}
	}
	scoped, err := sqlScopeToAccount(queryCtx, tx, accountId)
	if err != nil {
		return nil, 0, fmt.Errorf("sqlExecuteQuery: switch to %s: %w", sqlToolRole, err)
	}
	if !scoped && config.Config.LlmServerAgentSqlScopedRole {
		ctx.GetLogger().Error("sql: refused tool query, scoped role unavailable", "table", tableName, "role", sqlToolRole, "schema", sqlToolSchema)
		return nil, 0, errSqlScopedRoleUnavailable
	}

	// Prepared, not sent as a plain query: Postgres refuses to prepare more
	// than one statement, so "select ...; commit; delete ..." cannot end the
	// read-only transaction and run the rest in autocommit.
	stmt, err := tx.PreparexContext(queryCtx, finalQuery)
	if err != nil {
		return nil, 0, toolQueryError(ctx, tableName, timeout, err)
	}
	// A cut cancels the query (scan loop) -> Close errors are expected; don't warn.
	cut := false
	defer func() {
		if err := stmt.Close(); err != nil && !cut {
			ctx.GetLogger().Warn("sql: closing tool query statement failed", "error", err)
		}
	}()
	rows, err := stmt.QueryxContext(queryCtx)
	if err != nil {
		return nil, 0, toolQueryError(ctx, tableName, timeout, err)
	}
	defer func() {
		if err := rows.Close(); err != nil && !cut {
			ctx.GetLogger().Warn("sql: closing tool query rows failed", "error", err)
		}
	}()

	colTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, 0, fmt.Errorf("sqlExecuteQuery: read column types: %w", err)
	}
	numericCols := numericColumnNames(colTypes)
	data := []map[string]any{}
	size := 0
	for rows.Next() {
		row := map[string]any{}
		if err := rows.MapScan(row); err != nil {
			return nil, 0, fmt.Errorf("sqlExecuteQuery: scan row: %w", err)
		}
		// Row cap+1 reached -> more match. Past the size budget -> stop; the first row always fits.
		// ponytail: a row is read in full before it's measured -> a call holds up to budget + 1 row.
		size += sqlRowSize(row)
		if len(data) == rowCap || (len(data) > 0 && size > sqlToolMaxBytes) {
			// Cancel rather than let Close read the rest of the rows off the wire.
			cut = true
			cancel()
			break
		}
		keepNumericText(row, numericCols)
		data = append(data, row)
	}
	// After a cut, Err can report the cancel; the rows already read are whole.
	if err := rows.Err(); err != nil && !cut {
		return nil, 0, toolQueryError(ctx, tableName, timeout, err)
	}
	ctx.GetLogger().Info("sql: executed tool query", "account", accountId, "query", finalQuery, "time", time.Since(t0).String(), "rows", len(data), "bytes", size, "cut", cut, "scoped", scoped)
	if cut {
		return data, len(data), nil
	}
	return data, 0, nil
}

// sqlToolRole and sqlToolSchema come from migration V1112. The role can read
// only the views in the schema; each view has the name of the table it reads
// and returns only the rows of the account in app.account_id.
const (
	sqlToolRole   = "nb_llm_sql_tools"
	sqlToolSchema = "nb_llm_tools"
)

// sqlScopeQuery switches to sqlToolRole, with search_path = sqlToolSchema and
// app.account_id = $1, where the role and schema exist and this user may switch
// to the role. It returns no row, and changes nothing, otherwise. All three
// settings are LOCAL and end with the transaction.
const sqlScopeQuery = `SELECT set_config('app.account_id', $1, true), set_config('search_path', '` + sqlToolSchema + `', true),
	set_config('role', r.rolname, true)
FROM pg_roles r
WHERE r.rolname = '` + sqlToolRole + `'
	AND EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = '` + sqlToolSchema + `')
	AND CASE WHEN current_setting('server_version_num')::int >= 160000
		THEN pg_has_role(current_user, r.oid, 'SET') ELSE pg_has_role(current_user, r.oid, 'MEMBER') END`

// errSqlScopedRoleUnavailable refuses a tool query when the scoped role is on
// but V1112's role, schema or grant is missing: it never falls back to
// llm-server's own database user.
var errSqlScopedRoleUnavailable = errors.New("sql: this data is unavailable because the read-only database role " + sqlToolRole +
	" is not set up; an administrator must apply migration V1112's grants")

// sqlScopeToAccount makes the rest of tx read through the account-scoped
// views, so the tool's SQL, view text included, can see only accountId's rows
// and no table outside sqlToolSchema. It reports whether it did; false with
// the setting on means the role is unavailable, and the caller refuses.
//
// Known limit: Postgres checks SET ROLE against the login user, so SQL able to
// call set_config could switch back. sqlCheckSingleTable does not allow it; a
// separate login role would close this, at the cost of its own credentials.
func sqlScopeToAccount(ctx context.Context, tx *sqlx.Tx, accountId string) (bool, error) {
	if !config.Config.LlmServerAgentSqlScopedRole {
		return false, nil
	}
	var account, searchPath, role string
	err := tx.QueryRowxContext(ctx, sqlScopeQuery, accountId).Scan(&account, &searchPath, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// sqlToolMaxRows is the most rows one tool call returns, even under a LIMIT the
// model wrote. Dev, 90 days: p99 of rows asked for and returned is 100.
const sqlToolMaxRows = 100

// sqlToolMaxBytes bounds the row data one call holds. The model sees ≤64 KB of
// output, so more only costs memory (events_execute parses every row it holds).
const sqlToolMaxBytes = 4 << 20

// sqlRowSize approximates a scanned row's memory by its text and byte values;
// the rest (numbers, times, bools) are a few bytes each.
func sqlRowSize(row map[string]any) int {
	n := 0
	for _, v := range row {
		switch v := v.(type) {
		case string:
			n += len(v)
		case []byte:
			n += len(v)
		default:
			n += 8
		}
	}
	return n
}

// defaultSqlToolQueryTimeout applies when no positive timeout is configured.
const defaultSqlToolQueryTimeout = 120 * time.Second

// sqlToolQueryClientGrace is how long past the server-side statement_timeout
// the client waits before abandoning the connection.
const sqlToolQueryClientGrace = 5 * time.Second

// pqQueryCanceled is the SQLSTATE Postgres reports when statement_timeout
// cancels a statement.
const pqQueryCanceled = "57014"

// toolQueryError returns the error a failed tool query reports. A query that
// ran out of time gets an error the model can act on, and it stays an error:
// callers that read the rows must not mistake it for an empty result.
func toolQueryError(ctx *security.RequestContext, tableName string, timeout time.Duration, err error) error {
	if ctx.GetContext().Err() == nil {
		var pqErr *pq.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &pqErr) && pqErr.Code == pqQueryCanceled) {
			return fmt.Errorf("sql: the query on %s did not finish within %s; narrow it with a filter (status, category, a name or a time range) or aggregate instead of listing rows: %w", tableName, timeout, err)
		}
	}
	return fmt.Errorf("sqlExecuteQuery: %w", err)
}

var limitRegex = regexp.MustCompile(`(?i)\bLIMIT\b`)

// buildLimitedQuery wraps every query as a subquery limited to rowCap+1, so a cut
// is detectable. rowCap = the tool's limit, or sqlToolMaxRows if the query has its
// own LIMIT; never above sqlToolMaxRows.
func buildLimitedQuery(query string, limit int) (_ string, rowCap int) {
	// Strip single-line comments before checking so a `-- LIMIT ...` comment
	// doesn't count as the model's LIMIT. String-literal occurrences of LIMIT
	// can still false-positive; LLM-generated SQL rarely contains such literals.
	rowCap = limit
	if limitRegex.MatchString(stripSingleLineComments(query)) {
		rowCap = sqlToolMaxRows
	}
	rowCap = min(rowCap, sqlToolMaxRows)
	if rowCap <= 0 {
		rowCap = 5 // a non-positive configured cap falls back to 5 rows
	}
	query = strings.TrimSuffix(strings.TrimSpace(query), ";")
	return fmt.Sprintf("select * from (%s) as ql limit %d;", query, rowCap+1), rowCap
}

// issue with LLMs doing too much escaping
func sqlCleanUpQuery(query string) string {
	return strings.ReplaceAll(query, "\\", "")
}

// sqlTablePlaceholder marks the query's one FROM item until the view goes
// in, after the row cap is applied to the caller's SQL.
const sqlTablePlaceholder = "__nb_metastore_table__"

var (
	sqlFromWordAnyCase  = regexp.MustCompile(`(?i)\bfrom\b`)
	sqlFromItemAnyCase  = regexp.MustCompile(`(?i)^\s*(\w+)\b`)
	sqlClauseEndAnyCase = regexp.MustCompile(`(?i)\b(where|group|having|window|order|limit|offset|fetch|for)\b|;`)
)

// sqlBindTable swaps the query's one FROM item (sqlCheckSingleTable allows
// exactly one) for a placeholder, and returns what goes in its place: the view,
// filtered to the account unless the view already is (events, anomaly). Only
// that FROM item is touched, so string literals and column names that happen
// to contain the table name are left alone. The source keeps the table's name
// as its alias, so events.id and row_to_json(events) still resolve; a user
// alias (FROM events e) is kept instead.
func sqlBindTable(query, accountId, tableName, tableView string) (string, string, error) {
	// Belt-and-suspenders: the account id goes into SQL below.
	if _, err := uuid.Parse(accountId); err != nil {
		return "", "", fmt.Errorf("sql: invalid account_id format %q: %w", accountId, err)
	}
	// Search a copy with '...' literals blanked to spaces of the same length,
	// so a "from" or "where" inside a literal is not taken for the keyword
	// and offsets still line up with the query.
	masked := sqlStringLiteral.ReplaceAllStringFunc(query, func(lit string) string {
		return strings.Repeat(" ", len(lit))
	})
	from := sqlFromWordAnyCase.FindStringIndex(masked)
	if from == nil {
		return "", "", fmt.Errorf("sql: query must read FROM %s", tableName)
	}
	item := sqlFromItemAnyCase.FindStringSubmatchIndex(masked[from[1]:])
	if item == nil || !strings.EqualFold(query[from[1]+item[2]:from[1]+item[3]], tableName) {
		return "", "", fmt.Errorf("sql: query only %s", tableName)
	}
	start, end := from[1]+item[2], from[1]+item[3]

	rest := masked[end:]
	if clause := sqlClauseEndAnyCase.FindStringIndex(rest); clause != nil {
		rest = rest[:clause[0]]
	}
	hasAlias := strings.TrimSpace(rest) != ""

	source := tableName
	if tableView != "" {
		source = "(" + tableView + ")"
	}
	if tableName != "anomaly" && tableName != "events" {
		// These views are not filtered by account themselves.
		source = fmt.Sprintf("(select * from %s as tv where cloud_account_id = %s)", source, pq.QuoteLiteral(accountId))
	}
	if config.Config.LlmServerAgentSqlScopedRole {
		// OFFSET 0 stops Postgres pushing the model's WHERE below the views'
		// account filter, where a failing cast could print another account's row.
		source = "(select * from " + source + " as tool_view offset 0)"
	}
	if !hasAlias {
		source += " as " + tableName
	}
	return query[:start] + sqlTablePlaceholder + query[end:], source, nil
}

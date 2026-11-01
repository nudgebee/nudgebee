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
	"github.com/lib/pq"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// replaceFirstMatch replaces the first occurrence of a word-boundary regex match in s.
// Uses \b word boundaries for robust matching (handles semicolons, parens, end-of-string)
// but only replaces the first match to avoid duplicate aliases or broken column references.
func replaceFirstMatch(s string, tableName string, replacement string) string {
	re := regexp.MustCompile(fmt.Sprintf(`(?i)\b%s\b`, regexp.QuoteMeta(tableName)))
	loc := re.FindStringIndex(s)
	if loc == nil {
		return s
	}
	return s[:loc[0]] + replacement + s[loc[1]:]
}

// replaceAllMatches replaces every word-boundary occurrence of tableName in s,
// invoking makeReplacement for each match so callers can produce unique aliases
// (avoiding "duplicate alias" SQL errors when the same table appears more than once).
func replaceAllMatches(s string, tableName string, makeReplacement func(idx int) string) string {
	re := regexp.MustCompile(fmt.Sprintf(`(?i)\b%s\b`, regexp.QuoteMeta(tableName)))
	idx := 0
	return re.ReplaceAllStringFunc(s, func(_ string) string {
		r := makeReplacement(idx)
		idx++
		return r
	})
}

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

	data, err := sqlExecuteQuery(nbRequestContext.Ctx, query, nbRequestContext.AccountId, tableName, eventsView, limit)
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

	bytesData, err := common.MarshalJson(data)
	if err != nil {
		return core.NBToolResponse{}, nil, err
	}

	return core.NBToolResponse{
		Data:   string(bytesData),
		Type:   core.NBToolResponseTypeTable,
		Status: core.NBToolResponseStatusSuccess,
	}, data, nil
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

	// 2. If allowedTable is specified, validate it
	if allowedTable != "" {
		lowerAllowedTable := strings.ToLower(allowedTable)
		queryWithoutComments := strings.ToLower(stripSingleLineComments(strings.TrimSpace(finalQuery)))

		foundTable := false

		// Handle DESCRIBE commands
		if strings.HasPrefix(queryWithoutComments, "describe ") || strings.HasPrefix(queryWithoutComments, "desc ") {
			var describedTable string
			var baseCmd string

			if strings.HasPrefix(queryWithoutComments, "describe ") {
				baseCmd = "describe "
			} else {
				baseCmd = "desc "
			}
			describedTable = strings.TrimSpace(strings.TrimSuffix(queryWithoutComments[len(baseCmd):], ";"))
			// Handle schema.table format for describe
			if dotIndex := strings.LastIndex(describedTable, "."); dotIndex != -1 {
				describedTable = describedTable[dotIndex+1:]
			}

			if describedTable == lowerAllowedTable {
				foundTable = true
			}
		} else if strings.HasPrefix(queryWithoutComments, "select") {
			fromKeyword := " from "
			fromIndex := strings.Index(queryWithoutComments, fromKeyword)
			if fromIndex != -1 {
				// Extract string after " from " and before any subsequent clauses like WHERE, JOIN, GROUP BY, ORDER BY, LIMIT, or semicolon
				afterFromClause := queryWithoutComments[fromIndex+len(fromKeyword):]

				// Find the end of the first table name (and optional alias)
				// It could be followed by a space (then alias or next keyword), comma (for multiple FROMs, though less common), WHERE, JOIN, etc.
				endOfTablePartIndex := len(afterFromClause)
				terminators := []string{" where ", " join ", " group by ", " order by ", " limit ", ";", "(", ","}
				for _, term := range terminators {
					if idx := strings.Index(afterFromClause, term); idx != -1 && idx < endOfTablePartIndex {
						endOfTablePartIndex = idx
					}
				}
				tableAndAliasPart := strings.TrimSpace(afterFromClause[:endOfTablePartIndex])

				// The first word in tableAndAliasPart is the table name
				fields := strings.Fields(tableAndAliasPart)
				if len(fields) > 0 {
					tableCandidate := fields[0]
					// Handle schema.table format
					if dotIndex := strings.LastIndex(tableCandidate, "."); dotIndex != -1 {
						tableCandidate = tableCandidate[dotIndex+1:]
					}
					if tableCandidate == lowerAllowedTable {
						foundTable = true
					}
				}
			}
		} else if strings.HasPrefix(queryWithoutComments, "show ") {
			// Specific checks for SHOW commands
			if strings.Contains(queryWithoutComments, "show create table "+lowerAllowedTable) ||
				strings.Contains(queryWithoutComments, "show columns from "+lowerAllowedTable) ||
				strings.Contains(queryWithoutComments, "show index from "+lowerAllowedTable) {
				foundTable = true
			} else if strings.HasPrefix(queryWithoutComments, "show tables") {
				if strings.Contains(queryWithoutComments, " like ") {
					likePattern := ""
					if idxLike := strings.Index(queryWithoutComments, " like "); idxLike != -1 {
						patternStr := strings.TrimSpace(queryWithoutComments[idxLike+len(" like "):])
						if len(patternStr) > 0 && (patternStr[0] == '\'' || patternStr[0] == '"') {
							quote := patternStr[0]
							endQuoteIdx := strings.Index(patternStr[1:], string(quote))
							if endQuoteIdx != -1 {
								likePattern = patternStr[1 : 1+endQuoteIdx]
							}
						}
					}
					if likePattern == lowerAllowedTable {
						foundTable = true
					}
				}
				// If `SHOW TABLES` without `LIKE` and `allowedTable` is set, it's generally a mismatch unless `allowedTable` is a database name.
				// This part could be enhanced if database-level restrictions are needed for `SHOW TABLES FROM database`.
			}
		}

		if !foundTable {
			return errors.New("sql: not allowed")
		}
	}
	return nil

}

func sqlExecuteQuery(ctx *security.RequestContext, query string, accountId string, tableName string, tableView string, limit int) ([]map[string]any, error) {
	finalQuery := sqlCleanUpQuery(query)
	err := sqlValidateReadOnly(finalQuery, tableName)
	if err != nil {
		return nil, err
	}
	slog.Info("sql: updating tool query with account id", "account", accountId, "query", query, "table", tableName)
	finalQuery, err = SqlUpdateQueryWithAccountIdFilter(ctx, finalQuery, accountId, tableName)
	if err != nil {
		return nil, fmt.Errorf("sqlExecuteQuery: %w", err)
	}
	slog.Info("sql: updated tool query with account id", "account", accountId, "query", finalQuery, "table", tableName)

	if limit == 0 {
		limit = config.Config.LlmServerAgentMaxSqlRows
	}

	// The row cap is decided on the caller's SQL, before the view goes in: a
	// LIMIT inside the view's own subqueries is not the caller's LIMIT.
	finalQuery = buildLimitedQuery(finalQuery, limit)
	if tableView != "" {
		finalQuery = replaceFirstMatch(finalQuery, tableName, "("+tableView+") as tv")
	}
	slog.Info("sql: final tool query", "account", accountId, "query", finalQuery)

	t0 := time.Now()
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("sqlExecuteQuery: get database manager: %w", err)
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
		return nil, fmt.Errorf("sqlExecuteQuery: begin read-only transaction: %w", err)
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
			ctx.GetLogger().Warn("sql: rollback of tool query failed", "error", rerr)
		}
	}()
	if _, err := tx.ExecContext(queryCtx, fmt.Sprintf("SET LOCAL statement_timeout = %d", timeout.Milliseconds())); err != nil {
		return nil, fmt.Errorf("sqlExecuteQuery: set statement_timeout: %w", err)
	}
	// Enough memory for the view's ranking subqueries to stay hashed, so they
	// are built once per query rather than re-run for every row.
	if workMemMB := config.Config.LlmServerAgentSqlWorkMemMB; workMemMB > 0 {
		if _, err := tx.ExecContext(queryCtx, fmt.Sprintf("SET LOCAL work_mem = '%dMB'", workMemMB)); err != nil {
			return nil, fmt.Errorf("sqlExecuteQuery: set work_mem: %w", err)
		}
	}

	// Prepared, not sent as a plain query: Postgres refuses to prepare more
	// than one statement, so "select ...; commit; delete ..." cannot end the
	// read-only transaction and run the rest in autocommit.
	stmt, err := tx.PreparexContext(queryCtx, finalQuery)
	if err != nil {
		return nil, toolQueryError(ctx, tableName, timeout, err)
	}
	defer func() {
		if err := stmt.Close(); err != nil {
			ctx.GetLogger().Warn("sql: closing tool query statement failed", "error", err)
		}
	}()
	rows, err := stmt.QueryxContext(queryCtx)
	if err != nil {
		return nil, toolQueryError(ctx, tableName, timeout, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Warn("sql: closing tool query rows failed", "error", err)
		}
	}()

	colTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("sqlExecuteQuery: read column types: %w", err)
	}
	numericCols := numericColumnNames(colTypes)
	data := []map[string]any{}
	for rows.Next() {
		row := map[string]any{}
		if err := rows.MapScan(row); err != nil {
			return nil, fmt.Errorf("sqlExecuteQuery: scan row: %w", err)
		}
		keepNumericText(row, numericCols)
		data = append(data, row)
	}
	if err := rows.Err(); err != nil {
		return nil, toolQueryError(ctx, tableName, timeout, err)
	}
	ctx.GetLogger().Info("sql: executed tool query", "account", accountId, "query", finalQuery, "time", time.Since(t0).String(), "rows", len(data))
	if len(data) > 5 && limit <= 0 {
		data = data[0:5]
	}
	return data, nil
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

// buildLimitedQuery wraps a query with an appropriate LIMIT clause.
// Uses a subquery wrapper so the inner query (including DISTINCT/GROUP BY)
// is fully evaluated before the limit is applied. Queries that already have
// a LIMIT clause are left unchanged.
func buildLimitedQuery(query string, limit int) string {
	// Strip single-line comments before checking so a `-- LIMIT ...` comment
	// doesn't suppress the safety wrapper. String-literal occurrences of LIMIT
	// can still false-positive; LLM-generated SQL rarely contains such literals.
	hasUserLimit := limitRegex.MatchString(stripSingleLineComments(query))

	if !hasUserLimit {
		return fmt.Sprintf("select * from (%s) as ql limit %d;", query, limit)
	}

	if !strings.HasSuffix(strings.TrimSpace(query), ";") {
		return query + ";"
	}
	return query
}

// issue with LLMs doing too much escaping
func sqlCleanUpQuery(query string) string {
	return strings.ReplaceAll(query, "\\", "")
}

func SqlUpdateQueryWithAccountIdFilter(ctx *security.RequestContext, query string, accountId string, tableName string) (string, error) {
	// Validate accountId is a valid UUID for all tables. Belt-and-suspenders:
	// today the events/anomaly branch returns early, but a future view change
	// could start interpolating accountId, so we validate unconditionally.
	if _, err := uuid.Parse(accountId); err != nil {
		return "", fmt.Errorf("sql: invalid account_id format %q: %w", accountId, err)
	}

	// For anomaly and events table, the view already handles account filtering, so skip this step
	// The anomaly and events view will be substituted later and already has the account filter
	if tableName == "anomaly" || tableName == "events" {
		return query, nil
	}

	// Replace every reference to the table with a tenant-filtered subquery so
	// multi-table queries (JOINs, subqueries) cannot bypass tenant isolation by
	// referencing the raw table after the first occurrence is wrapped. Each
	// replacement gets a unique alias to avoid SQL "duplicate alias" errors.
	accountColumnName := "cloud_account_id"
	// accountId is UUID-validated above; defense-in-depth via pq.QuoteLiteral
	// keeps CodeQL happy and protects against future callers that skip the gate.
	quotedAccountId := pq.QuoteLiteral(accountId)
	return replaceAllMatches(query, tableName, func(idx int) string {
		return fmt.Sprintf("(select * from %s where %s = %s) q%d", tableName, accountColumnName, quotedAccountId, idx)
	}), nil
}

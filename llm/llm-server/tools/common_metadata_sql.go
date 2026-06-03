package tools

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

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
	if tableView != "" {
		finalQuery = replaceFirstMatch(finalQuery, tableName, "("+tableView+") as tv")
	}
	slog.Info("sql: executing tool query after appending view", "account", accountId, "query", finalQuery)

	if limit == 0 {
		limit = config.Config.LlmServerAgentMaxSqlRows
	}

	finalQuery = buildLimitedQuery(finalQuery, limit)
	slog.Info("sql: final tool query", "account", accountId, "query", finalQuery)

	t0 := time.Now()
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}
	rows, err := dbManager.Db.Queryx(finalQuery)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			fmt.Printf("Error closing rows: %v\n", err)
		}
	}()

	data := []map[string]any{}
	for rows.Next() {
		row := map[string]any{}
		if err := rows.MapScan(row); err != nil {
			return nil, err
		}
		data = append(data, row)
	}
	ctx.GetLogger().Info("sql: executed tool query", "account", accountId, "query", finalQuery, "time", time.Since(t0).String(), "rows", len(data))
	if len(data) > 5 && limit <= 0 {
		data = data[0:5]
	}
	return data, err
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
	return replaceAllMatches(query, tableName, func(idx int) string {
		return fmt.Sprintf("(select * from %s where %s = '%s') q%d", tableName, accountColumnName, accountId, idx)
	}), nil
}

package observability

import (
	"errors"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strings"
	"time"

	"github.com/samber/lo"
)

func convertOrderByWithMapping(orderBy []SortField, mapping map[string]string) []SortField {
	converted := make([]SortField, 0, len(orderBy))
	for _, ob := range orderBy {
		newCol, exists := mapping[ob.ColumnName]
		if !exists {
			newCol = ob.ColumnName
		}
		converted = append(converted, SortField{
			ColumnName: newCol,
			Order:      ob.Order,
		})
	}
	return converted
}

func convertWhereClauseWithMApping(whereClause query.QueryWhereClause, mapping map[string]string) query.QueryWhereClause {
	converted := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{},
		And:    []query.QueryWhereClause{},
		Or:     []query.QueryWhereClause{},
	}

	// Convert binary conditions
	for col, ops := range whereClause.Binary {
		newCol, exists := mapping[col]
		if !exists {
			newCol = col
		}
		if _, ok := converted.Binary[newCol]; !ok {
			converted.Binary[newCol] = make(map[query.BinaryWhereClauseType]any)
		}
		for op, val := range ops {
			converted.Binary[newCol][op] = val
		}
	}

	// Recursively convert AND conditions
	for _, andClause := range whereClause.And {
		converted.And = append(converted.And, convertWhereClauseWithMApping(andClause, mapping))
	}

	// Recursively convert OR conditions
	for _, orClause := range whereClause.Or {
		converted.Or = append(converted.Or, convertWhereClauseWithMApping(orClause, mapping))
	}

	return converted
}

func GetSeverityLevels(msg string) string {
	msgLower := strings.ToLower(msg)

	errorPatterns := []string{"error", "fail", "fatal", "exception", "traceback", "panic", "segfault"}
	warningPatterns := []string{"warn", "deprecated", "timeout", "retry", "unavailable"}
	debugPatterns := []string{"debug", "trace", "test", "dev", "profiling"}
	infoPatterns := []string{"info", "started", "connected", "listening", "running", "success"}

	for _, word := range errorPatterns {
		if strings.Contains(msgLower, word) {
			return "error"
		}
	}
	for _, word := range warningPatterns {
		if strings.Contains(msgLower, word) {
			return "warning"
		}
	}
	for _, word := range debugPatterns {
		if strings.Contains(msgLower, word) {
			return "debug"
		}
	}
	for _, word := range infoPatterns {
		if strings.Contains(msgLower, word) {
			return "info"
		}
	}

	// fallback
	return "info"

}

func getQueryRequest(ctx *security.RequestContext, traceRequest TracesQueryBuilderRequest, tableDef query.TableDefinition, tableName string) query.QueryRequest {
	queryRequest := query.QueryRequest{}
	queryRequest.Where = traceRequest.Where
	queryRequest.Limit = traceRequest.Limit
	queryRequest.OrderBy = traceRequest.OrderBy
	queryRequest.Offset = traceRequest.Offset
	queryRequest.Table = tableName
	traceQuery := queryRequest
	binary := traceQuery.Where.Binary
	tableDefination := ClickhouseTraceTableDefinition
	if tableName == "traces_grouping_v2" {
		tableDefination = ClickhouseTraceGroupingTableDefinition
	}
	cols := make([]query.QueryColumn, 0, len(tableDefination))
	for k, tableColDef := range tableDefination {
		var queryColDef query.QueryColumn
		queryColDef.Name = k
		if !tableColDef.IsAggregated {
			cols = append(cols, queryColDef)
		}
	}
	queryRequest.Columns = cols

	if binary != nil && binary[tableDef.AccountIdColumnName] != nil && binary[tableDef.AccountIdColumnName][query.Eq] != nil {
		delete(binary, tableDef.AccountIdColumnName)
		traceQuery.Columns = lo.Filter(traceQuery.Columns, func(column query.QueryColumn, i int) bool {
			return column.Name != tableDef.AccountIdColumnName
		})
	}
	return queryRequest
}

var possibleFormats = []string{
	"2006-01-02 15:04:05,000",      // 2025-11-18 09:18:59,999
	time.RFC3339,                   // 2025-11-19T15:04:05Z
	"2006-01-02 15:04:05",          // 2025-11-19 15:04:05
	"2006-01-02",                   // 2025-11-19
	"2006/01/02 15:04:05",          // 2025/11/19 15:04:05
	"02-01-2006",                   // 19-11-2025
	"02/01/2006",                   // 19/11/2025
	"2006-01-02T15:04:05.000Z0700", // ISO + timezone
	"2006-01-02T15:04:05Z0700",
}

func ParseDateToMillis(dateStr string) (int64, error) {
	for _, format := range possibleFormats {
		t, err := time.Parse(format, dateStr)
		if err == nil {
			return t.UnixMilli(), nil
		}
	}
	return 0, errors.New("no matching date format found for: " + dateStr)
}

// isEmptyWhereClause returns true when a QueryWhereClause has no conditions at all.
func isEmptyWhereClause(where query.QueryWhereClause) bool {
	return len(where.Binary) == 0 && len(where.And) == 0 && len(where.Or) == 0 && where.Not == nil
}

// removeKeysFromWhereClause returns a copy of where with all occurrences of the
// given keys removed from every Binary map, at every nesting level.
// Empty sub-clauses produced by the removal are pruned so that downstream query
// builders never receive dangling {Binary:{}} noise inside And/Or/Not.
// If keys is empty the original clause is returned unchanged.
func removeKeysFromWhereClause(where query.QueryWhereClause, keys []string) query.QueryWhereClause {
	if len(keys) == 0 {
		return where
	}
	ignoreSet := make(map[string]bool, len(keys))
	for _, k := range keys {
		ignoreSet[k] = true
	}
	return removeKeysFromWhereClauseRecursive(where, ignoreSet)
}

func removeKeysFromWhereClauseRecursive(where query.QueryWhereClause, ignoreSet map[string]bool) query.QueryWhereClause {
	return query.QueryWhereClause{
		Binary: filterBinaryClause(where.Binary, ignoreSet),
		And:    filterClauseList(where.And, ignoreSet),
		Or:     filterClauseList(where.Or, ignoreSet),
		Not:    filterNotClause(where.Not, ignoreSet),
	}
}

// filterBinaryClause removes ignored keys from a BinaryWhereClause map.
func filterBinaryClause(binary query.BinaryWhereClause, ignoreSet map[string]bool) query.BinaryWhereClause {
	if len(binary) == 0 {
		return nil
	}
	result := make(query.BinaryWhereClause, len(binary))
	for key, ops := range binary {
		if !ignoreSet[key] {
			result[key] = ops
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// filterClauseList recursively filters a slice of sub-clauses, pruning empty results.
func filterClauseList(clauses []query.QueryWhereClause, ignoreSet map[string]bool) []query.QueryWhereClause {
	var result []query.QueryWhereClause
	for _, clause := range clauses {
		filtered := removeKeysFromWhereClauseRecursive(clause, ignoreSet)
		if !isEmptyWhereClause(filtered) {
			result = append(result, filtered)
		}
	}
	return result
}

// parseInt64Value safely converts an interface value to int64.
// It handles int64, float64 (JSON default), and int types.
// Returns (value, true) on success, (0, false) if the type is unsupported.
func parseInt64Value(val any) (int64, bool) {
	switch v := val.(type) {
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case int:
		return int64(v), true
	default:
		return 0, false
	}
}

// filterNotClause recursively filters a Not clause, returning nil if it becomes empty.
func filterNotClause(not *query.QueryWhereClause, ignoreSet map[string]bool) *query.QueryWhereClause {
	if not == nil {
		return nil
	}
	filtered := removeKeysFromWhereClauseRecursive(*not, ignoreSet)
	if isEmptyWhereClause(filtered) {
		return nil
	}
	return &filtered
}

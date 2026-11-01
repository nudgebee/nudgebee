package query

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"slices"
	"strings"
	"time"

	"github.com/samber/lo"
)

type sqlDialect interface {
	QuoteIdentifier(s string) string
	QuoteLiteral(s any) string
	FuncDateTruncate(dateUnit string, columnName string) string
	FuncStringToDatetime(columnName string) string
}

var dialects = map[database.DatabaseManagerType]sqlDialect{
	database.Metastore:              &postgresDialect{},
	database.Warehouse:              &clickhouseDialect{},
	database.AgentWarehouse:         &clickhouseDialect{},
	database.AgentWarehouseBigQuery: &bigQueryDialect{},
	database.AzureMonitoring:        &kqlDialect{},
}

func getDialect(source database.DatabaseManagerType) sqlDialect {
	if dialect, ok := dialects[source]; ok {
		return dialect
	}
	return nil
}

// SQL injection safety contract for this package
// ------------------------------------------------
// Every untrusted entry on a QueryRequest is gated before it can reach SQL:
//
//   - request.Table        — caller (service.go:ExecuteQuery) rejects the
//                            request unless GetTableMetadata returns a hit.
//   - request.Columns[]    — generateSelectColumnsClause errors if a name
//                            isn't in tableDef.Columns (hardcoded schema).
//   - request.GroupBy[]    — generateGroupClause errors if a name isn't in
//                            tableDef.Columns.
//   - request.OrderBy[]    — generateOrderByClause errors if Column isn't in
//                            tableDef.Columns; Order is a closed enum switch
//                            with default→error.
//   - request.Where        — generateWhereClause errors if the Binary column
//                            key isn't in tableDef.Columns; the operator
//                            (BinaryWhereClauseType) is a closed enum switch
//                            with default→error.
//   - WHERE/HAVING values  — passed through dialect.QuoteLiteral for strings
//                            (single-quote-escaped) or writeSafeValue for
//                            typed numerics. *F operators route the value
//                            back through the column-allowlist.
//   - request.Limit/Offset — fmt.Fprintf("%d") on int — type-safe.
//
// Identifiers cannot be bound via $N placeholders in Postgres, so the
// allowlist (tableDef.Columns + table-metadata registry) is the canonical
// defense and must stay authoritative. See sql_security_test.go for
// regression coverage of value-injection and identifier-allowlist paths.
//
// Outstanding non-security TODOs that previously lived here:
//   - support for filtering on aggregates
//   - support for joins for handling nested objects
//   - refactor for better query generation using common interfaces
//   - more functions/expression support

func generateColumnExpression(columnDef ColumnDefinition, column QueryColumn, tableDef TableDefinition, accountId string, ctx *security.RequestContext) string {
	dialect := getDialect(tableDef.Source)
	castType := lo.Ternary(tableDef.Source == database.AgentWarehouseBigQuery, "STRING", "TEXT")
	if columnDef.Type == "datetime" {
		dateUnit := "day"
		if column.Expr != "" {
			switch column.Expr {
			case "date_unit":
				if len(column.Args) > 0 {
					dateUnit = strings.ToLower(column.Args[0])
				}
				return dialect.FuncDateTruncate(dateUnit, lo.Ternary(columnDef.Def == "", column.Name, columnDef.Def))
			default:
				return lo.Ternary(columnDef.Def == "", column.Name, columnDef.Def)
			}
		} else {
			return lo.Ternary(columnDef.Def == "", column.Name, columnDef.Def)
		}
	} else if columnDef.Type == "string" {
		return fmt.Sprintf("cast(%s as %s)", lo.Ternary(columnDef.Def == "", column.Name, columnDef.Def), castType)
	} else if columnDef.Type == "json" {
		return fmt.Sprintf("cast(%s as %s)", lo.Ternary(columnDef.Def == "", column.Name, columnDef.Def), castType)
	} else if tableDef.Type == Aggregate && columnDef.IsAggregated && (columnDef.Type == ColumnDefinitionTypeInt || columnDef.Type == ColumnDefinitionTypeFloat) {
		definitionColumn := ""
		if columnDef.DefGenerator != nil {
			columnDefinition, _, err := columnDef.DefGenerator(ctx, accountId, QueryRequest{})
			if err != nil {
				return ""
			}
			definitionColumn = columnDefinition
		} else {
			definitionColumn = columnDef.Def
		}
		if definitionColumn == "count(*)" && column.Expr == "distinct" && len(column.Args) > 0 {
			args := make([]string, 0)
			for _, argCol := range column.Args {
				for colName, colDef := range tableDef.Columns {
					if colName == argCol {
						if colDef.Def == "" {
							args = append(args, colName)
						} else {
							args = append(args, colDef.Def)
						}
					}
				}
			}
			if len(args) > 1 {
				return "count(DISTINCT concat(" + strings.Join(args, ",") + "))"
			} else {
				return "count(DISTINCT " + strings.Join(args, ",") + ")"
			}
		}
		return lo.Ternary(definitionColumn == "", column.Name, definitionColumn)
	} else {
		return lo.Ternary(columnDef.Def == "", column.Name, columnDef.Def)
	}
}

func generateSelectColumnsClause(request QueryRequest, tableDef TableDefinition, accountId string, ctx *security.RequestContext) (string, error) {
	if len(request.Columns) == 0 {
		columnDefs := make([]string, 0)
		for column, columnDef := range tableDef.Columns {
			columnDefs = append(columnDefs, generateColumnExpression(columnDef, QueryColumn{Name: column}, tableDef, accountId, ctx))
		}
		return strings.Join(columnDefs, ","), nil
	}

	selectBuilder := strings.Builder{}
	for i, column := range request.Columns {
		if columnDef, ok := tableDef.Columns[column.Name]; ok {
			selectBuilder.WriteString(generateColumnExpression(columnDef, column, tableDef, accountId, ctx))
			selectBuilder.WriteString(" AS ")
			selectBuilder.WriteString(column.Name)
		} else {
			return "", fmt.Errorf("column %s not found in table %s", column, request.Table)
		}
		if i < len(request.Columns)-1 {
			selectBuilder.WriteString(",")
		}
	}
	return selectBuilder.String(), nil
}

// generateWhereClauseColumn resolves a column reference for the WHERE/HAVING
// clause. The `column` arg must already be a key of tableDef.Columns —
// callers (generateWhereClause and the *F binary operators) validate this
// before invoking, and this function re-checks at line entry. The returned
// string is either columnDef.WhereDef, columnDef.Def, or the raw column
// name, all sourced from the in-code TableDefinition (never user input).
func generateWhereClauseColumn(column string, tableDef TableDefinition, isHavingArgs ...bool) (string, error) {
	isHaving := len(isHavingArgs) > 0 && isHavingArgs[0]
	columnDef, ok := tableDef.Columns[column]
	if !ok {
		return "", fmt.Errorf("column %s defined in where clause not found in table %s", column, tableDef.Name)
	}

	if columnDef.IsAggregated {
		if isHaving {
			// For HAVING clause with aggregated columns, always use Def (the aggregate expression)
			if columnDef.Def != "" {
				return columnDef.Def, nil
			}
		} else {
			return "", fmt.Errorf("column %s defined in where clause is aggregated and cannot be used in where clause", column)
		}
	}

	// Priority: WhereDef > Def > raw column name
	if columnDef.WhereDef != "" {
		return columnDef.WhereDef, nil
	}
	if columnDef.Def != "" {
		return columnDef.Def, nil
	}
	return column, nil
}

func resolveColumnReference(column string, def ColumnDefinition, forWhereClause bool) string {
	if forWhereClause && def.WhereDef != "" {
		return def.WhereDef
	}
	if def.Def != "" {
		return def.Def
	}
	return column
}

func writeSafeValue(w *strings.Builder, v any, d sqlDialect) error {
	switch v.(type) {
	case string:
		if d == nil {
			return fmt.Errorf("dialect not found")
		}
		w.WriteString(d.QuoteLiteral(v))
	default:
		fmt.Fprintf(w, "%v", v)
	}
	return nil
}

// orderedCompareColumn resolves the column expression for an ordered comparison
// (< <= > >=). A string-typed column is rejected -- lexicographic comparison against a
// value the caller means numerically is a silent wrong answer, not a query. A column
// that opts in via NumericCompareDef supplies a numeric projection of itself and is
// compared numerically instead. Returns (expression, compareNumerically, error).
func orderedCompareColumn(column string, columnDef ColumnDefinition, binaryType BinaryWhereClauseType) (string, bool, error) {
	if columnDef.Type == ColumnDefinitionTypeString {
		if columnDef.NumericCompareDef == "" {
			return "", false, fmt.Errorf("binary clause type %s not supported for string type", binaryType)
		}
		return columnDef.NumericCompareDef, true, nil
	}
	return lo.Ternary(columnDef.Def == "", column, columnDef.Def), false, nil
}

// generateWhereClause builds the WHERE (or HAVING, when isHaving=true) SQL
// fragment from a QueryWhereClause. Safety invariants enforced inline:
//   - Binary column keys must be in tableDef.Columns (allowlist).
//   - The operator (BinaryWhereClauseType) is dispatched via a closed switch;
//     unknown operators return an error.
//   - String values flow through dialect.QuoteLiteral (escapes single
//     quotes); numeric/typed values flow through writeSafeValue.
//   - *F operators (EqF, LtF, etc.) interpret the value as a column
//     reference and re-validate it via generateWhereClauseColumn.
//
// See the package-level contract above for the full input-trust model.
func generateWhereClause(whereClause QueryWhereClause, tableDef TableDefinition, isHavingArgs ...bool) (string, error) {
	isHaving := len(isHavingArgs) > 0 && isHavingArgs[0]
	dialect := getDialect(tableDef.Source)
	if dialect == nil {
		return "", fmt.Errorf("dialect not found for source %s", tableDef.Source)
	}

	// Collect all clause parts
	var clauseParts []string

	// Handle And conditions
	if len(whereClause.And) > 0 {
		andParts := make([]string, 0, len(whereClause.And))
		for _, andClause := range whereClause.And {
			andStr, err := generateWhereClause(andClause, tableDef, isHaving)
			if err != nil {
				return "", err
			}
			if andStr != "" {
				andParts = append(andParts, andStr)
			}
		}
		if len(andParts) > 0 {
			clauseParts = append(clauseParts, "("+strings.Join(andParts, " AND ")+")")
		}
	}

	// Handle Or conditions
	if len(whereClause.Or) > 0 {
		orParts := make([]string, 0, len(whereClause.Or))
		for _, orClause := range whereClause.Or {
			orStr, err := generateWhereClause(orClause, tableDef, isHaving)
			if err != nil {
				return "", err
			}
			if orStr != "" {
				orParts = append(orParts, orStr)
			}
		}
		if len(orParts) > 0 {
			clauseParts = append(clauseParts, "("+strings.Join(orParts, " OR ")+")")
		}
	}

	// Handle Not conditions
	if whereClause.Not != nil {
		notStr, err := generateWhereClause(*whereClause.Not, tableDef, isHaving)
		if err != nil {
			return "", err
		}
		if notStr != "" {
			clauseParts = append(clauseParts, "NOT ("+notStr+")")
		}
	}

	// Handle Binary conditions
	if len(whereClause.Binary) > 0 {
		binaryParts := make([]string, 0, len(whereClause.Binary))

		// Iterate in a stable column order so generated SQL is deterministic
		// (Go map iteration order is randomized). AND-term order is
		// semantically irrelevant but must be reproducible for caching/tests.
		binaryColumns := make([]string, 0, len(whereClause.Binary))
		for column := range whereClause.Binary {
			binaryColumns = append(binaryColumns, column)
		}
		slices.Sort(binaryColumns)

		for _, column := range binaryColumns {
			binaryClause := whereClause.Binary[column]
			columnDef := ColumnDefinition{}
			if columnDef1, ok := tableDef.Columns[column]; ok {
				if columnDef1.IsAggregated && !isHaving {
					return "", fmt.Errorf("column %s defined in where clause is aggregated and cannot be used in where clause", column)
				}
				columnDef = columnDef1
			} else {
				return "", fmt.Errorf("column %s defined in where clause not found in table %s", column, tableDef.Name)
			}

			columnBinaryParts := make([]string, 0, len(binaryClause))

			// Sort operator keys for deterministic output (see note above).
			binaryTypes := make([]BinaryWhereClauseType, 0, len(binaryClause))
			for binaryType := range binaryClause {
				binaryTypes = append(binaryTypes, binaryType)
			}
			slices.Sort(binaryTypes)

			for _, binaryType := range binaryTypes {
				value := binaryClause[binaryType]
				var binaryCondition strings.Builder

				switch binaryType {
				case IsNull:
					binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
					switch valueType := value.(type) {
					case bool:
						if valueType {
							binaryCondition.WriteString(" IS NULL")
						} else {
							binaryCondition.WriteString(" IS NOT NULL")
						}
					default:
						binaryCondition.WriteString(" IS NULL")
					}
				case Contains:
					if columnDef.Type == "string" || columnDef.Type == "json" {
						binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
						binaryCondition.WriteString(" @> ")
						binaryCondition.WriteString(dialect.QuoteLiteral(value) + "::jsonb")
					} else {
						return "", fmt.Errorf("contains clause %s not supported for non string type", binaryType)
					}
				case HasKey:
					if columnDef.Type == "string" || columnDef.Type == "json" {
						binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
						binaryCondition.WriteString(" ? ")
						binaryCondition.WriteString(dialect.QuoteLiteral(value))
					} else {
						return "", fmt.Errorf("has key clause %s not supported for non string type", binaryType)
					}
				case ILike:
					if columnDef.Type == "string" {
						binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
						binaryCondition.WriteString(" ILIKE ")
						binaryCondition.WriteString(dialect.QuoteLiteral(value))
					} else {
						return "", fmt.Errorf("contains clause %s not supported for non string type", binaryType)
					}
				case ILikeF:
					binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
					binaryCondition.WriteString(" ILIKE ")
					colStr, err := generateWhereClauseColumn(value.(string), tableDef)
					if err != nil {
						return "", err
					}
					binaryCondition.WriteString(colStr)
				case Like:
					binaryCondition.WriteString(resolveColumnReference(column, columnDef, true))
					binaryCondition.WriteString(" LIKE ")
					if columnDef.Type == "string" {
						binaryCondition.WriteString(dialect.QuoteLiteral(value))
					} else {
						return "", fmt.Errorf("like clause %s not supported for non string type", binaryType)
					}
				case LikeF:
					binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
					binaryCondition.WriteString(" LIKE ")
					colStr, err := generateWhereClauseColumn(value.(string), tableDef)
					if err != nil {
						return "", err
					}
					binaryCondition.WriteString(colStr)
				case NLike:
					binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
					binaryCondition.WriteString(" NOT LIKE ")
					if columnDef.Type == "string" {
						binaryCondition.WriteString(dialect.QuoteLiteral(value))
					} else {
						return "", fmt.Errorf("like clause %s not supported for non string type", binaryType)
					}
				case Regex, NRegex:
					pattern, isString := value.(string)
					if !isString || columnDef.Type != "string" {
						return "", fmt.Errorf("regex clause %s needs a string column and a string pattern", binaryType)
					}
					if err := ValidateRegexFilter(pattern); err != nil {
						return "", err
					}
					expr, err := regexMatchExpr(dialect, resolveColumnReference(column, columnDef, true), pattern, binaryType == NRegex)
					if err != nil {
						return "", err
					}
					binaryCondition.WriteString(expr)
				case Eq:
					binaryCondition.WriteString(resolveColumnReference(column, columnDef, true))
					if columnDef.Type == "json" && tableDef.Source != database.AgentWarehouse {
						binaryCondition.WriteString(" ")
						binaryCondition.WriteString("@>")
						binaryCondition.WriteString(" ")
					} else if columnDef.Type == "map" && tableDef.Source == database.AgentWarehouse {
						// clickhouse json equality handling
						if _, ok := value.(map[string]interface{}); ok {
							cn := 0
							for k, v := range value.(map[string]interface{}) {
								if cn > 0 {
									binaryCondition.WriteString(" AND ")
									binaryCondition.WriteString(column)
								}
								binaryCondition.WriteString("[")
								binaryCondition.WriteString(dialect.QuoteLiteral(k))
								binaryCondition.WriteString("]")
								binaryCondition.WriteString(" = ")
								binaryCondition.WriteString(dialect.QuoteLiteral(v))
								cn++
							}
						} else {
							return "", errors.New("json equality only supported for map[string]string in clickhouse")
						}
					} else {
						binaryCondition.WriteString(" ")
						binaryCondition.WriteString("=")
						binaryCondition.WriteString(" ")
					}
					switch columnDef.Type {
					case "string":
						binaryCondition.WriteString(dialect.QuoteLiteral(value))
					case "map":
						if tableDef.Source != database.AgentWarehouse {
							binaryCondition.WriteString(dialect.QuoteLiteral(value))
						}
					case "datetime":
						binaryCondition.WriteString(dialect.FuncStringToDatetime(dialect.QuoteLiteral(value)))
					default:
						if err := writeSafeValue(&binaryCondition, value, dialect); err != nil {
							return "", err
						}
					}
				case EqF:
					binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
					binaryCondition.WriteString(" = ")
					colStr, err := generateWhereClauseColumn(value.(string), tableDef)
					if err != nil {
						return "", err
					}
					binaryCondition.WriteString(colStr)
				case Nq:
					binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
					binaryCondition.WriteString(" != ")
					switch columnDef.Type {
					case "string":
						binaryCondition.WriteString(dialect.QuoteLiteral(value))
					case "datetime":
						binaryCondition.WriteString(dialect.FuncStringToDatetime(dialect.QuoteLiteral(value)))
					default:
						if err := writeSafeValue(&binaryCondition, value, dialect); err != nil {
							return "", err
						}
					}
				case NqF:
					binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
					binaryCondition.WriteString(" != ")
					colStr, err := generateWhereClauseColumn(value.(string), tableDef)
					if err != nil {
						return "", err
					}
					binaryCondition.WriteString(colStr)
				case Lt:
					colExpr, compareNumerically, err := orderedCompareColumn(column, columnDef, binaryType)
					if err != nil {
						return "", err
					}
					binaryCondition.WriteString(colExpr)
					binaryCondition.WriteString(" < ")
					switch {
					case !compareNumerically && columnDef.Type == "datetime":
						binaryCondition.WriteString(dialect.FuncStringToDatetime(dialect.QuoteLiteral(value)))
					default:
						if err := writeSafeValue(&binaryCondition, value, dialect); err != nil {
							return "", err
						}
					}
				case LtF:
					binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
					binaryCondition.WriteString(" < ")
					colStr, err := generateWhereClauseColumn(value.(string), tableDef)
					if err != nil {
						return "", err
					}
					binaryCondition.WriteString(colStr)
				case Gt:
					colExpr, compareNumerically, err := orderedCompareColumn(column, columnDef, binaryType)
					if err != nil {
						return "", err
					}
					binaryCondition.WriteString(colExpr)
					binaryCondition.WriteString(" > ")
					switch {
					case !compareNumerically && columnDef.Type == "datetime":
						binaryCondition.WriteString(dialect.FuncStringToDatetime(dialect.QuoteLiteral(value)))
					default:
						if err := writeSafeValue(&binaryCondition, value, dialect); err != nil {
							return "", err
						}
					}
				case GtF:
					binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
					binaryCondition.WriteString(" > ")
					colStr, err := generateWhereClauseColumn(value.(string), tableDef)
					if err != nil {
						return "", err
					}
					binaryCondition.WriteString(colStr)
				case Lte:
					colExpr, compareNumerically, err := orderedCompareColumn(column, columnDef, binaryType)
					if err != nil {
						return "", err
					}
					binaryCondition.WriteString(colExpr)
					binaryCondition.WriteString(" <= ")
					switch {
					case !compareNumerically && columnDef.Type == "datetime":
						binaryCondition.WriteString(dialect.FuncStringToDatetime(dialect.QuoteLiteral(value)))
					default:
						if err := writeSafeValue(&binaryCondition, value, dialect); err != nil {
							return "", err
						}
					}
				case LteF:
					binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
					binaryCondition.WriteString(" <= ")
					colStr, err := generateWhereClauseColumn(value.(string), tableDef)
					if err != nil {
						return "", err
					}
					binaryCondition.WriteString(colStr)
				case Gte:
					colExpr, compareNumerically, err := orderedCompareColumn(column, columnDef, binaryType)
					if err != nil {
						return "", err
					}
					binaryCondition.WriteString(colExpr)
					binaryCondition.WriteString(" >= ")
					switch {
					case !compareNumerically && columnDef.Type == "datetime":
						binaryCondition.WriteString(dialect.FuncStringToDatetime(dialect.QuoteLiteral(value)))
					default:
						if err := writeSafeValue(&binaryCondition, value, dialect); err != nil {
							return "", err
						}
					}
				case GteF:
					binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
					binaryCondition.WriteString(" >= ")
					colStr, err := generateWhereClauseColumn(value.(string), tableDef)
					if err != nil {
						return "", err
					}
					binaryCondition.WriteString(colStr)
				case In, NotIn:
					inClauseType := " IN "
					if binaryType == NotIn {
						inClauseType = " NOT IN "
					}

					if columnDef.Type == "string" {
						switch valueType := value.(type) {
						case []string:
							if len(valueType) == 0 {
								binaryCondition.WriteString("true")
								break
							}
							binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
							binaryCondition.WriteString(inClauseType)
							binaryCondition.WriteString("(")
							for i, v := range valueType {
								binaryCondition.WriteString(dialect.QuoteLiteral(v))
								if i < len(valueType)-1 {
									binaryCondition.WriteString(",")
								}
							}
							binaryCondition.WriteString(")")
						case []any:
							if len(valueType) == 0 {
								binaryCondition.WriteString("true")
								break
							}
							binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
							binaryCondition.WriteString(inClauseType)
							binaryCondition.WriteString("(")
							for i, v := range valueType {
								binaryCondition.WriteString(dialect.QuoteLiteral(v))
								if i < len(valueType)-1 {
									binaryCondition.WriteString(",")
								}
							}
							binaryCondition.WriteString(")")
						default:
							return "", fmt.Errorf("binary clause type %s not supported for string type", binaryType)
						}
					} else if columnDef.Type == "datetime" {
						switch valueType := value.(type) {
						case []string:
							if len(valueType) == 0 {
								binaryCondition.WriteString("true")
								break
							}
							binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
							binaryCondition.WriteString(inClauseType)
							binaryCondition.WriteString("(")
							for i, v := range valueType {
								binaryCondition.WriteString(dialect.FuncStringToDatetime(dialect.QuoteLiteral(v)))
								if i < len(valueType)-1 {
									binaryCondition.WriteString(",")
								}
							}
							binaryCondition.WriteString(")")
						case []any:
							if len(valueType) == 0 {
								binaryCondition.WriteString("true")
								break
							}
							binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
							binaryCondition.WriteString(inClauseType)
							binaryCondition.WriteString("(")
							for i, v := range valueType {
								binaryCondition.WriteString(dialect.FuncStringToDatetime(dialect.QuoteLiteral(v)))
								if i < len(valueType)-1 {
									binaryCondition.WriteString(",")
								}
							}
							binaryCondition.WriteString(")")
						default:
							return "", fmt.Errorf("binary clause type %s not supported for datetime type", binaryType)
						}
					} else if columnDef.Type == "integer" {
						switch valueType := value.(type) {
						case []int:
							if len(valueType) == 0 {
								binaryCondition.WriteString("true")
								break
							}
							binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
							binaryCondition.WriteString(inClauseType)
							binaryCondition.WriteString("(")
							for i, v := range valueType {
								if err := writeSafeValue(&binaryCondition, v, dialect); err != nil {
									return "", err
								}
								if i < len(valueType)-1 {
									binaryCondition.WriteString(",")
								}
							}
							binaryCondition.WriteString(")")
						case []any:
							if len(valueType) == 0 {
								binaryCondition.WriteString("true")
								break
							}
							binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
							binaryCondition.WriteString(inClauseType)
							binaryCondition.WriteString("(")
							for i, v := range valueType {
								if err := writeSafeValue(&binaryCondition, v, dialect); err != nil {
									return "", err
								}
								if i < len(valueType)-1 {
									binaryCondition.WriteString(",")
								}
							}
							binaryCondition.WriteString(")")
						default:
							return "", fmt.Errorf("binary clause type %s not supported for int type", binaryType)
						}
					} else if columnDef.Type == "float" {
						switch valueType := value.(type) {
						case []float32:
							if len(valueType) == 0 {
								binaryCondition.WriteString("true")
								break
							}
							binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
							binaryCondition.WriteString(inClauseType)
							binaryCondition.WriteString("(")
							for i, v := range valueType {
								if err := writeSafeValue(&binaryCondition, v, dialect); err != nil {
									return "", err
								}
								if i < len(valueType)-1 {
									binaryCondition.WriteString(",")
								}
							}
							binaryCondition.WriteString(")")
						case []any:
							if len(valueType) == 0 {
								binaryCondition.WriteString("true")
								break
							}
							binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
							binaryCondition.WriteString(inClauseType)
							binaryCondition.WriteString("(")
							for i, v := range valueType {
								if err := writeSafeValue(&binaryCondition, v, dialect); err != nil {
									return "", err
								}
								if i < len(valueType)-1 {
									binaryCondition.WriteString(",")
								}
							}
							binaryCondition.WriteString(")")
						default:
							return "", fmt.Errorf("binary clause type %s not supported for float type", binaryType)
						}
					} else {
						valueSlice := value.([]any)
						if len(valueSlice) == 0 {
							binaryCondition.WriteString("true")
							break
						}
						binaryCondition.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
						binaryCondition.WriteString(inClauseType)
						binaryCondition.WriteString("(")
						for i, v := range valueSlice {
							if err := writeSafeValue(&binaryCondition, v, dialect); err != nil {
								return "", err
							}
							if i < len(valueSlice)-1 {
								binaryCondition.WriteString(",")
							}
						}
						binaryCondition.WriteString(")")
					}
				case Between:
					betweenParts := make([]string, 0)
					betweenMap := value.(map[string]any)
					// Sort bound keys for deterministic output (Go map order is randomized).
					betweenKeys := make([]string, 0, len(betweenMap))
					for k := range betweenMap {
						betweenKeys = append(betweenKeys, k)
					}
					slices.Sort(betweenKeys)
					for _, k := range betweenKeys {
						v := betweenMap[k]
						var betweenPart strings.Builder
						switch columnDef.Type {
						case "string":
							return "", fmt.Errorf("binary clause type %s not supported for string type", binaryType)
						case "datetime":
							betweenPart.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
							switch k {
							case "_lt":
								betweenPart.WriteString(" < ")
							case "_lte":
								betweenPart.WriteString(" <= ")
							case "_gt":
								betweenPart.WriteString(" > ")
							case "_gte":
								betweenPart.WriteString(" >= ")
							}
							betweenPart.WriteString(dialect.FuncStringToDatetime(dialect.QuoteLiteral(v)))
						case "float", "integer":
							betweenPart.WriteString(lo.Ternary(columnDef.Def == "", column, columnDef.Def))
							switch k {
							case "_lt":
								betweenPart.WriteString(" < ")
							case "_lte":
								betweenPart.WriteString(" <= ")
							case "_gt":
								betweenPart.WriteString(" > ")
							case "_gte":
								betweenPart.WriteString(" >= ")
							}
							if err := writeSafeValue(&betweenPart, v, dialect); err != nil {
								return "", err
							}
						}
						betweenParts = append(betweenParts, betweenPart.String())
					}
					binaryCondition.WriteString("(" + strings.Join(betweenParts, " AND ") + ")")
				default:
					return "", fmt.Errorf("binary clause type %s not supported", binaryType)
				}

				columnBinaryParts = append(columnBinaryParts, binaryCondition.String())
			}

			if len(columnBinaryParts) > 0 {
				binaryParts = append(binaryParts, "("+strings.Join(columnBinaryParts, " AND ")+")")
			}
		}

		if len(binaryParts) > 0 {
			clauseParts = append(clauseParts, strings.Join(binaryParts, " AND "))
		}
	}

	// Join all clause parts with AND
	if len(clauseParts) == 0 {
		return "", nil
	} else if len(clauseParts) == 1 {
		return clauseParts[0], nil
	} else {
		return strings.Join(clauseParts, " AND "), nil
	}
}

func generateGroupClause(request QueryRequest, tableDef TableDefinition, accountId string, ctx *security.RequestContext) (string, error) {
	queryBuilder := strings.Builder{}

	groupByCols := request.GroupBy
	foundAggregatedColumn := false
	if len(request.Columns) == 0 {
		for column, columnDef := range tableDef.Columns {
			if !columnDef.IsAggregated {
				if !slices.Contains(groupByCols, column) {
					groupByCols = append(groupByCols, column)
				}
			} else {
				foundAggregatedColumn = true
			}
		}
	} else {
		for _, column := range request.Columns {
			if columnDef, ok := tableDef.Columns[column.Name]; ok {
				if !columnDef.IsAggregated {
					if !slices.Contains(groupByCols, column.Name) {
						groupByCols = append(groupByCols, column.Name)
					}
				} else {
					foundAggregatedColumn = true
				}
			}
		}
	}

	if !foundAggregatedColumn && tableDef.Type == Normal {
		return "", nil
	}

	if len(groupByCols) > 0 {
		for i, groupBy := range groupByCols {
			if columnDef, ok := tableDef.Columns[groupBy]; ok {
				if columnDef.IsAggregated {
					return "", fmt.Errorf("column %s defined in group by clause is aggregated and cannot be used in grouping", groupBy)
				}
				// check if same column part of select
				queryColumn := QueryColumn{Name: groupBy}
				for _, column := range request.Columns {
					if column.Name == groupBy {
						queryColumn = column
						break
					}
				}
				if tableDef.Source == database.AgentWarehouse {
					if columnDef.Type == "string" {
						queryBuilder.WriteString(queryColumn.Name)
					} else {
						queryBuilder.WriteString(generateColumnExpression(columnDef, queryColumn, tableDef, accountId, ctx))
					}
				} else {
					queryBuilder.WriteString(generateColumnExpression(columnDef, queryColumn, tableDef, accountId, ctx))
				}
			} else {
				return "", fmt.Errorf("column %s defined in group by clause not found in table %s", groupBy, request.Table)
			}

			if i < len(groupByCols)-1 {
				queryBuilder.WriteString(",")
			}
		}
	}

	return queryBuilder.String(), nil
}

func generateOrderByClause(request QueryRequest, tableDef TableDefinition) (string, error) {
	// Build the effective set of GROUP BY columns, replicating the same logic
	// as generateGroupClause: start with request.GroupBy, then auto-add
	// non-aggregated SELECT columns for Aggregate/Derived tables.
	groupBySet := make(map[string]bool, len(request.GroupBy))
	for _, col := range request.GroupBy {
		groupBySet[col] = true
	}
	foundAggregatedColumn := false
	if len(request.Columns) == 0 {
		for column, columnDef := range tableDef.Columns {
			if !columnDef.IsAggregated {
				groupBySet[column] = true
			} else {
				foundAggregatedColumn = true
			}
		}
	} else {
		for _, column := range request.Columns {
			if columnDef, ok := tableDef.Columns[column.Name]; ok {
				if !columnDef.IsAggregated {
					groupBySet[column.Name] = true
				} else {
					foundAggregatedColumn = true
				}
			}
		}
	}
	// For Normal tables without aggregated columns, there is no GROUP BY
	if !foundAggregatedColumn && tableDef.Type == Normal {
		groupBySet = nil
	}
	hasGroupBy := len(groupBySet) > 0

	queryBuilder := strings.Builder{}
	if len(request.OrderBy) > 0 {
		for i, sortBy := range request.OrderBy {
			if colDef, ok := tableDef.Columns[sortBy.Column]; ok {
				// When GROUP BY is present, wrap non-grouped columns in MAX() to produce valid SQL.
				// Aggregated columns (e.g. event_count = count(*)) are already aggregate expressions
				// and appear as aliases in SELECT, so they must be referenced directly in ORDER BY.
				if hasGroupBy && !groupBySet[sortBy.Column] && !colDef.IsAggregated {
					queryBuilder.WriteString("MAX(")
					queryBuilder.WriteString(sortBy.Column)
					queryBuilder.WriteString(")")
				} else {
					queryBuilder.WriteString(sortBy.Column)
				}
				queryBuilder.WriteString(" ")
				switch sortBy.Order {
				case Asc:
					queryBuilder.WriteString(" ASC ")
				case Desc:
					queryBuilder.WriteString(" DESC ")
				case DescNullsLast:
					queryBuilder.WriteString(" DESC ")
					queryBuilder.WriteString(" NULLS LAST ")
				case DescNullsFirst:
					queryBuilder.WriteString(" DESC ")
					queryBuilder.WriteString(" NULLS FIRST ")
				case AscNullsFirst:
					queryBuilder.WriteString(" ASC ")
					queryBuilder.WriteString(" NULLS FIRST ")
				case AscNullsLast:
					queryBuilder.WriteString(" ASC ")
					queryBuilder.WriteString(" NULLS LAST ")
				default:
					return "", fmt.Errorf("sort order %s not supported", sortBy.Order)
				}
			} else {
				return "", fmt.Errorf("column %s defined in sort clause not found in table %s", sortBy.Column, request.Table)
			}
			if i < len(request.OrderBy)-1 {
				queryBuilder.WriteString(",")
			}
		}
	}
	return queryBuilder.String(), nil
}

func generateLimitOffsetClause(request QueryRequest, tableDef TableDefinition) (string, error) {
	queryBuilder := strings.Builder{}
	if request.Limit > 0 {
		queryBuilder.WriteString(" LIMIT ")
		fmt.Fprintf(&queryBuilder, "%d", request.Limit)
	} else {
		queryBuilder.WriteString(" LIMIT 1000")
	}

	if request.Offset > 0 {
		queryBuilder.WriteString(" OFFSET ")
		fmt.Fprintf(&queryBuilder, "%d", request.Offset)
	}
	return queryBuilder.String(), nil
}

func GenerateSqlQuery(ctx *security.RequestContext, accountId string, request QueryRequest, tableDef TableDefinition) (string, error) {
	return generateSqlQuery(ctx, accountId, request, tableDef, "")
}

// generateSqlQuery builds the SQL for request. accountScope is a server-built
// predicate (see TableDefinition.AccountScope) ANDed with the request's WHERE.
func generateSqlQuery(ctx *security.RequestContext, accountId string, request QueryRequest, tableDef TableDefinition, accountScope string) (string, error) {
	// column transformation fix for datetime grouping
	// if transforamtions are misisng then use day as default unit
	for _, groupColumn := range request.GroupBy {
		columnDef, ok := tableDef.Columns[groupColumn]
		if !ok {
			return "", errors.New("group column not found - " + groupColumn)
		}
		if columnDef.Type != ColumnDefinitionTypeDatetime {
			continue
		}

		for i, column := range request.Columns {
			if strings.EqualFold(groupColumn, column.Name) && column.Expr == "" {
				column.Expr = "date_unit"
				column.Args = []string{"day"}
				request.Columns[i] = column
			}
		}
	}

	queryBuilder := strings.Builder{}
	queryBuilder.WriteString("SELECT ")
	columnsStr, err := generateSelectColumnsClause(request, tableDef, accountId, ctx)
	if err != nil {
		return "", err
	}
	queryBuilder.WriteString(columnsStr)
	queryBuilder.WriteString(" FROM ")

	tabelDefString := tableDef.Def
	if tableDef.DefGenerator != nil {
		tabelDefString, request, err = tableDef.DefGenerator(ctx, accountId, request)
		if err != nil {
			return "", err
		}
	}

	if request.Table == "traces_heatmap_v2" {
		dialect := getDialect(tableDef.Source)
		if dialect == nil {
			return "", fmt.Errorf("dialect not found for source %s", tableDef.Source)
		}
		queryBuilder.WriteString("(")
		traceId := request.Where.Binary["trace_id"]["_eq"]
		queryBuilder.WriteString(strings.ReplaceAll(tabelDefString, "@traceId", dialect.QuoteLiteral(traceId)))
		queryBuilder.WriteString(")")
	} else {
		// Late projection: pick the page first, project it after. The page is a
		// subquery that selects the def's rows as they are, with every filter,
		// the ordering and the limit inside it, and the select list above runs
		// on the rows it returns.
		projectLate := canProjectLate(request, tableDef, accountId, ctx)
		if projectLate {
			queryBuilder.WriteString("(SELECT * FROM ")
		}
		if tabelDefString != "" {
			queryBuilder.WriteString(tabelDefString)
		} else {
			queryBuilder.WriteString(request.Table)
		}

		if tableDef.UpdateFilters != nil {
			request, err = tableDef.UpdateFilters(ctx, request)
			if err != nil {
				return "", err
			}
		}
		whereStr, err := generateWhereClause(request.Where, tableDef)
		if err != nil {
			return "", err
		}
		if accountScope != "" {
			if whereStr != "" {
				whereStr = "(" + whereStr + ") AND (" + accountScope + ")"
			} else {
				whereStr = "(" + accountScope + ")"
			}
		}
		if whereStr != "" {
			queryBuilder.WriteString(" WHERE ")
			queryBuilder.WriteString(whereStr)
		}

		groupStr, err := generateGroupClause(request, tableDef, accountId, ctx)
		if err != nil {
			return "", err
		}
		if groupStr != "" {
			queryBuilder.WriteString(" GROUP BY ")
			queryBuilder.WriteString(groupStr)
		}

		if request.Having.And != nil || request.Having.Or != nil || request.Having.Not != nil || request.Having.Binary != nil {
			havingStr, err := generateWhereClause(request.Having, tableDef, true)
			if err != nil {
				return "", err
			}
			queryBuilder.WriteString(" HAVING ")
			queryBuilder.WriteString(havingStr)
		}

		sortStr, err := generateOrderByClause(request, tableDef)
		if err != nil {
			return "", err
		}
		if sortStr != "" {
			queryBuilder.WriteString(" ORDER BY ")
			queryBuilder.WriteString(sortStr)
		}

		limitStr, err := generateLimitOffsetClause(request, tableDef)
		if err != nil {
			return "", err
		}
		if limitStr != "" {
			queryBuilder.WriteString(limitStr)
		}
		if projectLate {
			// A subquery's row order is not guaranteed to survive, so the
			// ordering is repeated on the page.
			queryBuilder.WriteString(") AS ")
			queryBuilder.WriteString(tableDef.LateProjectionAlias)
			queryBuilder.WriteString(" ORDER BY ")
			queryBuilder.WriteString(sortStr)
		}

	}
	return queryBuilder.String(), nil
}

// canProjectLate reports whether request can be answered by selecting its page
// of rows first and applying the select list to that page (see
// TableDefinition.LateProjectionAlias).
//
// It holds only for a plain list: no aggregates, and an ORDER BY whose columns
// all sort on the bare column. ORDER BY names the output column, so a column
// the select list wraps (a string cast, a Def, a date truncation) orders by the
// wrapped value; inside the page there is only the bare column, and ordering
// by it could pick different rows.
func canProjectLate(request QueryRequest, tableDef TableDefinition, accountId string, ctx *security.RequestContext) bool {
	if tableDef.LateProjectionAlias == "" || tableDef.Type != Normal || len(request.OrderBy) == 0 || hasFilters(request.Having) {
		return false
	}
	selected := make(map[string]QueryColumn, len(request.Columns))
	for _, column := range request.Columns {
		if tableDef.Columns[column.Name].IsAggregated {
			return false
		}
		selected[column.Name] = column
	}
	if len(request.Columns) == 0 {
		for _, columnDef := range tableDef.Columns {
			if columnDef.IsAggregated {
				return false
			}
		}
	}
	for _, sortBy := range request.OrderBy {
		columnDef, ok := tableDef.Columns[sortBy.Column]
		if !ok {
			return false
		}
		column, ok := selected[sortBy.Column]
		if !ok {
			column = QueryColumn{Name: sortBy.Column}
		}
		if generateColumnExpression(columnDef, column, tableDef, accountId, ctx) != sortBy.Column {
			return false
		}
	}
	return true
}

// ErrQueryCancelled marks a table query that stopped because the caller that asked
// for it went away, as opposed to one that failed. Nobody reads the answer, so it
// is not an error worth alarming on; callers can tell the two apart with errors.Is.
var ErrQueryCancelled = errors.New("query cancelled by caller")

// sqlQueryParent picks the context a table query runs under.
//
// With app_database_query_cancel_on_disconnect on, it is the caller's own context:
// when a browser abandons a request the gateway drops its upstream call, Gin
// cancels the request context, and the driver cancels the statement, so the query
// stops holding a pool connection for an answer nobody will read (#39740 measured
// 13.7 s of that for a call the gateway had given up on after 1.1 s).
//
// Off (the default), it is context.Background(), which is what this path always
// used. The switch exists because cancelling is not free: lib/pq dials a second
// connection to send the cancel and discards the pooled one, so a wave of
// abandoned requests turns into connection churn against a 20-connection pool.
// Measure that on an environment before turning it on there.
func sqlQueryParent(caller context.Context) context.Context {
	if caller == nil || !config.Config.ServiceDBQueryCancelOnDisconnect {
		return context.Background()
	}
	return caller
}

// sqlQueryContext bounds a single RPC query, covering both the wait for a pool
// connection and the query itself. Without the bound a goroutine queued for a
// connection never gives up.
//
// This query drains its rows into a slice and never issues a nested query, so it
// only ever waits on the pool — it cannot be the handler that starves it. The
// bound therefore makes these requests fail fast instead of hanging; it does not
// by itself free a pool held by handlers that query while iterating rows (those
// need their own bound, or the nesting removed). Login goes through this path, so
// failing fast here is the difference between a clear error and a dead login page.
//
// app_database_query_timeout_seconds defaults to 120: well above the slowest
// queries we actually see (11-21s routinely, ~27s for a cold finops recompute —
// see #34921) and well under the 300s the frontend waits before giving up, so a
// starved pool surfaces as errors rather than a hang. Set it to 0 to remove the
// ceiling; the parent's own cancellation still applies.
func sqlQueryContext(parent context.Context) (context.Context, context.CancelFunc) {
	seconds := config.Config.ServiceDBQueryTimeoutSeconds
	if seconds <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, time.Duration(seconds)*time.Second)
}

// cancelledOr reports err as ErrQueryCancelled when the caller's context is done.
// It asks the context rather than inspecting err: lib/pq answers a statement
// cancelled mid-flight with its own *pq.Error (SQLSTATE 57014), not
// context.Canceled, so errors.Is(err, context.Canceled) misses the common case.
//
// The result also wraps the context's own error, so a generic
// errors.Is(err, context.Canceled) check further up still recognises it.
func cancelledOr(parent context.Context, err error) error {
	if ctxErr := parent.Err(); ctxErr != nil {
		return fmt.Errorf("%w (%w): %v", ErrQueryCancelled, ctxErr, err)
	}
	return err
}

func executeSqlQuery(caller context.Context, source database.DatabaseManagerType, query string, args []any, limit int) ([]QueryRow, error) {

	if source == "" {
		return nil, fmt.Errorf("source not found")
	}

	databaseManager, err := database.GetDatabaseManager(source)
	if err != nil {
		return nil, err
	}

	parent := sqlQueryParent(caller)
	ctx, cancel := sqlQueryContext(parent)
	defer cancel()

	rows, err := databaseManager.Db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, cancelledOr(parent, err)
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("sql: unable to close rows", "error", err)
		}
	}()

	// start with default initial capacity
	if limit <= 0 {
		limit = 1000
	}
	rowsMap := make([]QueryRow, 0, limit)
	columns, err := rows.Columns()
	if err != nil {
		slog.Error("sql: unable to get columns", "error", err)
		return nil, cancelledOr(parent, err)
	}
	colsLen := len(columns)
	for rows.Next() {
		var row = make(map[string]any, colsLen)
		err = rows.MapScan(row)
		if err != nil {
			return nil, cancelledOr(parent, err)
		}
		rowsMap = append(rowsMap, row)
	}
	if err := rows.Err(); err != nil {
		return nil, cancelledOr(parent, fmt.Errorf("error during sql query rows iteration: %w", err))
	}

	return rowsMap, nil
}

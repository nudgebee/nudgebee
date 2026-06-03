package query

import (
	"fmt"
	"strings"
)

type KqlGenerator struct {
}

func (m KqlGenerator) generateKqlColumnExpression(columnDef ColumnDefinition, column QueryColumn, tableDef TableDefinition, where_col bool) string {
	var dialect kqlDialect
	switch columnDef.Type {
	case "datetime":
		dateUnit := "day"
		if column.Expr == "date_unit" && len(column.Args) > 0 {
			dateUnit = column.Args[0]
		}
		return dialect.FuncDateTruncate(dateUnit, column.Name)

	case "string":
		return fmt.Sprintf("tostring(%s)", column.Name)

	case "json":
		// KQL stores JSON as dynamic
		return fmt.Sprintf("tostring(%s)", column.Name)

	case "float":
		return fmt.Sprintf("todouble(%s)", column.Name)

	case "integer":
		return fmt.Sprintf("toint(%s)", column.Name)

	default:
		return column.Name
	}
}

func (m KqlGenerator) generateKqlSelectClause(request QueryRequest, tableDef TableDefinition) (string, error) {
	if len(request.Columns) == 0 {
		cols := []string{}
		for name, colDef := range tableDef.Columns {
			cols = append(cols, m.generateKqlColumnExpression(colDef, QueryColumn{Name: name}, tableDef, false))
		}
		return "project " + strings.Join(cols, ", "), nil
	}

	parts := []string{}
	for _, col := range request.Columns {
		if colDef, ok := tableDef.Columns[col.Name]; ok {
			columnExp := m.generateKqlColumnExpression(colDef, col, tableDef, true)
			if columnExp == "" {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s = %s", col.Name, columnExp))
		} else {
			return "", fmt.Errorf("column %s not found", col.Name)
		}
	}
	return "project " + strings.Join(parts, ", "), nil
}

func (m KqlGenerator) generateKqlWhereClause(where QueryWhereClause, tableDef TableDefinition) (string, error) {
	var parts []string

	// Example: handle binary expressions
	for col, binary := range where.Binary {
		colDef, ok := tableDef.Columns[col]
		if !ok {
			continue
		}
		colType := colDef.Type
		if colDef.Def != "" {
			col = colDef.Def
		}
		for op, val := range binary {
			switch op {
			case Between:
				vals := val.(map[string]any)
				parts = append(parts, fmt.Sprintf("%s >= %s(%v)", col, colType, vals["_gte"]))
				parts = append(parts, fmt.Sprintf("%s <= %s(%v)", col, colType, vals["_lte"]))
			case Eq:
				parts = append(parts, fmt.Sprintf("%s == '%v'", col, val))
			case Nq:
				parts = append(parts, fmt.Sprintf("%s != '%v'", col, val))
			case Lte:
				parts = append(parts, fmt.Sprintf("%s <= %s(%v)", col, colType, val))
			case Lt:
				parts = append(parts, fmt.Sprintf("%s < %s(%v)", col, colType, val))
			case Gte:
				parts = append(parts, fmt.Sprintf("%s >= %s(%v)", col, colType, val))
			case Gt:
				parts = append(parts, fmt.Sprintf("%s > %s(%v)", col, colType, val))
			case Like, ILike:
				parts = append(parts, fmt.Sprintf("%s contains '%v'", col, val))
			case In, NotIn:
				var strValues []string
				switch v := val.(type) {
				case []string:
					strValues = v
				case []interface{}:
					strValues = make([]string, len(v))
					for i, item := range v {
						s, ok := item.(string)
						if !ok {
							return "", fmt.Errorf("value in 'in' clause for column '%s' is not a string: %v", col, item)
						}
						strValues[i] = s
					}
				default:
					return "", fmt.Errorf("unsupported type for 'in' clause on column '%s': %T", col, val)
				}
				if len(strValues) > 0 {
					inOp := "in"
					if op == NotIn {
						inOp = "!in"
					}
					parts = append(parts, fmt.Sprintf("%s %s (%s)", col, inOp, "'"+strings.Join(strValues, "','")+"'"))
				}
			}
		}
	}

	if len(parts) > 0 {
		return "where " + strings.Join(parts, " and "), nil
	}
	return "", nil
}

func (m KqlGenerator) generateKqlSummaryClause(col []QueryColumn, tableDef TableDefinition) (string, error) {
	if len(col) == 1 {
		if col[0].Name == "count" {
			return "count", nil
		}
	}
	return "", nil
}

func (m KqlGenerator) generateKqlGroupClause(request QueryRequest, tableDef TableDefinition) (string, error) {
	if len(request.GroupBy) == 0 {
		return "", nil
	}
	defGroupBy := []string{}
	for _, col := range request.GroupBy {
		if colDef, ok := tableDef.Columns[col]; ok {
			if colDef.Def != "" {
				defGroupBy = append(defGroupBy, colDef.Def)
			} else {
				defGroupBy = append(defGroupBy, col)
			}
		} else {
			return "", fmt.Errorf("group by column %s not found", col)
		}
	}

	return "summarize count() by " + strings.Join(defGroupBy, ", "), nil
}

func (m KqlGenerator) generateKqlOrderClause(request QueryRequest, tableDef TableDefinition) string {
	if len(request.OrderBy) == 0 {
		return ""
	}
	parts := []string{}
	for _, o := range request.OrderBy {
		colDef, ok := tableDef.Columns[o.Column]
		if !ok {
			continue
		}
		if colDef.Def != "" {
			o.Column = colDef.Def
		}
		dir := "asc"
		if o.Order == Desc {
			dir = "desc"
		}
		parts = append(parts, fmt.Sprintf("%s %s", o.Column, dir))
	}
	if len(parts) > 0 {
		return "order by " + strings.Join(parts, ", ")
	}
	return ""
}
func (m KqlGenerator) GenerateKqlQuery(request QueryRequest, tableDef TableDefinition) (string, error) {
	var query strings.Builder
	query.WriteString(tableDef.Def) // table name

	// WHERE
	whereStr, _ := m.generateKqlWhereClause(request.Where, tableDef)
	if whereStr != "" {
		query.WriteString(" | ")
		query.WriteString(whereStr)
	}

	// GROUP BY
	groupStr, err := m.generateKqlGroupClause(request, tableDef)
	if groupStr != "" {
		query.WriteString(" | ")
		query.WriteString(groupStr)
	}
	if err != nil {
		return "", err
	}

	// PROJECT
	// selectStr, _ := m.generateKqlSelectClause(request, tableDef)
	// if selectStr != "" {
	// 	query.WriteString(" | ")
	// 	query.WriteString(selectStr)
	// }

	// ORDER BY
	orderStr := m.generateKqlOrderClause(request, tableDef)
	if orderStr != "" {
		query.WriteString(" | ")
		query.WriteString(orderStr)
	}
	// Aggregation count
	countStr, _ := m.generateKqlSummaryClause(request.Columns, tableDef)
	if countStr != "" {
		query.WriteString(" | ")
		query.WriteString(countStr)
	}

	// LIMIT/OFFSET
	limitOffsetStr := m.LimitOffset(request)
	if limitOffsetStr != "" {
		query.WriteString(" | ")
		query.WriteString(limitOffsetStr)
	}

	return query.String(), nil
}

func (d *KqlGenerator) Limit(request QueryRequest) string {
	// KQL equivalent of LIMIT
	return fmt.Sprintf("| take %d", request.Limit)
}

func (d *KqlGenerator) Offset(request QueryRequest) string {
	// KQL doesn't support OFFSET directly, but we can simulate with skip
	// Requires serialize first in query pipeline
	return fmt.Sprintf("| skip %d", request.Offset)
}

func (d *KqlGenerator) LimitOffset(request QueryRequest) string {
	// Combined usage: serialize, skip, then take
	offset := ""
	limit := ""
	if request.Offset > 0 {
		offset = fmt.Sprintf(" serialize rn = row_number() | where rn >= %d ", request.Offset+1)
	}
	if request.Limit > 0 {
		limit = fmt.Sprintf(" take %d", request.Limit)
	}
	finalQuery := ""
	if offset != "" {
		finalQuery += offset
	}
	if limit != "" {
		if finalQuery != "" {
			finalQuery += " | "
		}
		finalQuery += limit
	}

	return finalQuery
}

package query

import (
	"fmt"
	"strings"
)

type postgresDialect struct {
}

func (d *postgresDialect) QuoteIdentifier(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func (d *postgresDialect) QuoteLiteral(s any) string {
	val := fmt.Sprintf("%v", s)
	// Postgres supports standard SQL string literals where single quotes are escaped by doubling them.
	return fmt.Sprintf("'%s'", strings.ReplaceAll(val, "'", "''"))
}

func (d *postgresDialect) FuncDateTruncate(dateUnit string, columnName string) string {
	cleanUnit := strings.ToLower(strings.TrimSpace(dateUnit))
	if !IsValidDateUnit(cleanUnit) {
		cleanUnit = "day"
	}
	return "DATE_TRUNC('" + cleanUnit + "', " + columnName + ")"
}

func (d *postgresDialect) FuncStringToDatetime(value string) string {
	return value
}

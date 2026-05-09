package query

import (
	"fmt"
	"strings"
)

type clickhouseDialect struct {
}

func (d *clickhouseDialect) QuoteIdentifier(s string) string {
	return "`" + s + "`"
}

func (d *clickhouseDialect) QuoteLiteral(s any) string {
	val := fmt.Sprintf("%v", s)
	// ClickHouse treats backslash as an escape character.
	// We must escape backslashes first, then single quotes.
	val = strings.ReplaceAll(val, "\\", "\\\\")
	val = strings.ReplaceAll(val, "'", "\\'")
	return fmt.Sprintf("'%s'", val)
}

func (d *clickhouseDialect) FuncDateTruncate(dateUnit string, columnName string) string {
	return "DATE_TRUNC('" + dateUnit + "', " + columnName + ")"
}

func (d *clickhouseDialect) FuncStringToDatetime(value string) string {
	return "parseDateTimeBestEffort(" + value + ")"
}

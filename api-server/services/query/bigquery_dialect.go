package query

import (
	"fmt"
	"strings"
)

type bigQueryDialect struct {
}

func (d *bigQueryDialect) QuoteIdentifier(s string) string {
	// BigQuery uses backticks for identifiers (same as ClickHouse)
	return "`" + s + "`"
}

func (d *bigQueryDialect) QuoteLiteral(s any) string {
	val := fmt.Sprintf("%v", s)
	// BigQuery treats backslash as an escape character.
	// We must escape backslashes first, then single quotes.
	val = strings.ReplaceAll(val, "\\", "\\\\")
	val = strings.ReplaceAll(val, "'", "\\'")
	return fmt.Sprintf("'%s'", val)
}

func (d *bigQueryDialect) FuncDateTruncate(dateUnit string, columnName string) string {
	// BigQuery uses DATETIME_TRUNC or DATE_TRUNC depending on data type
	// You could adjust this logic if you know the column type
	return fmt.Sprintf("DATE_TRUNC(%s, %s)", columnName, strings.ToUpper(dateUnit))
}

func (d *bigQueryDialect) FuncStringToDatetime(value string) string {
	// BigQuery expects TIMESTAMPs for time comparisons
	// Assume ISO8601 input (e.g., '2025-04-25T03:59:21.843Z')
	// Need to REPLACE 'T' with ' ' and 'Z' with ' UTC' and then cast as TIMESTAMP
	return fmt.Sprintf("TIMESTAMP(%s)", value)
}

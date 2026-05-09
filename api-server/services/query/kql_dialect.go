package query

import "fmt"

type kqlDialect struct {
}

func (d *kqlDialect) QuoteIdentifier(s string) string {
	// KQL identifiers don’t need quoting like SQL
	// But keeping it consistent in case you want brackets []
	return s
}

func (d *kqlDialect) QuoteLiteral(s any) string {
	// KQL string literals are wrapped in single quotes
	return fmt.Sprintf("'%v'", s)
}

func (d *kqlDialect) FuncDateTruncate(dateUnit string, columnName string) string {
	// KQL uses bin() for bucketing datetime columns
	unit := "1d"
	switch dateUnit {
	case "hour":
		unit = "1h"
	case "minute":
		unit = "1m"
	case "second":
		unit = "1s"
	}
	return fmt.Sprintf("bin(%s, %s)", columnName, unit)
}

func (d *kqlDialect) FuncStringToDatetime(value string) string {
	// Convert string to datetime in KQL
	return fmt.Sprintf("todatetime(%s)", value)
}

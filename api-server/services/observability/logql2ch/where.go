package logql2ch

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"nudgebee/services/query"
)

// bodyFields are where-clause fields that filter the log line itself. FetchLogs
// maps content/message onto `log` (LogsFilterMap); the others are accepted so a
// caller that skips the mapping still lands on Body.
var bodyFields = map[string]bool{"log": true, "content": true, "message": true, "body": true, "_body": true, "line": true}

// WhereCond compiles a canonical where-clause (Builder mode, nubi, event rules)
// straight to SQL, without rendering it as LogQL first, so `_in`, a cross-field
// `_or` and `_not` keep their meaning. Fields are the labels Labels() returns,
// a body field (`log`), `trace_id` and `timestamp`; anything else is refused
// with the same error as an unknown LogQL label.
func WhereCond(where query.QueryWhereClause, schema Schema) (Cond, error) {
	sql, err := whereSQL(where, schema)
	if err != nil {
		return Cond{}, err
	}
	if sql == "1" {
		return Cond{}, nil
	}
	return Cond{sql: sql}, nil
}

func whereSQL(w query.QueryWhereClause, schema Schema) (string, error) {
	var conds []string
	fields := make([]string, 0, len(w.Binary))
	for f := range w.Binary {
		fields = append(fields, f)
	}
	sort.Strings(fields) // deterministic SQL
	for _, f := range fields {
		ops := make([]string, 0, len(w.Binary[f]))
		for op := range w.Binary[f] {
			ops = append(ops, string(op))
		}
		sort.Strings(ops)
		for _, op := range ops {
			c, skip, err := fieldCond(f, query.BinaryWhereClauseType(op), w.Binary[f][query.BinaryWhereClauseType(op)], schema)
			if err != nil {
				return "", err
			}
			if !skip {
				conds = append(conds, c)
			}
		}
	}
	for _, sub := range w.And {
		c, err := whereSQL(sub, schema)
		if err != nil {
			return "", err
		}
		if c != "1" {
			conds = append(conds, parenthesize(c))
		}
	}
	if len(w.Or) > 0 {
		var alts []string
		for _, sub := range w.Or {
			c, err := whereSQL(sub, schema)
			if err != nil {
				return "", err
			}
			alts = append(alts, parenthesize(c))
		}
		conds = append(conds, "("+strings.Join(alts, " OR ")+")")
	}
	if w.Not != nil {
		c, err := whereSQL(*w.Not, schema)
		if err != nil {
			return "", err
		}
		// An empty clause filters nothing, and so does its negation.
		if c != "1" {
			conds = append(conds, "NOT ("+c+")")
		}
	}
	return whereOf(conds), nil
}

func parenthesize(c string) string {
	if c == "1" || wrapped(c) || !strings.Contains(c, " AND ") && !strings.Contains(c, " OR ") {
		return c
	}
	return "(" + c + ")"
}

// wrapped reports whether c is one parenthesized group, e.g. "(a OR b)" but
// not "(a) AND (b)". Parentheses inside string literals are skipped.
func wrapped(c string) bool {
	if !strings.HasPrefix(c, "(") || !strings.HasSuffix(c, ")") {
		return false
	}
	depth, inStr := 0, false
	for i := 0; i < len(c); i++ {
		switch ch := c[i]; {
		case inStr && ch == '\\':
			i++
		case ch == '\'':
			inStr = !inStr
		case inStr:
		case ch == '(':
			depth++
		case ch == ')':
			depth--
			if depth == 0 && i != len(c)-1 {
				return false
			}
		}
	}
	return depth == 0
}

// fieldCond renders one field/operator pair. skip is true for a condition that
// filters nothing (an empty case-insensitive substring, as Loki skips it).
func fieldCond(field string, op query.BinaryWhereClauseType, value any, schema Schema) (string, bool, error) {
	switch {
	case bodyFields[field]:
		return bodyCond(op, value)
	case field == "trace_id":
		c, err := traceIDCond(op, value)
		return c, false, err
	case field == "timestamp":
		c, err := timestampCond(op, value)
		return c, false, err
	}
	expr, ok := LabelExpr(field, schema)
	if !ok {
		return "", false, unknownLabelError(field)
	}
	isLevel := field == "level"
	s := fmt.Sprint(value)
	if isLevel && (op == query.Eq || op == query.Nq) {
		var err error
		if s, err = levelValue(s); err != nil {
			return "", false, err
		}
	}
	switch op {
	case query.Eq:
		return expr + " = " + quote(s), false, nil
	case query.Nq:
		return expr + " != " + quote(s), false, nil
	case query.In, query.NotIn:
		vals, err := stringList(value)
		if err != nil {
			return "", false, err
		}
		if isLevel {
			for i, v := range vals {
				if vals[i], err = levelValue(v); err != nil {
					return "", false, err
				}
			}
		}
		return inCond(expr, vals, op == query.NotIn), false, nil
	case query.Regex, query.NRegex:
		// Label regexes are anchored, as the Loki builder's `=~` is.
		if _, err := regexp.Compile("^(?:" + fmt.Sprint(value) + ")$"); err != nil {
			return "", false, badRequestf("bad regex %q: %v", value, err)
		}
		lop := "=~"
		if op == query.NRegex {
			lop = "!~"
		}
		c, err := matchCond(expr, isLevel, lop, fmt.Sprint(value), 0)
		return c, false, err
	}
	if isLevel {
		// level holds a fixed set of words; a pattern written with Loki's
		// spellings (warn, fatal) would silently match nothing.
		return "", false, badRequestf("operator %s is not supported on level; use _eq, _in or a list of values", op)
	}
	switch op {
	case query.Like:
		return expr + " LIKE " + quote(s), false, nil
	case query.NLike:
		return expr + " NOT LIKE " + quote(s), false, nil
	case query.ILike:
		return expr + " ILIKE " + quote(s), false, nil
	case query.Contains:
		return "position(" + expr + ", " + quote(s) + ") > 0", false, nil
	case query.IContains:
		return "positionCaseInsensitive(" + expr + ", " + quote(s) + ") > 0", false, nil
	case query.NIContains:
		return "positionCaseInsensitive(" + expr + ", " + quote(s) + ") = 0", false, nil
	}
	return "", false, badRequestf("operator %s is not supported on label %q", op, field)
}

// bodyCond follows the Loki builder's meaning for the log field: _eq is a
// substring match (a `|=` line filter), not equality with the whole line.
func bodyCond(op query.BinaryWhereClauseType, value any) (string, bool, error) {
	s := fmt.Sprint(value)
	switch op {
	case query.Eq, query.Contains:
		return "position(Body, " + quote(s) + ") > 0", false, nil
	case query.Nq:
		return "position(Body, " + quote(s) + ") = 0", false, nil
	case query.IContains, query.NIContains:
		if s == "" {
			return "", true, nil
		}
		cmp := " > 0"
		if op == query.NIContains {
			cmp = " = 0"
		}
		return "positionCaseInsensitive(Body, " + quote(s) + ")" + cmp, false, nil
	case query.Like:
		return "Body LIKE " + quote(s), false, nil
	case query.NLike:
		return "Body NOT LIKE " + quote(s), false, nil
	case query.ILike:
		return "Body ILIKE " + quote(s), false, nil
	case query.Regex, query.NRegex:
		if _, err := regexp.Compile(s); err != nil {
			return "", false, badRequestf("bad regex %q: %v", value, err)
		}
		c := "match(Body, " + quote("(?-s)"+s) + ")"
		if op == query.NRegex {
			c = "NOT " + c
		}
		return c, false, nil
	case query.In, query.NotIn:
		vals, err := stringList(value)
		if err != nil {
			return "", false, err
		}
		if len(vals) == 0 {
			return map[bool]string{false: "0", true: "1"}[op == query.NotIn], false, nil
		}
		c := "multiSearchAny(Body, [" + quoteAll(vals) + "])"
		if op == query.NotIn {
			c = "NOT " + c
		}
		return c, false, nil
	}
	return "", false, badRequestf("operator %s is not supported on the log line", op)
}

// traceIDCond matches the TraceId column or, for a log that carries the id
// only in its text (container logs), the line itself.
func traceIDCond(op query.BinaryWhereClauseType, value any) (string, error) {
	switch op {
	case query.Eq:
		s := quote(fmt.Sprint(value))
		return "(TraceId = " + s + " OR position(Body, " + s + ") > 0)", nil
	case query.Nq:
		s := quote(fmt.Sprint(value))
		return "(TraceId != " + s + " AND position(Body, " + s + ") = 0)", nil
	case query.In, query.NotIn:
		vals, err := stringList(value)
		if err != nil {
			return "", err
		}
		if len(vals) == 0 {
			return map[bool]string{false: "0", true: "1"}[op == query.NotIn], nil
		}
		list := quoteAll(vals)
		if op == query.NotIn {
			return "(TraceId NOT IN (" + list + ") AND NOT multiSearchAny(Body, [" + list + "]))", nil
		}
		return "(TraceId IN (" + list + ") OR multiSearchAny(Body, [" + list + "]))", nil
	}
	return "", badRequestf("operator %s is not supported on trace_id", op)
}

func timestampCond(op query.BinaryWhereClauseType, value any) (string, error) {
	cmp := map[query.BinaryWhereClauseType]string{query.Gt: ">", query.Gte: ">=", query.Lt: "<", query.Lte: "<="}[op]
	if cmp == "" {
		return "", badRequestf("operator %s is not supported on timestamp", op)
	}
	return "Timestamp " + cmp + " parseDateTime64BestEffort(" + quote(fmt.Sprint(value)) + ", 9)", nil
}

func inCond(expr string, vals []string, negate bool) string {
	if len(vals) == 0 {
		if negate {
			return "1"
		}
		return "0"
	}
	in := " IN ("
	if negate {
		in = " NOT IN ("
	}
	return expr + in + quoteAll(vals) + ")"
}

func quoteAll(vals []string) string {
	q := make([]string, len(vals))
	for i, v := range vals {
		q[i] = quote(v)
	}
	return strings.Join(q, ", ")
}

func stringList(v any) ([]string, error) {
	switch x := v.(type) {
	case []string:
		return slices.Clone(x), nil
	case []any:
		out := make([]string, len(x))
		for i, e := range x {
			out[i] = fmt.Sprint(e)
		}
		return out, nil
	case string:
		return []string{x}, nil
	}
	return nil, badRequestf("expected a list of values, got %T", v)
}

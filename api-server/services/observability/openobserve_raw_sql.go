package observability

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"nudgebee/services/integrations"
)

type openObserveSQLToken struct {
	text  string
	kind  byte
	depth int
	end   int
}

type openObserveRawSelect struct {
	where    string
	order    string
	limit    int
	hasLimit bool
}

const openObserveRawQueryMaxBytes = 16 * 1024

// buildOpenObserveRawSQL permits only the typed source's SELECT * shape. Trusted
// structured filters are ANDed into the raw predicate before the query is executed.
func buildOpenObserveRawSQL(raw, stream, enforcedWhere, defaultOrder string, maxRows int) (string, error) {
	parsed, err := parseOpenObserveRawSelect(raw, stream)
	if err != nil {
		return "", err
	}
	where := parsed.where
	if enforcedWhere != "" {
		if where != "" {
			where = "(" + where + ") AND (" + enforcedWhere + ")"
		} else {
			where = enforcedWhere
		}
	}
	if parsed.order == "" {
		parsed.order = defaultOrder
	}
	if maxRows <= 0 || maxRows > 10000 {
		maxRows = 10000
	}
	limit := maxRows
	if parsed.hasLimit && parsed.limit < limit {
		limit = parsed.limit
	}
	if where == "" {
		return fmt.Sprintf(`SELECT * FROM "%s" ORDER BY %s LIMIT %d`, stream, parsed.order, limit), nil
	}
	return fmt.Sprintf(`SELECT * FROM "%s" WHERE %s ORDER BY %s LIMIT %d`, stream, where, parsed.order, limit), nil
}

func parseOpenObserveRawSelect(raw, stream string) (openObserveRawSelect, error) {
	if len(raw) > openObserveRawQueryMaxBytes {
		return openObserveRawSelect{}, fmt.Errorf("openobserve: query exceeds %d bytes", openObserveRawQueryMaxBytes)
	}
	if !integrations.IsSafeOpenObserveStreamName(stream) {
		return openObserveRawSelect{}, fmt.Errorf("openobserve: invalid configured stream")
	}
	tokens, err := lexOpenObserveSQL(strings.TrimSpace(raw))
	if err != nil {
		return openObserveRawSelect{}, err
	}
	if len(tokens) < 4 || !openObserveWord(tokens[0], "select") || tokens[0].depth != 0 || tokens[1].text != "*" || tokens[1].depth != 0 || !openObserveWord(tokens[2], "from") || tokens[2].depth != 0 {
		return openObserveRawSelect{}, fmt.Errorf("openobserve: use SELECT * FROM the configured stream")
	}
	if (tokens[3].kind != 'w' && tokens[3].kind != 'i') || tokens[3].text != stream || tokens[3].depth != 0 {
		return openObserveRawSelect{}, fmt.Errorf("openobserve: SELECT may read only from configured stream %q", stream)
	}
	if len(tokens) > 4 && tokens[4].depth == 0 && !openObserveWord(tokens[4], "where") && !openObserveWord(tokens[4], "order") && !openObserveWord(tokens[4], "limit") {
		return openObserveRawSelect{}, fmt.Errorf("openobserve: aliases and additional clauses are not supported")
	}

	parsed := openObserveRawSelect{}
	pos := 4
	if pos < len(tokens) && openObserveWord(tokens[pos], "where") && tokens[pos].depth == 0 {
		start := tokens[pos].end
		pos++
		end := len(tokens)
		for i := pos; i < len(tokens); i++ {
			if tokens[i].depth == 0 && (openObserveWord(tokens[i], "order") || openObserveWord(tokens[i], "limit")) {
				end = i
				break
			}
		}
		if end == pos {
			return openObserveRawSelect{}, fmt.Errorf("openobserve: WHERE predicate is required")
		}
		parsed.where = strings.TrimSpace(strings.TrimSpace(raw)[start:tokens[end-1].end])
		for i := pos; i < end; i++ {
			if openObserveForbiddenPredicateWord(tokens[i]) {
				return openObserveRawSelect{}, fmt.Errorf("openobserve: unsupported SQL clause %q", tokens[i].text)
			}
		}
		pos = end
	}
	if pos < len(tokens) && openObserveWord(tokens[pos], "order") && tokens[pos].depth == 0 {
		if pos+1 >= len(tokens) || !openObserveWord(tokens[pos+1], "by") || tokens[pos+1].depth != 0 {
			return openObserveRawSelect{}, fmt.Errorf("openobserve: ORDER must be followed by BY")
		}
		start := pos + 2
		end := len(tokens)
		for i := start; i < len(tokens); i++ {
			if tokens[i].depth == 0 && openObserveWord(tokens[i], "limit") {
				end = i
				break
			}
		}
		order, ok := openObserveSimpleOrder(tokens[start:end])
		if !ok {
			return openObserveRawSelect{}, fmt.Errorf("openobserve: ORDER BY supports only field names and ASC/DESC")
		}
		parsed.order = order
		pos = end
	}
	if pos < len(tokens) && openObserveWord(tokens[pos], "limit") && tokens[pos].depth == 0 {
		if pos+1 >= len(tokens) || tokens[pos+1].kind != 'w' || tokens[pos+1].depth != 0 {
			return openObserveRawSelect{}, fmt.Errorf("openobserve: LIMIT must be an integer")
		}
		limit, err := strconv.Atoi(tokens[pos+1].text)
		if err != nil || limit < 0 || strconv.Itoa(limit) != tokens[pos+1].text {
			return openObserveRawSelect{}, fmt.Errorf("openobserve: LIMIT must be a non-negative integer")
		}
		parsed.limit, parsed.hasLimit = limit, true
		pos += 2
	}
	if pos != len(tokens) {
		return openObserveRawSelect{}, fmt.Errorf("openobserve: unsupported or trailing SQL clause")
	}
	if parsed.order == "" {
		parsed.order = "_timestamp DESC"
	}
	return parsed, nil
}

func openObserveSimpleOrder(tokens []openObserveSQLToken) (string, bool) {
	if len(tokens) == 0 {
		return "", false
	}
	var out []string
	for i := 0; i < len(tokens); {
		field := tokens[i]
		if (field.kind != 'w' && field.kind != 'i') || field.depth != 0 || !isSafeIdentifier(field.text) {
			return "", false
		}
		fieldName := field.text
		if field.kind == 'i' {
			fieldName = `"` + fieldName + `"`
		}
		out = append(out, fieldName)
		i++
		if i < len(tokens) && (openObserveWord(tokens[i], "asc") || openObserveWord(tokens[i], "desc")) {
			out = append(out, strings.ToUpper(tokens[i].text))
			i++
		}
		if i == len(tokens) {
			break
		}
		if tokens[i].text != "," || tokens[i].depth != 0 {
			return "", false
		}
		out = append(out, ",")
		i++
	}
	if out[len(out)-1] == "," {
		return "", false
	}
	return strings.Join(out, " "), true
}

func lexOpenObserveSQL(sql string) ([]openObserveSQLToken, error) {
	var tokens []openObserveSQLToken
	depth := 0
	for i := 0; i < len(sql); {
		c := sql[i]
		if unicode.IsSpace(rune(c)) {
			i++
			continue
		}
		if c >= 0x80 {
			return nil, fmt.Errorf("openobserve: non-ASCII identifiers are not supported")
		}
		if c == '\'' || c == '"' {
			quote, start := c, i
			i++
			for i < len(sql) {
				if sql[i] == '\\' {
					return nil, fmt.Errorf("openobserve: backslash escape strings are not supported")
				}
				if sql[i] == quote {
					if i+1 < len(sql) && sql[i+1] == quote {
						i += 2
						continue
					}
					kind := byte('i')
					if quote == '\'' {
						kind = 's'
					}
					tokens = append(tokens, openObserveSQLToken{sql[start+1 : i], kind, depth, i + 1})
					i++
					goto next
				}
				i++
			}
			return nil, fmt.Errorf("openobserve: unterminated quoted value")
		}
		if c == '-' && i+1 < len(sql) && sql[i+1] == '-' || c == '/' && i+1 < len(sql) && sql[i+1] == '*' {
			return nil, fmt.Errorf("openobserve: SQL comments are not supported")
		}
		if c == ';' || c == '$' || c == '`' || c == '#' {
			return nil, fmt.Errorf("openobserve: semicolons, comments, dollar quoting, and backticks are not supported")
		}
		if c == '(' {
			depth++
			tokens = append(tokens, openObserveSQLToken{"(", 'p', depth, i + 1})
			i++
			continue
		}
		if c == ')' {
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("openobserve: unbalanced parentheses")
			}
			tokens = append(tokens, openObserveSQLToken{")", 'p', depth, i + 1})
			i++
			continue
		}
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			start := i
			for i < len(sql) && (sql[i] == '_' || sql[i] >= 'a' && sql[i] <= 'z' || sql[i] >= 'A' && sql[i] <= 'Z' || sql[i] >= '0' && sql[i] <= '9') {
				i++
			}
			tokens = append(tokens, openObserveSQLToken{sql[start:i], 'w', depth, i})
			continue
		}
		tokens = append(tokens, openObserveSQLToken{string(c), 'p', depth, i + 1})
		i++
		continue
	next:
	}
	if depth != 0 {
		return nil, fmt.Errorf("openobserve: unbalanced parentheses")
	}
	return tokens, nil
}

func openObserveWord(token openObserveSQLToken, word string) bool {
	return token.kind == 'w' && strings.EqualFold(token.text, word)
}

func openObserveForbiddenPredicateWord(token openObserveSQLToken) bool {
	if token.kind != 'w' {
		return false
	}
	switch strings.ToLower(token.text) {
	case "select", "from", "with", "union", "intersect", "except", "join", "lateral", "group", "having", "window", "qualify", "offset", "into", "insert", "update", "delete", "drop", "alter", "create", "truncate", "copy", "attach", "detach", "pragma", "show", "explain", "describe", "returning":
		return true
	default:
		return false
	}
}

package tools

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/google/shlex"

	"nudgebee/llm/tools/core"
)

// The database query tools (Postgres, MySQL, MSSQL, Oracle, ClickHouse) run
// SQL on the customer's database. Which statements are allowed there is up to
// the customer's database user; what this decides is only whether a person is
// asked first. The input is a read when every statement is plainly a query,
// an update when a statement plainly writes, and "" (the LLM prompt decides)
// when it is unclear.

// sqlReadStatements start a statement that only reads.
var sqlReadStatements = wordSet("select", "show", "describe", "desc", "values", "table")

// sqlWriteWords start a statement that writes. Found later inside a query
// (SELECT ... INTO, FOR UPDATE, a data-modifying CTE, or just a column named
// "comment") they make the input unclear.
var sqlWriteWords = wordSet(
	"insert", "update", "delete", "merge", "upsert", "replace", "truncate",
	"drop", "alter", "create", "rename", "grant", "revoke", "into",
	"call", "exec", "execute", "do", "copy", "load", "import", "export",
	"vacuum", "reindex", "cluster", "refresh", "lock", "set", "reset",
	"comment", "discard", "begin", "commit", "rollback", "savepoint",
	"kill", "system", "optimize", "attach", "detach", "flush", "purge",
	"handler", "listen", "notify", "prepare", "deallocate", "checkpoint",
)

// sqlWriteFunctions change server state from inside a query.
var sqlWriteFunctions = regexp.MustCompile(`(?i)\b(pg_terminate_backend|pg_cancel_backend|pg_reload_conf|pg_rotate_logfile|pg_switch_wal|pg_create_restore_point|pg_promote|set_config|nextval|setval|lo_import|lo_export|lo_unlink|dblink\w*|pg_advisory\w*|xp_\w+|sp_\w+|dbms_\w+|utl_\w+|sys_exec|sys_eval)\s*\(`)

var sqlWordPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_$]*`)

// inferSQLRequestType classifies a database tool's input: update if any
// statement plainly writes, "" if any is unclear, read otherwise.
func inferSQLRequestType(input string) core.ToolRequestType {
	query := sqlQueryFromToolInput(input)
	if strings.TrimSpace(query) == "" {
		return ""
	}
	statements := splitSQLStatements(stripSQLLiterals(query))
	if statements == nil {
		return ""
	}
	result := core.ToolRequestTypeRead
	for _, statement := range statements {
		switch classifySQLStatement(statement) {
		case core.ToolRequestTypeUpdate:
			return core.ToolRequestTypeUpdate
		case "":
			result = ""
		}
	}
	return result
}

// sqlQueryFromToolInput returns the SQL the tool will run: the "command" or
// "query" field of a JSON input (in the order the tools read them), the -e
// value of a full mysql command, or the input itself.
func sqlQueryFromToolInput(input string) string {
	trimmed := strings.TrimSpace(input)
	if strings.HasPrefix(trimmed, "{") {
		var fields map[string]any
		if json.Unmarshal([]byte(trimmed), &fields) == nil {
			for _, key := range []string{"command", "query"} {
				if value, ok := fields[key].(string); ok {
					return sqlQueryFromToolInput(value)
				}
			}
			return ""
		}
	}
	if first := strings.Fields(trimmed); len(first) > 0 && (first[0] == "mysql" || first[0] == "mariadb") {
		words, err := shlex.Split(trimmed)
		if err != nil {
			return ""
		}
		for i, word := range words {
			if (word == "-e" || word == "--execute") && i+1 < len(words) {
				return words[i+1]
			}
			if value, ok := strings.CutPrefix(word, "--execute="); ok {
				return value
			}
		}
		return ""
	}
	return trimmed
}

// stripSQLLiterals blanks out string literals, quoted identifiers and comments
// so their contents are not read as keywords. It returns "" when a quote or
// comment is left open, which the caller treats as unclear.
func stripSQLLiterals(query string) string {
	var out strings.Builder
	for i := 0; i < len(query); i++ {
		switch c := query[i]; {
		case c == '\'' || c == '"' || c == '`':
			end := i + 1
			for end < len(query) {
				if query[end] == c {
					if end+1 < len(query) && query[end+1] == c {
						end += 2
						continue
					}
					break
				}
				if c == '\'' && query[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(query) {
				return ""
			}
			out.WriteString(" x ")
			i = end
		case c == '-' && i+1 < len(query) && query[i+1] == '-':
			for i < len(query) && query[i] != '\n' {
				i++
			}
			out.WriteByte(' ')
		case c == '/' && i+1 < len(query) && query[i+1] == '*':
			end := strings.Index(query[i+2:], "*/")
			if end < 0 {
				return ""
			}
			i += end + 3
			out.WriteByte(' ')
		case c == '$' && i+1 < len(query) && (query[i+1] == '$' || isSQLWordStart(query[i+1])):
			// Postgres dollar quoting ($$ ... $$, $tag$ ... $tag$) wraps code
			// that runs, such as a DO block: unclear.
			if tagEnd := strings.IndexByte(query[i+1:], '$'); tagEnd >= 0 && isDollarTag(query[i+1:i+1+tagEnd]) {
				return ""
			}
			out.WriteByte(c)
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

func isSQLWordStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDollarTag(tag string) bool {
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		if !isSQLWordStart(c) && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// splitSQLStatements splits on ';' (literals are already blanked) and drops
// empty statements. It returns nil when stripping failed.
func splitSQLStatements(query string) []string {
	if strings.TrimSpace(query) == "" {
		return nil
	}
	var statements []string
	for _, statement := range strings.Split(query, ";") {
		if strings.TrimSpace(statement) != "" {
			statements = append(statements, statement)
		}
	}
	return statements
}

// classifySQLStatement classifies one statement (literals already blanked):
//   - update: it starts with a write keyword, or calls a function that changes
//     server state;
//   - read: it starts with a query keyword (or WITH, or EXPLAIN without
//     ANALYZE, which does not run the statement) and has no write word;
//   - "": anything else, left to the LLM prompt.
func classifySQLStatement(statement string) core.ToolRequestType {
	if sqlWriteFunctions.MatchString(statement) {
		return core.ToolRequestTypeUpdate
	}
	words := sqlWordPattern.FindAllString(strings.ToLower(statement), -1)
	if len(words) == 0 {
		return ""
	}
	if words[0] == "explain" {
		analyze := false
		rest := words[1:]
		// Skip EXPLAIN's own options: ANALYZE, VERBOSE, (ANALYZE, BUFFERS) ...
	options:
		for len(rest) > 0 {
			switch rest[0] {
			case "analyze", "analyse":
				analyze = true
			case "verbose", "buffers", "costs", "settings", "wal", "timing", "summary", "format", "true", "false", "on", "off", "text", "json", "xml", "yaml", "plan", "query", "extended", "partitions":
			default:
				break options
			}
			rest = rest[1:]
		}
		if len(rest) == 0 {
			return ""
		}
		if !analyze {
			// Plain EXPLAIN only plans the statement; it does not run it.
			return core.ToolRequestTypeRead
		}
		words = rest
	}
	if _, write := sqlWriteWords[words[0]]; write {
		return core.ToolRequestTypeUpdate
	}
	if _, read := sqlReadStatements[words[0]]; !read && words[0] != "with" {
		return ""
	}
	for _, word := range words[1:] {
		if _, write := sqlWriteWords[word]; write {
			return ""
		}
	}
	return core.ToolRequestTypeRead
}

// sqlRequestTypePrompt is the LLM fallback for database queries the static
// rules cannot decide. The shared rules for every classification prompt
// (untrusted input, chained statements) are appended by the caller.
func sqlRequestTypePrompt(dialect string) string {
	return `You classify a ` + dialect + ` statement (or several) that will run on a customer's database.

Answer with exactly one word: read, create, update or delete.

- read: every statement only reads data or metadata (SELECT, SHOW, DESCRIBE, plain EXPLAIN, a WITH query whose parts only read).
- create: a statement creates data or objects (INSERT, CREATE, SELECT ... INTO a new table).
- update: a statement changes data, schema, permissions, settings or server state (UPDATE, MERGE, ALTER, GRANT, REVOKE, SET, LOCK, SELECT ... FOR UPDATE, a function that writes or kills sessions, EXPLAIN ANALYZE of a write).
- delete: a statement deletes data or objects (DELETE, DROP, TRUNCATE).

A word that only names a column, table or alias (for example a column called "comment" or "lock") does not make a statement a write. If you cannot tell whether a statement writes, answer update.`
}

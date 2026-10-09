package query

import (
	"regexp"
	"strings"
	"testing"

	"nudgebee/services/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func regexWhere(column string, op BinaryWhereClauseType, value any) QueryWhereClause {
	return QueryWhereClause{Binary: BinaryWhereClause{column: {op: value}}}
}

func TestValidateRegexFilter_AcceptsThePortableSubset(t *testing.T) {
	for _, pattern := range []string{
		"central|edge",
		"registry-(central|edge)",
		"^https?://[^/]+/api/v[0-9]+",
		`(?:central|edge)\.example\.com$`,
		"(?i)Central|EDGE",
		".*-prod$",
		"^(GET|POST) /",
		"[A-Za-z0-9_]+ [a-c]{2,3}",
		"a{255}",
		"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}",
		// Unbounded wildcards are one step each, however much they span; a fixed run may hold sixteen.
		".*central.*edge.*", "^[^/]+/[^/]+/[^/]+$", "(a|b|c|d|e).{16}(x|y|z)", "(.{4}){4}", "[a-z]{200}",
		strings.Repeat("abcdefgh", 32),
		// An anchor may be grouped and repeated, doubled, or escaped — just not quantified bare.
		"(^a)*", "(?:^)*a", "^^a", "a$$", `\^*`, "[$^]*", "[^a]*", "^a?$",
		`\$\{id\}`,
		// A bracket expression is read as one: none of these is a group, a POSIX
		// class or a repetition, whatever they look like character by character.
		"[(?]", "[.]", "[=]", "[:]", "[]a]", "[^]a]", `[a\]b]`, "[a{999}]", "[[]",
		// Without (?i) nothing is folded, so a non-ASCII literal is just a literal.
		"caf\u00e9|na\u00efve", "[\u00e0-\u00ff]+",
	} {
		assert.NoError(t, ValidateRegexFilter(pattern), pattern)
	}
}

// Every pattern here was run on both engines and matched DIFFERENT rows (or ran
// on one and failed on the other) before the validator refused it.
func TestValidateRegexFilter_RefusesWhatTheEnginesDisagreeOn(t *testing.T) {
	cases := []struct {
		pattern string
		message string
	}{
		// Word boundary in RE2, BACKSPACE in a Postgres ARE.
		{`\bedge\b`, `\b is not supported`},
		// Postgres reads these by locale — "é" is a word character there and not in RE2,
		// a vertical tab is whitespace there and not in RE2.
		{`\w+`, `\w is not supported`},
		{`\W`, `\W is not supported`},
		{`\s`, `\s is not supported`},
		{`\S`, `\S is not supported`},
		{`\d+`, `\d is not supported`},
		{`[\w-]`, `\w is not supported`},
		{"[[:alpha:]]", `"[:" is not supported`},
		{"[a[:upper:]]", `"[:" is not supported`},
		// An equivalence class and a collating element in Postgres, punctuation in RE2.
		{"[[=a=]]", `"[=" is not supported`},
		{"[[.a.]]", `"[." is not supported`},
		{"[[.hyphen.]]", `"[." is not supported`},
		// "ı" folds to "I" in one engine and not the other.
		{"(?i)\u0131", "may only contain ASCII"},
		{"(?i)caf\u00e9", "may only contain ASCII"},
		// RE2 counts to 1000, Postgres to 255.
		{"a{256}", "a count may not exceed 255"},
		{"a{1,300}", "a count may not exceed 255"},
		{"a{300,}", "a count may not exceed 255"},
		{"a{99999999999999999999}", "a count may not exceed 255"},
		// A quantified anchor is an error in Postgres and a no-op in RE2.
		{"^*", "directly after ^ or $"},
		{"a$+", "directly after ^ or $"},
		{"^?a", "directly after ^ or $"},
		{"^{2}a", "directly after ^ or $"},
		{"a|^*b", "directly after ^ or $"},
		{`\Aedge`, `\A is not supported`},
		{`\pL+`, `\p is not supported`},
		{`\x41`, `\x is not supported`},
		{`\n`, `\n is not supported`},
		{`(a)\1`, `\1 is not supported`},
		// Named groups and inline flags are RE2-only spellings.
		{`(?P<zone>edge)`, `"(?P<" is not supported`},
		{`edge(?i)central`, `"(?i)" is not supported`},
		{`(?s)edge`, `"(?s)" is not supported`},
		// Look-around is Postgres-only, and unbounded on its engine.
		{`edge(?=central)`, `"(?=c" is not supported`},
		{`edge\`, `bare backslash`},
		{`central|(edge`, `invalid regex pattern`},
		{`a**`, `invalid regex pattern`},
		{``, `empty`},
		{strings.Repeat("a", maxRegexFilterLength+1), `longer than`},
		// Short to type, huge once expanded: each of these took seconds per 200
		// rows on one engine or both.
		{"(a{250}){4}", "too large once its repetitions are expanded"},
		{"(((a{6}){6}){6}){4}", "too large once its repetitions are expanded"},
		{strings.Repeat("[a-z]{255}", 3), "too large once its repetitions are expanded"},
		{strings.Repeat(".{99}", 4), "once"},
		{"(a{255}|b{255}|c{255}){3}", "too large once its repetitions are expanded"},
		// A fixed run of wildcards keeps a candidate alive per position per step:
		// at 32 steps this cost six times a plain pattern filter on a day of spans.
		{"(a|b|c|d|e).{17}(x|y|z)", "too many wildcard steps"},
		{"a" + strings.Repeat(".", 40), "too many wildcard steps"},
		{"(.{5}){4}", "too many wildcard steps"},
		{"a[^/]{20}b", "too many wildcard steps"},
		{"a[A-Za-z0-9_ ]{20}b", "too many wildcard steps"},
	}
	for _, tc := range cases {
		err := ValidateRegexFilter(tc.pattern)
		require.Error(t, err, tc.pattern)
		assert.Contains(t, err.Error(), tc.message, tc.pattern)
	}
}

func TestGenerateWhereClause_Regex_Postgres(t *testing.T) {
	td := newNormalTable()

	sql, err := generateWhereClause(regexWhere("name", Regex, "central|edge"), td)
	require.NoError(t, err)
	assert.Equal(t, "(name ~ 'central|edge')", sql)

	sql, err = generateWhereClause(regexWhere("name", NRegex, "central|edge"), td)
	require.NoError(t, err)
	assert.Equal(t, "(name !~ 'central|edge')", sql)

	// Backslashes are literal in a standard-conforming Postgres string; only the
	// quote is doubled, so the pattern cannot close its own literal.
	sql, err = generateWhereClause(regexWhere("name", Regex, `it's\.here`), td)
	require.NoError(t, err)
	assert.Equal(t, `(name ~ 'it''s\.here')`, sql)
}

func TestGenerateWhereClause_Regex_ClickHouse(t *testing.T) {
	td := newNormalTable()
	td.Source = database.AgentWarehouse

	sql, err := generateWhereClause(regexWhere("name", Regex, "central|edge"), td)
	require.NoError(t, err)
	assert.Equal(t, "(match(name, '(?s)central|edge'))", sql)

	sql, err = generateWhereClause(regexWhere("name", NRegex, "central|edge"), td)
	require.NoError(t, err)
	assert.Equal(t, "(NOT match(name, '(?s)central|edge'))", sql)

	// ClickHouse reads a backslash in a literal as an escape, so it is doubled
	// for the pattern to reach RE2 as written.
	sql, err = generateWhereClause(regexWhere("name", Regex, `it's\.here`), td)
	require.NoError(t, err)
	assert.Equal(t, `(match(name, '(?s)it\'s\\.here'))`, sql)
}

func TestGenerateWhereClause_Regex_UsesTheColumnDefinition(t *testing.T) {
	td := newNormalTable()
	td.Columns["url"] = ColumnDefinition{Type: ColumnDefinitionTypeString, Def: "attrs->>'url'"}

	sql, err := generateWhereClause(regexWhere("url", Regex, "central|edge"), td)
	require.NoError(t, err)
	assert.Equal(t, "(attrs->>'url' ~ 'central|edge')", sql)
}

func TestGenerateWhereClause_Regex_Refusals(t *testing.T) {
	td := newNormalTable()

	_, err := generateWhereClause(regexWhere("age", Regex, "4."), td)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a string column")

	_, err = generateWhereClause(regexWhere("name", Regex, []any{"central", "edge"}), td)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a string column")

	_, err = generateWhereClause(regexWhere("name", Regex, `\bedge\b`), td)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `\b is not supported`)

	// The subset is only defined for Postgres and ClickHouse.
	td.Source = database.AgentWarehouseBigQuery
	_, err = generateWhereClause(regexWhere("name", Regex, "central|edge"), td)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported on this data source")
}

// decodePostgresLiteral reads a standard-conforming Postgres string literal back
// to its value: the only escape is a doubled quote. A lone quote before the end
// means the value closed its own literal — whatever follows would run as SQL.
func decodePostgresLiteral(t *testing.T, lit string) string {
	t.Helper()
	require.True(t, len(lit) >= 2 && lit[0] == '\'' && lit[len(lit)-1] == '\'', "not a quoted literal: %q", lit)
	body := lit[1 : len(lit)-1]
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] == '\'' {
			require.True(t, i+1 < len(body) && body[i+1] == '\'', "literal ends early at byte %d: %q", i, lit)
			i++
		}
		out.WriteByte(body[i])
	}
	return out.String()
}

// decodeClickhouseLiteral does the same for ClickHouse, where a backslash is an
// escape character: every backslash must escape a backslash or a quote, and a
// bare quote ends the literal.
func decodeClickhouseLiteral(t *testing.T, lit string) string {
	t.Helper()
	require.True(t, len(lit) >= 2 && lit[0] == '\'' && lit[len(lit)-1] == '\'', "not a quoted literal: %q", lit)
	body := lit[1 : len(lit)-1]
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\\':
			require.True(t, i+1 < len(body) && (body[i+1] == '\\' || body[i+1] == '\''), "backslash escapes something else at byte %d: %q", i, lit)
			i++
		case '\'':
			require.Failf(t, "literal ends early", "at byte %d: %q", i, lit)
		}
		out.WriteByte(body[i])
	}
	return out.String()
}

// The worst case for a filter that takes a pattern: one that ends its own SQL
// literal. Whatever the validator lets through — quotes, backslashes, comment
// markers, statement separators — must come back out of each engine's literal
// byte for byte, and must compile as RE2 within the length cap.
func FuzzRegexFilter_NeverLeavesItsLiteral(f *testing.F) {
	for _, seed := range []string{
		"central|edge",
		"'; DROP TABLE events; --",
		`\'; DROP TABLE events; --`,
		`\\'; SELECT pg_sleep(10); --`,
		`a' OR '1'='1`,
		`a\' OR 1=1 /*`,
		"a') UNION SELECT password FROM users --",
		`\\`,
		`\'`,
		`'`,
		`''`,
		`\\\'`,
		"a\nb",
		"a\x00b",
		"(?i)café|naïve",
		`^https?://[^/]+/v[0-9]+/(?:items|orders)\?id=[0-9]+$`,
		"(a+)+$",
		"(?:a|aa)+$",
		strings.Repeat("(a|b)", 40),
		"(a{250}){4}",
		"$$ $1 ${x} {y:String} ? :name @v",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, pattern string) {
		if err := ValidateRegexFilter(pattern); err != nil {
			return
		}
		require.LessOrEqual(t, len(pattern), maxRegexFilterLength)
		_, err := regexp.Compile(pattern)
		require.NoError(t, err)

		pg := &postgresDialect{}
		require.Equal(t, pattern, decodePostgresLiteral(t, pg.QuoteLiteral(pattern)))
		ch := &clickhouseDialect{}
		require.Equal(t, "(?s)"+pattern, decodeClickhouseLiteral(t, ch.QuoteLiteral("(?s)"+pattern)))

		// And the generator itself emits exactly one condition around that literal.
		for source, want := range map[database.DatabaseManagerType]string{
			database.Metastore:      "(name ~ " + pg.QuoteLiteral(pattern) + ")",
			database.AgentWarehouse: "(match(name, " + ch.QuoteLiteral("(?s)"+pattern) + "))",
		} {
			td := newNormalTable()
			td.Source = source
			sql, err := generateWhereClause(regexWhere("name", Regex, pattern), td)
			require.NoError(t, err)
			require.Equal(t, want, sql)
		}
	})
}

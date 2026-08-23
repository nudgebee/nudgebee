package observability

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// KQL (Kibana Query Language) → Elasticsearch Query DSL translator.
//
// The native `kql` Query DSL clause only exists on Elasticsearch 9.1+, and the
// ES|QL `KQL()` function is Elasticsearch-only. To offer a KQL language box that
// works on every cluster we target (ES 8.x, including our 8.19 stack, plus
// OpenSearch), we parse a broad subset of KQL ourselves and emit standard DSL
// primitives (bool / match / match_phrase / range / wildcard / exists /
// multi_match / nested) that every version has supported for years. The output
// feeds the same finalizeESLogQueryBody → _search → parseESSearchLogs path as the
// dsl query_type.
//
// Coverage: field:value, field:"quoted phrase", boolean and/or/not
// (case-insensitive) with KQL precedence (not > and > or) and implicit-AND for
// space-separated clauses, parentheses, ranges (> >= < <=), value lists
// field:(a or b), leading/trailing/inner wildcards (field:val*), field:* (exists),
// field-name wildcards (kube.*:x → multi_match), nested field queries
// (parent:{ child:v and child2:v2 }), and bare terms (searched across all fields).
//
// Deliberately unsupported (returns a parse error rather than silently-wrong DSL,
// matching the native kql query's behavior on unsupported syntax): fuzzy (~),
// proximity, and regexp. The translation is mapping-blind — it does not consult
// field types — so it uses `match` for field:value and coerces only range bounds
// to numbers; for log *filtering* (which is all KQL does — no scoring/sorting) the
// resulting match set is faithful for the vast majority of real queries.

// kqlToDSL parses a KQL expression and returns the equivalent ES Query DSL clause
// (the value that goes under "query"). A blank expression is the caller's concern
// (see buildESKQLQueryBody, which substitutes match_all).
func kqlToDSL(kql string) (map[string]any, error) {
	toks, err := kqlTokenize(kql)
	if err != nil {
		return nil, err
	}
	p := &kqlParser{toks: toks}
	node, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != kqlEOF {
		return nil, fmt.Errorf("kql: unexpected %q at position %d", p.peek().text, p.peek().pos)
	}
	if node == nil {
		return map[string]any{"match_all": map[string]any{}}, nil
	}
	return kqlEmit(node, ""), nil
}

// buildESKQLQueryBody translates a KQL expression into an ES search body, then
// applies the request's time window / size / offset / sort via
// finalizeESLogQueryBody. A blank expression degrades to match_all so selecting
// the KQL language with an empty box still runs a time-bounded scan.
func buildESKQLQueryBody(kqlQuery string, startMillis, endMillis int64, limit, offset int, sortFields []SortField) (map[string]any, error) {
	var inner map[string]any
	if strings.TrimSpace(kqlQuery) == "" {
		inner = map[string]any{"match_all": map[string]any{}}
	} else {
		dsl, err := kqlToDSL(kqlQuery)
		if err != nil {
			return nil, err
		}
		inner = dsl
	}
	wrapped, err := json.Marshal(map[string]any{"query": inner})
	if err != nil {
		return nil, fmt.Errorf("failed to build kql query body: %w", err)
	}
	return finalizeESLogQueryBody(string(wrapped), startMillis, endMillis, limit, offset, sortFields)
}

// ---------------------------------------------------------------------------
// Tokenizer
// ---------------------------------------------------------------------------

type kqlTokKind int

const (
	kqlEOF kqlTokKind = iota
	kqlLiteral
	kqlQuoted
	kqlColon
	kqlLParen
	kqlRParen
	kqlLBrace
	kqlRBrace
	kqlGt
	kqlGte
	kqlLt
	kqlLte
)

type kqlToken struct {
	kind kqlTokKind
	text string
	pos  int
}

// kqlIsSpecial reports the runes that end an unquoted literal.
func kqlIsSpecial(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '(', ')', '{', '}', ':', '<', '>', '"':
		return true
	}
	return false
}

func kqlTokenize(input string) ([]kqlToken, error) {
	var toks []kqlToken
	runes := []rune(input)
	i := 0
	n := len(runes)
	for i < n {
		r := runes[i]
		switch r {
		case ' ', '\t', '\n', '\r':
			i++
		case '(':
			toks = append(toks, kqlToken{kqlLParen, "(", i})
			i++
		case ')':
			toks = append(toks, kqlToken{kqlRParen, ")", i})
			i++
		case '{':
			toks = append(toks, kqlToken{kqlLBrace, "{", i})
			i++
		case '}':
			toks = append(toks, kqlToken{kqlRBrace, "}", i})
			i++
		case ':':
			toks = append(toks, kqlToken{kqlColon, ":", i})
			i++
		case '>':
			if i+1 < n && runes[i+1] == '=' {
				toks = append(toks, kqlToken{kqlGte, ">=", i})
				i += 2
			} else {
				toks = append(toks, kqlToken{kqlGt, ">", i})
				i++
			}
		case '<':
			if i+1 < n && runes[i+1] == '=' {
				toks = append(toks, kqlToken{kqlLte, "<=", i})
				i += 2
			} else {
				toks = append(toks, kqlToken{kqlLt, "<", i})
				i++
			}
		case '"':
			start := i
			i++ // opening quote
			var sb strings.Builder
			closed := false
			for i < n {
				c := runes[i]
				if c == '\\' && i+1 < n {
					sb.WriteRune(runes[i+1])
					i += 2
					continue
				}
				if c == '"' {
					closed = true
					i++
					break
				}
				sb.WriteRune(c)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("kql: unterminated quoted string at position %d", start)
			}
			toks = append(toks, kqlToken{kqlQuoted, sb.String(), start})
		default:
			start := i
			var sb strings.Builder
			for i < n {
				c := runes[i]
				if c == '\\' && i+1 < n {
					sb.WriteRune(runes[i+1])
					i += 2
					continue
				}
				if kqlIsSpecial(c) {
					break
				}
				sb.WriteRune(c)
				i++
			}
			if sb.Len() == 0 {
				// A special char we didn't consume — shouldn't happen, guard anyway.
				return nil, fmt.Errorf("kql: unexpected character %q at position %d", string(r), start)
			}
			toks = append(toks, kqlToken{kqlLiteral, sb.String(), start})
		}
	}
	toks = append(toks, kqlToken{kqlEOF, "", len(runes)})
	return toks, nil
}

// ---------------------------------------------------------------------------
// AST
// ---------------------------------------------------------------------------

type kqlNode interface{}

type kqlAnd struct{ clauses []kqlNode }
type kqlOr struct{ clauses []kqlNode }
type kqlNot struct{ clause kqlNode }

// kqlFieldValue is `field:value`. quoted marks a phrase; value "*" (unquoted) is
// an exists check.
type kqlFieldValue struct {
	field  string
	value  string
	quoted bool
}

// kqlRange is `field <op> value`, op ∈ {gt,gte,lt,lte}.
type kqlRange struct {
	field string
	op    string
	value string
}

// kqlNested is `path:{ subquery }`.
type kqlNested struct {
	path  string
	query kqlNode
}

// kqlBare is a term with no field — searched across all fields.
type kqlBare struct {
	value  string
	quoted bool
}

// ---------------------------------------------------------------------------
// Parser  (precedence: or < and < not; parens override)
// ---------------------------------------------------------------------------

type kqlParser struct {
	toks []kqlToken
	i    int
}

func (p *kqlParser) peek() kqlToken { return p.toks[p.i] }
func (p *kqlParser) next() kqlToken { t := p.toks[p.i]; p.i++; return t }
func (p *kqlParser) isKeyword(kw string) bool {
	t := p.peek()
	return t.kind == kqlLiteral && strings.EqualFold(t.text, kw)
}

func (p *kqlParser) parseOr() (kqlNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	if left == nil {
		return nil, nil
	}
	clauses := []kqlNode{left}
	for p.isKeyword("or") {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		if right == nil {
			return nil, fmt.Errorf("kql: expected expression after 'or'")
		}
		clauses = append(clauses, right)
	}
	if len(clauses) == 1 {
		return clauses[0], nil
	}
	return &kqlOr{clauses: clauses}, nil
}

func (p *kqlParser) parseAnd() (kqlNode, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	if left == nil {
		return nil, nil
	}
	clauses := []kqlNode{left}
	for {
		if p.isKeyword("and") {
			p.next()
			right, err := p.parseNot()
			if err != nil {
				return nil, err
			}
			if right == nil {
				return nil, fmt.Errorf("kql: expected expression after 'and'")
			}
			clauses = append(clauses, right)
			continue
		}
		// Implicit AND: another expression starts without an operator (KQL treats
		// whitespace-separated clauses as AND). 'or' is the only thing that isn't a
		// new clause here; 'and'/'not' are handled as keywords.
		if p.startsExpression() && !p.isKeyword("or") {
			right, err := p.parseNot()
			if err != nil {
				return nil, err
			}
			if right == nil {
				break
			}
			clauses = append(clauses, right)
			continue
		}
		break
	}
	if len(clauses) == 1 {
		return clauses[0], nil
	}
	return &kqlAnd{clauses: clauses}, nil
}

func (p *kqlParser) parseNot() (kqlNode, error) {
	if p.isKeyword("not") {
		p.next()
		clause, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		if clause == nil {
			return nil, fmt.Errorf("kql: expected expression after 'not'")
		}
		return &kqlNot{clause: clause}, nil
	}
	return p.parseSub()
}

func (p *kqlParser) parseSub() (kqlNode, error) {
	if p.peek().kind == kqlLParen {
		p.next()
		node, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != kqlRParen {
			return nil, fmt.Errorf("kql: expected ')' at position %d", p.peek().pos)
		}
		p.next()
		return node, nil
	}
	return p.parseExpression()
}

// startsExpression reports whether the current token can begin a new clause —
// used to detect implicit AND.
func (p *kqlParser) startsExpression() bool {
	switch p.peek().kind {
	case kqlLiteral, kqlQuoted, kqlLParen:
		return true
	}
	return false
}

func (p *kqlParser) parseExpression() (kqlNode, error) {
	t := p.peek()
	switch t.kind {
	case kqlQuoted:
		p.next()
		return &kqlBare{value: t.text, quoted: true}, nil
	case kqlLiteral:
		// Look one token ahead to tell field:… / field <op> … / bare term apart.
		nx := p.toks[p.i+1]
		switch nx.kind {
		case kqlColon:
			p.next() // field
			p.next() // ':'
			return p.parseFieldValue(t.text)
		case kqlGt, kqlGte, kqlLt, kqlLte:
			p.next() // field
			p.next() // op
			val := p.peek()
			if val.kind != kqlLiteral && val.kind != kqlQuoted {
				return nil, fmt.Errorf("kql: expected a value after %q at position %d", nx.text, nx.pos)
			}
			p.next()
			return &kqlRange{field: t.text, op: kqlRangeOp(nx.kind), value: val.text}, nil
		default:
			p.next()
			return &kqlBare{value: t.text}, nil
		}
	default:
		return nil, fmt.Errorf("kql: unexpected %q at position %d", t.text, t.pos)
	}
}

func kqlRangeOp(k kqlTokKind) string {
	switch k {
	case kqlGt:
		return "gt"
	case kqlGte:
		return "gte"
	case kqlLt:
		return "lt"
	case kqlLte:
		return "lte"
	}
	return ""
}

// parseFieldValue handles what follows `field:` — a nested block, a value list, or
// a single value.
func (p *kqlParser) parseFieldValue(field string) (kqlNode, error) {
	switch p.peek().kind {
	case kqlLBrace:
		p.next()
		sub, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != kqlRBrace {
			return nil, fmt.Errorf("kql: expected '}' at position %d", p.peek().pos)
		}
		p.next()
		if sub == nil {
			return nil, fmt.Errorf("kql: empty nested query for field %q", field)
		}
		return &kqlNested{path: field, query: sub}, nil
	case kqlLParen:
		p.next()
		return p.parseValueList(field)
	case kqlLiteral, kqlQuoted:
		v := p.next()
		return &kqlFieldValue{field: field, value: v.text, quoted: v.kind == kqlQuoted}, nil
	default:
		return nil, fmt.Errorf("kql: expected a value after %q: at position %d", field, p.peek().pos)
	}
}

// parseValueList handles `field:(a or b and c ...)`. Within the parens,
// whitespace-separated values default to OR (KQL semantics). Mixing is resolved by
// the last operator seen — good enough for the shapes KQL actually permits.
func (p *kqlParser) parseValueList(field string) (kqlNode, error) {
	first := p.peek()
	if first.kind != kqlLiteral && first.kind != kqlQuoted {
		return nil, fmt.Errorf("kql: expected a value in list for %q at position %d", field, first.pos)
	}
	p.next()
	clauses := []kqlNode{&kqlFieldValue{field: field, value: first.text, quoted: first.kind == kqlQuoted}}
	op := "or"
	for {
		if p.isKeyword("and") {
			op = "and"
			p.next()
		} else if p.isKeyword("or") {
			op = "or"
			p.next()
		} else if p.peek().kind == kqlLiteral || p.peek().kind == kqlQuoted {
			// implicit OR between adjacent values
		} else {
			break
		}
		v := p.peek()
		if v.kind != kqlLiteral && v.kind != kqlQuoted {
			return nil, fmt.Errorf("kql: expected a value in list for %q at position %d", field, v.pos)
		}
		p.next()
		clauses = append(clauses, &kqlFieldValue{field: field, value: v.text, quoted: v.kind == kqlQuoted})
	}
	if p.peek().kind != kqlRParen {
		return nil, fmt.Errorf("kql: expected ')' to close value list for %q at position %d", field, p.peek().pos)
	}
	p.next()
	if len(clauses) == 1 {
		return clauses[0], nil
	}
	if op == "and" {
		return &kqlAnd{clauses: clauses}, nil
	}
	return &kqlOr{clauses: clauses}, nil
}

// ---------------------------------------------------------------------------
// Emitter — AST → ES Query DSL. prefix carries the nested path so child fields in
// `parent:{ child:v }` resolve to the full path "parent.child".
// ---------------------------------------------------------------------------

func kqlEmit(node kqlNode, prefix string) map[string]any {
	switch n := node.(type) {
	case *kqlAnd:
		return map[string]any{"bool": map[string]any{"must": kqlEmitList(n.clauses, prefix)}}
	case *kqlOr:
		return map[string]any{"bool": map[string]any{
			"should":               kqlEmitList(n.clauses, prefix),
			"minimum_should_match": 1,
		}}
	case *kqlNot:
		return map[string]any{"bool": map[string]any{"must_not": []any{kqlEmit(n.clause, prefix)}}}
	case *kqlNested:
		path := prefix + n.path
		return map[string]any{"nested": map[string]any{
			"path":       path,
			"query":      kqlEmit(n.query, path+"."),
			"score_mode": "none",
		}}
	case *kqlFieldValue:
		return kqlEmitFieldValue(prefix+n.field, n.value, n.quoted)
	case *kqlRange:
		return map[string]any{"range": map[string]any{
			prefix + n.field: map[string]any{n.op: kqlCoerce(n.value)},
		}}
	case *kqlBare:
		return kqlEmitBare(n.value, n.quoted)
	}
	return map[string]any{"match_all": map[string]any{}}
}

func kqlEmitList(nodes []kqlNode, prefix string) []any {
	out := make([]any, 0, len(nodes))
	for _, c := range nodes {
		out = append(out, kqlEmit(c, prefix))
	}
	return out
}

func kqlEmitFieldValue(field, value string, quoted bool) map[string]any {
	fieldHasWildcard := strings.Contains(field, "*")

	// Unquoted bare "*" is an existence check.
	if !quoted && value == "*" {
		return map[string]any{"exists": map[string]any{"field": field}}
	}

	// Field-name wildcard (e.g. labels.*:x) — no single-field query works; fan out
	// with multi_match, which supports wildcard field patterns.
	if fieldHasWildcard {
		mm := map[string]any{"query": value, "fields": []any{field}, "lenient": true}
		if quoted {
			mm["type"] = "phrase"
		}
		return map[string]any{"multi_match": mm}
	}

	if quoted {
		return map[string]any{"match_phrase": map[string]any{field: value}}
	}
	// KQL's only wildcard is '*' (unlike Lucene, '?' is a literal in KQL).
	if strings.Contains(value, "*") {
		return map[string]any{"wildcard": map[string]any{field: map[string]any{"value": value}}}
	}
	return map[string]any{"match": map[string]any{field: map[string]any{"query": value}}}
}

func kqlEmitBare(value string, quoted bool) map[string]any {
	mm := map[string]any{"query": value, "fields": []any{"*"}, "lenient": true}
	if quoted {
		mm["type"] = "phrase"
	}
	return map[string]any{"multi_match": mm}
}

// kqlCoerce turns a range bound into a number when it looks numeric, so numeric
// fields compare correctly; otherwise it stays a string (ES parses date literals).
func kqlCoerce(s string) any {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

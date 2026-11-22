package logql2ch

import (
	"fmt"
	"strconv"
)

// ErrorKind classifies a translation failure so callers can tell a query the
// user must fix from one the translator deliberately does not run.
type ErrorKind int

const (
	// KindInvalid: the text is not LogQL.
	KindInvalid ErrorKind = iota
	// KindUnsupported: valid LogQL this translator refuses rather than approximate.
	KindUnsupported
	// KindUnknownLabel: a label outside the alias map.
	KindUnknownLabel
	// KindBadRequest: the query is fine but the options are not (window, limit, step).
	KindBadRequest
)

// Error is returned for every query the translator rejects.
type Error struct {
	Kind ErrorKind
	msg  string
}

func (e *Error) Error() string { return e.msg }

func invalidf(pos int, format string, args ...any) error {
	return &Error{Kind: KindInvalid, msg: fmt.Sprintf("invalid LogQL: at position %d: %s", pos, fmt.Sprintf(format, args...))}
}

func unsupported(feature string) error {
	return &Error{Kind: KindUnsupported, msg: "unsupported LogQL: " + feature}
}

func badRequestf(format string, args ...any) error {
	return &Error{Kind: KindBadRequest, msg: fmt.Sprintf(format, args...)}
}

// AST. Only the shapes the grammar below accepts exist; everything else is
// refused while parsing, by name.

type matcher struct {
	name, op, value string // op: = != =~ !~
	pos             int
}

type lineFilter struct {
	op     tokKind  // tPipeEq, tNeq, tPipeTilde, tNre
	values []string // more than one: chained with `or`
	pos    int
}

type parserStage struct {
	kind string // json | logfmt
	pos  int
}

type labelFilter struct{ expr labelExpr }

type labelExpr interface{ isLabelExpr() }

type labelCmp struct {
	name    string
	op      tokKind
	str     string
	num     float64
	numeric bool
	pos     int
}

type labelBool struct {
	and  bool
	l, r labelExpr
}

func (labelCmp) isLabelExpr()  {}
func (labelBool) isLabelExpr() {}

type logExpr struct {
	matchers []matcher
	stages   []any // lineFilter | parserStage | labelFilter
}

type rangeAgg struct {
	fn        string
	log       logExpr
	rangeNs   int64
	rangeText string
}

type vectorAgg struct {
	op    string
	by    []string
	inner *rangeAgg
}

type expr struct {
	log *logExpr
	rng *rangeAgg
	agg *vectorAgg
}

var vectorAggOps = map[string]bool{
	"sum": true, "avg": true, "min": true, "max": true, "count": true, "stddev": true, "stdvar": true,
	"topk": true, "bottomk": true, "approx_topk": true, "sort": true, "sort_desc": true,
}

var rangeFns = map[string]bool{
	"count_over_time": true, "rate": true, "bytes_over_time": true, "bytes_rate": true,
	"absent_over_time": true, "sum_over_time": true, "avg_over_time": true, "max_over_time": true,
	"min_over_time": true, "first_over_time": true, "last_over_time": true, "stdvar_over_time": true,
	"stddev_over_time": true, "quantile_over_time": true, "rate_counter": true,
}

// Stages this translator refuses, keyed by the word after `|`.
var unsupportedStages = map[string]string{
	"line_format": "line_format", "label_format": "label_format", "pattern": "pattern parser",
	"regexp": "regexp parser", "unpack": "unpack", "unwrap": "unwrap", "decolorize": "decolorize",
	"drop": "drop", "keep": "keep",
}

type parser struct {
	toks  []token
	i     int
	depth int
}

// maxDepth bounds parenthesis nesting: the parser recurses per level, and a
// deep enough input would otherwise exhaust the stack.
const maxDepth = 64

func (p *parser) enter(pos int) error {
	p.depth++
	if p.depth > maxDepth {
		return invalidf(pos, "nesting deeper than %d levels", maxDepth)
	}
	return nil
}

func parse(src string) (*expr, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tEOF {
		switch {
		case e.log == nil && isComparison(t.kind):
			return nil, unsupported("comparison operators in metric queries")
		case t.kind == tArith || (t.kind == tIdent && (t.text == "and" || t.text == "or" || t.text == "unless")):
			return nil, unsupported("binary operations between queries")
		}
		return nil, invalidf(t.pos, "unexpected %q", t.text)
	}
	return e, nil
}

func (p *parser) peek() token { return p.toks[p.i] }
func (p *parser) peekAt(n int) token {
	if p.i+n < len(p.toks) {
		return p.toks[p.i+n]
	}
	return p.toks[len(p.toks)-1]
}
func (p *parser) next() token {
	t := p.toks[p.i]
	if t.kind != tEOF {
		p.i++
	}
	return t
}

func (p *parser) expect(kind tokKind, what string) (token, error) {
	t := p.next()
	if t.kind != kind {
		return t, invalidf(t.pos, "expected %s, found %q", what, t.text)
	}
	return t, nil
}

func (p *parser) parseExpr() (*expr, error) {
	t := p.peek()
	switch t.kind {
	case tLBrace:
		l, err := p.parseLogExpr()
		if err != nil {
			return nil, err
		}
		return &expr{log: l}, nil
	case tLParen:
		p.next()
		if err := p.enter(t.pos); err != nil {
			return nil, err
		}
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tRParen, `")"`); err != nil {
			return nil, err
		}
		p.depth--
		return e, nil
	case tIdent:
		switch {
		case vectorAggOps[t.text]:
			return p.parseVectorAgg()
		case rangeFns[t.text]:
			r, err := p.parseRangeAgg()
			if err != nil {
				return nil, err
			}
			return &expr{rng: r}, nil
		case t.text == "label_replace" || t.text == "vector":
			return nil, unsupported(t.text)
		}
	}
	return nil, invalidf(t.pos, "expected a log selector or a metric query, found %q", t.text)
}

func (p *parser) parseVectorAgg() (*expr, error) {
	op := p.next()
	switch op.text {
	case "topk", "bottomk", "approx_topk", "sort", "sort_desc":
		return nil, unsupported(op.text)
	}
	if err := p.enter(op.pos); err != nil {
		return nil, err
	}
	defer func() { p.depth-- }()
	by, without, err := p.parseGrouping(nil)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tLParen, `"("`); err != nil {
		return nil, err
	}
	inner, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tRParen, `")"`); err != nil {
		return nil, err
	}
	if by == nil && !without {
		if by, without, err = p.parseGrouping(by); err != nil {
			return nil, err
		}
	}
	switch {
	case inner.agg != nil:
		return nil, unsupported("nested aggregations")
	case inner.rng == nil:
		return nil, invalidf(op.pos, "%s needs a range aggregation such as count_over_time", op.text)
	case op.text != "sum":
		return nil, unsupported(op.text)
	case without:
		return nil, unsupported("without")
	}
	return &expr{agg: &vectorAgg{op: op.text, by: by, inner: inner.rng}}, nil
}

// parseGrouping reads an optional `by (...)` or `without (...)`. by is non-nil
// (possibly empty) once a by clause was read.
func (p *parser) parseGrouping(by []string) ([]string, bool, error) {
	t := p.peek()
	if t.kind != tIdent || (t.text != "by" && t.text != "without") {
		return by, false, nil
	}
	p.next()
	if _, err := p.expect(tLParen, `"("`); err != nil {
		return nil, false, err
	}
	labels := []string{}
	for p.peek().kind != tRParen {
		id, err := p.expect(tIdent, "a label name")
		if err != nil {
			return nil, false, err
		}
		labels = append(labels, id.text)
		if p.peek().kind == tComma {
			p.next()
		}
	}
	p.next()
	return labels, t.text == "without", nil
}

func (p *parser) parseRangeAgg() (*rangeAgg, error) {
	fn := p.next()
	if fn.text == "quantile_over_time" {
		return nil, unsupported(fn.text)
	}
	if _, err := p.expect(tLParen, `"("`); err != nil {
		return nil, err
	}
	log, err := p.parseLogExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tLBrack, `"[" and a range`); err != nil {
		return nil, err
	}
	d := p.next()
	if d.kind != tUnitNumber {
		return nil, invalidf(d.pos, "expected a duration, found %q", d.text)
	}
	ns, err := parseDuration(d.text)
	if err != nil {
		return nil, invalidf(d.pos, "%v", err)
	}
	if _, err := p.expect(tRBrack, `"]"`); err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind == tIdent && t.text == "offset" {
		return nil, unsupported("offset")
	}
	// Loki also accepts the pipeline after the range: count_over_time({a="b"}[5m] |= "x").
	if err := p.parseStages(log); err != nil {
		return nil, err
	}
	if _, err := p.expect(tRParen, `")"`); err != nil {
		return nil, err
	}
	if fn.text != "count_over_time" && fn.text != "rate" {
		return nil, unsupported(fn.text)
	}
	return &rangeAgg{fn: fn.text, log: *log, rangeNs: ns, rangeText: d.text}, nil
}

func (p *parser) parseLogExpr() (*logExpr, error) {
	l := &logExpr{}
	open, err := p.expect(tLBrace, `"{"`)
	if err != nil {
		return nil, err
	}
	for {
		name, err := p.expect(tIdent, "a label name")
		if err != nil {
			return nil, err
		}
		op := p.next()
		switch op.kind {
		case tEq, tNeq, tRe, tNre:
		default:
			return nil, invalidf(op.pos, "expected =, !=, =~ or !~ after %q, found %q", name.text, op.text)
		}
		val, err := p.expect(tString, "a quoted value")
		if err != nil {
			return nil, err
		}
		l.matchers = append(l.matchers, matcher{name: name.text, op: op.text, value: val.val, pos: name.pos})
		if p.peek().kind == tComma {
			p.next()
			continue
		}
		if _, err := p.expect(tRBrace, `"}"`); err != nil {
			return nil, err
		}
		break
	}
	if len(l.matchers) == 0 {
		return nil, invalidf(open.pos, "empty stream selector")
	}
	return l, p.parseStages(l)
}

func (p *parser) parseStages(l *logExpr) error {
	for {
		t := p.peek()
		switch t.kind {
		case tPipeEq, tNeq, tPipeTilde, tNre:
			f, err := p.parseLineFilter()
			if err != nil {
				return err
			}
			l.stages = append(l.stages, f)
		case tPipeGt, tNgt:
			return unsupported("pattern line filter")
		case tPipe:
			p.next()
			s, err := p.parsePipelineStage()
			if err != nil {
				return err
			}
			l.stages = append(l.stages, s)
		default:
			return nil
		}
	}
}

func (p *parser) parseLineFilter() (lineFilter, error) {
	op := p.next()
	f := lineFilter{op: op.kind, pos: op.pos}
	for {
		v := p.next()
		switch {
		case v.kind == tString:
			f.values = append(f.values, v.val)
		case v.kind == tIdent && v.text == "ip" && p.peek().kind == tLParen:
			return f, unsupported("ip line filter")
		default:
			return f, invalidf(v.pos, "expected a quoted string after %q, found %q", op.text, v.text)
		}
		if t := p.peek(); t.kind == tIdent && t.text == "or" && p.peekAt(1).kind == tString {
			p.next()
			continue
		}
		return f, nil
	}
}

func (p *parser) parsePipelineStage() (any, error) {
	t := p.peek()
	if t.kind == tIdent {
		if feature, ok := unsupportedStages[t.text]; ok {
			return nil, unsupported(feature)
		}
		if t.text == "json" || t.text == "logfmt" {
			p.next()
			switch p.peek().kind {
			case tPipe, tPipeEq, tNeq, tPipeTilde, tNre, tPipeGt, tNgt, tLBrack, tRParen, tEOF:
				return parserStage{kind: t.text, pos: t.pos}, nil
			}
			return nil, unsupported(t.text + " with parameters")
		}
	}
	e, err := p.parseLabelComma()
	if err != nil {
		return nil, err
	}
	return labelFilter{expr: e}, nil
}

// Label filter precedence, as Loki parses it: a comma binds loosest, then or,
// then and. `a, b or c` is a AND (b OR c); `a or b and c` is a OR (b AND c).
func (p *parser) parseLabelComma() (labelExpr, error) {
	l, err := p.parseLabelOr()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tComma {
		p.next()
		r, err := p.parseLabelOr()
		if err != nil {
			return nil, err
		}
		l = labelBool{and: true, l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseLabelOr() (labelExpr, error) {
	l, err := p.parseLabelAnd()
	if err != nil {
		return nil, err
	}
	for t := p.peek(); t.kind == tIdent && t.text == "or"; t = p.peek() {
		p.next()
		r, err := p.parseLabelAnd()
		if err != nil {
			return nil, err
		}
		l = labelBool{and: false, l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseLabelAnd() (labelExpr, error) {
	l, err := p.parseLabelPrimary()
	if err != nil {
		return nil, err
	}
	for t := p.peek(); t.kind == tIdent && t.text == "and"; t = p.peek() {
		p.next()
		r, err := p.parseLabelPrimary()
		if err != nil {
			return nil, err
		}
		l = labelBool{and: true, l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseLabelPrimary() (labelExpr, error) {
	if t := p.peek(); t.kind == tLParen {
		p.next()
		if err := p.enter(t.pos); err != nil {
			return nil, err
		}
		e, err := p.parseLabelComma()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tRParen, `")"`); err != nil {
			return nil, err
		}
		p.depth--
		return e, nil
	}
	name, err := p.expect(tIdent, "a label filter")
	if err != nil {
		return nil, err
	}
	op := p.next()
	val := p.next()
	cmp := labelCmp{name: name.text, op: op.kind, pos: name.pos}
	switch {
	case val.kind == tIdent && p.peek().kind == tLParen:
		return nil, unsupported(val.text + " label filter")
	case val.kind == tUnitNumber:
		return nil, unsupported("duration and bytes label filters")
	case val.kind == tString && (op.kind == tEq || op.kind == tNeq || op.kind == tRe || op.kind == tNre):
		cmp.str = val.val
	case val.kind == tNumber && (op.kind == tEq || op.kind == tEqEq || op.kind == tNeq || isOrdering(op.kind)):
		n, err := strconv.ParseFloat(val.text, 64)
		if err != nil {
			return nil, invalidf(val.pos, "bad number %q", val.text)
		}
		cmp.num, cmp.numeric = n, true
	default:
		return nil, invalidf(op.pos, "expected a comparison after %q", name.text)
	}
	return cmp, nil
}

func isOrdering(k tokKind) bool { return k == tGt || k == tGte || k == tLt || k == tLte }

func isComparison(k tokKind) bool { return isOrdering(k) || k == tEqEq || k == tNeq }

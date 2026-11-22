package logql2ch

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tString
	tNumber
	tUnitNumber // a number followed by a unit: 5m, 1h30m, 250ms, 10KB
	tLBrace
	tRBrace
	tLParen
	tRParen
	tLBrack
	tRBrack
	tComma
	tPipe      // |
	tPipeEq    // |=
	tPipeTilde // |~
	tPipeGt    // |>
	tNeq       // !=
	tNre       // !~
	tNgt       // !>
	tEq        // =
	tEqEq      // ==
	tRe        // =~
	tGt        // >
	tGte       // >=
	tLt        // <
	tLte       // <=
	tArith     // + - * / % ^
)

type token struct {
	kind tokKind
	text string // the source text
	val  string // tString: the unquoted value
	pos  int    // byte offset in the query
}

// lex splits a LogQL query into tokens. It knows nothing about the grammar, so
// `!=` is one token whether it ends up a line filter or a label comparison.
func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
			continue
		case c == '#': // comment to end of line
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		case c == '"' || c == '`':
			val, n, err := lexString(src[i:])
			if err != nil {
				return nil, invalidf(i, "%v", err)
			}
			toks = append(toks, token{kind: tString, text: src[i : i+n], val: val, pos: i})
			i += n
			continue
		case isIdentStart(c):
			j := i + 1
			for j < len(src) && isIdentPart(src[j]) {
				j++
			}
			toks = append(toks, token{kind: tIdent, text: src[i:j], pos: i})
			i = j
			continue
		case isDigit(c) || (c == '-' && i+1 < len(src) && isDigit(src[i+1]) && afterComparison(toks)):
			j := i + 1
			for j < len(src) && (isDigit(src[j]) || src[j] == '.') {
				j++
			}
			// An exponent (1e3, 2.5E-2) is part of the number, not a unit.
			if j+1 < len(src) && (src[j] == 'e' || src[j] == 'E') {
				k := j + 1
				if src[k] == '+' || src[k] == '-' {
					k++
				}
				if k < len(src) && isDigit(src[k]) {
					for j = k; j < len(src) && isDigit(src[j]); j++ {
					}
				}
			}
			kind := tNumber
			if j < len(src) && isLetter(src[j]) {
				kind = tUnitNumber
				for j < len(src) && (isLetter(src[j]) || isDigit(src[j])) {
					j++
				}
			}
			toks = append(toks, token{kind: kind, text: src[i:j], pos: i})
			i = j
			continue
		}
		two := ""
		if i+1 < len(src) {
			two = src[i : i+2]
		}
		if kind, ok := twoCharTokens[two]; ok {
			toks = append(toks, token{kind: kind, text: two, pos: i})
			i += 2
			continue
		}
		if kind, ok := oneCharTokens[c]; ok {
			toks = append(toks, token{kind: kind, text: string(c), pos: i})
			i++
			continue
		}
		return nil, invalidf(i, "unexpected character %q", c)
	}
	return append(toks, token{kind: tEOF, pos: len(src)}), nil
}

var twoCharTokens = map[string]tokKind{
	"|=": tPipeEq, "|~": tPipeTilde, "|>": tPipeGt,
	"!=": tNeq, "!~": tNre, "!>": tNgt,
	"==": tEqEq, "=~": tRe, ">=": tGte, "<=": tLte,
}

var oneCharTokens = map[byte]tokKind{
	'{': tLBrace, '}': tRBrace, '(': tLParen, ')': tRParen, '[': tLBrack, ']': tRBrack,
	',': tComma, '|': tPipe, '=': tEq, '>': tGt, '<': tLt,
	'+': tArith, '-': tArith, '*': tArith, '/': tArith, '%': tArith, '^': tArith,
}

// lexString reads a LogQL string at the start of s: double-quoted with Go
// escapes, or a backtick raw string. It returns the value and the bytes consumed.
func lexString(s string) (string, int, error) {
	quote := s[0]
	for j := 1; j < len(s); j++ {
		switch {
		case s[j] == '\\' && quote == '"':
			j++ // skip the escaped byte
		case s[j] == quote:
			if quote == '`' {
				return s[1:j], j + 1, nil
			}
			val, err := strconv.Unquote(s[:j+1])
			if err != nil {
				return "", 0, fmt.Errorf("bad string %s: %v", s[:j+1], err)
			}
			return val, j + 1, nil
		}
	}
	return "", 0, fmt.Errorf("unterminated string")
}

// afterComparison reports whether a '-' here starts a negative number (as in
// `| delta > -5`) rather than being an arithmetic operator.
func afterComparison(toks []token) bool {
	if len(toks) == 0 {
		return false
	}
	switch toks[len(toks)-1].kind {
	case tGt, tGte, tLt, tLte, tEqEq, tEq, tNeq:
		return true
	}
	return false
}

func isIdentStart(c byte) bool { return isLetter(c) || c == '_' }
func isIdentPart(c byte) bool  { return isIdentStart(c) || isDigit(c) }
func isLetter(c byte) bool     { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isDigit(c byte) bool      { return c >= '0' && c <= '9' }

// parseDuration parses a Prometheus-style duration: one or more <int><unit>
// with unit ms, s, m, h, d, w or y (e.g. 5m, 1h30m).
func parseDuration(s string) (int64, error) {
	units := []struct {
		name string
		ns   int64
	}{
		{"ms", 1_000_000}, {"s", 1_000_000_000}, {"m", 60_000_000_000}, {"h", 3_600_000_000_000},
		{"d", 86_400_000_000_000}, {"w", 604_800_000_000_000}, {"y", 31_536_000_000_000_000},
	}
	var total int64
	rest := s
	for rest != "" {
		j := 0
		for j < len(rest) && isDigit(rest[j]) {
			j++
		}
		if j == 0 {
			return 0, fmt.Errorf("bad duration %q", s)
		}
		n, err := strconv.ParseInt(rest[:j], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("bad duration %q", s)
		}
		rest = rest[j:]
		matched := false
		for _, u := range units {
			if strings.HasPrefix(rest, u.name) && (u.name != "m" || !strings.HasPrefix(rest, "ms")) {
				if n > (math.MaxInt64-total)/u.ns {
					return 0, fmt.Errorf("duration %q is too long", s)
				}
				total += n * u.ns
				rest = rest[len(u.name):]
				matched = true
				break
			}
		}
		if !matched {
			return 0, fmt.Errorf("bad duration %q", s)
		}
	}
	return total, nil
}

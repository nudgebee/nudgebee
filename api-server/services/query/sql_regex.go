package query

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"

	"github.com/samber/lo"
)

// maxRegexFilterLength bounds a `_regex` pattern. A filter is a handful of
// alternatives, not a program; the cap keeps a pasted blob out of the planner.
const maxRegexFilterLength = 256

// maxRegexProgramSize bounds the pattern AFTER its repetitions are expanded,
// counted in compiled instructions. Length alone does not bound cost: the
// eleven characters of `(a{250}){4}` expand to a thousand states. Measured over
// 200 rows of 1,000 characters, that pattern took 2.3 s on ClickHouse and 0.7 s
// on Postgres where `central|edge` took 2 ms, and fifty-one `[a-z]{255}` took
// 2.0 s and 3.1 s. Of the shapes tried at or under 300 — counted, nested,
// dotted — the slowest took 85 ms on the same test.
const maxRegexProgramSize = 300

// maxRegexWideSteps bounds how many expanded steps may be a wildcard: `.`, or a
// class of more than wideRegexClassSize characters, which every negated class
// is. A fixed run of them between two literals keeps one candidate match alive
// per position per step. On a day of spans (1.09M), `(a|b|c|d|e).{K}(x|y|z)`
// cost 1.0x a plain pattern filter at K=4, 2.3x at K=16 and 6x at K=32; the
// same gap written `.*` cost 1.07x, which is what the refusal points to.
const maxRegexWideSteps = 16

const wideRegexClassSize = 32

// maxRegexRepetition is the largest count a `{m,n}` may carry. Postgres stops at
// 255 where RE2 allows 1000, so anything above would run on a traces panel and
// fail on an events panel.
const maxRegexRepetition = 255

// regexRepetition matches a counted repetition at the start of its input.
var regexRepetition = regexp.MustCompile(`^\{(\d+)(?:,(\d*))?\}`)

/*
ValidateRegexFilter accepts only the regex subset that means the same thing on
every engine this package emits a `_regex` filter for.

A stored panel filter is one string, but it runs as a Postgres ARE on the
metastore tables and as RE2 on ClickHouse, and the two disagree QUIETLY — the
query succeeds and returns different rows. Each rule below closes a disagreement
that was observed by running the same pattern on both engines:

  - `\b` is a word boundary in RE2 and a backspace in Postgres;
  - `\w` and `[[:alpha:]]` match "é" in Postgres and not in RE2, `\s` matches a
    vertical tab in Postgres and not in RE2;
  - `[[=a=]]` and `[[.a.]]` are an equivalence class and a collating element in
    Postgres, and a class of punctuation in RE2;
  - `(?i)ı` matches "I" in one and not the other;
  - `a{300}` and `^*` run in RE2 and are errors in Postgres.

Rather than translate between dialects, the pattern is held to their
intersection and anything outside it is refused with the construct named:

  - it must compile as RE2 (which rules out back-references and look-around);
  - a backslash may only escape punctuation — classes are written out, [0-9];
  - a bracket expression may not hold a POSIX `[:class:]`, `[=x=]` or `[.x.]`;
  - `(?` may only open a non-capturing group `(?:`, or be one leading `(?i)`,
    and a `(?i)` pattern is ASCII, where both engines fold case alike;
  - a counted repetition stops at 255, and no quantifier follows `^` or `$`;
  - expanded, it stays within maxRegexProgramSize and maxRegexWideSteps.

One disagreement is left standing because it lives in the data, not the pattern:
under `(?i)`, RE2 also folds the Kelvin sign (U+212A) to "k" and the long s
(U+017F) to "s", and Postgres does not. A value holding one of those two
characters can match on ClickHouse and not on Postgres.
*/
func ValidateRegexFilter(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("regex pattern is empty")
	}
	if len(pattern) > maxRegexFilterLength {
		return fmt.Errorf("regex pattern is longer than %d characters", maxRegexFilterLength)
	}
	if strings.HasPrefix(pattern, "(?i)") && strings.ContainsFunc(pattern, func(r rune) bool { return r > 0x7f }) {
		return fmt.Errorf("a case-insensitive (?i) regex may only contain ASCII characters")
	}
	inClass := false
	anchored := false // the previous token was an unescaped ^ or $
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		afterAnchor := anchored
		anchored = false
		switch {
		case c == '\\':
			if i+1 == len(pattern) {
				return fmt.Errorf("regex pattern ends with a bare backslash")
			}
			if next := pattern[i+1]; isASCIIAlphanumeric(next) {
				return fmt.Errorf(`regex escape \%c is not supported: a backslash may only escape punctuation, so write a class such as [0-9] or [A-Za-z0-9_] instead`, next)
			}
			i++
		case inClass:
			if c == ']' {
				inClass = false
			} else if c == '[' && i+1 < len(pattern) && strings.IndexByte(":=.", pattern[i+1]) >= 0 {
				return fmt.Errorf("regex class %q is not supported: list the characters or a range such as [a-z] instead", pattern[i:i+2])
			}
		case c == '[':
			inClass = true
			// A ] right after the opening bracket, or after its ^, is a member rather than the end.
			if i+1 < len(pattern) && pattern[i+1] == '^' {
				i++
			}
			if i+1 < len(pattern) && pattern[i+1] == ']' {
				i++
			}
		case c == '(':
			rest := pattern[i:]
			if !strings.HasPrefix(rest, "(?") || strings.HasPrefix(rest, "(?:") {
				continue
			}
			if i == 0 && strings.HasPrefix(rest, "(?i)") {
				continue
			}
			return fmt.Errorf("regex group %q is not supported: only (?:...) and one leading (?i) are", rest[:min(len(rest), 4)])
		case c == '^' || c == '$':
			anchored = true
		case c == '*' || c == '+' || c == '?':
			if afterAnchor {
				return fmt.Errorf("regex quantifier %q is not supported directly after ^ or $", string(c))
			}
		case c == '{':
			counts := regexRepetition.FindStringSubmatch(pattern[i:])
			if counts == nil {
				continue // a literal brace, not a repetition
			}
			if afterAnchor {
				return fmt.Errorf("regex quantifier %q is not supported directly after ^ or $", counts[0])
			}
			for _, count := range counts[1:] {
				if count == "" {
					continue // the open side of {m,}
				}
				if n, err := strconv.Atoi(count); err != nil || n > maxRegexRepetition {
					return fmt.Errorf("regex repetition %s is not supported: a count may not exceed %d", counts[0], maxRegexRepetition)
				}
			}
		}
	}
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return fmt.Errorf("invalid regex pattern: %w", err)
	}
	program, err := syntax.Compile(parsed.Simplify())
	if err != nil {
		return fmt.Errorf("invalid regex pattern: %w", err)
	}
	if len(program.Inst) > maxRegexProgramSize {
		return fmt.Errorf("regex pattern is too large once its repetitions are expanded (%d steps, the limit is %d): shorten it or lower its counts", len(program.Inst), maxRegexProgramSize)
	}
	if wide := regexWideSteps(program); wide > maxRegexWideSteps {
		return fmt.Errorf("regex pattern has too many wildcard steps once expanded (%d, the limit is %d): write a run such as .{40} as .* instead", wide, maxRegexWideSteps)
	}
	return nil
}

// regexWideSteps counts the compiled steps that accept most characters.
func regexWideSteps(program *syntax.Prog) int {
	wide := 0
	for _, inst := range program.Inst {
		switch inst.Op {
		case syntax.InstRuneAny, syntax.InstRuneAnyNotNL:
			wide++
		case syntax.InstRune:
			// Rune holds [lo, hi] pairs; a single rune has no pair and is never wide.
			size := 0
			for i := 0; i+1 < len(inst.Rune); i += 2 {
				size += int(inst.Rune[i+1]-inst.Rune[i]) + 1
			}
			if size > wideRegexClassSize {
				wide++
			}
		}
	}
	return wide
}

func isASCIIAlphanumeric(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// regexMatchExpr renders an unanchored, case-sensitive regex search of a column.
// Only the two engines ValidateRegexFilter's subset is defined for are served;
// any other dialect is refused rather than guessed at.
func regexMatchExpr(dialect sqlDialect, column string, pattern string, negate bool) (string, error) {
	switch dialect.(type) {
	case *postgresDialect:
		return column + lo.Ternary(negate, " !~ ", " ~ ") + dialect.QuoteLiteral(pattern), nil
	case *clickhouseDialect:
		// (?s) lets `.` match a newline, as it does in a Postgres ARE.
		return lo.Ternary(negate, "NOT ", "") + "match(" + column + ", " + dialect.QuoteLiteral("(?s)"+pattern) + ")", nil
	}
	return "", fmt.Errorf("regex filters are not supported on this data source")
}

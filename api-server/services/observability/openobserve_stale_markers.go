package observability

import "strings"

// Excluding OpenTelemetry "no recorded value" points from OpenObserve reads.
//
// When a series stops -- a pod deleted, a rollout replacing a ReplicaSet -- the OTel collector
// sends one final data point with the NoRecordedValue flag set, meaning "there is no value
// here". The wire format still requires a number, and that number is meaningless.
//
// Prometheus stores no sample for it. OpenObserve stores it as an ordinary sample and keeps the
// flag as an ordinary LABEL, so one pod has two series identical but for `flag`: the real
// measurement (`DATA_POINT_FLAGS_DO_NOT_USE` -- OTel's name for "no flags set") and the marker.
// Both match `resource="cpu"`, and the builders group with `max by (namespace, pod, container)`,
// which discards `flag` and lets the marker win its group. Measured on dev/workflow-server over
// one day: cpu_limit peaked at 4.56e+09 "cores", and a counter read 3,958,550 requests against a
// true 16,199 -- markers break the monotonicity rate() relies on, so each reads as a huge jump
// then a reset.
//
// Two constraints worth keeping in mind when editing this:
//
//   - The exclusion must go into the QUERY, not the response: the damaging `max by (...)` runs
//     inside OpenObserve, so by the time a payload arrives the marker has already won.
//   - A negative matcher also matches series LACKING the label, so this is inert against a
//     backend that never stored a flag -- which is why it is applied unconditionally.

const (
	// openObserveNoRecordedValueFlag is the label value OpenObserve stores for an OTel
	// data point whose NoRecordedValue flag was set.
	openObserveNoRecordedValueFlag = "DATA_POINT_FLAGS_NO_RECORDED_VALUE_MASK"
	// openObserveFlagLabel is the label OpenObserve renders OTel data-point flags into.
	openObserveFlagLabel = "flag"

	openObserveNoRecordedValueMatcher = openObserveFlagLabel + `!="` + openObserveNoRecordedValueFlag + `"`
)

// promQLKeywords are the bare words that are not metric names. A word followed by `(` is
// already recognised as a function call, so this list covers the ones that are NOT: set
// operators, the `offset` modifier, the bare literals, and — the case that is easy to miss —
// the aggregation operators, because `sum by (ns) (...)` puts a grouping clause between the
// operator and its parenthesis.
var promQLKeywords = map[string]struct{}{
	// set operators, modifiers and bare literals
	"and": {}, "or": {}, "unless": {}, "offset": {}, "bool": {},
	"inf": {}, "nan": {}, "start": {}, "end": {}, "atan2": {},
	// aggregation operators
	"sum": {}, "min": {}, "max": {}, "avg": {}, "group": {}, "stddev": {},
	"stdvar": {}, "count": {}, "count_values": {}, "bottomk": {}, "topk": {},
	"quantile": {}, "limitk": {}, "limit_ratio": {},
}

// promQLGroupingModifiers introduce a parenthesised list of LABEL names — `by (namespace,
// pod)`, `on (pod)`, `group_left(reason)`. Those names are not metric names, and appending a
// selector to each one produced `by (namespace{...}, pod{...})` until this was handled.
var promQLGroupingModifiers = map[string]struct{}{
	"by": {}, "without": {}, "on": {}, "ignoring": {},
	"group_left": {}, "group_right": {},
}

// excludeOpenObserveNoRecordedValue adds the exclusion to EVERY metric selector in a PromQL
// expression -- not just the first: the OOM-kill key joins two selectors and the latency keys
// divide one by another, so patching only the leading one leaves the marker in the other half
// of the same answer. A selector that already constrains `flag` is left alone, which keeps this
// idempotent and lets a caller opt out explicitly.
func excludeOpenObserveNoRecordedValue(expr string) string {
	runes := []rune(expr)
	var out strings.Builder
	out.Grow(len(expr) + 64)

	// Set after a grouping modifier, so its label list is copied verbatim rather than read
	// as a sequence of metric names.
	pendingLabelList := false

	for i := 0; i < len(runes); {
		c := runes[i]

		if c == '(' && pendingLabelList {
			pendingLabelList = false
			i = copyDelimited(&out, runes, i, '(', ')', true)
			continue
		}

		// Copied verbatim: a label value may contain braces or anything else that would
		// otherwise read as syntax.
		if c == '"' || c == '\'' || c == '`' {
			end := skipQuoted(runes, i)
			out.WriteString(string(runes[i:end]))
			i = end
			continue
		}

		// Durations, never selectors: injecting inside one produces `[5{...}m]`.
		if c == '[' {
			i = copyDelimited(&out, runes, i, '[', ']', false)
			continue
		}

		// A selector with no metric name, e.g. `{__name__="x"}`.
		if c == '{' {
			i = copySelector(&out, runes, i)
			continue
		}

		if isPromQLIdentStart(c) && !continuesPrevToken(runes, i) {
			j := i
			for j < len(runes) && isPromQLIdentChar(runes[j]) {
				j++
			}
			word := string(runes[i:j])
			out.WriteString(word)

			// One lookahead serves every decision below; the whitespace itself is
			// preserved so the rewrite stays readable beside the original.
			k := j
			for k < len(runes) && isSpace(runes[k]) {
				k++
			}
			nextIsParen := k < len(runes) && runes[k] == '('

			if _, isModifier := promQLGroupingModifiers[word]; isModifier {
				// Arm ONLY when a list actually follows: group_left/group_right take an
				// OPTIONAL include list where by/without/on/ignoring always carry one.
				// Arming unconditionally swallowed the next unrelated `(` -- a later
				// sum() -- leaving every selector inside it still carrying markers:
				// valid PromQL, no error, silently wrong. Reachable via raw user
				// PromQL (SupportsRawQuery).
				pendingLabelList = nextIsParen
				i = j
				continue
			}
			if _, isKeyword := promQLKeywords[word]; isKeyword {
				i = j
				continue
			}
			if nextIsParen {
				// A function call, not a metric name.
				i = j
				continue
			}
			if k < len(runes) && runes[k] == '{' {
				out.WriteString(string(runes[j:k]))
				i = copySelector(&out, runes, k)
				continue
			}
			// A bare metric name: `container_cpu_usage` or `container_cpu_usage[5m]`.
			out.WriteString("{" + openObserveNoRecordedValueMatcher + "}")
			i = j
			continue
		}

		out.WriteRune(c)
		i++
	}
	return out.String()
}

// copySelector copies a `{...}` selector, adding the exclusion unless `flag` is already
// constrained. An unterminated selector is left untouched, so the backend reports the parse
// error rather than this function inventing a different one.
func copySelector(out *strings.Builder, runes []rune, start int) int {
	end := start + 1
	for end < len(runes) {
		if runes[end] == '"' || runes[end] == '\'' || runes[end] == '`' {
			end = skipQuoted(runes, end)
			continue
		}
		if runes[end] == '}' {
			break
		}
		end++
	}
	if end >= len(runes) {
		out.WriteString(string(runes[start:]))
		return len(runes)
	}

	body := string(runes[start+1 : end])
	switch {
	case selectorConstrainsFlag(runes[start+1 : end]):
		out.WriteString("{" + body + "}")
	case strings.TrimSpace(body) == "":
		// `{}` and `{ }` are valid selectors -- the cluster placeholder expands to
		// nothing and leaves one behind.
		out.WriteString("{" + openObserveNoRecordedValueMatcher + "}")
	default:
		out.WriteString("{" + body + "," + openObserveNoRecordedValueMatcher + "}")
	}
	return end + 1
}

// selectorConstrainsFlag reports whether a selector body already constrains the flag label.
// The name is matched at a matcher boundary, so a label merely CONTAINING "flag"
// (`flagged_total`) or a VALUE of "flag" is not mistaken for one -- getting that wrong would
// skip the exclusion silently and leave the bug in place.
func selectorConstrainsFlag(body []rune) bool {
	isFlagMatcher := func(seg []rune) bool {
		name := strings.TrimSpace(string(seg))
		cut := strings.IndexAny(name, "=!~<>")
		return cut > 0 && strings.TrimSpace(name[:cut]) == openObserveFlagLabel
	}
	depth, segStart := 0, 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '"', '\'', '`':
			i = skipQuoted(body, i) - 1
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				if isFlagMatcher(body[segStart:i]) {
					return true
				}
				segStart = i + 1
			}
		}
	}
	return isFlagMatcher(body[segStart:])
}

// copyDelimited copies a bracketed run verbatim, returning the index just past its close.
// `nested` counts the open/close pair, which a `(...)` group needs; without it the first close
// ends the run, which is what a `[5m]` range or `[30m:75s]` subquery needs -- those hold
// durations, never selectors, so injecting inside one would produce `[5{...}m]`. An
// unterminated run consumes the rest and is left alone, so the backend reports the parse error
// rather than this function inventing a different one.
func copyDelimited(out *strings.Builder, runes []rune, start int, openCh, closeCh rune, nested bool) int {
	depth := 0
	i := start
	for i < len(runes) {
		switch runes[i] {
		case '"', '\'', '`':
			i = skipQuoted(runes, i)
			continue
		case openCh:
			depth++
		case closeCh:
			depth--
			if depth == 0 || !nested {
				i++
				out.WriteString(string(runes[start:i]))
				return i
			}
		}
		i++
	}
	out.WriteString(string(runes[start:]))
	return len(runes)
}

// skipQuoted returns the index just past the closing quote of the literal starting at start,
// honouring backslash escapes. An unterminated literal consumes the rest of the expression.
func skipQuoted(runes []rune, start int) int {
	quote := runes[start]
	i := start + 1
	for i < len(runes) {
		if runes[i] == '\\' {
			i += 2
			continue
		}
		if runes[i] == quote {
			return i + 1
		}
		i++
	}
	return len(runes)
}

// continuesPrevToken keeps the `e` of `1e-06` and the `m` of `5m` from scanning as the start
// of a metric name.
func continuesPrevToken(runes []rune, i int) bool {
	if i == 0 {
		return false
	}
	p := runes[i-1]
	return isPromQLIdentChar(p) || p == '.'
}

func isPromQLIdentStart(c rune) bool {
	return c == '_' || c == ':' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isPromQLIdentChar(c rune) bool {
	return isPromQLIdentStart(c) || (c >= '0' && c <= '9')
}

func isSpace(c rune) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

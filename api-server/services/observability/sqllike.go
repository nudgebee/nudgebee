package observability

import "strings"

// The canonical where-clause contract for _like / _ilike is SQL LIKE: `%` matches any
// run of characters, `_` matches exactly one, and `\%` / `\_` / `\\` are literals.
// Providers render it differently — Elasticsearch via sqlLikeToESWildcard, Loki via
// convertSQLLikeToRegex, and several others degrade it to a plain substring match — so
// anything that reasons about a pattern without executing it has to stay inside what
// every one of those renderings guarantees. These helpers exist for that: they extract
// the parts of a pattern that must appear in ANY string it matches, whichever backend
// runs it.

// sqlLikeSegments splits a SQL LIKE pattern into its literal segments: the runs of
// ordinary characters between unescaped `%` and `_` wildcards, with `\%`, `\_` and `\\`
// unescaped to the characters they denote.
//
//	"%error%"     → ["error"]
//	"api-server%" → ["api-server"]
//	"%5___%"      → ["5"]
//	"%a%b%"       → ["a", "b"]
//	`\%literal\%` → ["%literal%"]
//	"%" / "%%"    → nil
//
// A string matches the pattern only if it contains every returned segment, so the
// segments are a necessary (not sufficient) condition of a match — which is exactly
// what a diagnosis may rely on without risking a false accusation.
//
// Escapes are scanned rune by rune, mirroring sqlLikeToESWildcard: a lone `\` is a
// literal backslash, and only `\%`, `\_` and `\\` are escape sequences.
func sqlLikeSegments(pattern string) []string {
	var segments []string
	var sb strings.Builder
	flush := func() {
		if sb.Len() > 0 {
			segments = append(segments, sb.String())
			sb.Reset()
		}
	}
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		switch r := runes[i]; r {
		case '\\':
			if i+1 < len(runes) && (runes[i+1] == '%' || runes[i+1] == '_' || runes[i+1] == '\\') {
				sb.WriteRune(runes[i+1])
				i++
				continue
			}
			sb.WriteRune(r)
		case '%', '_':
			flush()
		default:
			sb.WriteRune(r)
		}
	}
	flush()
	return segments
}

// sqlLikeIsAnchored reports whether a pattern contains no unescaped `%` or `_`, i.e.
// whether SQL LIKE degenerates to plain equality on it.
func sqlLikeIsAnchored(pattern string) bool {
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		switch r := runes[i]; r {
		case '\\':
			if i+1 < len(runes) && (runes[i+1] == '%' || runes[i+1] == '_' || runes[i+1] == '\\') {
				i++
			}
		case '%', '_':
			return false
		}
	}
	return true
}

// containsAllSegments reports whether candidate contains every segment. Order and
// position are deliberately ignored: this must stay true for every backend rendering of
// the pattern, including the ones that reduce LIKE to a substring test, so it is a
// necessary condition of a match rather than a faithful evaluation of one.
func containsAllSegments(candidate string, segments []string, fold bool) bool {
	if fold {
		candidate = strings.ToLower(candidate)
	}
	for _, seg := range segments {
		if fold {
			seg = strings.ToLower(seg)
		}
		if !strings.Contains(candidate, seg) {
			return false
		}
	}
	return true
}

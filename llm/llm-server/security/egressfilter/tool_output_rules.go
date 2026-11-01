package egressfilter

import (
	"regexp"
	"sort"
	"strings"
)

// Tool-output rule ids. The token rules reuse the id of the outbound rule for
// the same format, so one disabled_rules entry covers both; the rest are
// tool-output only. They are not registered in the outbound registry: these
// hits cover the value only, while the outbound rules cover the whole match.
const (
	RuleIDURLPassword        = "url-password"
	RuleIDSecretAssignment   = "secret-assignment"
	RuleIDK8sEnvSecret       = "k8s-env-secret"
	RuleIDToolPrivateKey     = "private-key-block"
	RuleIDAuthorizationValue = "authorization-header"
)

// The rules aim for few false positives: a setting is redacted only when its
// name ends in a secret word (DB_PASSWORD, SLACK_BOT_TOKEN, "client_secret"),
// so names such as secretName or TOKEN_HEADER are left alone, and values that
// are references or placeholders ($VAR, ${VAR}, xxxxx, <...>) are kept.

// appendSegmentHits adds the hits of one segment, which starts at offset in
// the text. Each rule runs only when its cheap pre-check passes.
func appendSegmentHits(hits []Hit, seg string, offset int) []Hit {
	if strings.Contains(seg, "://") && strings.Contains(seg, "@") {
		for _, m := range urlPassword.FindAllStringSubmatchIndex(seg, -1) {
			if looksLikeSecretValue(seg[m[4]:m[5]]) {
				hits = append(hits, Hit{RuleID: RuleIDURLPassword, Start: offset + m[4], End: offset + m[5]})
			}
		}
	}
	if strings.ContainsAny(seg, "=:") {
		for _, w := range secretNameWindows(seg) {
			for _, rule := range secretAssignmentRules {
				hits = appendAssignmentHits(hits, rule, seg[w.start:w.end], offset+w.start, RuleIDSecretAssignment)
			}
		}
	}
	for _, token := range toolOutputTokenPatterns {
		if !containsAny(seg, token.literals) {
			continue
		}
		for _, m := range token.re.FindAllStringSubmatchIndex(seg, -1) {
			start := m[0]
			if len(m) >= 4 && m[3] >= 0 {
				// Keep a captured prefix (the Authorization header name).
				start = m[3]
			}
			hits = append(hits, Hit{RuleID: token.id, Start: offset + start, End: offset + m[1]})
		}
	}
	return hits
}

// appendAssignmentHits adds a hit for the value (group "v") of each match of
// rule in part, which starts at offset in the text. Surrounding quotes stay,
// and values that are references, placeholders or prose are skipped.
func appendAssignmentHits(hits []Hit, rule *regexp.Regexp, part string, offset int, ruleID string) []Hit {
	vi := rule.SubexpIndex("v")
	for _, m := range rule.FindAllStringSubmatchIndex(part, -1) {
		start, end := m[2*vi], m[2*vi+1]
		if start < 0 {
			continue
		}
		value := part[start:end]
		if !looksLikeSecretValue(value) || (rule == yamlSecretRule && looksLikeProse(value)) {
			continue
		}
		switch {
		case len(value) >= 4 && strings.HasPrefix(value, `\"`) && strings.HasSuffix(value, `\"`):
			start, end = start+2, end-2
		case len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0]:
			start, end = start+1, end-1
		}
		hits = append(hits, Hit{RuleID: ruleID, Start: offset + start, End: offset + end})
	}
	return hits
}

type redactionSegment struct {
	text string
	sep  string // what followed the segment in the original text
}

// maxRedactionSegment bounds how much text one regex pass sees. Tool output
// is often a single multi-megabyte line of JSON, and running the rules over
// all of it took seconds.
const maxRedactionSegment = 16 << 10

// splitRedactionSegments splits text at newlines and at escaped "\n" (JSON
// strings hold multi-line logs that way), then cuts any segment longer than
// maxRedactionSegment at a space or comma. No rule matches across a newline
// or a backslash, so the split never hides a match; only a secret that
// straddles a forced cut can be missed.
func splitRedactionSegments(text string) []redactionSegment {
	var segments []redactionSegment
	start := 0
	for i := 0; i < len(text); i++ {
		sep := ""
		switch {
		case text[i] == '\n':
			sep = "\n"
		case text[i] == '\\' && i+1 < len(text) && text[i+1] == 'n':
			sep = `\n`
		case i-start >= maxRedactionSegment && (text[i] == ' ' || text[i] == ','):
			sep = text[i : i+1]
		case i-start >= 2*maxRedactionSegment:
			// No separator in sight (a base64 blob): cut anyway. The cut
			// character stays in the next segment.
			segments = append(segments, redactionSegment{text: text[start:i]})
			start = i
			continue
		}
		if sep == "" {
			continue
		}
		segments = append(segments, redactionSegment{text: text[start:i], sep: sep})
		i += len(sep) - 1
		start = i + 1
	}
	return append(segments, redactionSegment{text: text[start:]})
}

type textWindow struct{ start, end int }

// secretNameWindows returns the merged windows of seg around each secret word
// that ends a name and is followed by "=", ":" or `":`. The setting rules run
// only there: running them over whole segments cost about 0.5s per rule on
// 5 MB of dense JSON, where nearly every segment contains a word such as "key".
func secretNameWindows(seg string) []textWindow {
	lower := asciiLower(seg)
	var windows []textWindow
	for _, word := range secretNameWords {
		for from := 0; ; {
			idx := strings.Index(lower[from:], word)
			if idx < 0 {
				break
			}
			idx += from
			from = idx + len(word)
			if end, ok := settingValueStart(lower, from); ok {
				windows = append(windows, textWindow{max(0, idx-maxSecretNamePrefix), valueWindowEnd(seg, end+maxSecretValueWindow)})
			}
		}
	}
	if len(windows) < 2 {
		return windows
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].start < windows[j].start })
	merged := windows[:1]
	for _, w := range windows[1:] {
		last := &merged[len(merged)-1]
		if w.start <= last.end {
			last.end = max(last.end, w.end)
			continue
		}
		merged = append(merged, w)
	}
	return merged
}

// maxSecretNamePrefix is how far before a secret word its name may start
// ("encryption_" in encryption_key); maxSecretValueWindow is how much of the
// value a window covers before valueWindowEnd looks for where it stops.
const (
	maxSecretNamePrefix  = 24
	maxSecretValueWindow = 2048
)

// valueWindowEnd moves a window end at i forward to the end of the value it
// falls in (whitespace, a quote that is not escaped, or the segment end), so a
// value longer than maxSecretValueWindow is not cut and left half visible.
func valueWindowEnd(seg string, i int) int {
	for ; i < len(seg); i++ {
		switch seg[i] {
		case ' ', '\t', '\r', '\n', '\'':
			return i + 1
		case '"':
			if seg[i-1] != '\\' {
				return i + 1
			}
		}
	}
	return len(seg)
}

// settingValueStart reports whether the name that contains a secret word at
// lower[:from] ends within a few characters and is followed by "=", ":" or
// `":`, and returns where the delimiter is.
func settingValueStart(lower string, from int) (int, bool) {
	i := from
	for i < len(lower) && i-from <= 16 {
		c := lower[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' {
			i++
			continue
		}
		break
	}
	if i < len(lower) && lower[i] == '"' {
		i++
		for i < len(lower) && (lower[i] == ' ' || lower[i] == '\t') {
			i++
		}
	}
	if i < len(lower) && (lower[i] == '=' || lower[i] == ':') {
		return i, true
	}
	return 0, false
}

// secretNameWords are the cheap pre-check for secretNameSuffix: a segment
// with none of them cannot hold a secret-named setting.
var secretNameWords = []string{"pass", "pwd", "secret", "token", "key", "credential", "cookie", "license"}

// asciiLower lower-cases ASCII letters only, so byte offsets in the result
// are offsets in s. strings.ToLower can change a rune's byte length (U+023A
// becomes three bytes), which would put offsets past the end of s.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// secretNameSuffix matches a setting name that ends in a secret word.
const secretNameSuffix = `(?:password|passwd|[_-]pwd|secret|token|api[_-]?key|apikey|access[_-]?key|secret[_-]?key|private[_-]?key|client[_-]?secret|credentials?|auth[_-]?key|encryption[_-]?key|signing[_-]?key|erlang[_-]?cookie|license)`

// secretAssignmentRules match `NAME=value`, `NAME: value` and `"name": "value"`
// where NAME ends in a secret word. Group "v" is the value. The patterns start
// at the secret word rather than at the start of NAME: the value is all that
// is replaced, and a pattern that opens with [A-Za-z0-9_.-]* starts a new
// match attempt at every letter, which made multi-megabyte output take seconds.
var secretAssignmentRules = []*regexp.Regexp{
	// "name": "value"  (JSON)
	regexp.MustCompile(`(?i)(` + secretNameSuffix + `"\s*:\s*")(?P<v>[^"\\]*(?:\\.[^"\\]*)*)(")`),
	// NAME=value  (env, flags, query strings). No spaces around "=": with
	// spaces it is usually code (api_secret = os.environ[...]).
	regexp.MustCompile(`(?i)(` + secretNameSuffix + `=)(?P<v>` + escapedQuotedValue + `|"[^"\n]*"|'[^'\n]*'|[^\s"'\\,;&|]+)()`),
	// NAME: value  (YAML). Also matches prose ("failed to decrypt token:
	// cipher error"), so its values are checked with looksLikeProse.
	regexp.MustCompile(`(?i)(` + secretNameSuffix + `:[ \t]+)(?P<v>` + escapedQuotedValue + `|"[^"\n]*"|'[^'\n]*'|[^\s"'\\,]+)()`),
}

// escapedQuotedValue is a value quoted with \" , as env and YAML text is when
// it sits inside a JSON string (a log line in a JSON tool response).
const escapedQuotedValue = `\\"[^"\\\n]*\\"`

// yamlSecretRule is the `NAME: value` rule, whose matches need the prose check.
var yamlSecretRule = secretAssignmentRules[2]

// looksLikeProse reports a value that reads like the next word of a sentence
// rather than a secret: lower-case letters only, or ending in punctuation.
// A YAML secret such as `password: hunter2hunter2` has a digit or mixed case.
func looksLikeProse(value string) bool {
	v := strings.Trim(value, `\"'`)
	if strings.HasSuffix(v, ":") || strings.HasSuffix(v, ".") || strings.HasSuffix(v, ",") || strings.HasSuffix(v, ";") {
		return true
	}
	for _, r := range v {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// k8sEnvSecretValue matches a Kubernetes env entry whose name ends in a secret
// word: `- name: DB_PASSWORD` followed by `value: ...` on the next line.
var k8sEnvSecretValue = regexp.MustCompile(`(?i)(name:[ \t]*"?[A-Za-z0-9_.-]*` + secretNameSuffix + `"?[ \t]*(?:\r?\n|\\n)[ \t]*value:[ \t]*)(?P<v>` + escapedQuotedValue + `|"[^"\n]*"|'[^'\n]*'|[^\s"'\\]+)()`)

// looksLikeSecretValue reports a value that may be a credential: not empty, a
// reference, a placeholder, an earlier redaction or code that names a secret.
func looksLikeSecretValue(value string) bool {
	v := strings.Trim(value, `\"'`)
	if len(v) < 4 {
		return false
	}
	lower := strings.ToLower(v)
	switch lower {
	case "true", "false", "null", "none", "nil", "required", "optional", "redacted", "******", "xxxxx":
		return false
	}
	if strings.HasPrefix(v, "$") || strings.HasPrefix(v, "<") || strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[") ||
		strings.Trim(v, "*x") == "" {
		return false
	}
	// Source code and config that name a secret without holding it: a call
	// or index (os.getenv(...), secrets[key]), a file path, or an identifier
	// such as api_key, clientSecret, account.AccessKey or GITHUB_TOKEN_KEY.
	if strings.ContainsAny(v, "()[]>") || strings.HasPrefix(v, "/") || strings.HasPrefix(v, "./") || strings.HasPrefix(v, "~/") ||
		codeIdentifier.MatchString(v) && (strings.ContainsAny(v, "._") || camelCase.MatchString(v)) {
		return false
	}
	return true
}

// codeIdentifier matches a dotted name with no digits; camelCase finds a
// lower-to-upper step inside one.
var (
	codeIdentifier = regexp.MustCompile(`^[A-Za-z_]+(?:\.[A-Za-z_]+)*$`)
	camelCase      = regexp.MustCompile(`[a-z][A-Z]`)
)

// urlPassword matches the password part of scheme://user:password@host.
var urlPassword = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://[^\s/:@"'<>]+:)([^\s@"'\\<>/]+)(@)`)

var privateKeyBlock = regexp.MustCompile(`-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----[\s\S]*?-----END (?:[A-Z]+ )?PRIVATE KEY-----`)

// toolOutputTokenPatterns match credentials by their own shape, wherever they
// appear. A first capture group, when present, is kept (a header name).
var toolOutputTokenPatterns = []struct {
	id       string
	literals []string // one must appear for the pattern to be tried
	re       *regexp.Regexp
}{
	{"aws-access-key-id", []string{"AKIA", "ASIA"}, regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"github-pat", []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_"}, regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`)},
	{"github-fine-grained-pat", []string{"github_pat_"}, regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{40,}\b`)},
	{"gitlab-pat", []string{"glpat-"}, regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}\b`)},
	{"slack-bot-token", []string{"xox"}, regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`)},
	{"google-oauth-client-secret", []string{"GOCSPX-"}, regexp.MustCompile(`\bGOCSPX-[A-Za-z0-9_-]{20,}\b`)},
	{"gcp-api-key", []string{"AIza"}, regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"anthropic-api-key", []string{"sk-ant-"}, regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{32,}\b`)},
	{"openai-api-key", []string{"sk-"}, regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{32,}\b`)},
	{RuleIDAuthorizationValue, []string{"uthorization", "UTHORIZATION"}, regexp.MustCompile(`(?i)\b(Authorization:[ \t]*(?:Bearer|Basic|Token)[ \t]+)[A-Za-z0-9._~+/=-]{8,}`)},
}

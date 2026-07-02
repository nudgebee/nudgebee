package audit

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// redactedPlaceholder replaces the value of any field whose key looks like a
// secret (and any secret matched by content). The key is preserved so an audit
// still records that a sensitive field was set/changed, without ever persisting
// its cleartext value.
const redactedPlaceholder = "[REDACTED]"

// sensitiveKeySubstrings match anywhere in the normalized (lowercase,
// alphanumeric-only) key. These tokens are specific enough that a substring hit
// is almost always a real secret — e.g. "secret" covers access_secret,
// client_secret, secret_key, webhook_secret; the "*key" tokens cover the
// credential-bearing key fields without matching benign identifiers.
var sensitiveKeySubstrings = []string{
	"secret",
	"password",
	"passwd",
	"passphrase",
	"apikey",
	"accesskey",
	"privatekey",
	"signingkey",
	"encryptionkey",
}

// sensitiveTerminalSegments are generic words that denote a secret only when
// they are the field's *final* meaningful segment. This matches access_token /
// refresh_token / authorization / cookie / credentials while leaving benign
// fields that merely contain the word alone — verification_token_expiration,
// token_count, cookie_policy, authorization_status, credential_type. Trailing
// version/numeric qualifiers (v2, 1) are skipped when finding the final segment.
var sensitiveTerminalSegments = map[string]bool{
	"token":         true,
	"authorization": true,
	"cookie":        true,
	"bearer":        true,
	"credential":    true,
	"credentials":   true,
}

// valueScrubbers redact high-confidence secret shapes found inside a *value*
// string (not its key) — the residual leak class for free-text carriers such as
// CLI command output. Deliberately limited to near-zero-false-positive patterns.
// NOTE: arbitrary secrets in free-text that match no pattern and sit under a
// non-sensitive key are NOT covered — key-based redaction is the primary control
// and value scrubbing is only a backstop.
var valueScrubbers = []*regexp.Regexp{
	// AWS access key IDs (AKIA/ASIA/…). The 40-char secret access key has no
	// distinctive shape and is intentionally not pattern-matched here; the
	// JSON-in-string recursion covers the common `{"SecretAccessKey":...}` shape.
	regexp.MustCompile(`\b(?:AKIA|ASIA|AGPA|AIDA|AROA|ANPA|ANVA)[0-9A-Z]{16}\b`),
	// PEM private key blocks.
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
}

// normalizeKey lowercases a key and strips non-alphanumeric characters so
// "access_secret", "AccessSecret" and "access-secret" all collapse to
// "accesssecret" for substring matching.
func normalizeKey(key string) string {
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range key {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// segments splits a key into lowercase alphanumeric words on non-alphanumeric
// boundaries and camelCase transitions ("access_secret" and "accessSecret" both
// -> [access, secret]).
func segments(key string) []string {
	var segs []string
	var b strings.Builder
	prevLowerOrDigit := false
	flush := func() {
		if b.Len() > 0 {
			segs = append(segs, b.String())
			b.Reset()
		}
	}
	for _, r := range key {
		switch {
		case r >= 'A' && r <= 'Z':
			if prevLowerOrDigit {
				flush()
			}
			b.WriteRune(r + ('a' - 'A'))
			prevLowerOrDigit = false
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			prevLowerOrDigit = true
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			prevLowerOrDigit = true
		default:
			flush()
			prevLowerOrDigit = false
		}
	}
	flush()
	return segs
}

// isQualifier reports whether a segment is a trailing version/numeric marker
// (v2, 1) that should be ignored when locating a key's final meaningful segment.
func isQualifier(s string) bool {
	if s == "" {
		return true
	}
	if s[0] == 'v' && len(s) > 1 && allDigits(s[1:]) {
		return true
	}
	return allDigits(s)
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isSensitiveKey reports whether a field key names a secret that must be redacted.
func isSensitiveKey(key string) bool {
	nk := normalizeKey(key)
	if nk == "" {
		return false
	}
	for _, tok := range sensitiveKeySubstrings {
		if strings.Contains(nk, tok) {
			return true
		}
	}
	segs := segments(key)
	for i := len(segs) - 1; i >= 0; i-- {
		if isQualifier(segs[i]) {
			continue
		}
		return sensitiveTerminalSegments[segs[i]]
	}
	return false
}

// redactValue returns a redacted copy of an arbitrary audit payload (map,
// struct, slice or scalar). It normalizes to a generic JSON tree via a
// marshal/unmarshal round-trip so both map[string]any (from LogChange / row
// scans) and typed structs (from direct EventState assignments) are handled
// uniformly, and the caller's original value is never mutated. UseNumber keeps
// integers exact across the round-trip (no float64 precision loss).
//
// If the value cannot be JSON-encoded the original is returned unchanged: there
// is nothing to leak (it could not have been serialized into the audit either)
// and the existing marshal-error handling in CreateAudit still applies.
func redactValue(v any) any {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	return redactGeneric(decodeGeneric(data))
}

// decodeGeneric decodes JSON bytes into a generic tree with numbers preserved
// as json.Number (exact re-marshaling).
func decodeGeneric(data []byte) any {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(data)
	}
	return v
}

// redactGeneric walks a generic JSON tree in place, replacing the value of any
// sensitive key with redactedPlaceholder, recursing into nested maps/slices, and
// scrubbing string values (including strings that are themselves serialized JSON,
// e.g. a cloud account's `data` column or a CLI command's JSON stdout).
func redactGeneric(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if isSensitiveKey(k) {
				t[k] = redactedPlaceholder
			} else {
				t[k] = redactGeneric(val)
			}
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = redactGeneric(val)
		}
		return t
	case string:
		return redactString(t)
	default:
		return v
	}
}

// redactString redacts a string value. If the whole string is serialized JSON
// (object/array) its interior is redacted recursively and re-serialized; every
// other string is content-scrubbed for high-confidence secret shapes.
func redactString(s string) any {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) > 1 && (trimmed[0] == '{' || trimmed[0] == '[') {
		if inner, ok := decodeJSONContainer(trimmed); ok {
			if b, err := json.Marshal(redactGeneric(inner)); err == nil {
				return string(b)
			}
		}
	}
	return scrubValue(s)
}

// decodeJSONContainer parses s as a single JSON object/array, consuming the
// entire string (mixed text like `{...}\nWarning:` is treated as non-JSON so its
// non-secret remainder is preserved and only content-scrubbed).
func decodeJSONContainer(s string) (any, bool) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if dec.More() {
		return nil, false
	}
	switch v.(type) {
	case map[string]any, []any:
		return v, true
	}
	return nil, false
}

func scrubValue(s string) string {
	for _, re := range valueScrubbers {
		s = re.ReplaceAllString(s, redactedPlaceholder)
	}
	return s
}

// redactAudit returns a copy of the audit with EventState, EventPrevState and
// EventAttr redacted. It is the single choke point applied on every write path
// (DB insert in CreateAudit and MQ publish in PublishAuditEvent), so no audit
// leaves this service with a cleartext secret regardless of what a caller passed.
// Redaction is idempotent.
func redactAudit(a Audit) Audit {
	a.EventState = redactValue(a.EventState)
	a.EventPrevState = redactValue(a.EventPrevState)
	if a.EventAttr != nil {
		if red, ok := redactValue(a.EventAttr).(map[string]any); ok {
			a.EventAttr = red
		}
	}
	return a
}

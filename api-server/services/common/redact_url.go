package common

import (
	"log/slog"
	"net/url"
	"strings"
)

// redactedValue replaces the value of a secret-bearing query parameter.
const redactedValue = "REDACTED"

// secretQueryKeys are query parameter names whose values are credentials.
// Inbound webhooks authenticate with ?token=<integration token>, so any log
// line carrying the raw query string hands that token to whoever can read
// logs. Matched case-insensitively.
var secretQueryKeys = map[string]struct{}{
	"token":         {},
	"access_token":  {},
	"authorization": {},
	"api_key":       {},
	"apikey":        {},
	"secret":        {},
	"password":      {},
}

// RedactQuerySecrets returns rawQuery (the part after '?', undecoded) with
// the values of secret-bearing parameters replaced. Parameter order and every
// other byte are kept, and malformed pairs pass through unchanged, so the
// result stays useful for debugging.
func RedactQuerySecrets(rawQuery string) string {
	if rawQuery == "" || !strings.Contains(rawQuery, "=") {
		return rawQuery
	}
	pairs := strings.Split(rawQuery, "&")
	changed := false
	for i, pair := range pairs {
		key, _, hasValue := strings.Cut(pair, "=")
		if !hasValue {
			continue
		}
		name, err := url.QueryUnescape(key)
		if err != nil {
			name = key
		}
		if _, secret := secretQueryKeys[strings.ToLower(name)]; secret {
			pairs[i] = key + "=" + redactedValue
			changed = true
		}
	}
	if !changed {
		return rawQuery
	}
	return strings.Join(pairs, "&")
}

// RedactURLSecrets returns rawURL with the values of secret-bearing query
// parameters replaced (see RedactQuerySecrets). Use it before logging a URL
// that may carry a credential, such as an inbound webhook's request URL.
func RedactURLSecrets(rawURL string) string {
	base, query, hasQuery := strings.Cut(rawURL, "?")
	if !hasQuery {
		return rawURL
	}
	query, fragment, hasFragment := strings.Cut(query, "#")
	out := base + "?" + RedactQuerySecrets(query)
	if hasFragment {
		out += "#" + fragment
	}
	return out
}

// RedactRequestLogAttr is a slog.HandlerOptions.ReplaceAttr function that
// redacts credentials from string attributes named "query" (a raw query
// string, as the slog-gin access log records for every request under
// "request") or "url" / "uri", at any nesting level. Without it each webhook
// delivery, which authenticates with ?token=, writes its integration token to
// the access log.
func RedactRequestLogAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() != slog.KindString {
		return a
	}
	var redact func(string) string
	switch a.Key {
	case "query":
		redact = RedactQuerySecrets
	case "url", "uri":
		redact = RedactURLSecrets
	default:
		return a
	}
	if redacted := redact(a.Value.String()); redacted != a.Value.String() {
		return slog.String(a.Key, redacted)
	}
	return a
}

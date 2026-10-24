package logsafe

import (
	"errors"
	"net/url"
	"sort"
	"strings"
)

// ScrubbedValue replaces a credential's literal value inside free text.
const ScrubbedValue = "***"

// minScrubLength keeps short values such as "true" or "8080" from blanking unrelated text.
const minScrubLength = 6

// SecretValues returns the literal forms a secret can take inside text: the raw
// value and its URL-encoded forms, longest first so a secret containing another
// is replaced whole. Values shorter than minScrubLength are skipped.
func SecretValues(secrets map[string]any) []string {
	seen := make(map[string]bool)
	for _, v := range secrets {
		s, ok := v.(string)
		if !ok || len(s) < minScrubLength {
			continue
		}
		seen[s] = true
		seen[url.QueryEscape(s)] = true
		seen[url.PathEscape(s)] = true
	}
	values := make([]string, 0, len(seen))
	for s := range seen {
		values = append(values, s)
	}
	sort.Slice(values, func(i, j int) bool {
		if len(values[i]) != len(values[j]) {
			return len(values[i]) > len(values[j])
		}
		return values[i] < values[j]
	})
	return values
}

// ScrubSecretValues replaces every known credential value in text with ***.
func ScrubSecretValues(text string, secretValues []string) string {
	for _, v := range secretValues {
		text = strings.ReplaceAll(text, v, ScrubbedValue)
	}
	return text
}

// ScrubValue returns a copy of v with credential values replaced in every string.
func ScrubValue(v any, secretValues []string) any {
	switch t := v.(type) {
	case string:
		return ScrubSecretValues(t, secretValues)
	case map[string]any:
		if t == nil {
			return t
		}
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[ScrubSecretValues(k, secretValues)] = ScrubValue(val, secretValues)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = ScrubValue(val, secretValues)
		}
		return out
	}
	return v
}

// RedactURL reduces a URL to its scheme and host for a log line or an error.
// Webhook URLs and APIs keyed in the query string carry the credential in the
// path or query, so neither is kept; nor is userinfo.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "<redacted url>"
	}
	return u.Scheme + "://" + u.Host
}

// redactedURLError is an error whose text had a *url.Error's URL reduced. It
// unwraps to the reduced *url.Error, never to the original: Temporal sends a
// step error's whole unwrap chain, and the original holds the full URL.
type redactedURLError struct {
	msg   string
	cause *url.Error
}

func (e *redactedURLError) Error() string { return e.msg }
func (e *redactedURLError) Unwrap() error { return e.cause }

// RedactURLError reduces the URL in the *url.Error that err holds, as returned
// by url.Parse and http.Client.Do, to its scheme and host. The *url.Error's own
// cause is kept, so a canceled or timed-out request still reads as one; any
// wrapping between err and the *url.Error survives in the text only.
func RedactURLError(err error) error {
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		return err
	}
	redacted := &url.Error{Op: uerr.Op, URL: RedactURL(uerr.URL), Err: uerr.Err}
	if err == error(uerr) {
		return redacted
	}
	return &redactedURLError{msg: strings.ReplaceAll(err.Error(), uerr.Error(), redacted.Error()), cause: redacted}
}

package logsafe

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretValues_CoversURLEncodedForms(t *testing.T) {
	secret := "p@ss/w+rd 1"
	values := SecretValues(map[string]any{"k": secret})

	for _, form := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
		assert.Contains(t, values, form)
		assert.Equal(t, "x=***", ScrubSecretValues("x="+form, values))
	}
}

// Short values would blank every "true" or "8080" in unrelated errors.
func TestSecretValues_SkipsShortAndNonStringValues(t *testing.T) {
	values := SecretValues(map[string]any{"a": "true", "b": 8080, "c": "long-enough"})
	assert.Equal(t, []string{"long-enough"}, values)
	assert.Equal(t, "flag true on port 8080", ScrubSecretValues("flag true on port 8080", values))
}

// A secret containing another must be replaced whole, not leave its tail behind.
func TestScrubSecretValues_ReplacesLongestFirst(t *testing.T) {
	values := SecretValues(map[string]any{"short": "abcdef", "long": "abcdef-ghijkl"})
	assert.Equal(t, "key=***", ScrubSecretValues("key=abcdef-ghijkl", values))
}

func TestScrubSecretValues_EmptyInputs(t *testing.T) {
	assert.Equal(t, "", ScrubSecretValues("", []string{"abcdef"}))
	assert.Equal(t, "nothing", ScrubSecretValues("nothing", nil))
}

func TestRedactURL_KeepsSchemeAndHostOnly(t *testing.T) {
	for raw, want := range map[string]string{
		"https://hooks.slack.com/services/T0/B0/Zx9canary":   "https://hooks.slack.com",
		"https://api.example.com:8443/v1?api_key=Zx9canary":  "https://api.example.com:8443",
		"https://user:Zx9canary@proxy.example.com/path#frag": "https://proxy.example.com",
		"not a url":                    "<redacted url>",
		"%zz://bad":                    "<redacted url>",
		"/relative/path?token=x":       "<redacted url>",
		"https://outlook.office.com/x": "https://outlook.office.com",
	} {
		assert.Equal(t, want, RedactURL(raw), raw)
	}
}

func TestRedactURLError(t *testing.T) {
	bare := &url.Error{Op: "Post", URL: "https://hooks.example.com/T0/Zx9canary?token=Zx9canary", Err: context.Canceled}
	got := RedactURLError(bare)
	assert.Equal(t, `Post "https://hooks.example.com": context canceled`, got.Error())
	assert.ErrorIs(t, got, context.Canceled, "a bare *url.Error keeps its cause")

	parseErr := &url.Error{Op: "parse", URL: "http://user:Zx9canary@proxy.example.com/x", Err: errors.New("invalid control character in URL")}
	wrapped := RedactURLError(fmt.Errorf("invalid URL: %w", parseErr))
	assert.NotContains(t, wrapped.Error(), "Zx9canary")
	assert.Contains(t, wrapped.Error(), "invalid URL: parse")

	// Temporal sends every error in the unwrap chain, so none may hold the URL,
	// and a wrapped cancellation must still read as one.
	doErr := &url.Error{Op: "Post", URL: "https://hooks.example.com/Zx9canary", Err: context.Canceled}
	chained := RedactURLError(fmt.Errorf("failed to execute request: %w", doErr))
	assert.ErrorIs(t, chained, context.Canceled)
	var uerr *url.Error
	require.ErrorAs(t, chained, &uerr)
	assert.Equal(t, "https://hooks.example.com", uerr.URL)
	for e := chained; e != nil; e = errors.Unwrap(e) {
		assert.NotContains(t, e.Error(), "Zx9canary")
	}

	plain := errors.New("no url here")
	assert.Same(t, plain, RedactURLError(plain))
	assert.NoError(t, RedactURLError(nil))
}

package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSQLLikeSegments(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{"surrounded", "%error%", []string{"error"}},
		{"prefix", "api-server%", []string{"api-server"}},
		{"underscore wildcards", "%5___%", []string{"5"}},
		{"multiple segments", "%a%b%", []string{"a", "b"}},
		{"escaped percent is literal", `\%literal\%`, []string{"%literal%"}},
		{"escaped underscore is literal", `a\_b`, []string{"a_b"}},
		{"escaped backslash is literal", `a\\b`, []string{`a\b`}},
		{"lone backslash is literal", `a\b`, []string{`a\b`}},
		{"match-everything has no segments", "%", nil},
		{"double percent has no segments", "%%", nil},
		{"single underscore has no segments", "_", nil},
		{"empty", "", nil},
		{"no wildcards", "plain", []string{"plain"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sqlLikeSegments(tt.pattern))
		})
	}
}

func TestSQLLikeIsAnchored(t *testing.T) {
	assert.True(t, sqlLikeIsAnchored("plain"))
	assert.True(t, sqlLikeIsAnchored(`\%escaped\%`))
	assert.True(t, sqlLikeIsAnchored(`a\_b`))
	assert.False(t, sqlLikeIsAnchored("%error%"))
	assert.False(t, sqlLikeIsAnchored("api-server%"))
	assert.False(t, sqlLikeIsAnchored("a_b"))
}

func TestContainsAllSegments(t *testing.T) {
	assert.True(t, containsAllSegments("payment-service-v2", []string{"payment"}, false))
	assert.True(t, containsAllSegments("my-api-server-1", []string{"api-server"}, false),
		"unanchored: a substring-degraded provider really would match this")
	assert.False(t, containsAllSegments("prod", []string{"auth"}, false))
	assert.True(t, containsAllSegments("PROD", []string{"prod"}, true))
	assert.False(t, containsAllSegments("PROD", []string{"prod"}, false))
	assert.True(t, containsAllSegments("axxxb", []string{"a", "b"}, false))
	assert.False(t, containsAllSegments("axxx", []string{"a", "b"}, false))
}

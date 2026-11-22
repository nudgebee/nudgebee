package analytics

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEscapeLikePattern verifies that a literal title prefix cannot widen the
// exclusion. "%" and "_" are LIKE wildcards, so an unescaped prefix containing
// either would silently drop conversations that should have been counted.
func TestEscapeLikePattern(t *testing.T) {
	cases := []struct {
		name     string
		literal  string
		expected string
	}{
		{"plain prefix is unchanged", "Get the details of Event with id", "Get the details of Event with id"},
		{"percent is escaped", "100% done", `100\% done`},
		{"underscore is escaped", "event_id", `event\_id`},
		{"backslash is escaped first", `a\b`, `a\\b`},
		{"backslash before wildcard stays literal", `a\%`, `a\\\%`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.expected, escapeLikePattern(testCase.literal))
		})
	}
}

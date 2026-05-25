package observability

import (
	"errors"
	"fmt"
	"net/url"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"strconv"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractLabelSelectors(t *testing.T) {
	testCases := []struct {
		name     string
		where    query.QueryWhereClause
		expected string
		wantErr  bool
	}{
		{
			name: "Simple Eq operator",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"app": {query.Eq: "accounting"},
				},
			},
			expected: `app="accounting"`,
			wantErr:  false,
		},
		{
			name: "Simple Nq operator",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"env": {query.Nq: "dev"},
				},
			},
			expected: `env!="dev", env=~".+"`,
			wantErr:  false,
		},
		{
			name: "In operator",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"app": {query.In: []string{"accounting", "billing"}},
				},
			},
			expected: `app=~"accounting|billing"`,
			wantErr:  false,
		},
		{
			name: "NotIn operator",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"env": {query.NotIn: []string{"dev", "test"}},
				},
			},
			expected: `env!~"dev|test", env=~".+"`,
			wantErr:  false,
		},
		{
			name: "Multiple binary conditions (AND)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"app": {query.Eq: "accounting"},
					"env": {query.Eq: "prod"},
				},
			},
			expected: `app="accounting", env="prod"`,
			wantErr:  false,
		},
		{
			name: "AND clause",
			where: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "accounting"},
						},
					},
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"env": {query.Eq: "prod"},
						},
					},
				},
			},
			expected: `app="accounting", env="prod"`,
			wantErr:  false,
		},
		{
			name: "OR clause - same field merges to regex",
			where: query.QueryWhereClause{
				Or: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "accounting"},
						},
					},
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "billing"},
						},
					},
				},
			},
			expected: `app=~"accounting|billing"`,
			wantErr:  false,
		},
		{
			name: "OR clause - different fields (best effort)",
			where: query.QueryWhereClause{
				Or: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "accounting"},
						},
					},
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"env": {query.Eq: "prod"},
						},
					},
				},
			},
			expected: `app="accounting", env="prod"`,
			wantErr:  false,
		},
		{
			name: "NOT clause",
			where: query.QueryWhereClause{
				Not: &query.QueryWhereClause{
					Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
						"app": {query.Eq: "accounting"},
					},
				},
			},
			expected: `app!="accounting", app=~".+"`,
			wantErr:  false,
		},
		{
			name: "NOT with regex operator",
			where: query.QueryWhereClause{
				Not: &query.QueryWhereClause{
					Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
						"app": {query.In: []string{"accounting", "billing"}},
					},
				},
			},
			expected: `app!~"accounting|billing", app=~".+"`,
			wantErr:  false,
		},
		{
			name: "Escape quotes in value",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"message": {query.Eq: `value with "quotes"`},
				},
			},
			expected: `message="value with \"quotes\""`,
			wantErr:  false,
		},
		{
			name: "Escape regex metacharacters in OR",
			where: query.QueryWhereClause{
				Or: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "test.prod"},
						},
					},
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "test*dev"},
						},
					},
				},
			},
			expected: `app=~"test\.prod|test\*dev"`,
			wantErr:  false,
		},
		{
			name: "Complex nested AND and OR",
			where: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"namespace": {query.Eq: "production"},
						},
					},
					{
						Or: []query.QueryWhereClause{
							{
								Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
									"app": {query.Eq: "accounting"},
								},
							},
							{
								Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
									"app": {query.Eq: "billing"},
								},
							},
						},
					},
				},
			},
			expected: `app=~"accounting|billing", namespace="production"`,
			wantErr:  false,
		},
		{
			name: "In operator with []interface{} type",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"app": {query.In: []interface{}{"accounting", "billing"}},
				},
			},
			expected: `app=~"accounting|billing"`,
			wantErr:  false,
		},
		{
			name: "Multiple values for same field with != operator",
			where: query.QueryWhereClause{
				Or: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"env": {query.Nq: "dev"},
						},
					},
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"env": {query.Nq: "test"},
						},
					},
				},
			},
			expected: `env!~"dev|test", env=~".+"`,
			wantErr:  false,
		},
		{
			name: "Regex special chars in In operator",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"app": {query.In: []string{"api.v1", "api.v2"}},
				},
			},
			expected: `app=~"api\.v1|api\.v2"`,
			wantErr:  false,
		},
		// === CONTAINS Tests ===
		{
			name: "Contains operator on label (substring match)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.Contains: "api"},
				},
			},
			expected: `pod=~".*api.*"`, // No anchors - matches substring
			wantErr:  false,
		},
		{
			name: "Contains with regex special chars",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"host": {query.Contains: "test.com"},
				},
			},
			expected: `host=~".*test\.com.*"`, // Dot is escaped
			wantErr:  false,
		},
		{
			name: "Contains with empty string",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.Contains: ""},
				},
			},
			expected: `pod=~".*.*"`, // Matches anything (including empty)
			wantErr:  false,
		},
		// === LIKE Tests ===
		{
			name: "Like operator with trailing wildcard",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.Like: "api-%"},
				},
			},
			expected: `pod=~"^api-.*$"`, // Anchored - matches full string
			wantErr:  false,
		},
		{
			name: "Like with leading and trailing wildcards",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.Like: "%api%"},
				},
			},
			expected: `pod=~"^.*api.*$"`, // Anchored substring
			wantErr:  false,
		},
		{
			name: "Like with underscore wildcard (single char)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.Like: "test_pod"},
				},
			},
			expected: `pod=~"^test.pod$"`, // _ becomes .
			wantErr:  false,
		},
		{
			name: "Like with only wildcard %",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.Like: "%"},
				},
			},
			expected: `pod=~"^.*$"`, // Matches anything
			wantErr:  false,
		},
		{
			name: "Like with only wildcard _",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.Like: "_"},
				},
			},
			expected: `pod=~"^.$"`, // Matches exactly one char
			wantErr:  false,
		},
		{
			name: "Like with empty string",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.Like: ""},
				},
			},
			expected: `pod=~"^$"`, // Matches only empty labels
			wantErr:  false,
		},
		{
			name: "Like with escaped percent (literal %)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"value": {query.Like: `test\%value`},
				},
			},
			expected: `value=~"^test%value$"`, // Literal %
			wantErr:  false,
		},
		{
			name: "Like with escaped underscore (literal _)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"value": {query.Like: `test\_value`},
				},
			},
			expected: `value=~"^test_value$"`, // Literal _
			wantErr:  false,
		},
		{
			name: "Like with escaped backslash",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"path": {query.Like: `path\\file%`},
				},
			},
			expected: `path=~"^path\\\\file.*$"`, // Literal \ followed by wildcard
			wantErr:  false,
		},
		{
			name: "Like with regex special chars (should be escaped)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"value": {query.Like: `test.log`},
				},
			},
			expected: `value=~"^test\.log$"`, // Dot is escaped (literal .)
			wantErr:  false,
		},
		// === ILIKE Tests ===
		{
			name: "ILike operator case-insensitive",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.ILike: "API-%"},
				},
			},
			expected: `pod=~"^(?i)API-.*$"`, // Case-insensitive with anchors
			wantErr:  false,
		},
		{
			name: "ILike with leading/trailing wildcards",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.ILike: "%API%"},
				},
			},
			expected: `pod=~"^(?i).*API.*$"`, // Case-insensitive contains
			wantErr:  false,
		},
		// === NLIKE Tests ===
		{
			name: "NLike operator (negative)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.NLike: "test-%"},
				},
			},
			expected: `pod!~"^test-.*$", pod=~".+"`, // Negative match with anchors + positive existence
			wantErr:  false,
		},
		// === Combined Operations ===
		{
			name: "OR with Contains operators (should merge)",
			where: query.QueryWhereClause{
				Or: []query.QueryWhereClause{
					{Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
						"pod": {query.Contains: "api"},
					}},
					{Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
						"pod": {query.Contains: "web"},
					}},
				},
			},
			expected: `pod=~".*api.*|.*web.*"`, // Merged into alternation
			wantErr:  false,
		},
		{
			name: "NOT with Contains",
			where: query.QueryWhereClause{
				Not: &query.QueryWhereClause{
					Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
						"pod": {query.Contains: "test"},
					},
				},
			},
			expected: `pod!~".*test.*", pod=~".+"`, // Negated + positive existence
			wantErr:  false,
		},
		{
			name: "AND with Contains and Like",
			where: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
						"namespace": {query.Contains: "prod"},
					}},
					{Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
						"pod": {query.Like: "api-%"},
					}},
				},
			},
			expected: `namespace=~".*prod.*", pod=~"^api-.*$"`, // Both conditions
			wantErr:  false,
		},

		// === SECURITY TESTS - Quote Escaping to Prevent LogQL Injection ===
		{
			name: "Contains with double quotes (LogQL injection prevention)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.Contains: `test"injected`},
				},
			},
			expected: `pod=~".*test\"injected.*"`, // Quote is escaped
			wantErr:  false,
		},
		{
			name: "Like with double quotes (LogQL injection prevention)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.Like: `api-"attack%`},
				},
			},
			expected: `pod=~"^api-\"attack.*$"`, // Quote is escaped
			wantErr:  false,
		},
		{
			name: "ILike with double quotes (LogQL injection prevention)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.ILike: `API-"ATTACK%`},
				},
			},
			expected: `pod=~"^(?i)API-\"ATTACK.*$"`, // Quote is escaped
			wantErr:  false,
		},
		{
			name: "NLike with double quotes (LogQL injection prevention)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.NLike: `test-"bad%`},
				},
			},
			expected: `pod!~"^test-\"bad.*$", pod=~".+"`, // Quote is escaped + positive existence
			wantErr:  false,
		},
		// === ICONTAINS Tests ===
		{
			name: "IContains operator on label (case-insensitive substring)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.IContains: "api"},
				},
			},
			expected: `pod=~"(?i).*api.*"`,
			wantErr:  false,
		},
		{
			name: "IContains with mixed case input",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.IContains: "ErRoR"},
				},
			},
			expected: `pod=~"(?i).*ErRoR.*"`,
			wantErr:  false,
		},
		{
			name: "IContains with regex special chars (escaped)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"host": {query.IContains: "test.com"},
				},
			},
			expected: `host=~"(?i).*test\.com.*"`, // Dot is escaped
			wantErr:  false,
		},
		{
			name: "IContains with empty string (skipped)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.IContains: ""},
				},
			},
			expected: ``, // Empty selector, filter skipped
			wantErr:  false,
		},
		{
			name: "IContains with double quotes (LogQL injection prevention)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.IContains: `test"injected`},
				},
			},
			expected: `pod=~"(?i).*test\"injected.*"`, // Quote is escaped
			wantErr:  false,
		},
		// === NICONTAINS Tests ===
		{
			name: "NIContains operator",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.NIContains: "debug"},
				},
			},
			expected: `pod!~"(?i).*debug.*", pod=~".+"`,
			wantErr:  false,
		},
		{
			name: "NIContains with empty string (skipped)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.NIContains: ""},
				},
			},
			expected: ``,
			wantErr:  false,
		},
		// === REGEX Tests ===
		{
			name: "Regex operator with valid pattern",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.Regex: "api-[0-9]+"},
				},
			},
			expected: `pod=~"api-[0-9]+"`,
			wantErr:  false,
		},
		{
			name: "Regex with alternation",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.Regex: "error|warning|fatal"},
				},
			},
			expected: `pod=~"error|warning|fatal"`,
			wantErr:  false,
		},
		{
			name: "Regex with quotes (escaped)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"message": {query.Regex: `status="error"`},
				},
			},
			expected: `message=~"status=\"error\""`, // Quote is escaped
			wantErr:  false,
		},
		{
			name: "Regex with empty string (error)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.Regex: ""},
				},
			},
			expected: "",
			wantErr:  true, // Should return validation error
		},
		{
			name: "Regex with invalid syntax (error)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.Regex: "[unclosed"},
				},
			},
			expected: "",
			wantErr:  true, // Should detect invalid regex
		},
		// === NREGEX Tests ===
		{
			name: "NRegex operator",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.NRegex: "test-.*"},
				},
			},
			expected: `pod!~"test-.*", pod=~".+"`,
			wantErr:  false,
		},
		{
			name: "NRegex with empty string (error)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.NRegex: ""},
				},
			},
			expected: "",
			wantErr:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := extractLabelSelectors(tc.where)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}

func TestEscapeLabelValue(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "No special characters",
			input:    "simple",
			expected: "simple",
		},
		{
			name:     "With quotes",
			input:    `value with "quotes"`,
			expected: `value with \"quotes\"`,
		},
		{
			name:     "Multiple quotes",
			input:    `"start" and "end"`,
			expected: `\"start\" and \"end\"`,
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := escapeLabelValue(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestEscapeRegexValue(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "No special characters",
			input:    "simple",
			expected: "simple",
		},
		{
			name:     "Dot character",
			input:    "test.prod",
			expected: `test\.prod`,
		},
		{
			name:     "Asterisk character",
			input:    "test*dev",
			expected: `test\*dev`,
		},
		{
			name:     "Plus character",
			input:    "value+extra",
			expected: `value\+extra`,
		},
		{
			name:     "Question mark",
			input:    "what?",
			expected: `what\?`,
		},
		{
			name:     "Brackets and braces",
			input:    "test[0]{1}",
			expected: `test\[0\]\{1\}`,
		},
		{
			name:     "Pipe character",
			input:    "a|b",
			expected: `a\|b`,
		},
		{
			name:     "Parentheses",
			input:    "(test)",
			expected: `\(test\)`,
		},
		{
			name:     "Caret and dollar",
			input:    "^start$",
			expected: `\^start\$`,
		},
		{
			name:     "Backslash",
			input:    `path\to\file`,
			expected: `path\\to\\file`,
		},
		{
			name:     "Multiple special chars",
			input:    "a.b*c+d?e",
			expected: `a\.b\*c\+d\?e`,
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := escapeRegexValue(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestToStringArray(t *testing.T) {
	testCases := []struct {
		name     string
		input    interface{}
		expected []string
		wantErr  bool
	}{
		{
			name:     "String array",
			input:    []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
			wantErr:  false,
		},
		{
			name:     "Interface array with strings",
			input:    []interface{}{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
			wantErr:  false,
		},
		{
			name:     "Interface array with mixed types",
			input:    []interface{}{"string", 123, true},
			expected: []string{"string", "123", "true"},
			wantErr:  false,
		},
		{
			name:     "Empty string array",
			input:    []string{},
			expected: []string{},
			wantErr:  false,
		},
		{
			name:     "Empty interface array",
			input:    []interface{}{},
			expected: []string{},
			wantErr:  false,
		},
		{
			name:     "Invalid type - string",
			input:    "not an array",
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "Invalid type - int",
			input:    123,
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "Invalid type - map",
			input:    map[string]string{"key": "value"},
			expected: nil,
			wantErr:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := toStringArray(tc.input)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}

func TestNegateLabelSelector(t *testing.T) {
	testCases := []struct {
		name     string
		input    labelSelector
		expected labelSelector
	}{
		{
			name:     "Negate = to !=",
			input:    labelSelector{field: "app", operator: "=", value: "test"},
			expected: labelSelector{field: "app", operator: "!=", value: "test"},
		},
		{
			name:     "Negate != to =",
			input:    labelSelector{field: "app", operator: "!=", value: "test"},
			expected: labelSelector{field: "app", operator: "=", value: "test"},
		},
		{
			name:     "Negate =~ to !~",
			input:    labelSelector{field: "app", operator: "=~", value: "test.*"},
			expected: labelSelector{field: "app", operator: "!~", value: "test.*"},
		},
		{
			name:     "Negate !~ to =~",
			input:    labelSelector{field: "app", operator: "!~", value: "test.*"},
			expected: labelSelector{field: "app", operator: "=~", value: "test.*"},
		},
		{
			name:     "Unknown operator unchanged",
			input:    labelSelector{field: "app", operator: "unknown", value: "test"},
			expected: labelSelector{field: "app", operator: "unknown", value: "test"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := negateLabelSelector(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestBuildWhere(t *testing.T) {
	testCases := []struct {
		name     string
		where    query.QueryWhereClause
		expected string
	}{
		{
			name: "Contains operator on log message",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.Contains: "error"},
				},
			},
			expected: `|= "error"`,
		},
		{
			name: "Contains with double quotes (LogQL injection prevention)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.Contains: `test"injected`},
				},
			},
			expected: `|= "test\"injected"`,
		},
		{
			name: "Eq operator on log message (same as Contains)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.Eq: "warning"},
				},
			},
			expected: `|= "warning"`,
		},
		{
			name: "Nq operator on log message",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.Nq: "debug"},
				},
			},
			expected: `!= "debug"`,
		},
		{
			name: "Nq with double quotes (LogQL injection prevention)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.Nq: `test"attack`},
				},
			},
			expected: `!= "test\"attack"`,
		},
		{
			name: "Like operator with SQL pattern",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.Like: "Error:%"},
				},
			},
			expected: `|~ "^Error:.*$"`,
		},
		{
			name: "Like with escaped percent (literal %)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.Like: `100\% complete`},
				},
			},
			expected: `|~ "^100% complete$"`,
		},
		{
			name: "Like with double quotes (LogQL injection prevention)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.Like: `Error: "bad%`},
				},
			},
			expected: `|~ "^Error: \"bad.*$"`,
		},
		{
			name: "ILike operator case-insensitive",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.ILike: "ERROR:%"},
				},
			},
			expected: `|~ "^(?i)ERROR:.*$"`,
		},
		{
			name: "ILike with double quotes (LogQL injection prevention)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.ILike: `WARNING: "test%`},
				},
			},
			expected: `|~ "^(?i)WARNING: \"test.*$"`,
		},
		{
			name: "NLike operator (negative pattern)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.NLike: "debug:%"},
				},
			},
			expected: `!~ "^debug:.*$"`,
		},
		{
			name: "NLike with double quotes (LogQL injection prevention)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.NLike: `test"bad%`},
				},
			},
			expected: `!~ "^test\"bad.*$"`,
		},
		{
			name: "Multiple log filters with AND",
			where: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
						"log": {query.Contains: "error"},
					}},
					{Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
						"log": {query.Nq: "debug"},
					}},
				},
			},
			expected: `|= "error" != "debug"`,
		},
		{
			name: "Empty where clause",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{},
			},
			expected: "",
		},
		// === ICONTAINS on log field ===
		{
			name: "IContains on log message",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.IContains: "error"},
				},
			},
			expected: `|~ "(?i).*error.*"`,
		},
		{
			name: "IContains with special chars (escaped)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.IContains: "test.com"},
				},
			},
			expected: `|~ "(?i).*test\.com.*"`,
		},
		{
			name: "IContains with quotes (escaped)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.IContains: `test"value`},
				},
			},
			expected: `|~ "(?i).*test\"value.*"`,
		},
		{
			name: "IContains with empty string (skipped)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.IContains: ""},
				},
			},
			expected: ``, // Empty string skipped
		},
		// === NICONTAINS on log field ===
		{
			name: "NIContains on log message",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.NIContains: "debug"},
				},
			},
			expected: `!~ "(?i).*debug.*"`,
		},
		{
			name: "NIContains with empty string (skipped)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.NIContains: ""},
				},
			},
			expected: ``,
		},
		// === REGEX on log field ===
		{
			name: "Regex on log message",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.Regex: "error|warning"},
				},
			},
			expected: `|~ "error|warning"`,
		},
		{
			name: "Regex with character class",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.Regex: "[Ee]rror [0-9]+"},
				},
			},
			expected: `|~ "[Ee]rror [0-9]+"`,
		},
		{
			name: "Regex with quotes (escaped)",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.Regex: `status="error"`},
				},
			},
			expected: `|~ "status=\"error\""`,
		},
		// === NREGEX on log field ===
		{
			name: "NRegex on log message",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.NRegex: "test-.*"},
				},
			},
			expected: `!~ "test-.*"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := buildWhere(tc.where)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestBuildWhereValidationErrors tests error cases for buildWhere
func TestBuildWhereValidationErrors(t *testing.T) {
	testCases := []struct {
		name          string
		where         query.QueryWhereClause
		expectedError string
	}{
		{
			name: "Regex with empty string",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.Regex: ""},
				},
			},
			expectedError: "regex pattern cannot be empty",
		},
		{
			name: "Regex with invalid syntax",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.Regex: "[unclosed"},
				},
			},
			expectedError: "invalid regex syntax",
		},
		{
			name: "NRegex with empty string",
			where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log": {query.NRegex: ""},
				},
			},
			expectedError: "regex pattern cannot be empty",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildWhere(tc.where)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedError)
		})
	}
}

func TestBuildLokiQuery(t *testing.T) {
	s := &LokiSource{}

	testCases := []struct {
		name     string
		request  LogsQueryBuilderRequest
		expected string
		wantErr  bool
	}{
		{
			name: "Simple label selector",
			request: LogsQueryBuilderRequest{
				Where: query.QueryWhereClause{
					Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
						"app": {query.Eq: "accounting"},
					},
				},
			},
			expected: `{app="accounting"}`,
			wantErr:  false,
		},
		{
			name: "Label selector with AND",
			request: LogsQueryBuilderRequest{
				Where: query.QueryWhereClause{
					And: []query.QueryWhereClause{
						{
							Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
								"app": {query.Eq: "accounting"},
							},
						},
						{
							Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
								"env": {query.Eq: "prod"},
							},
						},
					},
				},
			},
			expected: `{app="accounting", env="prod"}  `, // buildWhere adds trailing spaces
			wantErr:  false,
		},
		{
			name: "Empty where clause",
			request: LogsQueryBuilderRequest{
				Where: query.QueryWhereClause{},
			},
			expected: `{}`,
			wantErr:  false,
		},
		{
			name: "Contains operator on namespace label",
			request: LogsQueryBuilderRequest{
				Where: query.QueryWhereClause{
					And: []query.QueryWhereClause{
						{
							Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
								"namespace": {query.Contains: "nudgebee"},
							},
						},
					},
				},
			},
			expected: `{namespace=~".*nudgebee.*"}`,
			wantErr:  false,
		},
		{
			name: "Like operator on pod label",
			request: LogsQueryBuilderRequest{
				Where: query.QueryWhereClause{
					Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
						"pod": {query.Like: "api-%"},
					},
				},
			},
			expected: `{pod=~"^api-.*$"}`,
			wantErr:  false,
		},
		{
			name: "Combined namespace label and log message filters",
			request: LogsQueryBuilderRequest{
				Where: query.QueryWhereClause{
					And: []query.QueryWhereClause{
						{
							Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
								"namespace": {query.Contains: "nudgebee"},
								"log":       {query.Contains: "INFO"},
							},
						},
					},
				},
			},
			expected: `{namespace=~".*nudgebee.*"} |= "INFO"`,
			wantErr:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := s.BuildLokiQuery(tc.request)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}

func TestEnsureCompleteLokiQuery(t *testing.T) {
	s := &LokiSource{}
	startTime := int64(1672531200000) // 2023-01-01 00:00:00 UTC in milliseconds
	endTime := int64(1672534800000)   // 2023-01-01 01:00:00 UTC in milliseconds

	testCases := []struct {
		name          string
		inputRequest  FetchLogRequest
		expectedQuery string
		expectError   bool
	}{
		{
			name: "Empty query string",
			inputRequest: FetchLogRequest{
				StartTime: startTime,
				EndTime:   endTime,
				Limit:     100,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "nudgebee"},
						},
					},
				},
			},
			expectedQuery: "direction=backward&end=" + strconv.FormatInt(endTime*1000000, 10) + "&limit=100&query=%7Bapp%3D%22nudgebee%22%7D&start=" + strconv.FormatInt(startTime*1000000, 10),
			expectError:   false,
		},
		{
			name: "Raw LogQL query",
			inputRequest: FetchLogRequest{
				Query:     `{job="api"}`,
				StartTime: startTime,
				EndTime:   endTime,
				Limit:     200,
			},
			expectedQuery: "direction=backward&end=" + strconv.FormatInt(endTime*1000000, 10) + "&limit=200&query=%7Bjob%3D%22api%22%7D&start=" + strconv.FormatInt(startTime*1000000, 10),
			expectError:   false,
		},
		{
			name: "Partial URL query with limit",
			inputRequest: FetchLogRequest{
				Query:     `query={job="api"}&limit=50`,
				StartTime: startTime,
				EndTime:   endTime,
				Limit:     200, // This should be ignored
			},
			expectedQuery: "direction=backward&end=" + strconv.FormatInt(endTime*1000000, 10) + "&limit=50&query=%7Bjob%3D%22api%22%7D&start=" + strconv.FormatInt(startTime*1000000, 10),
			expectError:   false,
		},
		{
			name: "Full URL query",
			inputRequest: FetchLogRequest{
				Query:     `query={job="api"}&start=1672531000000000&end=1672534000000000&limit=25&direction=forward`,
				StartTime: startTime, // Should be ignored
				EndTime:   endTime,   // Should be ignored
				Limit:     200,       // Should be ignored
			},
			expectedQuery: "direction=forward&end=1672534000000000&limit=25&query=%7Bjob%3D%22api%22%7D&start=1672531000000000",
			expectError:   false,
		},
		{
			name: "Query with no 'query' parameter, should treat whole string as logql",
			inputRequest: FetchLogRequest{
				Query:     `{app="test"}`,
				StartTime: startTime,
				EndTime:   endTime,
			},
			expectedQuery: "direction=backward&end=" + strconv.FormatInt(endTime*1000000, 10) + "&query=%7Bapp%3D%22test%22%7D&start=" + strconv.FormatInt(startTime*1000000, 10),
			expectError:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.inputRequest // Make a copy to avoid modification issues
			actualQuery, err := s.ensureCompleteLokiQuery(&req)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				// Parse both actual and expected queries to compare them regardless of parameter order.
				parsedActual, _ := url.ParseQuery(actualQuery)
				parsedExpected, _ := url.ParseQuery(tc.expectedQuery)
				assert.Equal(t, parsedExpected, parsedActual)
			}
		})
	}
}

// ==================== End-to-End Tests ====================
// The following tests use gomonkey to mock relay.Execute and test the full flow
// Note: mockRequestContext is defined in loggly_test.go and shared across test files

const (
	testAccountID = "a2a30b02-0f67-42e5-a2ab-c658230fd798"
	testStartTime = int64(1770967207187)
	testEndTime   = int64(1771572007187)
)

// createMockLogsResponse creates a realistic mock relay response for QueryLogs
func createMockLogsResponse(numLogs int) map[string]any {
	values := make([][]string, numLogs)
	for i := 0; i < numLogs; i++ {
		timestamp := fmt.Sprintf("167253120%d000000000", i)
		message := fmt.Sprintf("Log message %d", i)
		if i%2 == 0 {
			message = "Error: " + message
		} else {
			message = "Info: " + message
		}
		values[i] = []string{timestamp, message}
	}

	return map[string]any{
		"data": map[string]any{
			"data": map[string]any{
				"data": map[string]any{
					"result": []map[string]any{
						{
							"stream": map[string]any{
								"app": "accounting",
								"env": "prod",
							},
							"values": values,
						},
					},
				},
			},
		},
	}
}

// createMockLabelsResponse creates a realistic mock relay response for QueryLabels
func createMockLabelsResponse(labels []string) map[string]any {
	labelInterfaces := make([]interface{}, len(labels))
	for i, l := range labels {
		labelInterfaces[i] = l
	}

	return map[string]any{
		"data": map[string]any{
			"data": map[string]any{
				"data": labelInterfaces,
			},
		},
	}
}

// createMockLabelValuesResponse creates a realistic mock relay response for QueryLabelValues
func createMockLabelValuesResponse(values []string) map[string]any {
	valueInterfaces := make([]interface{}, len(values))
	for i, v := range values {
		valueInterfaces[i] = v
	}

	return map[string]any{
		"data": map[string]any{
			"data": map[string]any{
				"data": valueInterfaces,
			},
		},
	}
}

// TestLokiSource_QueryLogs_E2E tests the complete QueryLogs flow with various request bodies
func TestLokiSource_QueryLogs_E2E(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	testCases := []struct {
		name          string
		request       FetchLogRequest
		mockResponse  map[string]any
		mockError     error
		expectedLogs  int
		expectedError string
		validateRelay func(t *testing.T, relayReq relay.RelayExecuteRequest)
	}{
		{
			name: "Simple equality query",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "accounting"},
						},
					},
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     100,
			},
			mockResponse: createMockLogsResponse(2),
			expectedLogs: 2,
			validateRelay: func(t *testing.T, relayReq relay.RelayExecuteRequest) {
				assert.Equal(t, testAccountID, relayReq.Body.AccountID)
				assert.Equal(t, "query_loki", relayReq.Body.ActionName)
				queryParam := relayReq.Body.ActionParams["query"].(string)
				assert.Contains(t, queryParam, "app%3D%22accounting%22") // URL-encoded {app="accounting"}
			},
		},
		{
			name: "Multiple labels AND",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "accounting"},
							"env": {query.Eq: "prod"},
						},
					},
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     100,
			},
			mockResponse: createMockLogsResponse(3),
			expectedLogs: 3,
			validateRelay: func(t *testing.T, relayReq relay.RelayExecuteRequest) {
				queryParam := relayReq.Body.ActionParams["query"].(string)
				assert.Contains(t, queryParam, "app")
				assert.Contains(t, queryParam, "env")
			},
		},
		{
			name: "IN operator regex",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.In: []string{"accounting", "billing"}},
						},
					},
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     100,
			},
			mockResponse: createMockLogsResponse(2),
			expectedLogs: 2,
			validateRelay: func(t *testing.T, relayReq relay.RelayExecuteRequest) {
				queryParam := relayReq.Body.ActionParams["query"].(string)
				assert.Contains(t, queryParam, "app")
			},
		},
		{
			name: "NOT IN operator",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"env": {query.NotIn: []string{"dev", "test"}},
						},
					},
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     100,
			},
			mockResponse: createMockLogsResponse(1),
			expectedLogs: 1,
		},
		{
			name: "Contains operator",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"pod": {query.Contains: "api"},
						},
					},
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     100,
			},
			mockResponse: createMockLogsResponse(2),
			expectedLogs: 2,
		},
		{
			name: "Like operator",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"pod": {query.Like: "api-%"},
						},
					},
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     100,
			},
			mockResponse: createMockLogsResponse(1),
			expectedLogs: 1,
		},
		{
			name: "ILike operator case insensitive",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"pod": {query.ILike: "API-%"},
						},
					},
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     100,
			},
			mockResponse: createMockLogsResponse(2),
			expectedLogs: 2,
		},
		{
			name: "Complex nested AND OR",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						And: []query.QueryWhereClause{
							{
								Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
									"namespace": {query.Eq: "production"},
								},
							},
							{
								Or: []query.QueryWhereClause{
									{
										Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
											"app": {query.Eq: "accounting"},
										},
									},
									{
										Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
											"app": {query.Eq: "billing"},
										},
									},
								},
							},
						},
					},
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     100,
			},
			mockResponse: createMockLogsResponse(3),
			expectedLogs: 3,
		},
		{
			name: "NOT clause",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Not: &query.QueryWhereClause{
							Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
								"app": {query.Eq: "accounting"},
							},
						},
					},
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     100,
			},
			mockResponse: createMockLogsResponse(2),
			expectedLogs: 2,
		},
		{
			name: "Raw LogQL query",
			request: FetchLogRequest{
				AccountId: testAccountID,
				Query:     `{job="api"}`,
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     200,
			},
			mockResponse: createMockLogsResponse(5),
			expectedLogs: 5,
			validateRelay: func(t *testing.T, relayReq relay.RelayExecuteRequest) {
				queryParam := relayReq.Body.ActionParams["query"].(string)
				assert.Contains(t, queryParam, "job")
			},
		},
		{
			name: "Partial URL query",
			request: FetchLogRequest{
				AccountId: testAccountID,
				Query:     `query={job="api"}&limit=50`,
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     200, // This will be ignored
			},
			mockResponse: createMockLogsResponse(4),
			expectedLogs: 4,
		},
		{
			name: "Empty where clause",
			request: FetchLogRequest{
				AccountId:    "test-account-123",
				QueryRequest: LogsQueryBuilderRequest{Where: query.QueryWhereClause{}},
				StartTime:    1672531200000,
				EndTime:      1672534800000,
				Limit:        100,
			},
			mockResponse: createMockLogsResponse(10),
			expectedLogs: 10,
		},
		{
			name: "Default limit becomes 5000",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "test"},
						},
					},
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     0, // Should become 5000
			},
			mockResponse: createMockLogsResponse(1),
			expectedLogs: 1,
		},
		{
			name: "Limit exceeds maximum",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "test"},
						},
					},
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     5001,
			},
			expectedError: "limit exceeds maximum of 5000",
		},
		{
			name: "With StepInterval",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "test"},
						},
					},
				},
				StartTime:    1672531200000,
				EndTime:      1672534800000,
				Limit:        100,
				StepInterval: 60,
			},
			mockResponse: createMockLogsResponse(2),
			expectedLogs: 2,
		},
		{
			name: "Empty results",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "nonexistent"},
						},
					},
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     100,
			},
			mockResponse: createMockLogsResponse(0),
			expectedLogs: 0,
		},
		{
			name: "Relay error",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "test"},
						},
					},
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     100,
			},
			mockError:     errors.New("relay connection failed"),
			expectedError: "failed to execute loki query",
		},
		{
			name: "Malformed response - string error",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "test"},
						},
					},
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     100,
			},
			mockResponse: map[string]any{
				"data": "error: invalid query",
			},
			expectedError: "received an error response from the service",
		},
		{
			name: "Malformed response - missing nested data",
			request: FetchLogRequest{
				AccountId: testAccountID,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"app": {query.Eq: "test"},
						},
					},
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				Limit:     100,
			},
			mockResponse: map[string]any{
				"data": map[string]any{},
			},
			expectedError: "data2 field not found or is nil",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			patches.Reset()
			patches.ApplyFunc(relay.Execute,
				func(relayReq relay.RelayExecuteRequest) (map[string]any, error) {
					if tc.validateRelay != nil {
						tc.validateRelay(t, relayReq)
					}
					if tc.mockError != nil {
						return nil, tc.mockError
					}
					return tc.mockResponse, nil
				})

			ctx := mockRequestContext()
			source := &LokiSource{}

			logs, err := source.QueryLogs(ctx, tc.request)

			if tc.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			} else {
				require.NoError(t, err)
				assert.Len(t, logs, tc.expectedLogs)
				// Validate log structure for non-empty results
				if tc.expectedLogs > 0 {
					assert.NotEmpty(t, logs[0].Timestamp)
					assert.NotEmpty(t, logs[0].Message)
					assert.NotNil(t, logs[0].Labels)
				}
			}
		})
	}
}

// TestLokiSource_QueryLabels_E2E tests the complete QueryLabels flow
func TestLokiSource_QueryLabels_E2E(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	testCases := []struct {
		name           string
		request        FetchLogLabelRequest
		mockResponse   map[string]any
		mockError      error
		expectedLabels []string
		expectedError  string
	}{
		{
			name: "Success with query parameter",
			request: FetchLogLabelRequest{
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request: map[string]any{
					"query": "{app=\"test\"}",
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
			},
			mockResponse:   createMockLabelsResponse([]string{"app", "env", "pod", "namespace"}),
			expectedLabels: []string{"app", "env", "pod", "namespace"},
		},
		{
			name: "Success without query parameter",
			request: FetchLogLabelRequest{
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockResponse:   createMockLabelsResponse([]string{"app", "env"}),
			expectedLabels: []string{"app", "env"},
		},
		{
			name: "Success with nil request",
			request: FetchLogLabelRequest{
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           nil,
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockResponse:   createMockLabelsResponse([]string{"pod"}),
			expectedLabels: []string{"pod"},
		},
		{
			name: "Empty label list",
			request: FetchLogLabelRequest{
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockResponse:   createMockLabelsResponse([]string{}),
			expectedLabels: []string{},
		},
		{
			name: "Single label",
			request: FetchLogLabelRequest{
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockResponse:   createMockLabelsResponse([]string{"container"}),
			expectedLabels: []string{"container"},
		},
		{
			name: "Labels with special characters",
			request: FetchLogLabelRequest{
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockResponse:   createMockLabelsResponse([]string{"workload_namespace", "service_name"}),
			expectedLabels: []string{"workload_namespace", "service_name"},
		},
		{
			name: "Relay error",
			request: FetchLogLabelRequest{
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockError:     errors.New("relay connection failed"),
			expectedError: "failed to execute loki query",
		},
		{
			name: "Malformed response - missing data field",
			request: FetchLogLabelRequest{
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockResponse: map[string]any{
				"data": map[string]any{},
			},
			expectedError: "nested 'data' field not found",
		},
		{
			name: "Malformed response - data not array",
			request: FetchLogLabelRequest{
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockResponse: map[string]any{
				"data": map[string]any{
					"data": map[string]any{
						"data": "not an array",
					},
				},
			},
			expectedError: "expected 'data' to be a slice",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			patches.Reset()
			patches.ApplyFunc(relay.Execute,
				func(relayReq relay.RelayExecuteRequest) (map[string]any, error) {
					assert.Equal(t, testAccountID, relayReq.Body.AccountID)
					assert.Equal(t, "query_loki_labels", relayReq.Body.ActionName)
					if tc.mockError != nil {
						return nil, tc.mockError
					}
					return tc.mockResponse, nil
				})

			ctx := mockRequestContext()
			source := &LokiSource{}

			labels, err := source.QueryLabels(ctx, tc.request)

			if tc.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			} else {
				require.NoError(t, err)
				assert.Len(t, labels, len(tc.expectedLabels))
				for i, expectedLabel := range tc.expectedLabels {
					assert.Equal(t, expectedLabel, labels[i].Label)
					assert.NotNil(t, labels[i].Attributes)
				}
			}
		})
	}
}

// TestLokiSource_QueryLabelValues_E2E tests the complete QueryLabelValues flow
func TestLokiSource_QueryLabelValues_E2E(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	testCases := []struct {
		name           string
		request        FetchLogLabelValuesRequest
		mockResponse   map[string]any
		mockError      error
		expectedValues []string
		expectedError  string
	}{
		{
			name: "Success - app label values",
			request: FetchLogLabelValuesRequest{
				LabelName:         "app",
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request: map[string]any{
					"query": "{namespace=\"production\"}",
				},
				StartTime: testStartTime,
				EndTime:   testEndTime,
				CurrentFilters: map[string]string{
					"env": "prod",
				},
			},
			mockResponse:   createMockLabelValuesResponse([]string{"accounting", "billing", "api"}),
			expectedValues: []string{"accounting", "billing", "api"},
		},
		{
			name: "Success - env label values",
			request: FetchLogLabelValuesRequest{
				LabelName:         "env",
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request: map[string]any{
					"query": "{app=\"test\"}",
				},
				StartTime:      testStartTime,
				EndTime:        testEndTime,
				CurrentFilters: map[string]string{},
			},
			mockResponse:   createMockLabelValuesResponse([]string{"dev", "test", "prod"}),
			expectedValues: []string{"dev", "test", "prod"},
		},
		{
			name: "Success - pod label values",
			request: FetchLogLabelValuesRequest{
				LabelName:         "pod",
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockResponse:   createMockLabelValuesResponse([]string{"api-pod-123", "api-pod-456"}),
			expectedValues: []string{"api-pod-123", "api-pod-456"},
		},
		{
			name: "Success - namespace label values",
			request: FetchLogLabelValuesRequest{
				LabelName:         "namespace",
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockResponse:   createMockLabelValuesResponse([]string{"production", "staging"}),
			expectedValues: []string{"production", "staging"},
		},
		{
			name: "Success - workload_namespace with underscore",
			request: FetchLogLabelValuesRequest{
				LabelName:         "workload_namespace",
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockResponse:   createMockLabelValuesResponse([]string{"kube-system", "default"}),
			expectedValues: []string{"kube-system", "default"},
		},
		{
			name: "Single value",
			request: FetchLogLabelValuesRequest{
				LabelName:         "container",
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockResponse:   createMockLabelValuesResponse([]string{"main"}),
			expectedValues: []string{"main"},
		},
		{
			name: "Empty value list",
			request: FetchLogLabelValuesRequest{
				LabelName:         "nonexistent",
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockResponse:   createMockLabelValuesResponse([]string{}),
			expectedValues: []string{},
		},
		{
			name: "Values with special characters",
			request: FetchLogLabelValuesRequest{
				LabelName:         "service_name",
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockResponse:   createMockLabelValuesResponse([]string{"api-v1.0", "api-v2.0"}),
			expectedValues: []string{"api-v1.0", "api-v2.0"},
		},
		{
			name: "Relay error",
			request: FetchLogLabelValuesRequest{
				LabelName:         "app",
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockError:     errors.New("relay connection failed"),
			expectedError: "failed to execute loki query",
		},
		{
			name: "Malformed response - missing data field",
			request: FetchLogLabelValuesRequest{
				LabelName:         "app",
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockResponse: map[string]any{
				"data": map[string]any{},
			},
			expectedError: "nested 'data' field not found",
		},
		{
			name: "Malformed response - data not array",
			request: FetchLogLabelValuesRequest{
				LabelName:         "app",
				AccountId:         testAccountID,
				LogProvider:       "loki",
				LogProviderSource: "grafana",
				Request:           map[string]any{},
				StartTime:         testStartTime,
				EndTime:           testEndTime,
			},
			mockResponse: map[string]any{
				"data": map[string]any{
					"data": map[string]any{
						"data": "not an array",
					},
				},
			},
			expectedError: "expected 'data' to be a slice",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			patches.Reset()
			patches.ApplyFunc(relay.Execute,
				func(relayReq relay.RelayExecuteRequest) (map[string]any, error) {
					assert.Equal(t, testAccountID, relayReq.Body.AccountID)
					assert.Equal(t, "query_grafana_loki_label_values", relayReq.Body.ActionName)
					// Validate label parameter is passed
					labelParam, ok := relayReq.Body.ActionParams["label"]
					if ok {
						assert.Equal(t, tc.request.LabelName, labelParam)
					}
					if tc.mockError != nil {
						return nil, tc.mockError
					}
					return tc.mockResponse, nil
				})

			ctx := mockRequestContext()
			source := &LokiSource{}

			values, err := source.QueryLabelValues(ctx, tc.request)

			if tc.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			} else {
				require.NoError(t, err)
				assert.Len(t, values, len(tc.expectedValues))
				for i, expectedValue := range tc.expectedValues {
					assert.Equal(t, expectedValue, values[i].Value)
					assert.NotNil(t, values[i].Attributes)
				}
			}
		})
	}
}

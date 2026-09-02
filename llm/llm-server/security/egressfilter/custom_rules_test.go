package egressfilter

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseCustomRules(t *testing.T) {
	// Empty / sentinel blobs → no rules, no error.
	for _, raw := range [][]byte{nil, {}, []byte("  "), []byte("[]"), []byte("null")} {
		rules, err := ParseCustomRules(raw)
		assert.NoError(t, err)
		assert.Empty(t, rules)
	}

	rules, err := ParseCustomRules([]byte(`[{"id":"1","name":"Internal","regex":"INT-[0-9]{6}","enabled":true}]`))
	assert.NoError(t, err)
	assert.Len(t, rules, 1)
	assert.Equal(t, "Internal", rules[0].Name)

	_, err = ParseCustomRules([]byte(`{not json`))
	assert.Error(t, err)
}

func TestValidateCustomRules(t *testing.T) {
	ok := []CustomRule{{Name: "A", Regex: `INT-[0-9]{6}`, Enabled: true}, {Name: "B", Regex: `foo`, Enabled: false}}
	assert.NoError(t, ValidateCustomRules(ok))

	// Empty set is valid.
	assert.NoError(t, ValidateCustomRules(nil))

	// Missing name.
	assert.Error(t, ValidateCustomRules([]CustomRule{{Name: "  ", Regex: "x"}}))
	// Missing regex.
	assert.Error(t, ValidateCustomRules([]CustomRule{{Name: "A", Regex: "   "}}))
	// Invalid regex.
	assert.Error(t, ValidateCustomRules([]CustomRule{{Name: "A", Regex: "([unclosed"}}))
	// Duplicate name (case-insensitive).
	assert.Error(t, ValidateCustomRules([]CustomRule{{Name: "Dup", Regex: "a"}, {Name: "dup", Regex: "b"}}))
	// Too many rules.
	many := make([]CustomRule, maxCustomRules+1)
	for i := range many {
		many[i] = CustomRule{Name: string(rune('a'+i%26)) + strings.Repeat("x", i), Regex: "a"}
	}
	assert.Error(t, ValidateCustomRules(many))
	// Regex too long.
	assert.Error(t, ValidateCustomRules([]CustomRule{{Name: "A", Regex: strings.Repeat("a", maxCustomRuleRegexLen+1)}}))
	// Name too long.
	assert.Error(t, ValidateCustomRules([]CustomRule{{Name: strings.Repeat("n", maxCustomRuleNameLen+1), Regex: "a"}}))
}

func TestCompileAndScanCustomRules(t *testing.T) {
	rules := []CustomRule{
		{Name: "Internal token", Regex: `INT-[0-9]{6}`, Enabled: true},
		{Name: "Disabled one", Regex: `NEVER`, Enabled: false}, // must be skipped
		{Name: "Bad regex", Regex: `([`, Enabled: true},        // must be skipped (won't compile)
	}
	compiled := compileCustomRules(rules)
	assert.Len(t, compiled, 1, "only the one enabled, compilable rule survives")

	hits := scanCustomRules("here is INT-123456 and INT-999999 plus NEVER", compiled)
	assert.Len(t, hits, 2)
	for _, h := range hits {
		assert.Equal(t, CustomRuleRuleID, h.RuleID, "custom hits share the bounded rule id")
		assert.Equal(t, "Internal token", h.CustomRuleName)
		assert.Greater(t, h.End, h.Start)
	}

	// No compiled rules / empty payload → no hits, no panic.
	assert.Nil(t, scanCustomRules("INT-123456", nil))
	assert.Nil(t, scanCustomRules("", compiled))
}

// TestScanCustomRules_HitsAreScannableEndToEnd proves a custom hit rides
// through Scan-adjacent machinery: offsets index into the payload correctly,
// so redaction (which slices payload[Start:End]) would mask the right span.
func TestScanCustomRules_OffsetsAreValid(t *testing.T) {
	payload := "prefix INT-424242 suffix"
	compiled := compileCustomRules([]CustomRule{{Name: "x", Regex: `INT-[0-9]{6}`, Enabled: true}})
	hits := scanCustomRules(payload, compiled)
	assert.Len(t, hits, 1)
	assert.Equal(t, "INT-424242", payload[hits[0].Start:hits[0].End])
}

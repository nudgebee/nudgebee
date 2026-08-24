package api

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/stretchr/testify/require"
	"nudgebee/llm/security/egressfilter"
)

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }

func TestParseEgressMode(t *testing.T) {
	cases := []struct {
		in     string
		wantOK bool
		want   egressfilter.Mode
	}{
		{"detect", true, egressfilter.ModeDetect},
		{"enforce", true, egressfilter.ModeEnforce},
		{"redact", true, egressfilter.ModeRedact},
		{"  ENFORCE  ", true, egressfilter.ModeEnforce}, // trimmed + case-insensitive
		{"audit", true, egressfilter.ModeDetect},        // legacy synonym → detect
		{"block", false, ""},
		{"", false, ""},
		{"detectt", false, ""},
	}
	for _, tc := range cases {
		got, ok := parseEgressMode(tc.in)
		assert.Equal(t, tc.wantOK, ok, "parseEgressMode(%q) ok", tc.in)
		if tc.wantOK {
			assert.Equal(t, tc.want, got, "parseEgressMode(%q) mode", tc.in)
		}
	}
}

// TestMergeEgressConfigUpdate_PreservesOtherFields is the key regression:
// changing mode/enabled must NOT wipe allowlist / disabled_rules /
// custom_rules on an existing per-tenant row.
func TestMergeEgressConfigUpdate_PreservesOtherFields(t *testing.T) {
	cfg := &egressfilter.TenantConfig{
		TenantID:      uuid.New(),
		Mode:          egressfilter.ModeDetect,
		Enabled:       true,
		Allowlist:     []string{"AKIAIOSFODNN7EXAMPLE"},
		DisabledRules: []string{"jwt"},
		CustomRules:   []byte(`[{"id":"x"}]`),
	}

	err := mergeEgressConfigUpdate(cfg, egressConfigRequest{Mode: strp("enforce")})
	assert.NoError(t, err)
	assert.Equal(t, egressfilter.ModeEnforce, cfg.Mode)
	// Untouched fields survive.
	assert.Equal(t, []string{"AKIAIOSFODNN7EXAMPLE"}, cfg.Allowlist)
	assert.Equal(t, []string{"jwt"}, cfg.DisabledRules)
	assert.Equal(t, []byte(`[{"id":"x"}]`), cfg.CustomRules)
	assert.True(t, cfg.Enabled, "enabled untouched when not supplied")
}

func TestMergeEgressConfigUpdate_PartialAndInvalid(t *testing.T) {
	// Only enabled supplied → mode unchanged.
	cfg := &egressfilter.TenantConfig{Mode: egressfilter.ModeRedact, Enabled: true}
	assert.NoError(t, mergeEgressConfigUpdate(cfg, egressConfigRequest{Enabled: boolp(false)}))
	assert.Equal(t, egressfilter.ModeRedact, cfg.Mode)
	assert.False(t, cfg.Enabled)

	// Invalid mode → error, cfg untouched.
	cfg2 := &egressfilter.TenantConfig{Mode: egressfilter.ModeDetect, Enabled: true}
	err := mergeEgressConfigUpdate(cfg2, egressConfigRequest{Mode: strp("nope")})
	assert.Error(t, err)
	assert.Equal(t, egressfilter.ModeDetect, cfg2.Mode, "cfg must be unchanged on invalid mode")
}

func TestEgressConfigResponse_Keys(t *testing.T) {
	resp := egressConfigResponse(nil, egressfilter.ModeEnforce, true, true, nil)
	assert.Equal(t, "enforce", resp["mode"])
	assert.Equal(t, true, resp["enabled"])
	assert.Equal(t, true, resp["has_override"])
	// nil patterns must serialize as an empty slice, never null.
	patterns, ok := resp["custom_patterns"].([]egressfilter.CustomRule)
	assert.True(t, ok, "custom_patterns present and typed")
	assert.Empty(t, patterns)
	for _, k := range []string{
		"master_enabled", "secrets_enabled", "env_default_mode",
		"env_pii_ner_enabled", "env_pii_default_mode",
		"pii_enabled", "pii_mode", "pii_ner_enabled", "pii_disabled_categories",
	} {
		_, ok := resp[k]
		assert.True(t, ok, "response missing platform-context key %q", k)
	}
	// env_pii_enabled was removed 2026-07-30 (PII moved to per-tenant
	// opt-in with no platform-level enable flag). Pin its absence so a
	// regression that re-adds the key gets caught.
	_, hasEnvPIIEnabled := resp["env_pii_enabled"]
	assert.False(t, hasEnvPIIEnabled, "env_pii_enabled must not be re-added to the response")
	// cfg == nil path: nullable bools out as JSON null; categories as empty [].
	assert.Nil(t, resp["pii_enabled"])
	assert.Nil(t, resp["pii_ner_enabled"])
	assert.Equal(t, "", resp["pii_mode"])
	assert.Empty(t, resp["pii_disabled_categories"])
}

func TestSetCustomRulesAndPatternsRoundTrip(t *testing.T) {
	cfg := &egressfilter.TenantConfig{}

	// Empty set → "[]", parses back to empty.
	assert.NoError(t, setCustomRules(cfg, nil))
	assert.Equal(t, "[]", string(cfg.CustomRules))
	assert.Empty(t, patternsFromCfg(cfg))

	// Non-empty set round-trips through the JSONB column.
	rules := []egressfilter.CustomRule{
		{ID: "a", Name: "One", Regex: `INT-[0-9]{6}`, Enabled: true},
		{ID: "b", Name: "Two", Regex: `x`, Enabled: false},
	}
	assert.NoError(t, setCustomRules(cfg, rules))
	got := patternsFromCfg(cfg)
	assert.Equal(t, rules, got)
}

// Names are not a closed set -> trim/dedupe/cap IS the whole contract.
func TestParseEgressDisabledAgents(t *testing.T) {
	t.Run("normalises", func(t *testing.T) {
		got, err := parseEgressDisabledAgents([]string{"  websearch  ", "", "memory_compose", "WebSearch", "   "})
		require.NoError(t, err)
		// Dedupe keeps the FIRST spelling -> operator's casing survives.
		assert.Equal(t, []string{"websearch", "memory_compose"}, got)
	})

	t.Run("empty input yields nil, not an empty slice", func(t *testing.T) {
		got, err := parseEgressDisabledAgents(nil)
		require.NoError(t, err)
		assert.Nil(t, got, "nil lets the DAO write the column default rather than an explicit []")
	})

	t.Run("all-blank input yields nil", func(t *testing.T) {
		got, err := parseEgressDisabledAgents([]string{"  ", ""})
		require.NoError(t, err)
		// Nil, not Empty -> Empty passes for []string{} too, so it would not
		// verify what the name claims.
		assert.Nil(t, got)
	})

	t.Run("rejects over the cap", func(t *testing.T) {
		many := make([]string, 0, maxDisabledAgentsEntries+1)
		for i := 0; i <= maxDisabledAgentsEntries; i++ {
			many = append(many, fmt.Sprintf("agent_%d", i))
		}
		_, err := parseEgressDisabledAgents(many)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "disabled_agents")
	})

	t.Run("at the cap is allowed", func(t *testing.T) {
		many := make([]string, 0, maxDisabledAgentsEntries)
		for i := 0; i < maxDisabledAgentsEntries; i++ {
			many = append(many, fmt.Sprintf("agent_%d", i))
		}
		got, err := parseEgressDisabledAgents(many)
		require.NoError(t, err)
		assert.Len(t, got, maxDisabledAgentsEntries)
	})
}

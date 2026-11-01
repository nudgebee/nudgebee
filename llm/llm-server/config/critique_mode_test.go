package config

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestResolveCritiqueMode(t *testing.T) {
	tests := []struct {
		name, raw, want string
		wantErr         bool
	}{
		{name: "unset defaults to continuation", raw: "", want: CritiqueModeContinuation},
		{name: "blank defaults to continuation", raw: " ", want: CritiqueModeContinuation},
		{name: "case-insensitive", raw: "Guided", want: CritiqueModeGuided},
		{name: "off", raw: "off", want: CritiqueModeOff},
		{name: "audit", raw: "audit", want: CritiqueModeAudit},
		{name: "unknown value", raw: "strict", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveCritiqueMode(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// LLM_CRITIQUE_MODE must reach the struct. viper.Unmarshal only maps env vars for
// keys it knows, so without a registered default the variable was silently
// ignored and every deployment stayed on the older flags (found on a local run).
func TestCritiqueModeEnvReachesConfig(t *testing.T) {
	t.Setenv("LLM_CRITIQUE_MODE", "continuation")
	var cfg appConfig
	require.NoError(t, viper.Unmarshal(&cfg))
	require.Equal(t, CritiqueModeContinuation, cfg.CritiqueMode)

	t.Setenv("LLM_CRITIQUE_MODE", "off")
	require.NoError(t, viper.Unmarshal(&cfg))
	require.Equal(t, CritiqueModeOff, cfg.CritiqueMode)
}

// The replaced flags no longer reach the struct: only llm_critique_mode decides.
func TestReplacedCritiqueFlagsAreIgnored(t *testing.T) {
	t.Setenv("LLM_CLAIM_CRITIQUE_FEEDBACK_ENABLED", "false")
	t.Setenv("LLM_CLAIM_CRITIQUE_ACTIONABLE_FEEDBACK_ENABLED", "false")
	var cfg appConfig
	require.NoError(t, viper.Unmarshal(&cfg))
	require.NoError(t, cfg.deriveCritiqueFlags())
	require.Equal(t, CritiqueModeContinuation, cfg.CritiqueMode)
	require.True(t, cfg.ClaimCritiqueFeedbackEnabled)
	require.True(t, cfg.ClaimCritiqueActionableFeedbackEnabled)
	require.True(t, cfg.CritiqueContinuationEnabled)
}

func TestDeriveCritiqueFlags(t *testing.T) {
	tests := []struct {
		mode                             string
		feedback, guidance, continuation bool
	}{
		{CritiqueModeOff, false, false, false},
		{CritiqueModeAudit, true, false, false},
		{CritiqueModeGuided, true, true, false},
		{CritiqueModeContinuation, true, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			cfg := appConfig{CritiqueMode: tt.mode}
			require.NoError(t, cfg.deriveCritiqueFlags())
			require.Equal(t, tt.feedback, cfg.ClaimCritiqueFeedbackEnabled)
			require.Equal(t, tt.guidance, cfg.ClaimCritiqueActionableFeedbackEnabled)
			require.Equal(t, tt.continuation, cfg.CritiqueContinuationEnabled)
		})
	}
	require.Error(t, (&appConfig{CritiqueMode: "strict"}).deriveCritiqueFlags())
}

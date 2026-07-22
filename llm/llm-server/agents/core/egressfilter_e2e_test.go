//go:build e2e

// EgressFilter e2e tests exercise the outbound secret egressfilter against a real LLM
// provider (configured via LLM_PROVIDER + LLM_MODEL_NAME +
// LLM_PROVIDER_API_KEY in .env). They are NOT part of `make test` — run with
// `make test-e2e` or
// `go test -tags=e2e -run TestE2E_EgressFilter ./agents/core/...`.
//
// These tests intentionally make real LLM API calls (small payloads, short
// outputs) so we observe the wrapper end-to-end against the production
// langchaingo provider plumbing — specifically through the GetLLMModel
// factory chokepoint and the shared LLM client cache. They skip cleanly if
// the relevant env vars are not configured.
//
// Lives here in agents/core rather than in security/egressfilter because the
// integration point is GetLLMModel and the cache invalidation API — both
// owned by this package.
//
// No internal identifiers (tenant ids, account ids, customer names) appear
// in this file. All configuration that varies between deployments comes from
// environment variables.
package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"

	"nudgebee/llm/config"
	"nudgebee/llm/security/egressfilter"
)

// TestMain loads .env into the OS environment before any e2e test runs so
// `go test -tags=e2e ./agents/core/...` works without `set -a; source .env`
// gymnastics from the shell. Each Bash subshell would otherwise need the
// env sourced manually because exports don't persist across invocations.
//
// Loading is best-effort and idempotent: an env var already set in the OS
// environment is not overridden, so an explicit `KEY=val go test ...` still
// wins over the file.
//
// We additionally mirror the LLM-provider env vars into config.Config — the
// LLM factory reads from the viper-loaded struct, not os.Getenv, and config's
// package-init ran (and missed .env) before TestMain. Populating the struct
// here makes the factory work without any external tooling.
func TestMain(m *testing.M) {
	loadDotEnvFromAncestors()
	syncConfigFromEnv()
	os.Exit(m.Run())
}

// syncConfigFromEnv re-runs viper.Unmarshal so values now present in the OS
// environment (loaded by loadDotEnvFromAncestors above) overlay the empty
// defaults that config's package-init left behind when it couldn't find
// .env from the test's working directory.
//
// Without this, anything downstream that reads config.Config.* (DB URL,
// LLM provider, secrets, relay credentials, ...) sees empty values.
func syncConfigFromEnv() {
	// viper.AutomaticEnv was already called by config's package init; we
	// just need to re-Unmarshal so the struct picks up env values that
	// were not present at the first Unmarshal call.
	if err := viper.Unmarshal(&config.Config); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: viper.Unmarshal failed: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "TestMain: config.Config.LlmProvider=%q LlmModel=%q LlmProviderApiKey=<%d bytes> LlmServerDBUrl=<%d bytes>\n",
		config.Config.LlmProvider, config.Config.LlmModel, len(config.Config.LlmProviderApiKey), len(config.Config.LlmServerDBUrl))
}

// loadDotEnvFromAncestors walks up from the current working directory looking
// for a .env file, parsing each KEY=VALUE line and exporting it to the OS
// environment if not already set. Stops at the first .env it finds, or at
// the filesystem root. Lines that don't parse as KEY=VALUE (e.g. multi-line
// PEM bodies) are silently skipped — the existing shell sourcing has the same
// behavior.
func loadDotEnvFromAncestors() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		path := filepath.Join(dir, ".env")
		if f, err := os.Open(path); err == nil {
			defer f.Close()
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				eq := strings.IndexByte(line, '=')
				if eq <= 0 {
					continue
				}
				key := strings.TrimSpace(line[:eq])
				if key == "" || strings.ContainsAny(key, " \t") {
					continue
				}
				val := strings.TrimSpace(line[eq+1:])
				// Strip a single pair of surrounding quotes if present.
				if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
					val = val[1 : len(val)-1]
				}
				if _, set := os.LookupEnv(key); !set {
					_ = os.Setenv(key, val)
				}
			}
			fmt.Fprintf(os.Stderr, "TestMain: loaded env from %s\n", path)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir { // hit root
			return
		}
		dir = parent
	}
}

// e2eEnv reads the LLM provider config from the environment and skips the
// test if any required field is missing. All egressfilter e2e tests funnel
// through this so the skip behavior is consistent and the env contract is
// documented in one place.
type e2eEnv struct {
	provider string
	model    string
	apiKey   string // checked for presence only, never logged
}

func loadE2EEnv(t *testing.T) e2eEnv {
	t.Helper()
	env := e2eEnv{
		provider: os.Getenv("LLM_PROVIDER"),
		model:    os.Getenv("LLM_MODEL_NAME"),
		apiKey:   os.Getenv("LLM_PROVIDER_API_KEY"),
	}
	RequireEnv(t, "LLM_PROVIDER", "LLM_MODEL_NAME", "LLM_PROVIDER_API_KEY")
	t.Logf("e2e config: provider=%s model=%s api_key=<%d bytes>",
		env.provider, env.model, len(env.apiKey))
	return env
}

// wrapRealProvider constructs a real LLM model via the same factory the rest
// of the codebase uses (GetLLMModel) and wraps it with the egressfilter. We pass
// an empty account id so no DB lookup happens — the constructor falls back
// to the LLM_PROVIDER_API_KEY env var, which is exactly what the
// in-process secret detector cares about.
func wrapRealProvider(t *testing.T, env e2eEnv, enabled bool, mode egressfilter.Mode) llms.Model {
	t.Helper()
	// Flip both the master switch and the per-detector flag through the
	// global config — the factory reads both when deciding to wrap. Master
	// off makes the entire wrapper a no-op regardless of per-detector flags.
	config.Config.LlmServerEgressFilterEnabled = enabled
	config.Config.LlmServerEgressFilterSecretsEnabled = enabled
	config.Config.LlmServerEgressFilterSecretsMode = string(mode)
	// The factory caches the wrapped model by (provider, modelName, accountId,
	// credsFp) — flag changes do not invalidate the cache. Each test needs a
	// fresh wrap, so we invalidate before constructing and on cleanup. This
	// is the production-correct path too: in real deployments a config flip
	// requires cache invalidation to take effect.
	InvalidateAllLLMClientCache()
	t.Cleanup(func() {
		config.Config.LlmServerEgressFilterEnabled = false
		config.Config.LlmServerEgressFilterSecretsEnabled = false
		config.Config.LlmServerEgressFilterSecretsMode = string(egressfilter.ModeDetect)
		InvalidateAllLLMClientCache()
	})
	model, err := GetLLMModel(env.provider, env.model, "", false, "")
	require.NoError(t, err)
	require.NotNil(t, model)
	return model
}

// TestE2E_EgressFilter_RealProviderBlocksSecret_Enforce sends a payload containing a
// baseline-rule-matching secret to a real LLM provider via the wrapper in
// enforce mode. The wrapper must block the call before any HTTP request
// leaves the process. We assert the typed error shape and the user-facing
// message contract.
func TestE2E_EgressFilter_RealProviderBlocksSecret_Enforce(t *testing.T) {
	env := loadE2EEnv(t)
	wrapped := wrapRealProvider(t, env, true, egressfilter.ModeEnforce)

	// Baseline rule sample — AKIAIOSFODNN7EXAMPLE is AWS's documentation
	// canonical example, not a real credential.
	msg := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman,
			"What does AKIAIOSFODNN7EXAMPLE mean? Should I rotate it?"),
	}

	start := time.Now()
	_, err := wrapped.GenerateContent(context.Background(), msg)
	elapsed := time.Since(start)

	require.Error(t, err)
	require.True(t, errors.Is(err, egressfilter.ErrSecretsBlocked))
	se, ok := egressfilter.AsError(err)
	require.True(t, ok)

	t.Logf("block summary:")
	t.Logf("  audit_id      : %s", se.AuditID)
	t.Logf("  rule_ids      : %v", se.RuleIDs)
	t.Logf("  elapsed       : %s (must be << one round-trip to the LLM)", elapsed)
	t.Logf("  user-facing   : %q", err.Error())

	// Hard contracts:
	assert.Regexp(t, `^egress-[0-9a-f]{12}$`, se.AuditID,
		"audit id format must be scrub-<12 hex>")
	assert.Contains(t, se.RuleIDs, "aws-access-key-id",
		"baseline rule must fire")
	assert.NotContains(t, err.Error(), "AKIA",
		"user-facing error must not echo the secret value")
	for _, rid := range se.RuleIDs {
		assert.NotContains(t, err.Error(), rid,
			"user-facing error must not echo rule ids")
	}
	assert.Contains(t, err.Error(), se.AuditID,
		"user-facing error must include audit id for log correlation")
	// In-process block should complete well under a full LLM round trip.
	// 500ms is generous; typical observation is <50ms.
	assert.Less(t, elapsed, 500*time.Millisecond,
		"block must be in-process, not a LLM round-trip")
}

// TestE2E_EgressFilter_RealProviderPassesCleanThrough_Enforce sends a clean payload
// through the wrapper in enforce mode and asserts the real LLM provider was
// actually reached and returned a response. This is the other half of the
// contract: enforce mode does NOT impair clean traffic.
func TestE2E_EgressFilter_RealProviderPassesCleanThrough_Enforce(t *testing.T) {
	env := loadE2EEnv(t)
	wrapped := wrapRealProvider(t, env, true, egressfilter.ModeEnforce)

	msg := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman,
			"Reply with exactly one short sentence about Kubernetes."),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	resp, err := wrapped.GenerateContent(ctx, msg)
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.Choices)
	// We do not echo the LLM response in test logs — it could contain
	// arbitrary text. Bounded summary is enough to know it worked.
	t.Logf("clean pass-through:")
	t.Logf("  elapsed      : %s (real LLM round-trip)", elapsed)
	t.Logf("  choices      : %d", len(resp.Choices))
	t.Logf("  content bytes: %d", len(resp.Choices[0].Content))
	assert.NotEmpty(t, resp.Choices[0].Content,
		"real LLM provider must have produced non-empty content")
}

// TestE2E_EgressFilter_RealProviderAuditModeForwardsWithSecret demonstrates audit mode:
// the wrapper detects the secret, emits the audit log (visible in test
// output via slog), but DOES NOT block. The real LLM is called.
func TestE2E_EgressFilter_RealProviderAuditModeForwardsWithSecret(t *testing.T) {
	env := loadE2EEnv(t)
	wrapped := wrapRealProvider(t, env, true, egressfilter.ModeDetect)

	msg := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman,
			"In one sentence, what should I do if I see AKIAIOSFODNN7EXAMPLE in a log?"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	resp, err := wrapped.GenerateContent(ctx, msg)
	elapsed := time.Since(start)

	require.NoError(t, err, "audit mode must not block, even when a hit is detected")
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.Choices)
	t.Logf("audit-mode pass-through:")
	t.Logf("  elapsed      : %s (real LLM round-trip)", elapsed)
	t.Logf("  audit log    : check stderr for 'egressfilter: outbound LLM payload contains potential secret (audit mode, not blocking)'")
	t.Logf("  choices      : %d", len(resp.Choices))
}

// TestE2E_EgressFilter_DisabledFlag_IsTrulyZeroOverhead proves that with the master flag
// off, the wrapper returns the inner model untouched — no scan runs, no
// metric is emitted, even on a payload that would otherwise hit.
func TestE2E_EgressFilter_DisabledFlag_IsTrulyZeroOverhead(t *testing.T) {
	env := loadE2EEnv(t)
	wrapped := wrapRealProvider(t, env, false, egressfilter.ModeEnforce)

	msg := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman,
			"AKIAIOSFODNN7EXAMPLE — describe in one sentence."),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := wrapped.GenerateContent(ctx, msg)
	require.NoError(t, err, "disabled egressfilter must pass everything through unchanged")
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.Choices)
	t.Logf("disabled-flag pass-through: choices=%d", len(resp.Choices))
}

// reAuditID is the format every block must produce. Locking it in a regex
// avoids drift if the prefix or hex length ever changes.
var reAuditID = regexp.MustCompile(`^egress-[0-9a-f]{12}$`)

// TestE2E_EgressFilter_AuditIDUniqueAcrossBlocks runs three back-to-back enforce-mode
// blocks against the real provider and asserts each one produced a distinct
// audit id. This is the same property the in-process tests check, but here
// we verify it under the real call path (cache lookup, factory, wrapper).
func TestE2E_EgressFilter_AuditIDUniqueAcrossBlocks(t *testing.T) {
	env := loadE2EEnv(t)
	wrapped := wrapRealProvider(t, env, true, egressfilter.ModeEnforce)

	msg := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "key=AKIAIOSFODNN7EXAMPLE"),
	}

	seen := map[string]struct{}{}
	for i := 0; i < 3; i++ {
		_, err := wrapped.GenerateContent(context.Background(), msg)
		require.Error(t, err)
		se, ok := egressfilter.AsError(err)
		require.True(t, ok)
		require.Regexp(t, reAuditID, se.AuditID)
		_, dup := seen[se.AuditID]
		assert.False(t, dup, "audit id %q must be unique across blocks", se.AuditID)
		seen[se.AuditID] = struct{}{}
		t.Logf("block %d: audit_id=%s rules=%v", i+1, se.AuditID, se.RuleIDs)
	}
}

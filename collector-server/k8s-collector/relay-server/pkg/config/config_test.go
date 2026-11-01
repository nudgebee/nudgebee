package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func captureLogs(fn func()) string {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	fn()
	return buf.String()
}

func TestLoadWarnsOnEmptySecretKey(t *testing.T) {
	t.Setenv("RELAY_SERVER_SECRET_KEY", "")

	out := captureLogs(func() {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}
		if cfg.Security.SecretKey != "" {
			t.Fatalf("expected empty secret key, got %q", cfg.Security.SecretKey)
		}
	})

	if !strings.Contains(out, "security.secret_key is empty") {
		t.Errorf("expected warning about empty security.secret_key, got logs: %s", out)
	}
}

func TestLoadNoWarningWhenSecretKeySet(t *testing.T) {
	t.Setenv("RELAY_SERVER_SECRET_KEY", "strong-random-value")

	out := captureLogs(func() {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}
		if cfg.Security.SecretKey != "strong-random-value" {
			t.Fatalf("expected secret key from env, got %q", cfg.Security.SecretKey)
		}
	})

	if strings.Contains(out, "security.secret_key is empty") {
		t.Errorf("did not expect warning when secret key is set, got logs: %s", out)
	}
}

// The relay publishes to the same exchange llm-server consumes and api-server
// publishes to, so the default must match theirs
// (rabbit_mq_llm_cache_invalidation_exchange = "llm_cache_invalidation").
func TestLoadLLMCacheInvalidationExchange(t *testing.T) {
	t.Setenv("RABBIT_MQ_LLM_CACHE_INVALIDATION_EXCHANGE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if got := cfg.RabbitMQ.LLMCacheInvalidationExchange; got != "llm_cache_invalidation" {
		t.Fatalf("default exchange = %q, want llm_cache_invalidation", got)
	}

	t.Setenv("RABBIT_MQ_LLM_CACHE_INVALIDATION_EXCHANGE", "custom_exchange")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if got := cfg.RabbitMQ.LLMCacheInvalidationExchange; got != "custom_exchange" {
		t.Fatalf("env override = %q, want custom_exchange", got)
	}
}

// Relay pods share MCP sessions through Redis unless an install explicitly
// opts out (docs/mcp-sessions.md).
func TestLoadMCPSessionStoreDefaultsToRedis(t *testing.T) {
	t.Setenv("MCP_SESSION_STORE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.MCP.SessionStore != "redis" {
		t.Errorf("MCP.SessionStore = %q, want default %q", cfg.MCP.SessionStore, "redis")
	}

	t.Setenv("MCP_SESSION_STORE", "memory")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.MCP.SessionStore != "memory" {
		t.Errorf("MCP.SessionStore = %q, want override %q", cfg.MCP.SessionStore, "memory")
	}
}

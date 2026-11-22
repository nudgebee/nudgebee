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

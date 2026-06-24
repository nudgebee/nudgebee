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

func TestLogSecurityWarningsOnDefaultRelaySecret(t *testing.T) {
	prev := Config.RelayServerSecretKey
	defer func() { Config.RelayServerSecretKey = prev }()
	Config.RelayServerSecretKey = insecureRelaySecret

	out := captureLogs(LogSecurityWarnings)

	if !strings.Contains(out, "relay_server_secret_key is set to the publicly known default") {
		t.Errorf("expected warning about default relay_server_secret_key, got logs: %s", out)
	}
}

func TestLogSecurityWarningsNoWarningOnStrongRelaySecret(t *testing.T) {
	prev := Config.RelayServerSecretKey
	defer func() { Config.RelayServerSecretKey = prev }()
	Config.RelayServerSecretKey = "strong-random-value"

	out := captureLogs(LogSecurityWarnings)

	if strings.Contains(out, "level=WARN") {
		t.Errorf("did not expect any warning, got logs: %s", out)
	}
}

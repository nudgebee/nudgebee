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

func TestLogSecurityWarnings(t *testing.T) {
	cases := []struct {
		name         string
		jwt          string
		relay        string
		mode         string
		wantWarns    int
		wantContains []string
	}{
		{
			name: "jwt default in local mode", jwt: insecureJWTSecret, relay: "strong", mode: "local",
			wantWarns:    1,
			wantContains: []string{"llm_server_jwt_secret is set to the insecure default"},
		},
		{
			name: "jwt default outside local mode", jwt: insecureJWTSecret, relay: "strong", mode: "",
			wantWarns:    1,
			wantContains: []string{"llm_server_jwt_secret is set to the publicly known default"},
		},
		{
			name: "relay default outside local mode", jwt: "strong", relay: insecureRelaySecret, mode: "",
			wantWarns:    1,
			wantContains: []string{"relay_server_secret_key is set to the publicly known default"},
		},
		{
			name: "relay default in local mode", jwt: "strong", relay: insecureRelaySecret, mode: "local",
			wantWarns:    1,
			wantContains: []string{"relay_server_secret_key is set to the insecure default"},
		},
		{
			name: "both defaults outside local mode", jwt: insecureJWTSecret, relay: insecureRelaySecret, mode: "",
			wantWarns: 2,
			wantContains: []string{
				"llm_server_jwt_secret is set to the publicly known default",
				"relay_server_secret_key is set to the publicly known default",
			},
		},
		{
			name: "non-default values", jwt: "strong-jwt", relay: "strong-relay", mode: "",
			wantWarns: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prevJwt := Config.LlmServerJwtSecret
			prevRelay := Config.RelayServerSecretKey
			prevMode := Config.LlmServerSecurityMode
			defer func() {
				Config.LlmServerJwtSecret = prevJwt
				Config.RelayServerSecretKey = prevRelay
				Config.LlmServerSecurityMode = prevMode
			}()
			Config.LlmServerJwtSecret = tc.jwt
			Config.RelayServerSecretKey = tc.relay
			Config.LlmServerSecurityMode = tc.mode

			out := captureLogs(LogSecurityWarnings)

			if got := strings.Count(out, "level=WARN"); got != tc.wantWarns {
				t.Errorf("expected %d warnings, got %d; logs: %s", tc.wantWarns, got, out)
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("expected logs to contain %q; logs: %s", want, out)
				}
			}
		})
	}
}

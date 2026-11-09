package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// llm-server must not start on a missing or publicly known JWT or relay
// secret outside local mode.
func TestCheckSecuritySecrets(t *testing.T) {
	cases := []struct {
		name, jwt, relay, mode string
		wantErr                []string
	}{
		{name: "jwt default", jwt: insecureJWTSecret, relay: "strong", wantErr: []string{"LLM_SERVER_JWT_SECRET"}},
		{name: "jwt empty", jwt: "", relay: "strong", wantErr: []string{"LLM_SERVER_JWT_SECRET"}},
		{name: "relay default", jwt: "strong", relay: insecureRelaySecret, wantErr: []string{"RELAY_SERVER_SECRET_KEY"}},
		{name: "relay empty", jwt: "strong", relay: "", wantErr: []string{"RELAY_SERVER_SECRET_KEY"}},
		{name: "both default", jwt: insecureJWTSecret, relay: insecureRelaySecret, wantErr: []string{"LLM_SERVER_JWT_SECRET", "RELAY_SERVER_SECRET_KEY"}},
		{name: "both default in local mode", jwt: insecureJWTSecret, relay: insecureRelaySecret, mode: "local"},
		{name: "strong values", jwt: "strong-jwt", relay: "strong-relay"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prevJwt, prevRelay, prevMode := Config.LlmServerJwtSecret, Config.RelayServerSecretKey, Config.LlmServerSecurityMode
			t.Cleanup(func() {
				Config.LlmServerJwtSecret, Config.RelayServerSecretKey, Config.LlmServerSecurityMode = prevJwt, prevRelay, prevMode
			})
			Config.LlmServerJwtSecret, Config.RelayServerSecretKey, Config.LlmServerSecurityMode = tc.jwt, tc.relay, tc.mode

			err := CheckSecuritySecrets()
			if len(tc.wantErr) == 0 {
				assert.NoError(t, err)
				return
			}
			for _, want := range tc.wantErr {
				assert.ErrorContains(t, err, want)
			}
		})
	}
}

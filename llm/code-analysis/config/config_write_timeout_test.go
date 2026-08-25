package config

import (
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

// TestLoadConfig_ServerWriteTimeoutEnvBinding covers the SERVER_WRITE_TIMEOUT ->
// server.write_timeout binding that llm-server's workspace pods rely on to raise
// the in-pod command execution deadline above HandleExecute's hardcoded 30s
// fallback (see workspace.CreateWorkspace).
func TestLoadConfig_ServerWriteTimeoutEnvBinding(t *testing.T) {
	t.Run("env var overrides the default", func(t *testing.T) {
		t.Setenv("SERVER_WRITE_TIMEOUT", "45s")
		viper.Reset()
		defer viper.Reset()

		cfg, err := LoadConfig()
		assert.NoError(t, err)
		assert.Equal(t, 45*time.Second, cfg.Server.WriteTimeout)
	})

	t.Run("unset env var keeps write_timeout disabled", func(t *testing.T) {
		viper.Reset()
		defer viper.Reset()

		cfg, err := LoadConfig()
		assert.NoError(t, err)
		assert.Equal(t, time.Duration(0), cfg.Server.WriteTimeout)
	})
}

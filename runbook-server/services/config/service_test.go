package config

import (
	"errors"
	"net/http"
	"testing"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeepMaskedSecret(t *testing.T) {
	masked := func(typ model.ConfigType) model.Config {
		return model.Config{Key: "API_TOKEN", Value: SecretMask, Type: typ}
	}
	storedSecret := &model.Config{Key: "API_TOKEN", Value: "encrypted-blob", Type: model.ConfigTypeSecret}

	t.Run("untouched secret edit keeps the stored encrypted value", func(t *testing.T) {
		value, err := keepMaskedSecret(masked(model.ConfigTypeSecret), storedSecret)
		require.NoError(t, err)
		assert.Equal(t, "encrypted-blob", value)
	})

	t.Run("plain config may hold the literal mask when no secret is stored", func(t *testing.T) {
		value, err := keepMaskedSecret(masked(model.ConfigTypeConfig), nil)
		require.NoError(t, err)
		assert.Equal(t, SecretMask, value)
	})

	refused := []struct {
		name     string
		incoming model.Config
		existing *model.Config
	}{
		{"no stored row", masked(model.ConfigTypeSecret), nil},
		{"plain config switched to secret without a new value", masked(model.ConfigTypeSecret), &model.Config{Key: "API_TOKEN", Value: "plain", Type: model.ConfigTypeConfig}},
		{"secret switched to config without a new value", masked(model.ConfigTypeConfig), storedSecret},
	}
	for _, tc := range refused {
		t.Run("refuses: "+tc.name, func(t *testing.T) {
			_, err := keepMaskedSecret(tc.incoming, tc.existing)
			var commonErr common.Error
			require.True(t, errors.As(err, &commonErr), "want a common.Error, got %v", err)
			assert.Equal(t, http.StatusBadRequest, commonErr.Code)
		})
	}
}

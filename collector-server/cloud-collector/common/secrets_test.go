package common

import (
	"testing"

	"nudgebee/collector/cloud/config"

	"github.com/stretchr/testify/assert"
)

func TestEncrypt(t *testing.T) {
	// Set a dummy encryption key for testing
	oldKey := config.Config.NudgebeeEncryptionKey
	config.Config.NudgebeeEncryptionKey = "6368616e676520746869732070617373"

	defer func() {
		// Restore the original key after the test
		config.Config.NudgebeeEncryptionKey = oldKey
	}()

	data, err := Encrypt(`abc`)
	assert.Nil(t, err) // Expect no error
	assert.NotEmpty(t, data)

	data2, err := Decrypt(data)
	assert.Nil(t, err) // Expect no error
	assert.Equal(t, `abc`, data2)
}

func TestDecryptEmptyString(t *testing.T) {
	// Set a dummy encryption key for testing
	oldKey := config.Config.NudgebeeEncryptionKey
	config.Config.NudgebeeEncryptionKey = "6368616e676520746869732070617373"

	defer func() {
		config.Config.NudgebeeEncryptionKey = oldKey
	}()

	// Test empty string
	_, err := Decrypt("")
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestDecryptInvalidHex(t *testing.T) {
	// Set a dummy encryption key for testing
	oldKey := config.Config.NudgebeeEncryptionKey
	config.Config.NudgebeeEncryptionKey = "6368616e676520746869732070617373"

	defer func() {
		config.Config.NudgebeeEncryptionKey = oldKey
	}()

	// Test invalid hex string
	_, err := Decrypt("not-valid-hex")
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "hex")
}

func TestDecryptTooShort(t *testing.T) {
	// Set a dummy encryption key for testing
	oldKey := config.Config.NudgebeeEncryptionKey
	config.Config.NudgebeeEncryptionKey = "6368616e676520746869732070617373"

	defer func() {
		config.Config.NudgebeeEncryptionKey = oldKey
	}()

	// Test data shorter than 12 bytes (24 hex chars)
	// "abcdef" is only 3 bytes when decoded
	_, err := Decrypt("abcdef")
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "too short")
}

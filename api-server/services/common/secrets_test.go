package common

import (
	"testing"

	"nudgebee/services/config"

	"github.com/stretchr/testify/assert"
)

func TestEncryptDescrypt(t *testing.T) {
	// Encrypt/Decrypt is pure crypto; set a fixed valid AES-256 key so the
	// round-trip is self-contained instead of panicking when the key is unset.
	orig := config.Config.NudgebeeEncryptionKey
	config.Config.NudgebeeEncryptionKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	defer func() { config.Config.NudgebeeEncryptionKey = orig }()

	t.Run("TestTimeDecoding", func(t *testing.T) {
		data := "mysupersecretkey"
		encrypted, err := Encrypt(data)

		assert.Nil(t, err)

		decrypted, err := Decrypt(encrypted)

		assert.Nil(t, err)
		assert.Equal(t, data, decrypted)
	})
}

func TestHashPassword(t *testing.T) {
	t.Run("TestTimeDecoding", func(t *testing.T) {
		data := "mysupersecretkey"
		hashedPassword, err := HashPassword(data)

		assert.Nil(t, err)

		err = ValidateHashPassword(data, hashedPassword)
		assert.Nil(t, err)
	})
}

package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEncryptDescrypt(t *testing.T) {
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

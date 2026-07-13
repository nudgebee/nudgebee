package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"

	"nudgebee/llm-gateway/config"
)

// decrypt reverses llm-server's Encrypt (AES-GCM, 12-byte nonce prefix, hex-encoded)
// using the shared NB encryption key, so a stored provider secret can be read back.
func decrypt(encrypted string) (string, error) {
	data, err := hex.DecodeString(encrypted)
	if err != nil {
		return "", err
	}
	if len(data) < 12 {
		return "", errors.New("invalid ciphertext: too short")
	}
	iv, ciphertext := data[:12], data[12:]

	key, err := hex.DecodeString(config.Config.NudgebeeEncryptionKey)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := aesgcm.Open(nil, iv, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

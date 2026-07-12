package common

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"

	"nudgebee/llm-gateway/config"
)

// Decrypt reverses llm-server's Encrypt (AES-GCM, 12-byte nonce prefix, hex-encoded)
// using the shared NB encryption key. The gateway only needs to READ per-tenant
// provider secrets from integration_config_values, so only Decrypt is ported.
func Decrypt(encrypted string) (string, error) {
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

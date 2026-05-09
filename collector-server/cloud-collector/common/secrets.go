package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"nudgebee/collector/cloud/config"
)

func Encrypt(message string) (string, error) {
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

	nonce := make([]byte, aesgcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	prefix := nonce

	ciphertext := aesgcm.Seal(prefix, nonce, []byte(message), nil)
	s := hex.EncodeToString(ciphertext)
	return s, err
}

func Decrypt(encrypted string) (string, error) {
	// Handle empty or whitespace-only strings
	if len(encrypted) == 0 {
		return "", fmt.Errorf("encrypted string is empty")
	}

	data, err := hex.DecodeString(encrypted)
	if err != nil {
		return "", fmt.Errorf("failed to decode hex string: %w", err)
	}

	// Validate minimum length for IV (12 bytes for GCM nonce)
	if len(data) < 12 {
		return "", fmt.Errorf("encrypted data too short: expected at least 12 bytes, got %d bytes", len(data))
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

func Encode(message string) string {
	hash := sha1.New()
	hash.Write([]byte(message))
	return hex.EncodeToString(hash.Sum(nil))
}

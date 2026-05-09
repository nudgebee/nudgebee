package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

func Encrypt(message string) (string, error) {
	key, err := hex.DecodeString(Config.NudgebeeEncryptionKey)
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
	raw := strings.TrimPrefix(encrypted, "enc:")

	data, err := hex.DecodeString(raw)
	if err != nil {
		return "", err
	}

	// AES-GCM ciphertext must contain at least 12-byte IV + 16-byte auth tag.
	const minLen = 12 + 16
	if len(data) < minLen {
		return "", fmt.Errorf("ciphertext too short: %d bytes (minimum %d)", len(data), minLen)
	}

	iv := data[:12]
	ciphertext := data[12 : len(data)-16]
	authTag := data[len(data)-16:]

	key, err := hex.DecodeString(Config.NudgebeeEncryptionKey)
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

	plaintext, err := aesgcm.Open(nil, iv, append(ciphertext, authTag...), nil)
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

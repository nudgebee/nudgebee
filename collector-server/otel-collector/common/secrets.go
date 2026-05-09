package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"nudgebee/collector/otel/config"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
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
	data, err := hex.DecodeString(encrypted)
	if err != nil {
		return "", err
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

func GenerateUUID() string {
	uuid, _ := uuid.NewRandom()
	return uuid.String()
}

func GenerateRandomHexString(n int) string {
	bytes := make([]byte, 36)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func HashKey(key string) (string, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(key), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashedPassword), nil
}

func ValidateHashKey(password, hashedPassword string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	if err != nil {
		return errors.New("invalid password")
	}
	return nil
}

package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"nudgebee/services/config"

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

	// AES-GCM ciphertext is IV (12 bytes) + ciphertext + auth tag (16 bytes).
	if len(data) < 28 {
		return "", errors.New("invalid encrypted value: too short")
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

// HashTextFast uses sha256 for hashing strings, its fast, but dont use for passwords
func HashTextFast(message string) string {
	hash := sha256.New()
	hash.Write([]byte(message))
	return hex.EncodeToString(hash.Sum(nil))
}

// GenerateUUID generates a random UUID
func GenerateUUID() string {
	uuid, _ := uuid.NewRandom()
	return uuid.String()
}

// GenerateRandomHexString generates n bytes random string
func GenerateRandomHexString(n int) (string, error) {
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// HashPassword uses bcrypt to hash given key, use it for oneway encryption of passwords or similar secret text
func HashPassword(key string) (string, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(key), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashedPassword), nil
}

// ValidateHashPassword compares password && hased password
func ValidateHashPassword(password, hashedPassword string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	if err != nil {
		return errors.New("invalid password")
	}
	return nil
}

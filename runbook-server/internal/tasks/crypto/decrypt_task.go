package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
)

type CryptoDecryptTask struct{}

func (t *CryptoDecryptTask) GetName() string {
	return "crypto.decrypt"
}

func (t *CryptoDecryptTask) GetDescription() string {
	return "Decrypt AES-GCM encrypted data."
}

func (t *CryptoDecryptTask) GetDisplayName() string {
	return "Decrypt"
}

func (t *CryptoDecryptTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeString,
				Description: "The data to decrypt (base64 encoded)",
				Required:    true,
			},
			"key": {
				Type:        types.PropertyTypeString,
				Description: "The encryption key. Behavior depends on key_encoding.",
				Required:    true,
				IsEncrypted: true,
			},
			"algorithm": {
				Type:        types.PropertyTypeString,
				Description: "The encryption algorithm",
				Required:    false,
				Default:     "aes-256-gcm",
				Options:     []string{"aes-256-gcm"},
			},
			"key_encoding": {
				Type:        types.PropertyTypeString,
				Description: "The encoding of the provided key (text, base64, hex). 'text' will hash the key to ensure correct length.",
				Required:    false,
				Default:     "text",
				Options:     []string{"text", "base64", "hex"},
			},
		},
	}
}

func (t *CryptoDecryptTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeString,
				Description: "The decrypted data",
				Required:    true,
			},
		},
	}
}

func (t *CryptoDecryptTask) Execute(ctx types.TaskContext, params map[string]any) (any, error) {
	// Redact sensitive data from logs
	logParams := make(map[string]any)
	for k, v := range params {
		if k == "key" || k == "data" {
			logParams[k] = "[REDACTED]"
		} else {
			logParams[k] = v
		}
	}
	ctx.GetLogger().Debug("Executing CryptoDecryptTask", "params", logParams)

	dataStr, ok := params["data"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid data format")
	}

	keyStr, ok := params["key"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid key format")
	}

	algo, _ := params["algorithm"].(string)
	if algo == "" {
		algo = "aes-256-gcm"
	}

	keyEncoding, _ := params["key_encoding"].(string)
	if keyEncoding == "" {
		keyEncoding = "text"
	}

	var key []byte
	var err error

	switch keyEncoding {
	case "text":
		// Hash key to ensure it is 32 bytes for AES-256
		h := sha256.Sum256([]byte(keyStr))
		key = h[:]
	case "base64":
		key, err = base64.StdEncoding.DecodeString(keyStr)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 key: %w", err)
		}
	case "hex":
		key, err = hex.DecodeString(keyStr)
		if err != nil {
			return nil, fmt.Errorf("invalid hex key: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported key_encoding: %s", keyEncoding)
	}

	if algo != "aes-256-gcm" {
		return nil, fmt.Errorf("unsupported algorithm: %s", algo)
	}

	if len(key) != 32 {
		return nil, fmt.Errorf("invalid key length for %s: expected 32 bytes, got %d", algo, len(key))
	}

	ciphertext, err := base64.StdEncoding.DecodeString(dataStr)
	if err != nil {
		return nil, fmt.Errorf("failed to decode data: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}

	return map[string]any{
		"data": string(plaintext),
	}, nil
}

package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

type CredentialHandler struct {
	encryptionKey []byte
}

func NewCredentialHandler(encryptionKey string) *CredentialHandler {
	// Use first 32 bytes of key for AES-256
	key := make([]byte, 32)
	copy(key, []byte(encryptionKey))
	return &CredentialHandler{encryptionKey: key}
}

type GitCredentials struct {
	Type string `json:"type" binding:"required,oneof=token ssh_key basic encrypted env_ref"`

	// For type: "token" (GitHub PAT, GitLab token, etc.)
	Token string `json:"token,omitempty"`

	// For type: "ssh_key"
	SSHKey        string `json:"ssh_key,omitempty"`
	SSHPassphrase string `json:"ssh_passphrase,omitempty"`

	// For type: "basic" (username/password)
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

	// For type: "encrypted" (AES encrypted credentials)
	EncryptedData string `json:"encrypted_data,omitempty"`

	// For type: "env_ref" (environment variable reference)
	EnvRef string `json:"env_ref,omitempty"`
}

type ResolvedCredentials struct {
	Type          string
	Token         string
	SSHKey        string
	SSHPassphrase string
	Username      string
	Password      string
}

func (ch *CredentialHandler) ResolveCredentials(creds GitCredentials) (*ResolvedCredentials, error) {
	switch creds.Type {
	case "token":
		return &ResolvedCredentials{
			Type:  "token",
			Token: creds.Token,
		}, nil

	case "ssh_key":
		sshKey, err := ch.decodeIfBase64(creds.SSHKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decode SSH key: %w", err)
		}

		return &ResolvedCredentials{
			Type:          "ssh_key",
			SSHKey:        sshKey,
			SSHPassphrase: creds.SSHPassphrase,
		}, nil

	case "basic":
		return &ResolvedCredentials{
			Type:     "basic",
			Username: creds.Username,
			Password: creds.Password,
		}, nil

	case "encrypted":
		return ch.decryptCredentials(creds.EncryptedData)

	case "env_ref":
		return ch.resolveFromEnv(creds.EnvRef)

	case "none":
		// For public repositories that don't require authentication
		return &ResolvedCredentials{
			Type: "none",
		}, nil

	default:
		return nil, fmt.Errorf("unsupported credential type: %s", creds.Type)
	}
}

func (ch *CredentialHandler) decryptCredentials(encryptedData string) (*ResolvedCredentials, error) {
	// Decode base64
	data, err := base64.StdEncoding.DecodeString(encryptedData)
	if err != nil {
		return nil, fmt.Errorf("failed to decode encrypted data: %w", err)
	}

	// Decrypt using AES-GCM
	block, err := aes.NewCipher(ch.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	if len(data) < aesgcm.NonceSize() {
		return nil, errors.New("encrypted data too short")
	}

	nonce, ciphertext := data[:aesgcm.NonceSize()], data[aesgcm.NonceSize():]
	plaintext, err := aesgcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}

	// Unmarshal JSON
	var creds ResolvedCredentials
	if err := json.Unmarshal(plaintext, &creds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal credentials: %w", err)
	}

	return &creds, nil
}

func (ch *CredentialHandler) resolveFromEnv(envRef string) (*ResolvedCredentials, error) {
	envData := os.Getenv(envRef)
	if envData == "" {
		return nil, fmt.Errorf("environment variable %s not found", envRef)
	}

	// Try to parse as JSON first
	var creds ResolvedCredentials
	if err := json.Unmarshal([]byte(envData), &creds); err != nil {
		// If not JSON, treat as token
		return &ResolvedCredentials{
			Type:  "token",
			Token: envData,
		}, nil
	}

	return &creds, nil
}

func (ch *CredentialHandler) decodeIfBase64(data string) (string, error) {
	// Try to decode as base64, if fails return original
	if decoded, err := base64.StdEncoding.DecodeString(data); err == nil {
		return string(decoded), nil
	}
	return data, nil
}

// EncryptCredentials encrypts credentials for secure storage/transmission
func (ch *CredentialHandler) EncryptCredentials(creds ResolvedCredentials) (string, error) {
	// Marshal to JSON
	jsonData, err := json.Marshal(creds)
	if err != nil {
		return "", err
	}

	// Create cipher
	block, err := aes.NewCipher(ch.encryptionKey)
	if err != nil {
		return "", err
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Create nonce
	nonce := make([]byte, aesgcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	// Encrypt
	ciphertext := aesgcm.Seal(nonce, nonce, jsonData, nil)

	// Encode to base64
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

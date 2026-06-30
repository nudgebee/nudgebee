package credentials

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
)

type CredentialHandler struct{}

func NewCredentialHandler() *CredentialHandler {
	return &CredentialHandler{}
}

type GitCredentials struct {
	Type string `json:"type" binding:"required,oneof=token ssh_key basic env_ref none"`

	// For type: "token" (GitHub PAT, GitLab token, etc.)
	Token string `json:"token,omitempty"`

	// For type: "ssh_key"
	SSHKey        string `json:"ssh_key,omitempty"`
	SSHPassphrase string `json:"ssh_passphrase,omitempty"`

	// For type: "basic" (username/password)
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

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

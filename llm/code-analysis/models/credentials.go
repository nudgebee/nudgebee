package models

// Credentials represents authentication credentials for Git operations
type Credentials struct {
	Type     string `json:"type"`     // "token", "ssh_key", "basic", "encrypted", "env_ref"
	Value    string `json:"value"`    // The credential value
	Username string `json:"username"` // For basic auth
	Password string `json:"password"` // For basic auth
}

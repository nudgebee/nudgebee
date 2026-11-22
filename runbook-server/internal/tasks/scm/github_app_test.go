package scm

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
)

// The platform App's signed JWT must never leave github.com: any other host
// could replay it to mint tokens for every installation of the App.
func TestBuildInstallationTokenURL(t *testing.T) {
	const id int64 = 42
	const githubURL = "https://api.github.com/app/installations/42/access_tokens"
	tests := []struct {
		name    string
		apiURL  string
		allowed bool
	}{
		{name: "empty url", apiURL: "", allowed: true},
		{name: "api.github.com", apiURL: "https://api.github.com", allowed: true},
		{name: "github.com with path", apiURL: "https://github.com/acme", allowed: true},
		{name: "www.github.com", apiURL: "https://www.github.com/", allowed: true},
		{name: "no scheme", apiURL: "api.github.com", allowed: true},
		{name: "upper case host with port", apiURL: "https://API.GitHub.com:443", allowed: true},
		{name: "enterprise host", apiURL: "https://ghe.example.com", allowed: false},
		{name: "enterprise api/v3", apiURL: "https://ghe.example.com/api/v3", allowed: false},
		{name: "github.com as a subdomain of another host", apiURL: "https://api.github.com.example.com", allowed: false},
		{name: "github.com in the path only", apiURL: "https://example.com/api.github.com", allowed: false},
		{name: "github.com as userinfo", apiURL: "https://api.github.com@example.com", allowed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildInstallationTokenURL(tt.apiURL, id)
			if tt.allowed {
				if err != nil || got != githubURL {
					t.Fatalf("buildInstallationTokenURL(%q) = %q, %v; want %q", tt.apiURL, got, err, githubURL)
				}
				return
			}
			if !errors.Is(err, ErrGithubAppOnEnterprise) || got != "" {
				t.Fatalf("buildInstallationTokenURL(%q) = %q, %v; want ErrGithubAppOnEnterprise", tt.apiURL, got, err)
			}
		})
	}
}

// GetGithubAppInstallationToken must refuse a non-github.com host before
// sending anything, even with the App credentials configured.
func TestGetGithubAppInstallationToken_RefusesOtherHosts(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	t.Setenv("GITHUB_APP_ID", "1")
	t.Setenv("GITHUB_PRIVATE_KEY", string(keyPEM))
	_, err = GetGithubAppInstallationToken(context.Background(), "https://ghe.example.com", 42)
	if !errors.Is(err, ErrGithubAppOnEnterprise) {
		t.Fatalf("err = %v, want ErrGithubAppOnEnterprise", err)
	}
}

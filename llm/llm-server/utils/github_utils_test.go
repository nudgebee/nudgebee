package utils

import (
	"errors"
	"testing"
)

func TestReplaceEscapedNewlines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "single escaped newline", in: `a\nb`, want: "a\nb"},
		{name: "multiple escaped newlines", in: `a\nb\nc`, want: "a\nb\nc"},
		{name: "no escapes unchanged", in: "plain text", want: "plain text"},
		{name: "already real newline unchanged", in: "a\nb", want: "a\nb"},
		{name: "empty string", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := replaceEscapedNewlines(tt.in); got != tt.want {
				t.Errorf("replaceEscapedNewlines(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

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

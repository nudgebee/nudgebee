package utils

import "testing"

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

func TestBuildInstallationTokenURL(t *testing.T) {
	const id int64 = 42
	tests := []struct {
		name   string
		apiURL string
		want   string
	}{
		{
			name:   "empty url defaults to github.com",
			apiURL: "",
			want:   "https://api.github.com/app/installations/42/access_tokens",
		},
		{
			name:   "github.com api url",
			apiURL: "https://api.github.com",
			want:   "https://api.github.com/app/installations/42/access_tokens",
		},
		{
			name:   "enterprise base url appends api/v3",
			apiURL: "https://ghe.example.com",
			want:   "https://ghe.example.com/api/v3/app/installations/42/access_tokens",
		},
		{
			name:   "enterprise url already has api/v3",
			apiURL: "https://ghe.example.com/api/v3",
			want:   "https://ghe.example.com/api/v3/app/installations/42/access_tokens",
		},
		{
			name:   "enterprise url with trailing slash is trimmed",
			apiURL: "https://ghe.example.com/",
			want:   "https://ghe.example.com/api/v3/app/installations/42/access_tokens",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildInstallationTokenURL(tt.apiURL, id); got != tt.want {
				t.Errorf("buildInstallationTokenURL(%q, %d) = %q, want %q", tt.apiURL, id, got, tt.want)
			}
		})
	}
}

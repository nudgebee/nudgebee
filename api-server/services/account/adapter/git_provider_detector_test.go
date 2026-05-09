package adapter

import (
	"testing"
)

func TestDetectGitProviderFromURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		// GitHub URLs
		{
			name:     "GitHub HTTPS URL",
			url:      "https://github.com/org/repo",
			expected: GitProviderGitHub,
		},
		{
			name:     "GitHub HTTPS URL with .git suffix",
			url:      "https://github.com/org/repo.git",
			expected: GitProviderGitHub,
		},
		{
			name:     "GitHub URL without scheme",
			url:      "github.com/org/repo",
			expected: GitProviderGitHub,
		},
		{
			name:     "GitHub Enterprise (github. prefix)",
			url:      "https://github.mycompany.com/org/repo",
			expected: GitProviderGitHub,
		},

		// GitLab URLs
		{
			name:     "GitLab HTTPS URL",
			url:      "https://gitlab.com/group/project",
			expected: GitProviderGitLab,
		},
		{
			name:     "GitLab HTTPS URL with subgroup",
			url:      "https://gitlab.com/group/subgroup/project",
			expected: GitProviderGitLab,
		},
		{
			name:     "GitLab URL with .git suffix",
			url:      "https://gitlab.com/group/project.git",
			expected: GitProviderGitLab,
		},
		{
			name:     "GitLab self-hosted (gitlab. prefix)",
			url:      "https://gitlab.mycompany.com/group/project",
			expected: GitProviderGitLab,
		},
		{
			name:     "GitLab self-hosted with custom domain",
			url:      "https://gitlab.example.org/team/repo",
			expected: GitProviderGitLab,
		},
		{
			name:     "GitLab self-hosted via /gitlab/ path",
			url:      "https://code.company.com/gitlab/team/repo",
			expected: GitProviderGitLab,
		},

		// Unknown/Unsupported providers
		{
			name:     "Bitbucket URL",
			url:      "https://bitbucket.org/org/repo",
			expected: "",
		},
		{
			name:     "Azure DevOps URL",
			url:      "https://dev.azure.com/org/project/_git/repo",
			expected: "",
		},
		{
			name:     "Empty URL",
			url:      "",
			expected: "",
		},
		{
			name:     "Whitespace only URL",
			url:      "   ",
			expected: "",
		},
		{
			name:     "Invalid URL",
			url:      "not-a-valid-url",
			expected: "",
		},

		// Edge cases
		{
			name:     "URL with trailing spaces",
			url:      "  https://github.com/org/repo  ",
			expected: GitProviderGitHub,
		},
		{
			name:     "HTTP instead of HTTPS",
			url:      "http://github.com/org/repo",
			expected: GitProviderGitHub,
		},
		{
			name:     "Mixed case URL",
			url:      "https://GitHub.COM/org/repo",
			expected: GitProviderGitHub,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectGitProviderFromURL(tt.url)
			if result != tt.expected {
				t.Errorf("DetectGitProviderFromURL(%q) = %q, want %q", tt.url, result, tt.expected)
			}
		})
	}
}

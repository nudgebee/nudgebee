package gitprovider

import (
	"fmt"
	"net/url"
	"strings"
)

// GitProvider represents a git hosting provider
type GitProvider string

const (
	GitProviderGitHub  GitProvider = "github"
	GitProviderGitLab  GitProvider = "gitlab"
	GitProviderUnknown GitProvider = "unknown"
)

// RepoInfo contains parsed repository information
type RepoInfo struct {
	Provider    GitProvider
	Host        string // "github.com" or "gitlab.com" or custom host
	Owner       string // For GitHub: owner, For GitLab: first group
	Repo        string // For GitHub: repo, For GitLab: project name
	FullPath    string // Full path: "owner/repo" or "group/subgroup/project"
	ProjectPath string // URL-encoded path for API calls
}

// DetectProvider auto-detects the git provider from a repository URL
func DetectProvider(repoURL string) GitProvider {
	if repoURL == "" {
		return GitProviderUnknown
	}

	lowerURL := strings.ToLower(repoURL)

	// Check for GitLab patterns first (more specific)
	if strings.Contains(lowerURL, "gitlab.com") ||
		strings.Contains(lowerURL, "gitlab:") ||
		strings.Contains(lowerURL, "/-/") { // GitLab-specific URL pattern
		return GitProviderGitLab
	}

	// Check for GitHub patterns
	if strings.Contains(lowerURL, "github.com") ||
		strings.Contains(lowerURL, "github:") {
		return GitProviderGitHub
	}

	// Default to GitHub for backward compatibility
	return GitProviderGitHub
}

// DetectProviderFromHost determines provider from hostname
func DetectProviderFromHost(host string) GitProvider {
	lowerHost := strings.ToLower(host)

	if strings.Contains(lowerHost, "gitlab") {
		return GitProviderGitLab
	}
	if strings.Contains(lowerHost, "github") {
		return GitProviderGitHub
	}

	// Default to unknown for custom hosts
	return GitProviderUnknown
}

// ExtractRepoInfo parses a repository URL and extracts structured information
func ExtractRepoInfo(repoURL string, provider GitProvider) (*RepoInfo, error) {
	if repoURL == "" {
		return nil, fmt.Errorf("empty repository URL")
	}

	// Remove .git suffix
	repoURL = strings.TrimSuffix(repoURL, ".git")

	// Handle SSH URLs: git@host:path
	if strings.HasPrefix(repoURL, "git@") {
		return extractFromSSHURL(repoURL, provider)
	}

	// Handle HTTPS URLs
	if strings.HasPrefix(repoURL, "http://") || strings.HasPrefix(repoURL, "https://") {
		return extractFromHTTPURL(repoURL, provider)
	}

	// Try parsing as a simple path (owner/repo or group/project)
	return extractFromPath(repoURL, provider)
}

// extractFromSSHURL parses SSH-style URLs: git@host:path
func extractFromSSHURL(repoURL string, provider GitProvider) (*RepoInfo, error) {
	// git@github.com:owner/repo.git -> owner/repo
	// git@gitlab.com:group/subgroup/project.git -> group/subgroup/project

	// Remove git@ prefix
	url := strings.TrimPrefix(repoURL, "git@")
	url = strings.TrimSuffix(url, ".git")

	// Split on : to get host and path
	parts := strings.SplitN(url, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid SSH URL format: %s", repoURL)
	}

	host := parts[0]
	path := parts[1]

	return buildRepoInfo(host, path, provider)
}

// extractFromHTTPURL parses HTTP(S) URLs
func extractFromHTTPURL(repoURL string, provider GitProvider) (*RepoInfo, error) {
	// https://github.com/owner/repo.git -> owner/repo
	// https://gitlab.com/group/subgroup/project.git -> group/subgroup/project

	parsed, err := url.Parse(repoURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Remove leading slash and .git suffix
	path := strings.TrimPrefix(parsed.Path, "/")
	path = strings.TrimSuffix(path, ".git")

	return buildRepoInfo(parsed.Host, path, provider)
}

// extractFromPath handles simple path format (owner/repo)
func extractFromPath(path string, provider GitProvider) (*RepoInfo, error) {
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")

	if path == "" {
		return nil, fmt.Errorf("empty path")
	}

	return buildRepoInfo("", path, provider)
}

// buildRepoInfo constructs RepoInfo from host and path
func buildRepoInfo(host, path string, provider GitProvider) (*RepoInfo, error) {
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")

	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid repository path: %s (expected owner/repo format)", path)
	}

	info := &RepoInfo{
		Provider:    provider,
		Host:        host,
		FullPath:    path,
		ProjectPath: url.PathEscape(path),
	}

	if provider == GitProviderGitLab {
		// GitLab: group/subgroup/.../project
		// Owner is the first group, Repo is the last segment (project)
		info.Owner = parts[0]
		info.Repo = parts[len(parts)-1]
	} else {
		// GitHub: owner/repo (always 2 parts)
		info.Owner = parts[0]
		info.Repo = parts[1]
	}

	return info, nil
}

// GetMergeRequestTerminology returns the appropriate term for the provider
func GetMergeRequestTerminology(provider GitProvider) string {
	if provider == GitProviderGitLab {
		return "MR"
	}
	return "PR"
}

// GetMergeRequestFullTerminology returns the full term for the provider
func GetMergeRequestFullTerminology(provider GitProvider) string {
	if provider == GitProviderGitLab {
		return "merge request"
	}
	return "pull request"
}

// GetCLIToolName returns the CLI tool name for the provider
func GetCLIToolName(provider GitProvider) string {
	if provider == GitProviderGitLab {
		return "glab"
	}
	return "gh"
}

// GetTokenUsername returns the username to use with token authentication
func GetTokenUsername(provider GitProvider) string {
	if provider == GitProviderGitLab {
		return "oauth2"
	}
	return "x-access-token"
}

// IsValidProvider checks if the provider string is valid
func IsValidProvider(provider string) bool {
	switch GitProvider(provider) {
	case GitProviderGitHub, GitProviderGitLab:
		return true
	default:
		return false
	}
}

// ParseProvider converts a string to GitProvider
func ParseProvider(provider string) GitProvider {
	switch strings.ToLower(provider) {
	case "github":
		return GitProviderGitHub
	case "gitlab":
		return GitProviderGitLab
	default:
		return GitProviderUnknown
	}
}

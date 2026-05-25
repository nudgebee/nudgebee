package clients

import (
	"fmt"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// CreateGitLabClient creates a GitLab API client with the given personal access token.
// If baseURL is empty, it defaults to gitlab.com.
// For self-hosted GitLab instances, provide the base URL (e.g., "https://gitlab.example.com").
func CreateGitLabClient(token, baseURL string) (*gitlab.Client, error) {
	if baseURL == "" {
		// Default to gitlab.com
		client, err := gitlab.NewClient(token)
		if err != nil {
			return nil, fmt.Errorf("failed to create GitLab client: %w", err)
		}
		return client, nil
	}

	// Self-hosted GitLab instance
	client, err := gitlab.NewClient(token, gitlab.WithBaseURL(baseURL))
	if err != nil {
		return nil, fmt.Errorf("failed to create GitLab client with base URL %s: %w", baseURL, err)
	}
	return client, nil
}

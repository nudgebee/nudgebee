package adapter

import (
	"fmt"
	"path/filepath"
	"strings"

	"nudgebee/services/internal/database/models"
)

// safeFilePath joins dir and untrusted userPath, then verifies the result
// stays inside dir. Returns an error on path-traversal attempts.
func safeFilePath(dir, userPath string) (string, error) {
	base := filepath.Clean(dir)
	joined := filepath.Join(base, userPath)
	if joined != base && !strings.HasPrefix(joined, base+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal detected: %q escapes base directory", userPath)
	}
	return joined, nil
}

func GetAdapter(name string) AccountAdapter {
	switch name {
	case "kubernetes":
		return &kuberntesAdapter{}
	case "github":
		return &githubAdapter{}
	case "gitlab":
		return &gitlabAdapter{}
	case "aws", "azure", "gcp":
		return &awsAdapter{}
	}
	return nil
}

func GetAdapterFromResolutionProvider(name models.RecommendationResolutionType) AccountAdapter {
	switch name {
	case models.RecommendationResolutionTypeDeploymentChange:
		return &kuberntesAdapter{}
	case models.RecommendationResolutionTypePullRequest:
		return &githubAdapter{}
	case models.RecommendationResolutionTypeCloudResource:
		return &awsAdapter{}
	}
	return nil
}

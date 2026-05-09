package adapter

import "nudgebee/services/internal/database/models"

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

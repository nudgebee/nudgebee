//go:build live

// Live, network-touching variants of the adapter tests. These clone a real
// GitHub repository and require credentials, so they are excluded from the
// default `go test ./...` run and only execute under `-tags live` against a
// sandbox repo (see #31114, Class B drift suite). The self-contained,
// credential-free equivalents live in github_test.go.
package adapter

import (
	"fmt"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestUpdateCodeCommitLive is the original TestUpdateCodeCommit: it clones a
// real repo with a real token and commits locally. Kept for drift detection
// against the live GitHub flow; the credential-free version is
// TestUpdateCodeCommit in github_test.go.
func TestUpdateCodeCommitLive(t *testing.T) {
	m := testenv.RequireEnv(t, "GITHUB_TOKEN", "TEST_GITHUB_ORG", "TEST_GITHUB_REPO")
	details := gitDetailFromDeployment{
		Org:        m["TEST_GITHUB_ORG"],
		Repo:       m["TEST_GITHUB_REPO"],
		BaseBranch: "main",
		FilePath:   "deploy/kubernetes/app/values-dev.yaml",
		Annotations: map[string]string{
			"ci.nudgebee.com/helm.values.memoryRequestJsonPath": "resources.requests.memory",
		},
		Token: os.Getenv("GITHUB_TOKEN"),
	}
	recommendation := ApplyRecommendationRequest{
		Recommendation: models.Recommendation{
			Id:       "1",
			Category: "RightSizing",
			RuleName: "pod_right_sizing",
		},
		Data: map[string]any{
			"app": map[string]any{ // Container name
				"memory": map[string]any{
					"request": "200Mi",
				},
			},
		},
	}
	dir, err := checkoutCodeRepo(security.NewRequestContextForSuperAdmin(nil, nil, nil), recommendation, details)
	defer func() {
		if dir != "" {
			err := os.RemoveAll(dir)
			assert.Nil(t, err)
		}
	}()
	fmt.Println(dir)
	assert.Nil(t, err)
	assert.NotNil(t, dir)
	assert.NotEmptyf(t, dir, "")

	branchName, err := commitCode(security.NewRequestContextForSuperAdmin(nil, nil, nil), dir, recommendation, details, false)
	assert.Nil(t, err)
	assert.NotEmpty(t, branchName)
}

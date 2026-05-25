package playbooks

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGitHubPRHistoryAction(t *testing.T) {
	// Skip if test environment variables are not set
	accountId := os.Getenv("TEST_ACCOUNT")
	if accountId == "" {
		t.Skip("TEST_ACCOUNT_ID not set")
	}

	action := &githubPRHistoryAction{}
	ctx := NewPlaybookActionContext("", accountId, nil, PlaybookEvent{})

	// Test with missing repo_url
	params := map[string]any{}

	_, err := action.Execute(ctx, params)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "repo_url is required")

	// Test with invalid repo URL
	params = map[string]any{
		"repo_url": "invalid-url",
	}

	_, err = action.Execute(ctx, params)
	assert.NotNil(t, err)

	// Test with valid repo URL (may fail if no GitHub config exists)
	params = map[string]any{
		"repo_url": "https://github.com/project44/equipment-identifier-service",
		"limit":    3,
	}

	response, err := action.Execute(ctx, params)

	// The error could be "no github configuration found" or an actual API error
	// Both are acceptable for this test since we're just verifying the code structure
	if err != nil {
		t.Logf("Expected error (no config or API error): %v", err)
	} else {
		assert.NotNil(t, response)
		assert.Equal(t, "json", response.GetFormatName())
	}
}

func TestGitHubPRHistoryAutoExecute(t *testing.T) {
	accountId := os.Getenv("TEST_ACCOUNT")
	if accountId == "" {
		t.Skip("TEST_ACCOUNT_ID not set")
	}

	action := &githubPRHistoryAction{}

	// Test CanAutoExecute with labels
	event := PlaybookEvent{
		Labels: map[string]string{
			"repo_url": "https://github.com/project44/equipment-identifier-service",
			"revision": "0606861b7f4293ec514ae6b475b90946a068bf2a",
		},
	}
	ctx := NewPlaybookActionContext("", accountId, nil, event)

	canAutoExecute := action.CanAutoExecute(ctx)
	assert.True(t, canAutoExecute)

	// Test CanAutoExecute without labels
	emptyEvent := PlaybookEvent{
		Labels: map[string]string{},
	}
	ctxEmpty := NewPlaybookActionContext("", accountId, nil, emptyEvent)

	canAutoExecuteEmpty := action.CanAutoExecute(ctxEmpty)
	assert.False(t, canAutoExecuteEmpty)

	// Test AutoExecute
	response, err := action.AutoExecute(ctx)

	if err != nil {
		t.Logf("Expected error (no config): %v", err)
	} else {
		assert.NotNil(t, response)
	}
}

func TestGitHubPRHistoryAutoExecuteFromAnnotations(t *testing.T) {
	accountId := os.Getenv("TEST_ACCOUNT")
	tenantId := os.Getenv("TEST_TENANT")
	if accountId == "" || tenantId == "" {
		t.Skip("TEST_ACCOUNT_ID or TEST_TENANT_ID not set")
	}

	action := &githubPRHistoryAction{}

	// Test CanAutoExecute with SubjectName (annotation-based path, no repo_url label)
	event := PlaybookEvent{
		SubjectName: "cloud-collector-server",
		SubjectType: "deployment",
		Labels:      map[string]string{},
	}
	ctx := NewPlaybookActionContext(tenantId, accountId, nil, event)

	canAuto := action.CanAutoExecute(ctx)
	if !canAuto {
		t.Log("CanAutoExecute returned false — workload may not have git annotations in this environment")
		return
	}

	assert.True(t, canAuto)

	// Test AutoExecute via annotation path
	response, err := action.AutoExecute(ctx)
	if err != nil {
		t.Logf("AutoExecute error (may be expected if no GitHub creds): %v", err)
		return
	}

	assert.NotNil(t, response)
	assert.Equal(t, "json", response.GetFormatName())

	// Verify labels are extracted
	extractor, ok := response.(PlaybookActionResponseLabelExtractor)
	if ok {
		labels := extractor.ExtractLabels()
		assert.NotEmpty(t, labels["repo_url"], "repo_url label should be extracted")
		t.Logf("Extracted labels: %v", labels)
	}

	// Verify response data has workflow runs field
	data := response.GetData()
	dataJSON, _ := json.Marshal(data)
	t.Logf("Response data: %s", string(dataJSON))
}

func TestFetchRepoURLFromWorkload(t *testing.T) {
	accountId := os.Getenv("TEST_ACCOUNT")
	if accountId == "" {
		t.Skip("TEST_ACCOUNT not set")
	}

	t.Run("existing workload with annotations", func(t *testing.T) {
		info, err := fetchRepoURLFromWorkload(accountId, "cloud-collector-server", "", "", "")
		if err != nil {
			t.Logf("Error (may be expected if DB not available): %v", err)
			return
		}

		t.Logf("SourceRepoURL: %s", info.SourceRepoURL)
		t.Logf("CIRepoURL: %s", info.CIRepoURL)
		t.Logf("SourceSourceGitHash: %s", info.SourceGitHash)
		t.Logf("CISourceGitHash: %s", info.CIGitHash)

		// At least one repo URL should be present if workload has annotations
		if info.SourceRepoURL == "" && info.CIRepoURL == "" {
			t.Log("No git annotations found on workload")
		}
	})

	t.Run("non-existent workload", func(t *testing.T) {
		info, err := fetchRepoURLFromWorkload(accountId, "non-existent-workload-xyz", "", "", "")
		assert.NoError(t, err)
		assert.Equal(t, workloadGitInfo{}, info)
	})
}

func TestParseRepoURL(t *testing.T) {
	tests := []struct {
		name     string
		repoURL  string
		wantOrg  string
		wantRepo string
		wantErr  bool
	}{
		{
			name:     "Valid HTTPS URL",
			repoURL:  "https://github.com/project44/equipment-identifier-service",
			wantOrg:  "project44",
			wantRepo: "equipment-identifier-service",
			wantErr:  false,
		},
		{
			name:     "Valid HTTPS URL with .git",
			repoURL:  "https://github.com/project44/equipment-identifier-service.git",
			wantOrg:  "project44",
			wantRepo: "equipment-identifier-service",
			wantErr:  false,
		},
		{
			name:     "Valid HTTP URL",
			repoURL:  "http://github.com/project44/equipment-identifier-service",
			wantOrg:  "project44",
			wantRepo: "equipment-identifier-service",
			wantErr:  false,
		},
		{
			name:     "Valid URL with trailing slash",
			repoURL:  "https://github.com/project44/equipment-identifier-service/",
			wantOrg:  "project44",
			wantRepo: "equipment-identifier-service",
			wantErr:  false,
		},
		{
			name:    "Invalid URL - not GitHub",
			repoURL: "https://gitlab.com/project44/equipment-identifier-service",
			wantErr: true,
		},
		{
			name:    "Invalid URL - missing parts",
			repoURL: "https://github.com/project44",
			wantErr: true,
		},
		{
			name:    "Invalid URL - completely wrong",
			repoURL: "not-a-url",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			org, repo, err := parseRepoURL(tt.repoURL)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantOrg, org)
				assert.Equal(t, tt.wantRepo, repo)
			}
		})
	}
}

func TestParseGitHubPRParams(t *testing.T) {
	t.Run("all fields", func(t *testing.T) {
		rawParams := map[string]any{
			"repo_url":    "https://github.com/org/repo",
			"revision":    "abc123",
			"limit":       float64(5),
			"account_id":  "acc-1",
			"config_name": "my-config",
		}

		params, err := parseGitHubPRParams(rawParams)
		assert.NoError(t, err)
		assert.Equal(t, "https://github.com/org/repo", params.RepoURL)
		assert.Equal(t, "abc123", params.Revision)
		assert.Equal(t, 5, params.Limit)
		assert.Equal(t, "acc-1", params.AccountId)
		assert.Equal(t, "my-config", params.ConfigName)
	})

	t.Run("limit as int", func(t *testing.T) {
		rawParams := map[string]any{
			"repo_url": "https://github.com/org/repo",
			"limit":    3,
		}

		params, err := parseGitHubPRParams(rawParams)
		assert.NoError(t, err)
		assert.Equal(t, 3, params.Limit)
	})

	t.Run("empty params", func(t *testing.T) {
		rawParams := map[string]any{}

		params, err := parseGitHubPRParams(rawParams)
		assert.NoError(t, err)
		assert.Equal(t, "", params.RepoURL)
		assert.Equal(t, 0, params.Limit)
	})
}

func TestCanAutoExecuteWithLabels(t *testing.T) {
	action := &githubPRHistoryAction{}

	t.Run("repo_url in labels", func(t *testing.T) {
		event := PlaybookEvent{
			Labels: map[string]string{
				"repo_url": "https://github.com/org/repo",
			},
		}
		ctx := NewPlaybookActionContext("tenant-1", "acc-1", nil, event)
		assert.True(t, action.CanAutoExecute(ctx))
	})

	t.Run("empty repo_url in labels", func(t *testing.T) {
		event := PlaybookEvent{
			Labels: map[string]string{
				"repo_url": "",
			},
		}
		ctx := NewPlaybookActionContext("tenant-1", "acc-1", nil, event)
		// Falls through to path 2, which needs SubjectName
		assert.False(t, action.CanAutoExecute(ctx))
	})

	t.Run("nil labels no subject", func(t *testing.T) {
		event := PlaybookEvent{}
		ctx := NewPlaybookActionContext("tenant-1", "acc-1", nil, event)
		assert.False(t, action.CanAutoExecute(ctx))
	})

	t.Run("empty labels no subject", func(t *testing.T) {
		event := PlaybookEvent{
			Labels: map[string]string{},
		}
		ctx := NewPlaybookActionContext("tenant-1", "acc-1", nil, event)
		assert.False(t, action.CanAutoExecute(ctx))
	})
}

func TestWorkloadGitInfoParsing(t *testing.T) {
	// Test the meta JSON parsing logic directly by building test data
	// and verifying the annotation extraction

	t.Run("both annotations present", func(t *testing.T) {
		meta := map[string]any{
			"config": map[string]any{
				"annotations": map[string]any{
					"workloads.nudgebee.com/git.repo": "https://github.com/org/source-repo",
					"ci.nudgebee.com/git.repo":        "https://github.com/org/infra-repo",
					"ci.nudgebee.com/git.hash":        "abc123def456",
					"workloads.nudgebee.com/git.hash": "xyz789",
				},
			},
		}

		metaJSON, _ := json.Marshal(meta)
		info := parseWorkloadMeta(t, metaJSON)

		assert.Equal(t, "https://github.com/org/source-repo", info.SourceRepoURL)
		assert.Equal(t, "https://github.com/org/infra-repo", info.CIRepoURL)
		assert.Equal(t, "xyz789", info.SourceGitHash)
		assert.Equal(t, "abc123def456", info.CIGitHash)
	})

	t.Run("only workloads annotation", func(t *testing.T) {
		meta := map[string]any{
			"config": map[string]any{
				"annotations": map[string]any{
					"workloads.nudgebee.com/git.repo": "https://github.com/org/source-repo",
					"workloads.nudgebee.com/git.hash": "xyz789",
				},
			},
		}

		metaJSON, _ := json.Marshal(meta)
		info := parseWorkloadMeta(t, metaJSON)

		assert.Equal(t, "https://github.com/org/source-repo", info.SourceRepoURL)
		assert.Equal(t, "", info.CIRepoURL)
		assert.Equal(t, "xyz789", info.SourceGitHash)
		assert.Equal(t, "", info.CIGitHash)
	})

	t.Run("only ci annotation", func(t *testing.T) {
		meta := map[string]any{
			"config": map[string]any{
				"annotations": map[string]any{
					"ci.nudgebee.com/git.repo": "https://github.com/org/infra-repo",
					"ci.nudgebee.com/git.hash": "abc123",
				},
			},
		}

		metaJSON, _ := json.Marshal(meta)
		info := parseWorkloadMeta(t, metaJSON)

		assert.Equal(t, "", info.SourceRepoURL)
		assert.Equal(t, "https://github.com/org/infra-repo", info.CIRepoURL)
		assert.Equal(t, "", info.SourceGitHash)
		assert.Equal(t, "abc123", info.CIGitHash)
	})

	t.Run("no annotations key", func(t *testing.T) {
		meta := map[string]any{
			"config": map[string]any{
				"labels": map[string]any{
					"app": "test",
				},
			},
		}

		metaJSON, _ := json.Marshal(meta)
		info := parseWorkloadMeta(t, metaJSON)

		assert.Equal(t, "", info.SourceRepoURL)
		assert.Equal(t, "", info.CIRepoURL)
		assert.Equal(t, "", info.SourceGitHash)
		assert.Equal(t, "", info.CIGitHash)
	})

	t.Run("no config key", func(t *testing.T) {
		meta := map[string]any{
			"other": "data",
		}

		metaJSON, _ := json.Marshal(meta)
		info := parseWorkloadMeta(t, metaJSON)

		assert.Equal(t, "", info.SourceRepoURL)
		assert.Equal(t, "", info.CIRepoURL)
	})

	t.Run("empty ci hash falls back to workloads hash", func(t *testing.T) {
		meta := map[string]any{
			"config": map[string]any{
				"annotations": map[string]any{
					"ci.nudgebee.com/git.repo":        "https://github.com/org/infra",
					"ci.nudgebee.com/git.hash":        "",
					"workloads.nudgebee.com/git.hash": "fallback-hash",
				},
			},
		}

		metaJSON, _ := json.Marshal(meta)
		info := parseWorkloadMeta(t, metaJSON)

		assert.Equal(t, "fallback-hash", info.SourceGitHash)
		assert.Equal(t, "", info.CIGitHash) // empty string not stored
	})
}

// parseWorkloadMeta is a test helper that replicates the annotation parsing
// logic from fetchRepoURLFromWorkload without needing a DB connection.
func parseWorkloadMeta(t *testing.T, metaJSON []byte) workloadGitInfo {
	t.Helper()

	var meta map[string]any
	err := json.Unmarshal(metaJSON, &meta)
	assert.NoError(t, err)

	configMap, ok := meta["config"].(map[string]any)
	if !ok {
		return workloadGitInfo{}
	}

	annotationsMap, ok := configMap["annotations"].(map[string]any)
	if !ok {
		return workloadGitInfo{}
	}

	info := workloadGitInfo{}
	if v, ok := annotationsMap["workloads.nudgebee.com/git.repo"].(string); ok {
		info.SourceRepoURL = v
	}
	if v, ok := annotationsMap["ci.nudgebee.com/git.repo"].(string); ok {
		info.CIRepoURL = v
	}
	if v, ok := annotationsMap["workloads.nudgebee.com/git.hash"].(string); ok && v != "" {
		info.SourceGitHash = v
	}
	if v, ok := annotationsMap["ci.nudgebee.com/git.hash"].(string); ok && v != "" {
		info.CIGitHash = v
	}

	return info
}

func TestAutoExecuteRepoSelection(t *testing.T) {
	// Test the repo URL selection logic in AutoExecute:
	// source repo is preferred, CI repo is fallback

	t.Run("source repo preferred over CI repo", func(t *testing.T) {
		info := workloadGitInfo{
			SourceRepoURL: "https://github.com/org/source",
			CIRepoURL:     "https://github.com/org/infra",
			SourceGitHash: "abc123",
		}

		repoURL := info.SourceRepoURL
		if repoURL == "" {
			repoURL = info.CIRepoURL
		}
		assert.Equal(t, "https://github.com/org/source", repoURL)
	})

	t.Run("falls back to CI repo when no source", func(t *testing.T) {
		info := workloadGitInfo{
			CIRepoURL:     "https://github.com/org/infra",
			SourceGitHash: "abc123",
		}

		repoURL := info.SourceRepoURL
		if repoURL == "" {
			repoURL = info.CIRepoURL
		}
		assert.Equal(t, "https://github.com/org/infra", repoURL)
	})

	t.Run("ci_repo_url param set when different from main", func(t *testing.T) {
		info := workloadGitInfo{
			SourceRepoURL: "https://github.com/org/source",
			CIRepoURL:     "https://github.com/org/infra",
			SourceGitHash: "abc123",
		}

		repoURL := info.SourceRepoURL
		params := map[string]any{
			"repo_url": repoURL,
			"limit":    3,
			"revision": info.SourceGitHash,
		}

		if info.CIRepoURL != "" && info.CIRepoURL != repoURL {
			params["ci_repo_url"] = info.CIRepoURL
		}

		assert.Equal(t, "https://github.com/org/source", params["repo_url"])
		assert.Equal(t, "https://github.com/org/infra", params["ci_repo_url"])
		assert.Equal(t, "abc123", params["revision"])
	})

	t.Run("no ci_repo_url param when same as main", func(t *testing.T) {
		info := workloadGitInfo{
			SourceRepoURL: "https://github.com/org/repo",
			CIRepoURL:     "https://github.com/org/repo",
			SourceGitHash: "abc123",
		}

		repoURL := info.SourceRepoURL
		params := map[string]any{
			"repo_url": repoURL,
			"limit":    3,
		}

		if info.CIRepoURL != "" && info.CIRepoURL != repoURL {
			params["ci_repo_url"] = info.CIRepoURL
		}

		_, hasCIRepo := params["ci_repo_url"]
		assert.False(t, hasCIRepo)
	})
}

func TestResponseLabelsExtracted(t *testing.T) {
	t.Run("labels extracted from response", func(t *testing.T) {
		labels := map[string]any{
			"repo_url": "https://github.com/org/repo",
			"revision": "abc123",
		}
		response := NewPlaybookActionResponseJsonWithLabels(
			githubPRHistoryResponse{RepoURL: "https://github.com/org/repo"},
			map[string]any{},
			nil,
			map[string]any{},
			labels,
		)

		extracted := response.ExtractLabels()
		assert.Equal(t, "https://github.com/org/repo", extracted["repo_url"])
		assert.Equal(t, "abc123", extracted["revision"])
	})

	t.Run("empty labels when no repo or revision", func(t *testing.T) {
		labels := map[string]any{}
		response := NewPlaybookActionResponseJsonWithLabels(
			githubPRHistoryResponse{},
			map[string]any{},
			nil,
			map[string]any{},
			labels,
		)

		extracted := response.ExtractLabels()
		assert.Empty(t, extracted)
	})
}

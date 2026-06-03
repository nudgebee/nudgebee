package adapter

import (
	"context"
	"log/slog"
	"net/http"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestCheckoutCodeRepoGitLab(t *testing.T) {
	// Skip if GitLab token not configured
	gitlabToken := os.Getenv("GITLAB_TOKEN")
	if gitlabToken == "" {
		t.Skip("Skipping test - GITLAB_TOKEN not configured")
	}

	details := gitLabDetailFromDeployment{
		ProjectPath: "nudgebee/nudgebee-test", // Test project path
		BaseBranch:  "main",
		FilePath:    "deploy/kubernetes/app/values-dev.yaml",
		Annotations: map[string]string{
			"ci.nudgebee.com/helm.values.memoryRequestJsonPath": "resources.requests.memory",
		},
		Token:     gitlabToken,
		Username:  os.Getenv("GITLAB_USERNAME"),
		GitLabURL: "https://gitlab.com",
	}
	recommendation := ApplyRecommendationRequest{
		Recommendation: models.Recommendation{
			Id:       "test-gitlab-1",
			Category: "RightSizing",
			RuleName: "pod_right_sizing",
		},
		Data: map[string]any{
			"app": map[string]any{
				"memory": map[string]any{
					"request": "200Mi",
				},
			},
		},
	}

	// Create test context
	ctxt := &testAccountAdapterContext{
		ctx:             context.Background(),
		logger:          slog.Default(),
		securityContext: security.NewSecurityContextForTenantAdmin(os.Getenv("TEST_TENANT")),
	}

	dir, err := checkoutCodeRepoGitLab(ctxt, recommendation, details)
	defer func() {
		if dir != "" {
			err := os.RemoveAll(dir)
			assert.Nil(t, err)
		}
	}()

	if err != nil {
		t.Logf("Error checking out GitLab repo: %v", err)
		t.Skip("Skipping - GitLab repo checkout failed (check credentials and repo access)")
	}

	assert.NotNil(t, dir)
	assert.NotEmptyf(t, dir, "Directory should not be empty")
	t.Logf("Successfully checked out GitLab repo to: %s", dir)
}

func TestCommitCodeGitLab(t *testing.T) {
	// Skip if GitLab token not configured
	gitlabToken := os.Getenv("GITLAB_TOKEN")
	if gitlabToken == "" {
		t.Skip("Skipping test - GITLAB_TOKEN not configured")
	}

	details := gitLabDetailFromDeployment{
		ProjectPath: "nudgebee/nudgebee-test",
		BaseBranch:  "main",
		FilePath:    "deploy/kubernetes/app/values-dev.yaml",
		Annotations: map[string]string{
			"ci.nudgebee.com/helm.values.memoryRequestJsonPath": "resources.requests.memory",
		},
		Token:     gitlabToken,
		Username:  os.Getenv("GITLAB_USERNAME"),
		GitLabURL: "https://gitlab.com",
	}
	recommendation := ApplyRecommendationRequest{
		Recommendation: models.Recommendation{
			Id:       "test-gitlab-commit-1",
			Category: "RightSizing",
			RuleName: "pod_right_sizing",
		},
		Data: map[string]any{
			"app": map[string]any{
				"memory": map[string]any{
					"request": "200Mi",
				},
			},
		},
	}

	// Create test context
	ctxt := &testAccountAdapterContext{
		ctx:             context.Background(),
		logger:          slog.Default(),
		securityContext: security.NewSecurityContextForTenantAdmin(os.Getenv("TEST_TENANT")),
	}

	dir, err := checkoutCodeRepoGitLab(ctxt, recommendation, details)
	defer func() {
		if dir != "" {
			err := os.RemoveAll(dir)
			assert.Nil(t, err)
		}
	}()

	if err != nil {
		t.Skipf("Skipping - GitLab repo checkout failed: %v", err)
	}

	t.Logf("Checked out to: %s", dir)

	branchName, err := commitCodeGitLab(ctxt, dir, recommendation, details, false)
	if err != nil {
		t.Logf("Error committing (may be expected if no changes): %v", err)
	} else {
		assert.NotEmpty(t, branchName)
		t.Logf("Created branch: %s", branchName)
	}
}

func TestApplyRightsizingRecommendationUsingCodeAgentGitLab(t *testing.T) {
	// Check if LLM server is configured and accessible
	llmEndpoint := config.Config.LLMServerEndpoint
	if llmEndpoint == "" {
		t.Skip("Skipping test - LLM_SERVER_ENDPOINT not configured")
	}

	// Quick health check for LLM server
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(llmEndpoint + "/health")
	if err != nil || (resp != nil && resp.StatusCode != http.StatusOK) {
		t.Skipf("Skipping test - LLM server not accessible at %s: %v", llmEndpoint, err)
	}
	if resp != nil {
		err := resp.Body.Close()
		if err != nil {
			t.Logf("Warning: Failed to close response body: %v", err)
		}
	}

	// Get database connection
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping test - database not accessible: %v", err)
	}

	// Load real recommendation from database
	recommendationId := os.Getenv("TEST_GITLAB_RECOMMENDATION_ID")
	if recommendationId == "" {
		t.Skip("Skipping test - TEST_GITLAB_RECOMMENDATION_ID not configured")
	}

	var recommendation models.Recommendation
	err = dbms.Db.Get(&recommendation, "SELECT * FROM recommendation WHERE id = $1", recommendationId)
	if err != nil {
		t.Fatalf("Failed to load recommendation from database: %v", err)
	}

	t.Logf("Loaded recommendation: %s (Category: %s, Status: %s)", recommendation.Id, recommendation.Category, recommendation.Status)

	// Load associated resource (optional - can be empty)
	var resource models.Resource
	if recommendation.ResourceId != nil {
		_ = dbms.Db.Get(&resource, "SELECT * FROM cloud_resourses WHERE id = $1", *recommendation.ResourceId)
	}

	// Create git details for test GitLab repository
	gitDetails := gitLabDetailFromDeployment{
		ProjectPath: os.Getenv("TEST_GITLAB_PROJECT_PATH"), // e.g., "nudgebee/nudgebee"
		BaseBranch:  "main",
		FilePath:    os.Getenv("TEST_GITLAB_VALUES_FILE"), // e.g., "deploy/kubernetes/app/values-dev.yaml"
		Annotations: map[string]string{},
		GitLabURL:   os.Getenv("TEST_GITLAB_URL"), // e.g., "https://gitlab.com" or self-hosted
	}

	if gitDetails.ProjectPath == "" {
		gitDetails.ProjectPath = "nudgebee/nudgebee"
	}
	if gitDetails.FilePath == "" {
		gitDetails.FilePath = "deploy/kubernetes/notifications-server/values-dev.yaml"
	}
	if gitDetails.GitLabURL == "" {
		gitDetails.GitLabURL = "https://gitlab.com"
	}

	// Build the recommendation data map
	recommendationData := map[string]any{
		"notifications": []map[string]any{
			{
				"resource":    "cpu",
				"allocated":   map[string]any{"request": 0.5, "limit": 0.8},
				"recommended": map[string]any{"request": 0.01, "limit": nil},
			},
			{
				"resource":    "memory",
				"allocated":   map[string]any{"request": 524288000, "limit": 786432000},
				"recommended": map[string]any{"request": 188743680, "limit": 188743680},
			},
		},
	}

	// Create the apply recommendation request
	applyRequest := ApplyRecommendationRequest{
		Data:           recommendationData,
		Recommendation: recommendation,
		Resource:       resource,
		ProviderConfig: map[string]any{},
		ResolverType:   "recommendation",
	}

	// Create a new recommendation_resolution record for this test
	resolutionId := uuid.NewString()
	userId := os.Getenv("TEST_USER")
	if userId == "" {
		userId = "test-user-id"
	}
	_, err = dbms.Db.Exec(`
		INSERT INTO recommendation_resolution (id, recommendation_id, type, status, type_reference_id, resolver_type, resolver_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, resolutionId, recommendation.Id, "DeploymentChange", models.RecommendationResolutionStatusInProgress, "", "User", userId, time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Failed to create recommendation_resolution record: %v", err)
	}

	// Cleanup: delete the test resolution record after test
	defer func() {
		_, err := dbms.Db.Exec("DELETE FROM recommendation_resolution WHERE id = $1", resolutionId)
		if err != nil {
			t.Logf("Warning: Failed to cleanup test resolution record: %v", err)
		}
	}()

	// Create request context
	ctxt := &testAccountAdapterContext{
		ctx:             context.Background(),
		logger:          slog.Default(),
		securityContext: security.NewSecurityContextForTenantAdmin(os.Getenv("TEST_TENANT")),
	}

	// Call the function
	t.Logf("Calling ApplyRightsizingRecommendationUsingCodeAgentGitLab...")
	err = ApplyRightsizingRecommendationUsingCodeAgentGitLab(ctxt, applyRequest, gitDetails, resolutionId)
	if err != nil {
		t.Fatalf("Function returned error: %v", err)
	}

	// Since the function runs asynchronously, poll the database for status updates
	t.Logf("Polling database for status updates (max 3 minutes)...")
	maxWaitTime := 3 * time.Minute
	pollInterval := 5 * time.Second
	startTime := time.Now()

	var finalStatus string
	var statusMessage string
	var mrUrl string

	for time.Since(startTime) < maxWaitTime {
		time.Sleep(pollInterval)

		var resolution struct {
			Status          string  `db:"status"`
			StatusMessage   *string `db:"status_message"`
			TypeReferenceId *string `db:"type_reference_id"`
		}

		err = dbms.Db.Get(&resolution, "SELECT status, status_message, type_reference_id FROM recommendation_resolution WHERE id = $1", resolutionId)
		if err != nil {
			t.Logf("Error querying resolution status: %v", err)
			continue
		}

		finalStatus = resolution.Status
		if resolution.StatusMessage != nil {
			statusMessage = *resolution.StatusMessage
		}
		if resolution.TypeReferenceId != nil {
			mrUrl = *resolution.TypeReferenceId
		}

		t.Logf("Status: %s, Message: %s, MR URL: %s", finalStatus, statusMessage, mrUrl)

		// Check if we've reached a terminal state
		if finalStatus == string(models.RecommendationResolutionStatusSuccess) ||
			finalStatus == string(models.RecommendationResolutionStatusFailed) ||
			(finalStatus == string(models.RecommendationResolutionStatusInProgress) && mrUrl != "") {
			break
		}
	}

	// Assertions
	assert.NotEmpty(t, finalStatus, "Status should be updated")
	assert.NotEqual(t, string(models.RecommendationResolutionStatusInProgress), finalStatus,
		"Status should have moved from InProgress to a terminal state")

	if finalStatus == string(models.RecommendationResolutionStatusSuccess) || mrUrl != "" {
		assert.NotEmpty(t, mrUrl, "MR URL should be set on success")
		t.Logf("Test passed! MR created at: %s", mrUrl)
	} else {
		t.Logf("Test completed with status: %s, message: %s", finalStatus, statusMessage)
	}
}

func TestGetResolutionStatusGitLab(t *testing.T) {
	// Get database connection
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping test - database not accessible: %v", err)
	}

	// Find a resolution with GitLab MR URL
	var resolution models.RecommendationResolution

	query := `
		SELECT *
		FROM recommendation_resolution
		WHERE type_reference_id IS NOT NULL
		AND type_reference_id LIKE '%gitlab%'
		AND type_reference_id LIKE '%merge_requests%'
		LIMIT 1
	`

	err = dbms.Db.Get(&resolution, query)
	if err != nil {
		t.Skipf("Skipping test - no GitLab resolutions found in database: %v", err)
	}

	t.Logf("Loaded resolution: %s (Status: %s)", resolution.Id, resolution.Status)
	if resolution.TypeReferenceId != "" {
		t.Logf("MR URL: %s", resolution.TypeReferenceId)
	}
	if resolution.StatusMessage != nil {
		t.Logf("Status Message: %s", *resolution.StatusMessage)
	}

	// Load the associated recommendation
	var recommendation models.Recommendation
	err = dbms.Db.Get(&recommendation, "SELECT * FROM recommendation WHERE id = $1", resolution.RecommendationId)
	if err != nil {
		t.Fatalf("Failed to load recommendation from database: %v", err)
	}

	t.Logf("Loaded recommendation: %s (Category: %s, Status: %s)",
		recommendation.Id, recommendation.Category, recommendation.Status)

	// Create request context
	ctxt := &testAccountAdapterContext{
		ctx:             context.Background(),
		logger:          slog.Default(),
		securityContext: security.NewSecurityContextForTenantAdmin(os.Getenv("TEST_TENANT")),
	}

	// Create adapter and call GetRecommendationResolutionStatus
	adapter := gitlabAdapter{}

	mrUrl := resolution.TypeReferenceId

	statusMessage := ""
	if resolution.StatusMessage != nil {
		statusMessage = *resolution.StatusMessage
	}

	// Call the function
	t.Logf("Calling GetRecommendationResolutionStatus for MR: %s", mrUrl)
	response, err := adapter.GetRecommendationResolutionStatus(
		ctxt,
		recommendation,
		mrUrl,
		resolution.Data,
		statusMessage,
	)

	// Assertions
	if err != nil {
		t.Logf("Error returned: %v", err)
	} else {
		t.Logf("Status check succeeded!")
		t.Logf("  Status: %s", response.Status)
		t.Logf("  Message: %s", response.StatusMessage)

		// Validate the response
		assert.NotEmpty(t, response.Status, "Status should not be empty")
		assert.NotEmpty(t, response.StatusMessage, "Status message should not be empty")

		// If we get a success response, the status should be one of the valid statuses
		validStatuses := []RecommendationResolutionStatus{
			RecommendationResolutionStatusInProgress,
			RecommendationResolutionStatusSuccess,
			RecommendationResolutionStatusFailed,
		}
		assert.Contains(t, validStatuses, response.Status, "Status should be a valid resolution status")
	}
}

func TestParseGitLabMRURL(t *testing.T) {
	testCases := []struct {
		name          string
		mrURL         string
		gitLabURL     string
		expectedPath  string
		expectedMRIID string
		expectedError bool
	}{
		{
			name:          "Standard gitlab.com URL",
			mrURL:         "https://gitlab.com/acme/widget/-/merge_requests/123",
			gitLabURL:     "https://gitlab.com",
			expectedPath:  "acme/widget",
			expectedMRIID: "123",
			expectedError: false,
		},
		{
			name:          "Self-hosted GitLab URL",
			mrURL:         "https://gitlab.example.com/group/subgroup/project/-/merge_requests/456",
			gitLabURL:     "https://gitlab.example.com",
			expectedPath:  "group/subgroup/project",
			expectedMRIID: "456",
			expectedError: false,
		},
		{
			name:          "URL with trailing slash",
			mrURL:         "https://gitlab.com/acme/widget/-/merge_requests/789/",
			gitLabURL:     "https://gitlab.com",
			expectedPath:  "acme/widget",
			expectedMRIID: "789",
			expectedError: false,
		},
		{
			name:          "Invalid URL - no merge_requests",
			mrURL:         "https://gitlab.com/acme/widget/-/issues/123",
			gitLabURL:     "https://gitlab.com",
			expectedPath:  "",
			expectedMRIID: "",
			expectedError: true,
		},
		{
			name:          "Deep nested project path",
			mrURL:         "https://gitlab.com/org/team/subteam/project/-/merge_requests/42",
			gitLabURL:     "https://gitlab.com",
			expectedPath:  "org/team/subteam/project",
			expectedMRIID: "42",
			expectedError: false,
		},
		{
			name:          "URL with query string on IID",
			mrURL:         "https://gitlab.com/acme/widget/-/merge_requests/123?expanded=1",
			gitLabURL:     "https://gitlab.com",
			expectedPath:  "acme/widget",
			expectedMRIID: "123",
			expectedError: false,
		},
		{
			name:          "URL with note anchor on IID",
			mrURL:         "https://gitlab.com/acme/widget/-/merge_requests/456#note_789",
			gitLabURL:     "https://gitlab.com",
			expectedPath:  "acme/widget",
			expectedMRIID: "456",
			expectedError: false,
		},
		{
			name:          "Alt URL format (no /-/) with query string",
			mrURL:         "https://gitlab.com/acme/widget/merge_requests/321?expanded=1",
			gitLabURL:     "https://gitlab.com",
			expectedPath:  "acme/widget",
			expectedMRIID: "321",
			expectedError: false,
		},
		{
			name:          "Alt URL format (no /-/) with note anchor",
			mrURL:         "https://gitlab.com/acme/widget/merge_requests/654#note_111",
			gitLabURL:     "https://gitlab.com",
			expectedPath:  "acme/widget",
			expectedMRIID: "654",
			expectedError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			projectPath, mrIID, err := parseGitLabMRURL(tc.mrURL, tc.gitLabURL)

			if tc.expectedError {
				assert.Error(t, err, "Expected error for invalid URL")
			} else {
				assert.NoError(t, err, "Should not return error for valid URL")
				assert.Equal(t, tc.expectedPath, projectPath, "Project path should match")
				assert.Equal(t, tc.expectedMRIID, mrIID, "MR IID should match")
			}
		})
	}
}

func TestExtractGitLabProjectPath(t *testing.T) {
	testCases := []struct {
		name          string
		repoURL       string
		gitLabURL     string
		expectedPath  string
		expectedError bool
	}{
		{
			name:          "Standard gitlab.com HTTPS URL",
			repoURL:       "https://gitlab.com/nudgebee/nudgebee",
			gitLabURL:     "https://gitlab.com",
			expectedPath:  "nudgebee/nudgebee",
			expectedError: false,
		},
		{
			name:          "URL with .git suffix",
			repoURL:       "https://gitlab.com/nudgebee/nudgebee.git",
			gitLabURL:     "https://gitlab.com",
			expectedPath:  "nudgebee/nudgebee",
			expectedError: false,
		},
		{
			name:          "Self-hosted GitLab",
			repoURL:       "https://gitlab.example.com/group/project",
			gitLabURL:     "https://gitlab.example.com",
			expectedPath:  "group/project",
			expectedError: false,
		},
		{
			name:          "Nested group path",
			repoURL:       "https://gitlab.com/org/team/project",
			gitLabURL:     "https://gitlab.com",
			expectedPath:  "org/team/project",
			expectedError: false,
		},
		{
			name:          "URL without protocol",
			repoURL:       "gitlab.com/nudgebee/nudgebee",
			gitLabURL:     "https://gitlab.com",
			expectedPath:  "nudgebee/nudgebee",
			expectedError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			projectPath, err := extractGitLabProjectPath(tc.repoURL, tc.gitLabURL)

			if tc.expectedError {
				assert.Error(t, err, "Expected error")
			} else {
				assert.NoError(t, err, "Should not return error")
				assert.Equal(t, tc.expectedPath, projectPath, "Project path should match")
			}
		})
	}
}

func TestGetResolutionStatusWithGitLabToken(t *testing.T) {
	// Skip if GitLab token not configured
	gitlabToken := os.Getenv("GITLAB_TOKEN")
	if gitlabToken == "" {
		t.Skip("Skipping test - GITLAB_TOKEN not configured")
	}

	// Get database connection
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping test - database not accessible: %v", err)
	}

	// Find a resolution with GitLab MR URL
	var resolution models.RecommendationResolution

	query := `
		SELECT *
		FROM recommendation_resolution
		WHERE type_reference_id IS NOT NULL
		AND type_reference_id LIKE '%gitlab%'
		AND type_reference_id LIKE '%merge_requests%'
		LIMIT 1
	`

	err = dbms.Db.Get(&resolution, query)
	if err != nil {
		t.Skipf("Skipping test - no GitLab resolutions found in database: %v", err)
	}

	t.Logf("Testing GitLab token authentication with resolution: %s", resolution.Id)
	if resolution.TypeReferenceId != "" {
		t.Logf("MR URL: %s", resolution.TypeReferenceId)
	}

	// Load the associated recommendation
	var recommendation models.Recommendation
	err = dbms.Db.Get(&recommendation, "SELECT * FROM recommendation WHERE id = $1", resolution.RecommendationId)
	if err != nil {
		t.Fatalf("Failed to load recommendation from database: %v", err)
	}

	// Create request context
	ctxt := &testAccountAdapterContext{
		ctx:             context.Background(),
		logger:          slog.Default(),
		securityContext: security.NewSecurityContextForTenantAdmin(recommendation.TenantId),
	}

	// Create adapter and call GetRecommendationResolutionStatus
	adapter := gitlabAdapter{}

	mrUrl := resolution.TypeReferenceId

	statusMessage := ""
	if resolution.StatusMessage != nil {
		statusMessage = *resolution.StatusMessage
	}

	// Call the function - this should use GitLab token authentication
	t.Logf("Calling GetRecommendationResolutionStatus with GitLab token auth...")
	response, err := adapter.GetRecommendationResolutionStatus(
		ctxt,
		recommendation,
		mrUrl,
		resolution.Data,
		statusMessage,
	)

	// Assertions
	assert.NoError(t, err, "Should not error with proper GitLab token")
	assert.NotEmpty(t, response.Status, "Status should not be empty")

	// Log the result
	t.Logf("GitLab token authentication test passed!")
	t.Logf("  Status: %s", response.Status)
	t.Logf("  Message: %s", response.StatusMessage)

	// The status should be one of the valid statuses (not "Failed" from bad credentials)
	if response.Status == RecommendationResolutionStatusFailed {
		// If failed, it should NOT be due to authentication issues
		assert.NotContains(t, response.StatusMessage, "401",
			"Should not fail with 401 when GitLab token is properly configured")
		assert.NotContains(t, response.StatusMessage, "Unauthorized",
			"Should not fail with Unauthorized when GitLab token is properly configured")
	}
}

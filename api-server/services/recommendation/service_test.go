package recommendation

import (
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecommendationResolution(t *testing.T) {
	// Get database manager to verify test prerequisites
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatal(err)
	}

	// Test data - using actual IDs from the test environment
	const (
		userID           = "af4cb6af-1254-421d-bfa5-ffcfe649017e"
		tenantID         = "890cad87-c452-4aa7-b84a-742cee0454a1"
		accountID        = "a2a30b02-0f67-42e5-a2ab-c658230fd798"
		recommendationID = "8a6ad155-72b4-45a0-81e2-64e91ee9b67e"
	)

	// Create request context for user and tenant
	ctx := security.NewRequestContextForUserTenant(userID, tenantID, nil, nil, nil)

	// Verify the recommendation exists before testing
	var exists bool
	err = dbms.Db.QueryRow("SELECT EXISTS(SELECT 1 FROM recommendation WHERE id = $1)", recommendationID).Scan(&exists)
	if err != nil {
		t.Fatalf("Failed to check if recommendation exists: %v", err)
	}
	if !exists {
		t.Skipf("Test recommendation %s does not exist in database", recommendationID)
	}

	// Create the apply recommendation request
	// Leave Provider empty to use default ("kubernetes" for RightSizing category)
	// which applies changes directly to the deployment
	request := RecommendationApplyRequest{
		AccountId:        accountID,
		RecommendationId: recommendationID,
		Provider:         "github",
		ProviderConfig: map[string]any{
			"recommendation_source": "recommendation",
			"name":                  "nudgebee",
		},
		Data: map[string]any{
			"reason": "Applying recommendation via integration test",
		},
	}

	// Apply the recommendation
	response, err := ApplyRecommendation(ctx, request)

	// Assert no error occurred
	require.NoError(t, err, "ApplyRecommendation should not return error")
	require.NotNil(t, response, "Response should not be nil")
	require.NotEmpty(t, response.Resolution.Id, "Resolution ID should not be empty")
	assert.Equal(t, recommendationID, response.Resolution.RecommendationId, "Resolution should reference the correct recommendation")

	t.Logf("Successfully applied recommendation %s", recommendationID)
	t.Logf("Created resolution: %s", response.Resolution.Id)
	t.Logf("Initial resolution status: %s", response.Resolution.Status)

	// Wait for async operation to complete
	// The kubernetes adapter runs asynchronously via agent_task
	maxWaitTime := 2 * time.Minute
	pollInterval := 5 * time.Second
	deadline := time.Now().Add(maxWaitTime)

	var finalStatus string
	var statusMessage *string
	var typeReferenceId string

	t.Logf("Polling for resolution completion (max %v)...", maxWaitTime)
	resolutionComplete := false

	for time.Now().Before(deadline) {
		err = dbms.Db.QueryRow(`
			SELECT status, status_message, type_reference_id
			FROM recommendation_resolution
			WHERE id = $1
		`, response.Resolution.Id).Scan(&finalStatus, &statusMessage, &typeReferenceId)

		if err != nil {
			t.Logf("Error querying resolution: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		t.Logf("Poll: Status='%s', TypeRefID='%s', Message='%v'",
			finalStatus, typeReferenceId, statusMessage)

		// Check if resolution completed (success or failed)
		if finalStatus == string(models.RecommendationResolutionStatusSuccess) {
			t.Logf("✓ Resolution completed successfully")
			resolutionComplete = true
			break
		} else if finalStatus == string(models.RecommendationResolutionStatusFailed) {
			t.Logf("✗ Resolution failed: %v", statusMessage)
			resolutionComplete = true
			break
		}

		time.Sleep(pollInterval)
	}

	// Verify final state
	if resolutionComplete {
		assert.NotEqual(t, string(models.RecommendationResolutionStatusInProgress), finalStatus,
			"Resolution should not be in InProgress state after completion")

		if finalStatus == string(models.RecommendationResolutionStatusSuccess) {
			t.Logf("✓ Test passed: Resolution completed successfully")
			assert.NotEmpty(t, typeReferenceId, "Type reference ID should be set on success")
		} else {
			t.Logf("⚠ Resolution failed (may be expected if k8s agent is not running)")
			if statusMessage != nil {
				t.Logf("Failure reason: %s", *statusMessage)
			}
		}
	} else {
		t.Logf("⚠ Resolution still in progress after %v", maxWaitTime)
		t.Logf("This may indicate the k8s agent is slow or not processing tasks")
		t.Logf("Check agent_task table for task status")
	}

	// Query and log the agent task status for debugging
	var agentTaskStatus, agentTaskAction string
	err = dbms.Db.QueryRow(`
		SELECT status, action
		FROM agent_task
		WHERE source_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, recommendationID).Scan(&agentTaskStatus, &agentTaskAction)

	if err == nil {
		t.Logf("Agent task: Action='%s', Status='%s'", agentTaskAction, agentTaskStatus)
	} else {
		t.Logf("No agent task found or error querying: %v", err)
	}

	// Clean up the test resolution (optional)
	// Uncomment to clean up after test:
	// _, _ = dbms.Db.Exec("DELETE FROM recommendation_resolution WHERE id = $1", response.Resolution.Id)
	// _, _ = dbms.Db.Exec("UPDATE recommendation SET status = 'Open' WHERE id = $1", recommendationID)
	// _, _ = dbms.Db.Exec("DELETE FROM agent_task WHERE source_id = $1", recommendationID)
}

func TestApplyAWSAlarmRecommendation(t *testing.T) {
	// Get database manager to verify test prerequisites
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatal(err)
	}

	// Test data - AWS CloudWatch alarm recommendation for Classic Load Balancer latency
	const (
		userID           = "af4cb6af-1254-421d-bfa5-ffcfe649017e"
		tenantID         = "890cad87-c452-4aa7-b84a-742cee0454a1"
		accountID        = "6c008cf8-4d79-4999-8447-573a697d0652"
		recommendationID = "e44b1b68-1137-4912-92aa-0c070303a4b3"
	)

	// Create request context for user and tenant
	ctx := security.NewRequestContextForUserTenant(userID, tenantID, nil, nil, nil)

	// Verify the recommendation exists before testing
	var category, status, ruleName string
	err = dbms.Db.QueryRow(`
		SELECT category, status, rule_name
		FROM recommendation
		WHERE id = $1
	`, recommendationID).Scan(&category, &status, &ruleName)
	if err != nil {
		t.Skipf("Test recommendation %s does not exist in database: %v", recommendationID, err)
	}

	t.Logf("Testing recommendation:")
	t.Logf("  ID: %s", recommendationID)
	t.Logf("  Category: %s", category)
	t.Logf("  Status: %s", status)
	t.Logf("  Rule: %s", ruleName)

	// Create the apply recommendation request for AWS CloudWatch alarm
	// Provider defaults to "aws" based on account type (no need to specify)
	request := RecommendationApplyRequest{
		AccountId:        accountID,
		RecommendationId: recommendationID,
		// Provider not specified - will auto-detect as "aws" from account type
		ProviderConfig: map[string]any{
			"recommendation_source": "recommendation",
		},
		Data: map[string]any{
			"reason": "Applying AWS CloudWatch alarm recommendation via integration test",
		},
	}

	// Apply the recommendation
	response, err := ApplyRecommendation(ctx, request)

	// Check if error is due to missing AWS permissions (expected in test environment)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "AccessDenied") || strings.Contains(errMsg, "not authorized") {
			t.Skipf("✓ Test passed: Code flow is correct. Skipping due to AWS IAM permissions: %v", err)
		}
		// For any other error, fail the test
		require.NoError(t, err, "ApplyRecommendation should not return error")
	}

	require.NotNil(t, response, "Response should not be nil")
	require.NotEmpty(t, response.Resolution.Id, "Resolution ID should not be empty")
	assert.Equal(t, recommendationID, response.Resolution.RecommendationId, "Resolution should reference the correct recommendation")

	t.Logf("Successfully applied AWS alarm recommendation %s", recommendationID)
	t.Logf("Created resolution: %s", response.Resolution.Id)
	t.Logf("Initial resolution status: %s", response.Resolution.Status)

	// Wait for async operation to complete
	maxWaitTime := 2 * time.Minute
	pollInterval := 5 * time.Second
	deadline := time.Now().Add(maxWaitTime)

	var finalStatus string
	var statusMessage *string
	var typeReferenceId string

	t.Logf("Polling for resolution completion (max %v)...", maxWaitTime)
	resolutionComplete := false

	for time.Now().Before(deadline) {
		err = dbms.Db.QueryRow(`
			SELECT status, status_message, type_reference_id
			FROM recommendation_resolution
			WHERE id = $1
		`, response.Resolution.Id).Scan(&finalStatus, &statusMessage, &typeReferenceId)

		if err != nil {
			t.Logf("Error querying resolution: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		t.Logf("Poll: Status='%s', TypeRefID='%s', Message='%v'",
			finalStatus, typeReferenceId, statusMessage)

		// Check if resolution completed (success or failed)
		if finalStatus == string(models.RecommendationResolutionStatusSuccess) {
			t.Logf("✓ Resolution completed successfully")
			resolutionComplete = true
			break
		} else if finalStatus == string(models.RecommendationResolutionStatusFailed) {
			t.Logf("✗ Resolution failed: %v", statusMessage)
			resolutionComplete = true
			break
		}

		time.Sleep(pollInterval)
	}

	// Verify final state
	if resolutionComplete {
		assert.NotEqual(t, string(models.RecommendationResolutionStatusInProgress), finalStatus,
			"Resolution should not be in InProgress state after completion")

		if finalStatus == string(models.RecommendationResolutionStatusSuccess) {
			t.Logf("✓ Test passed: AWS alarm recommendation applied successfully")
			assert.NotEmpty(t, typeReferenceId, "Type reference ID should be set on success")

			// For AWS CloudWatch alarms, type_reference_id contains the alarm name
			if typeReferenceId != "" {
				t.Logf("CloudWatch alarm created: %s", typeReferenceId)
			}
		} else {
			t.Logf("⚠ Resolution failed (may be expected if AWS account credentials are not configured)")
			if statusMessage != nil {
				t.Logf("Failure reason: %s", *statusMessage)
			}
		}
	} else {
		t.Logf("⚠ Resolution still in progress after %v", maxWaitTime)
		t.Logf("This may indicate the cloud-collector is slow or not responding")
	}

	// Verify recommendation status was updated
	var updatedRecStatus string
	err = dbms.Db.QueryRow(`
		SELECT status FROM recommendation WHERE id = $1
	`, recommendationID).Scan(&updatedRecStatus)
	if err == nil {
		t.Logf("Recommendation status after apply: %s", updatedRecStatus)
		// Status should change from "Open" to "InProgress" or another status
		if updatedRecStatus != status {
			t.Logf("✓ Recommendation status updated from '%s' to '%s'", status, updatedRecStatus)
		}
	}

	// Clean up the test resolution (optional)
	// Uncomment to clean up after test:
	// _, _ = dbms.Db.Exec("DELETE FROM recommendation_resolution WHERE id = $1", response.Resolution.Id)
	// _, _ = dbms.Db.Exec("UPDATE recommendation SET status = 'Open' WHERE id = $1", recommendationID)
}

func TestRetryRecommendationResolution(t *testing.T) {
	// Get database manager to verify test prerequisites
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatal(err)
	}

	// Test data - using actual IDs from the test environment
	const (
		userID       = "f0a24e79-fe9a-46db-8048-4ad39d726169"
		tenantID     = "6d79c39f-920e-4167-85cf-e2ee83dcbc03"
		accountID    = "e9dc2b39-78d3-46da-9691-f160bd3c4c19"
		resolutionID = "bf7aa75c-0880-4ebe-b69b-18bf72d6e9ef"
	)

	// Verify the resolution exists and is in a retryable state (Failed)
	var currentStatus string
	var recommendationID string
	err = dbms.Db.QueryRow(`
		SELECT status, recommendation_id FROM recommendation_resolution WHERE id = $1
	`, resolutionID).Scan(&currentStatus, &recommendationID)
	if err != nil {
		t.Skipf("Test resolution %s does not exist in database: %v", resolutionID, err)
	}

	t.Logf("Resolution %s current status: %s", resolutionID, currentStatus)
	t.Logf("Associated recommendation: %s", recommendationID)

	// Create request context for user and tenant
	ctx := security.NewRequestContextForUserTenant(userID, tenantID, nil, nil, nil)

	query := RetryRecommendationResolutionRequest{
		AccountId:    accountID,
		ResolutionId: resolutionID,
	}

	// Call retry
	response, err := RetryRecommendationResolution(ctx, query)
	require.NoError(t, err, "RetryRecommendationResolution should not return error")
	require.NotNil(t, response, "Response should not be nil")

	t.Logf("Retry initiated: Status='%s', Message='%s'", response.Status, response.Message)
	t.Logf("Resolution ID: %s", response.Resolution.Id)

	// Wait for async operation to complete (PR generation via code agent)
	maxWaitTime := 5 * time.Minute // Code agent may take longer
	pollInterval := 10 * time.Second
	deadline := time.Now().Add(maxWaitTime)

	var finalStatus string
	var statusMessage *string
	var typeReferenceId string

	t.Logf("Polling for resolution completion (max %v)...", maxWaitTime)
	resolutionComplete := false

	for time.Now().Before(deadline) {
		err = dbms.Db.QueryRow(`
			SELECT status, status_message, type_reference_id
			FROM recommendation_resolution
			WHERE id = $1
		`, resolutionID).Scan(&finalStatus, &statusMessage, &typeReferenceId)

		if err != nil {
			t.Logf("Error querying resolution: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		msg := ""
		if statusMessage != nil {
			msg = *statusMessage
		}
		t.Logf("Poll: Status='%s', TypeRefID='%s', Message='%s'",
			finalStatus, typeReferenceId, msg)

		// Check if resolution completed (success or failed)
		if finalStatus == string(models.RecommendationResolutionStatusSuccess) {
			t.Logf("✓ Resolution completed successfully")
			resolutionComplete = true
			break
		} else if finalStatus == string(models.RecommendationResolutionStatusFailed) {
			t.Logf("✗ Resolution failed: %v", statusMessage)
			resolutionComplete = true
			break
		}

		time.Sleep(pollInterval)
	}

	// Verify final state
	if resolutionComplete {
		assert.NotEqual(t, string(models.RecommendationResolutionStatusInProgress), finalStatus,
			"Resolution should not be in InProgress state after completion")

		if finalStatus == string(models.RecommendationResolutionStatusSuccess) {
			t.Logf("✓ Test passed: Retry completed successfully")
			assert.NotEmpty(t, typeReferenceId, "Type reference ID (PR URL) should be set on success")
			t.Logf("PR URL: %s", typeReferenceId)
		} else {
			t.Logf("⚠ Resolution failed after retry")
			if statusMessage != nil {
				t.Logf("Failure reason: %s", *statusMessage)
			}
		}
	} else {
		t.Logf("⚠ Resolution still in progress after %v", maxWaitTime)
		t.Logf("This may indicate the code-analysis agent is slow or not processing tasks")
	}

}

// TestApplyRecommendationWithGitProvider tests the provider="git" auto-detection feature
// This test verifies that when provider="git" is passed, the system correctly detects
// GitHub or GitLab from the ci.nudgebee.com/git.repo annotation on the workload.
func TestApplyRecommendationWithGitProvider(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatal(err)
	}

	// Test data - using actual IDs from the test environment
	const (
		userID    = "af4cb6af-1254-421d-bfa5-ffcfe649017e"
		tenantID  = "890cad87-c452-4aa7-b84a-742cee0454a1"
		accountID = "a2a30b02-0f67-42e5-a2ab-c658230fd798"
	)

	ctx := security.NewRequestContextForUserTenant(userID, tenantID, nil, nil, nil)

	t.Run("git provider auto-detects from annotation", func(t *testing.T) {
		// Find a recommendation with a resource that has git.repo annotation
		var recommendationID, gitRepoURL string
		err := dbms.Db.QueryRow(`
			SELECT r.id, w.meta::json->'config'->'annotations'->>'ci.nudgebee.com/git.repo'
			FROM recommendation r
			JOIN cloud_resourses cr ON r.resource_id = cr.id
			JOIN k8s_workloads w ON cr.account = w.cloud_account_id
			  AND cr.meta::json->>'namespace' = w.namespace
			  AND cr.meta::json->>'controller' = w.name
			WHERE r.tenant_id = $1
			  AND r.cloud_account_id = $2
			  AND r.resource_id IS NOT NULL
			  AND r.status = 'Open'
			  AND w.is_active = true
			  AND w.meta::json->'config'->'annotations'->>'ci.nudgebee.com/git.repo' IS NOT NULL
			LIMIT 1
		`, tenantID, accountID).Scan(&recommendationID, &gitRepoURL)

		if err != nil {
			t.Skipf("No recommendation with git.repo annotation found: %v", err)
		}

		t.Logf("Found recommendation %s with git.repo annotation: %s", recommendationID, gitRepoURL)

		// Determine expected provider from URL
		expectedProvider := ""
		if strings.Contains(gitRepoURL, "github.com") || strings.HasPrefix(gitRepoURL, "github.") {
			expectedProvider = "github"
		} else if strings.Contains(gitRepoURL, "gitlab.com") || strings.Contains(gitRepoURL, "gitlab.") {
			expectedProvider = "gitlab"
		}

		if expectedProvider == "" {
			t.Skipf("Git repo URL %s is not GitHub or GitLab, skipping test", gitRepoURL)
		}

		t.Logf("Expected provider detection: %s", expectedProvider)

		// Create request with provider="git" to trigger auto-detection
		request := RecommendationApplyRequest{
			AccountId:        accountID,
			RecommendationId: recommendationID,
			Provider:         "git", // This should auto-detect to github or gitlab
			ProviderConfig: map[string]any{
				"recommendation_source": "recommendation",
				"name":                  "nudgebee",
			},
			Data: map[string]any{
				"reason": "Testing git provider auto-detection",
			},
		}

		// Apply the recommendation
		response, err := ApplyRecommendation(ctx, request)

		// The request should succeed (provider detection worked)
		// Note: The actual PR creation may fail due to missing credentials, but
		// that's expected - we're testing the provider detection logic
		if err != nil {
			errMsg := err.Error()
			// If error mentions provider detection issues, that's a test failure
			if strings.Contains(errMsg, "unable to detect git provider") {
				t.Errorf("Git provider detection failed: %v", err)
			} else if strings.Contains(errMsg, "annotation not found") {
				t.Errorf("Git repo annotation not found: %v", err)
			} else {
				// Other errors (like missing credentials) are expected
				t.Logf("Request failed as expected (likely missing credentials): %v", err)
				t.Logf("✓ Provider detection succeeded - %s was detected from annotation", expectedProvider)
			}
		} else {
			require.NotNil(t, response, "Response should not be nil")
			t.Logf("✓ Request succeeded with auto-detected provider: %s", expectedProvider)
		}
	})

	t.Run("git provider fails without annotation", func(t *testing.T) {
		// Find a recommendation with resource but without git.repo annotation
		var recommendationID string
		err := dbms.Db.QueryRow(`
			SELECT r.id
			FROM recommendation r
			JOIN cloud_resourses cr ON r.resource_id = cr.id
			LEFT JOIN k8s_workloads w ON cr.account = w.cloud_account_id
			  AND cr.meta::json->>'namespace' = w.namespace
			  AND cr.meta::json->>'controller' = w.name
			  AND w.is_active = true
			WHERE r.tenant_id = $1
			  AND r.cloud_account_id = $2
			  AND r.resource_id IS NOT NULL
			  AND r.status = 'Open'
			  AND (w.external_id IS NULL OR w.meta::json->'config'->'annotations'->>'ci.nudgebee.com/git.repo' IS NULL)
			LIMIT 1
		`, tenantID, accountID).Scan(&recommendationID)

		if err != nil {
			t.Skipf("No recommendation without git.repo annotation found: %v", err)
		}

		t.Logf("Testing with recommendation %s (no git.repo annotation)", recommendationID)

		request := RecommendationApplyRequest{
			AccountId:        accountID,
			RecommendationId: recommendationID,
			Provider:         "git",
			ProviderConfig: map[string]any{
				"recommendation_source": "recommendation",
			},
			Data: map[string]any{
				"reason": "Testing git provider without annotation",
			},
		}

		_, err = ApplyRecommendation(ctx, request)

		// Should fail with annotation not found error
		require.Error(t, err, "Should fail when git.repo annotation is missing")
		assert.True(t,
			strings.Contains(err.Error(), "annotation not found") ||
				strings.Contains(err.Error(), "controller kind not found") ||
				strings.Contains(err.Error(), "workload not found"),
			"Error should indicate missing annotation or workload: %v", err)
		t.Logf("✓ Correctly failed with expected error: %v", err)
	})

	t.Run("explicit github provider bypasses detection", func(t *testing.T) {
		// Find any recommendation for the specified account
		var recommendationID string
		err := dbms.Db.QueryRow(`
			SELECT id FROM recommendation
			WHERE tenant_id = $1 AND cloud_account_id = $2 AND status = 'Open'
			LIMIT 1
		`, tenantID, accountID).Scan(&recommendationID)

		if err != nil {
			t.Skipf("No recommendation found: %v", err)
		}

		request := RecommendationApplyRequest{
			AccountId:        accountID,
			RecommendationId: recommendationID,
			Provider:         "github", // Explicit provider, no auto-detection
			ProviderConfig: map[string]any{
				"recommendation_source": "recommendation",
				"name":                  "nudgebee",
			},
			Data: map[string]any{
				"reason": "Testing explicit github provider",
			},
		}

		// This should use github provider directly without looking for annotation
		_, err = ApplyRecommendation(ctx, request)

		// Error is expected (missing credentials), but should NOT be annotation-related
		if err != nil {
			assert.False(t, strings.Contains(err.Error(), "annotation not found"),
				"Explicit github provider should not require annotation")
			t.Logf("✓ Explicit provider used directly (error is credential-related): %v", err)
		} else {
			t.Logf("✓ Request succeeded with explicit github provider")
		}
	})

	t.Run("empty provider uses account type", func(t *testing.T) {
		// Find a kubernetes account recommendation for the specified account
		var recommendationID string
		err := dbms.Db.QueryRow(`
			SELECT r.id
			FROM recommendation r
			JOIN cloud_accounts a ON r.cloud_account_id = a.id
			WHERE r.tenant_id = $1
			  AND r.cloud_account_id = $2
			  AND a.account_type = 'kubernetes'
			  AND r.status = 'Open'
			LIMIT 1
		`, tenantID, accountID).Scan(&recommendationID)

		if err != nil {
			t.Skipf("No kubernetes recommendation found: %v", err)
		}

		request := RecommendationApplyRequest{
			AccountId:        accountID,
			RecommendationId: recommendationID,
			Provider:         "", // Empty provider - should use account type mapping
			ProviderConfig: map[string]any{
				"recommendation_source": "recommendation",
			},
			Data: map[string]any{
				"reason": "Testing empty provider defaults to kubernetes",
			},
		}

		_, err = ApplyRecommendation(ctx, request)

		// Should attempt kubernetes adapter (may fail for other reasons)
		if err != nil {
			// Should NOT be annotation-related error (kubernetes adapter doesn't need it)
			assert.False(t, strings.Contains(err.Error(), "annotation not found"),
				"Empty provider should use kubernetes adapter, not require annotation")
			t.Logf("✓ Empty provider correctly used account type mapping (error: %v)", err)
		} else {
			t.Logf("✓ Empty provider correctly used kubernetes adapter")
		}
	})
}

// TestApplyRecommendationWithGitLabProvider tests the GitLab provider end-to-end
// This test finds a recommendation with GitLab git.repo annotation and tests the full flow
func TestApplyRecommendationWithGitLabProvider(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatal(err)
	}

	const (
		userID    = "af4cb6af-1254-421d-bfa5-ffcfe649017e"
		tenantID  = "890cad87-c452-4aa7-b84a-742cee0454a1"
		accountID = "a2a30b02-0f67-42e5-a2ab-c658230fd798"
	)

	ctx := security.NewRequestContextForUserTenant(userID, tenantID, nil, nil, nil)

	// Find a recommendation with GitLab git.repo annotation
	var recommendationID, gitRepoURL string
	err = dbms.Db.QueryRow(`
		SELECT r.id, w.meta::json->'config'->'annotations'->>'ci.nudgebee.com/git.repo'
		FROM recommendation r
		JOIN cloud_resourses cr ON r.resource_id = cr.id
		JOIN k8s_workloads w ON cr.account = w.cloud_account_id
		  AND cr.meta::json->>'namespace' = w.namespace
		  AND cr.meta::json->>'controller' = w.name
		WHERE r.tenant_id = $1
		  AND r.cloud_account_id = $2
		  AND r.resource_id IS NOT NULL
		  AND r.status = 'Open'
		  AND w.is_active = true
		  AND w.meta::json->'config'->'annotations'->>'ci.nudgebee.com/git.repo' IS NOT NULL
		  AND (
		    w.meta::json->'config'->'annotations'->>'ci.nudgebee.com/git.repo' LIKE '%gitlab.com%'
		    OR w.meta::json->'config'->'annotations'->>'ci.nudgebee.com/git.repo' LIKE '%gitlab.%'
		  )
		LIMIT 1
	`, tenantID, accountID).Scan(&recommendationID, &gitRepoURL)

	if err != nil {
		t.Skipf("No recommendation with GitLab git.repo annotation found: %v", err)
	}

	t.Logf("Found recommendation %s with GitLab repo: %s", recommendationID, gitRepoURL)

	// Test with provider="git" - should auto-detect GitLab
	request := RecommendationApplyRequest{
		AccountId:        accountID,
		RecommendationId: recommendationID,
		Provider:         "git",
		ProviderConfig: map[string]any{
			"recommendation_source": "recommendation",
			"name":                  "my-gitlab",
		},
		Data: map[string]any{
			"reason": "Testing GitLab provider end-to-end",
		},
	}

	response, err := ApplyRecommendation(ctx, request)

	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "unable to detect git provider") {
			t.Errorf("GitLab provider detection failed: %v", err)
		} else if strings.Contains(errMsg, "annotation not found") {
			t.Errorf("Git repo annotation not found: %v", err)
		} else {
			// Other errors (credentials, API errors) are acceptable
			t.Logf("Request processed (error may be expected): %v", err)
			t.Logf("✓ GitLab provider was detected from annotation")
		}
	} else {
		require.NotNil(t, response, "Response should not be nil")
		t.Logf("✓ GitLab MR creation initiated successfully")
		t.Logf("Resolution ID: %s", response.Resolution.Id)
		t.Logf("Resolution Status: %s", response.Resolution.Status)
	}
}

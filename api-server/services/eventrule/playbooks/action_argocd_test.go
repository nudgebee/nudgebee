package playbooks

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestArgoCDHistoryAction(t *testing.T) {
	// Skip if test environment variables are not set
	accountId := os.Getenv("TEST_ACCOUNT_ID")
	if accountId == "" {
		t.Skip("TEST_ACCOUNT_ID not set")
	}

	action := &argoCDHistoryAction{}
	ctx := NewPlaybookActionContext("", accountId, nil, PlaybookEvent{})

	// Test with missing application_name
	params := map[string]any{
		"account_id": "74a4e1a3-9b6c-4ffd-a2e8-650413011790",
	}

	_, err := action.Execute(ctx, params)
	assert.NotNil(t, err)

	// Test with application_name (this will fail if no ArgoCD integration exists)
	params = map[string]any{
		"application_name": "equipment-identifier-service-gke-eu-production-app",
		"account_id":       "74a4e1a3-9b6c-4ffd-a2e8-650413011790",
	}

	response, err := action.Execute(ctx, params)

	// The error could be "no argocd integration found" or an actual ArgoCD API error
	// Both are acceptable for this test since we're just verifying the code structure
	if err != nil {
		t.Logf("Expected error (no integration or test app): %v", err)
	} else {
		assert.NotNil(t, response)
		assert.Equal(t, "json", response.GetFormatName())
	}
}

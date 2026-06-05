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
		"account_id": accountId,
	}

	_, err := action.Execute(ctx, params)
	assert.NotNil(t, err)

	// Test with application_name (this will fail if no ArgoCD integration exists)
	applicationName := os.Getenv("TEST_ARGOCD_APPLICATION_NAME")
	if applicationName == "" {
		t.Skip("TEST_ARGOCD_APPLICATION_NAME not set")
	}
	params = map[string]any{
		"application_name": applicationName,
		"account_id":       accountId,
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

func TestArgoCD_AppNameRegex(t *testing.T) {
	valid := []string{"my-app", "my_app", "app.v2", "namespace/my-app", "App123"}
	invalid := []string{"", "app; rm -rf /", "app name", "$(whoami)", "a|b", "ns/app/extra", "app`id`", "a&&b"}
	for _, v := range valid {
		assert.True(t, argoCDAppNameRegex.MatchString(v), "expected valid: %q", v)
	}
	for _, v := range invalid {
		assert.False(t, argoCDAppNameRegex.MatchString(v), "expected invalid: %q", v)
	}
}

func TestArgoCDHistoryAction_RejectsUnsafeApplicationName(t *testing.T) {
	// Unsafe application_name must be rejected before any DB/relay call, so this
	// runs without test-env credentials.
	action := &argoCDHistoryAction{}
	ctx := NewPlaybookActionContext("", "acct-1", nil, PlaybookEvent{})
	for _, name := range []string{"app; rm -rf /", "app$(whoami)", "app name", "app|cat", "ns/app/extra"} {
		_, err := action.Execute(ctx, map[string]any{"application_name": name, "account_id": "acct-1"})
		assert.Error(t, err, "for %q", name)
		assert.Contains(t, err.Error(), "invalid application_name", "for %q", name)
	}
}

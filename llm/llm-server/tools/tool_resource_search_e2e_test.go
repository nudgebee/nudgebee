//go:build e2e

package tools

import (
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetCurrentPrometheusOtelHosts(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("requires a live services backend; set TEST_ACCOUNT to run")
	}
	data := GetCurrentOtelHosts(os.Getenv("TEST_ACCOUNT"))
	assert.NotEmpty(t, data)
}

func TestGetCurrentK8sAccountState(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("requires a live services backend; set TEST_ACCOUNT to run")
	}
	data := GetCurrentK8sAccountState(os.Getenv("TEST_ACCOUNT"), 100)
	assert.NotEmpty(t, data)
}

func TestResourceSearchTool_SearchDbForResources(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("TEST_ACCOUNT not set")
	}

	tool := K8sResourceSearchTool{}
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForSuperAdmin()
	dummyCtx := core.NbToolContext{Ctx: sc, AccountId: accountId}

	// Test 1: Simple name
	results := tool.searchDbForResources("llm-server", accountId, dummyCtx)
	t.Logf("Search 'llm-server' found %d results", len(results))

	// Test 2: Multi-word name (should trigger variations)
	results2 := tool.searchDbForResources("llm server", accountId, dummyCtx)
	t.Logf("Search 'llm server' found %d results", len(results2))

	// Test 3: Empty name
	results3 := tool.searchDbForResources("", accountId, dummyCtx)
	assert.Equal(t, 0, len(results3))
}

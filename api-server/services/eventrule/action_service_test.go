package eventrule

import (
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTemplate(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, testenv.User)
	ctxt := security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)
	err := LoadEventActions(ctxt)
	if err != nil {
		println(err)
		t.Errorf("Test case 1 failed")
	}
}

func TestListAction(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, testenv.Account, testenv.User)
	ctxt := security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)

	listActionRequest := ListActionsRequest{CloudAccountId: os.Getenv("TEST_ACCOUNT"), Query: "sum(container_memory_usage_bytes{pod!=\"\"}) by (pod, namespace)"}
	actions, err := ListAction(ctxt, listActionRequest)
	assert.Nil(t, err)
	assert.NotNil(t, actions)

	for _, action := range actions {
		if action.Category != "All" && action.Category != "Pod" {
			assert.Fail(t, "Test case 1 failed")
		}
	}
}

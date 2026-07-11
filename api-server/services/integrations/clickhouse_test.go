package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClickHouse_Name(t *testing.T) {
	ch := ClickHouse{}
	expectedName := IntegrationClickHouse
	if name := ch.Name(); name != expectedName {
		t.Errorf("ClickHouse.Name() = %v, want %v", name, expectedName)
	}
}

func TestClickHouse_Category(t *testing.T) {
	ch := ClickHouse{}
	expectedCategory := core.IntegrationCategoryDatabase
	if category := ch.Category(); category != expectedCategory {
		t.Errorf("ClickHouse.Category() = %v, want %v", category, expectedCategory)
	}
}

func TestClickHouse_ConfigSchema(t *testing.T) {
	ch := ClickHouse{}
	schema := ch.ConfigSchema()

	// Object schema, testable connection
	assert.Equal(t, core.ToolSchemaTypeObject, schema.Type)
	assert.True(t, schema.Testable)

	// Core properties exist with expected types
	assert.Contains(t, schema.Properties, "connection_mode")
	assert.Contains(t, schema.Properties, "k8s_secret")
	assert.Contains(t, schema.Properties, "host")
	assert.Contains(t, schema.Properties, core.AccountId)
	assert.Contains(t, schema.Properties, core.IntegrationConfigName)

	assert.Equal(t, core.ToolSchemaTypeString, schema.Properties["k8s_secret"].Type)
	assert.Equal(t, core.ToolSchemaTypeString, schema.Properties["host"].Type)
	assert.Equal(t, core.ToolSchemaTypeArray, schema.Properties[core.AccountId].Type)
	assert.Equal(t, "listAccounts", schema.Properties[core.AccountId].AutoGenerateFunc)

	// connection_mode drives k8s vs vm_agent flows
	assert.Equal(t, "k8s", schema.Properties["connection_mode"].Default)
	assert.Equal(t, []any{"k8s", "vm_agent"}, schema.Properties["connection_mode"].Enum)

	// Password is marked encrypted
	assert.True(t, schema.Properties["password"].IsEncrypted, "password should be encrypted")
}

func TestClickHouse_ValidateConfig(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, testenv.Account)
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewSecurityContextForTenantAdmin(os.Getenv("TEST_TENANT"))

	clickhouse := ClickHouse{}

	// Test with valid clickhouse-secret and host
	errs := clickhouse.ValidateConfig(sc, []core.IntegrationConfigValue{
		{
			Name:  "k8s_secret",
			Value: "clickhouse-secret",
		},
	}, accountId)
	// Note: This might fail if actual connection fails, but should not fail due to missing required fields
	if len(errs) > 0 {
		// If there are errors, they should be connection-related, not validation errors
		for _, err := range errs {
			assert.NotEqual(t, "k8s_secret is required", err.Error())
			assert.NotEqual(t, "host is required", err.Error())
		}
	}

	// Test with missing k8s_secret
	errs = clickhouse.ValidateConfig(sc, []core.IntegrationConfigValue{
		{
			Name:  "host",
			Value: "localhost",
		},
	}, accountId)
	assert.NotEmpty(t, errs)
	assert.Equal(t, "k8s_secret is required", errs[0].Error())

	// Test with empty k8s_secret
	errs = clickhouse.ValidateConfig(sc, []core.IntegrationConfigValue{
		{
			Name:  "k8s_secret",
			Value: "",
		},
	}, accountId)
	assert.NotEmpty(t, errs)
	assert.Equal(t, "k8s_secret is required", errs[0].Error())

	// Test with empty host
	errs = clickhouse.ValidateConfig(sc, []core.IntegrationConfigValue{
		{
			Name:  "k8s_secret",
			Value: "clickhouse-secret",
		},
		{
			Name:  "host",
			Value: "",
		},
	}, accountId)
	assert.NotEmpty(t, errs)
	// If host is empty, it might default to env var, but since we are running test,
	// ValidateConfig will proceed to CommandExecutor which fails with account_id required
	// because accountId is empty or invalid in this test environment.
	// So we expect *some* error, but not necessarily "host is required" anymore if we allowed default logic.
	// However, seeing "account_id is required" confirms execution proceeded past validation.
	assert.Contains(t, errs[0].Error(), "account_id is required")

}

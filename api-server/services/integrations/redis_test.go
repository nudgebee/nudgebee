package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTools_GetCreateRedisToolConfigs(t *testing.T) {
	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")

	sc := security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil)
	toolConfigName := "nb-test-toolconfig-redis-1"

	err := core.DeleteIntegrationConfig(sc, IntegrationRedis, toolConfigName, "")
	assert.Nil(t, err)

	config, err := core.CreateIntegrationConfig(sc, "", IntegrationRedis, toolConfigName, []core.IntegrationConfigValue{
		{
			Name:  "k8s_secret",
			Value: "redis-secret",
		},
	},
		map[string]any{
			"env": "dev",
		}, []string{accountId}, false, "",
	)

	assert.Nil(t, err)
	assert.NotEmpty(t, config.Name)

	configs, err := core.ListIntegrationConfigs(sc, accountId, IntegrationRedis)
	assert.Nil(t, err)
	assert.NotEmpty(t, configs)

}

func TestTools_ValidateRedisTool(t *testing.T) {
	accountId := os.Getenv("TEST_ACCOUNT")
	tenantId := os.Getenv("TEST_TENANT")
	sc := security.NewSecurityContextForSuperAdminAndTenant(tenantId)

	err := Redis{}.ValidateConfig(sc, []core.IntegrationConfigValue{
		{
			Name:  "k8s_secret",
			Value: "redis-secret",
		},
	}, accountId)
	assert.Nil(t, err)
}

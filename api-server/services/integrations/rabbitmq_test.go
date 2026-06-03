package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTools_GetCreateRabbitmqToolConfigs(t *testing.T) {
	testenv.RequireEnv(t, testenv.User, testenv.Tenant, testenv.Account)
	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil)
	toolConfigName := "nb-test-toolconfig-rabbit-1"

	err := core.DeleteIntegrationConfig(sc, IntegrationRabbitMQ, toolConfigName, "")
	assert.Nil(t, err)

	config, err := core.CreateIntegrationConfig(sc, "", IntegrationRabbitMQ, toolConfigName, []core.IntegrationConfigValue{
		{
			Name:  "k8s_secret",
			Value: "rabbit-secret",
		},
	},
		map[string]any{
			"env": "dev",
		}, []string{accountId}, false, "",
	)

	assert.Nil(t, err)
	assert.NotEmpty(t, config.Name)

	configs, err := core.ListIntegrationConfigs(sc, accountId, IntegrationRabbitMQ)
	assert.Nil(t, err)
	assert.NotEmpty(t, configs)

}

func TestRabbitmq_ValidateConfig(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, testenv.Account)
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewSecurityContextForTenantAdmin(os.Getenv("TEST_TENANT"))

	rabbitmq := RabbitMq{}

	errs := rabbitmq.ValidateConfig(sc, []core.IntegrationConfigValue{
		{
			Name:  "k8s_secret",
			Value: "rabbit-secret",
		},
	}, accountId)
	assert.Empty(t, errs)

	errs = rabbitmq.ValidateConfig(sc, []core.IntegrationConfigValue{
		{
			Name:  "k8s_secret",
			Value: "",
		},
	}, accountId)
	assert.NotEmpty(t, errs)
	assert.Equal(t, "k8s_secret is required", errs[0].Error())
}

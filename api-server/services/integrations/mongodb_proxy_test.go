package integrations

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/services/integrations/core"
)

func TestMongoDBProxy_ValidateConfig(t *testing.T) {
	proxy := MongoDBProxy{}

	t.Run("cloud_push requires username and password", func(t *testing.T) {
		errs := proxy.ValidateConfig(nil, []core.IntegrationConfigValue{
			{Name: "host", Value: "mongo.example.com"},
			{Name: "credential_source", Value: "cloud_push"},
		}, "acc")

		assert.Len(t, errs, 2)
		assert.Contains(t, errs[0].Error(), "username is required for cloud_push credential source")
		assert.Contains(t, errs[1].Error(), "password is required for cloud_push credential source")
	})

	t.Run("secret managers require secret_ref", func(t *testing.T) {
		errs := proxy.ValidateConfig(nil, []core.IntegrationConfigValue{
			{Name: "host", Value: "mongo.example.com"},
			{Name: "credential_source", Value: "aws_sm"},
		}, "acc")

		assert.Len(t, errs, 1)
		assert.Contains(t, errs[0].Error(), "secret_ref is required for aws_sm credential source")
	})

	t.Run("valid cloud_push config passes", func(t *testing.T) {
		errs := proxy.ValidateConfig(nil, []core.IntegrationConfigValue{
			{Name: "host", Value: "mongo.example.com"},
			{Name: "credential_source", Value: "cloud_push"},
			{Name: "username", Value: "user"},
			{Name: "password", Value: "pass"},
		}, "acc")

		assert.Empty(t, errs)
	})
}

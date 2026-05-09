package api

import (
	"nudgebee/collector/cloud/account"
	"nudgebee/collector/cloud/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExecuteCliCommand(t *testing.T) {
	response, err := account.ExecuteCliCommand(security.NewRequestContextForSuperAdmin(), "569c15cb-962b-44c6-951e-d0730a23c0e8", "gcloud projects list")
	assert.Nil(t, err)
	assert.NotNil(t, response)
}

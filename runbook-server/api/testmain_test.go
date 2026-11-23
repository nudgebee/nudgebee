package api

import (
	"os"
	"testing"

	"nudgebee/runbook/config"
)

// testServiceToken is the service token every test request carries; the
// middleware rejects requests without it.
const testServiceToken = "test-service-token"

func TestMain(m *testing.M) {
	setServiceToken(testServiceToken)
	os.Exit(m.Run())
}

func setServiceToken(token string) { config.Config.WorkflowServerToken = token }

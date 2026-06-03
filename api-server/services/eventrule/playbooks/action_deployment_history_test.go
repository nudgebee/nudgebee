package playbooks

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/internal/testenv"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDeployment(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, testenv.Account)
	deploymentAction := deploymentHistoryAction{}

	// Parse event start time
	eventStartTime, _ := time.Parse(time.RFC3339, "2025-11-20T14:35:30Z")

	defaultPlaybookActionContext := NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), slog.Default(), PlaybookEvent{
		Name:        "TestDeploymentAlert",
		SubjectName: "shipment-tracking-service-production-eu-gcp",
		Labels:      map[string]string{"namespace": "p44-production-eu"},
		Annotations: map[string]string{},
		StartedAt:   &eventStartTime,
	})

	response, err := deploymentAction.Execute(defaultPlaybookActionContext, map[string]any{
		"service_name": "shipment-tracking-service-production-eu-gcp",
	})

	assert.NotNil(t, response)
	assert.Nil(t, err)

	jsonBytes, _ := json.Marshal(response)
	fmt.Println(string(jsonBytes))
}

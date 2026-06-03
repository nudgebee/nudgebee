package playbooks

import (
	"log/slog"
	"nudgebee/services/internal/testenv"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResourceEnricherAction(t *testing.T) {
	testenv.RequireEnv(t, testenv.Account)
	resourceEnricherAction := k8sResourceAction{}
	defaultPlaybookActionContext := defaultPlaybookActionContext{
		accountId: os.Getenv("TEST_ACCOUNT"),
		logger:    slog.Default(),
		event: PlaybookEvent{
			Name:        "HighFileSystemUtilizationNbDev",
			Labels:      map[string]string{},
			Annotations: map[string]string{},
			StartedAt:   nil,
			EndedAt:     nil,
		},
	}
	response, err := resourceEnricherAction.Execute(&defaultPlaybookActionContext, map[string]any{
		"resource_type":  "persistentvolumes",
		"group":          "",
		"version":        "v1",
		"all_namespaces": true,
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)
}

package playbooks

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKubectlAction(t *testing.T) {
	resourceEnricherAction := k8sKubectlAction{}
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
		"command": "kubectl get po -n nudgebee",
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)
}

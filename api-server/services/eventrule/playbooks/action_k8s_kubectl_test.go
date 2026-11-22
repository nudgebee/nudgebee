package playbooks

import (
	"log/slog"
	"nudgebee/services/internal/testenv"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKubectlAction(t *testing.T) {
	testenv.RequireEnv(t, testenv.Account)
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

func TestKubectlActionRefusesSecretAccess(t *testing.T) {
	ctx := defaultPlaybookActionContext{accountId: "acc", logger: slog.Default()}
	for _, cmd := range []string{
		"kubectl get secrets -A -o yaml",
		"kubectl get --raw /api/v1/secrets",
		"kubectl get pods -o go-template-file=/var/run/secrets/kubernetes.io/serviceaccount/token",
	} {
		response, err := (&k8sKubectlAction{}).Execute(&ctx, map[string]any{"command": cmd})
		assert.Nil(t, response, cmd)
		assert.ErrorContains(t, err, "blocked", cmd)
	}
}

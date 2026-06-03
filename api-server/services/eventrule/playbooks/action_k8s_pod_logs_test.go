package playbooks

import (
	"log/slog"
	"nudgebee/services/internal/testenv"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPodLogEnricherAction(t *testing.T) {
	testenv.RequireEnv(t, testenv.Account)
	podlogEnricher := podLogAction{}
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
	response, err := podlogEnricher.Execute(&defaultPlaybookActionContext, map[string]any{
		"name":      "app-dev-85b5fbbfcf-bwfns",
		"namespace": "nudgebee",
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)
}

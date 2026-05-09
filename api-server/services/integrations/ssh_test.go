package integrations

import (
	"log/slog"
	"nudgebee/services/eventrule/playbooks"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTools_ExecutSSHCommand(t *testing.T) {
	ssh := SSH{}
	playbookResponse, err := ssh.Execute(playbooks.NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), slog.Default(), playbooks.PlaybookEvent{}), map[string]any{
		"command":          "uname -a",
		"integration_name": "nb-dev-db",
		"account_id":       os.Getenv("TEST_ACCOUNT"),
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, playbookResponse.GetData())
}

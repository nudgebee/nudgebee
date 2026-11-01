package core

import (
	"errors"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// llmStub is named "llm" so CreateIntegrationConfig takes the LLM paths (no
// one-per-account take-over, tenant-default handling). It registers under the
// source-qualified key, which GetIntegrationBySource checks first, so the real
// provider registered by the test binary's imports cannot shadow it.
type llmStub struct{ stubIntegration }

func (llmStub) ConfigSchema() IntegrationSchema {
	return IntegrationSchema{Type: ToolSchemaTypeObject, Properties: map[string]IntegrationSchemaProperty{
		"llm_provider":     {Type: ToolSchemaTypeString},
		DefaultLLMProvider: {Type: ToolSchemaTypeString},
	}}
}

func init() { RegisterIntegrationWithSource("llm", "user", llmStub{stubIntegration{name: "llm"}}) }

// A Select-All save links every account inside one transaction: success commits
// all N links, and a failure on any one link rolls back the whole save (#39690).
func TestCreateIntegrationConfig_MultiAccountSaveIsAtomic(t *testing.T) {
	const n = 25
	accounts := make([]string, n)
	for i := range accounts {
		accounts[i] = fmt.Sprintf("acc-%02d", i)
	}
	values := func() []IntegrationConfigValue {
		return []IntegrationConfigValue{{Name: "llm_provider", Value: "openai"}, {Name: DefaultLLMProvider, Value: "false"}}
	}
	expectWritesUpTo := func(failAt int) {
		pkgMock.ExpectBegin()
		pkgMock.ExpectQuery(`SELECT id::text FROM integrations\s+WHERE name`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		pkgMock.ExpectQuery(`INSERT INTO integrations\(`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("int-new"))
		pkgMock.ExpectExec(`DELETE FROM integrations_cloud_accounts\s+WHERE integration_id = \S+\s+AND cloud_account_id NOT IN`).WillReturnResult(sqlmock.NewResult(0, 0))
		pkgMock.ExpectExec(`INSERT INTO integration_config_values`).WithArgs(sqlmock.AnyArg(), "int-new", "llm_provider", "openai", false, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
		pkgMock.ExpectExec(`UPDATE integration_config_values\s+SET value = 'false'`).WillReturnResult(sqlmock.NewResult(0, 0))
		for i := 0; i < n; i++ {
			e := pkgMock.ExpectExec(`INSERT INTO integrations_cloud_accounts`).WithArgs("int-new", accounts[i], testTenant)
			if i == failAt {
				e.WillReturnError(errors.New("link failed"))
				return
			}
			e.WillReturnResult(sqlmock.NewResult(0, 1))
		}
	}

	t.Run("every requested account is linked before the commit", func(t *testing.T) {
		ctx := ctxForTenant(t, testTenant)
		expectWritesUpTo(-1)
		pkgMock.ExpectCommit()

		dto, err := CreateIntegrationConfig(ctx, "", "llm", "openai-main", values(), nil, accounts, true, "user")
		require.NoError(t, err)
		assert.Equal(t, "int-new", dto.Id)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("a failed link part-way rolls back the whole save", func(t *testing.T) {
		ctx := ctxForTenant(t, testTenant)
		expectWritesUpTo(17)
		pkgMock.ExpectRollback()

		_, err := CreateIntegrationConfig(ctx, "", "llm", "openai-main", values(), nil, accounts, true, "user")
		require.ErrorContains(t, err, "link failed")
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})
}

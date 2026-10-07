package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A failed query's output is psql's / sqlplus's error text, not CSV. It must
// reach the model unchanged, not reshaped into rows (#30401).
func TestSQLToolResponsesKeepErrorTextOnFailure(t *testing.T) {
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin()}
	errText := "ERROR:  permission denied for table users\nHINT:  grant SELECT to the reporting role"
	failed := errors.New("workspace command failed")
	require.NotEqual(t, errText, convertCsvToJsonString(ctx, errText, ','), "the CSV conversion must change this text, or this test proves nothing")

	pg, err := postgresToolResponse(ctx, errText, false, failed)
	require.ErrorIs(t, err, failed)
	assert.Equal(t, core.NBToolResponseStatusError, pg.Status)
	assert.Equal(t, errText, pg.Data)

	pgPlan, _ := postgresToolResponse(ctx, errText, true, failed)
	assert.Equal(t, errText, pgPlan.Data, "a failed EXPLAIN is not wrapped as a plan")

	ora, err := oracleToolResponse(ctx, errText, failed)
	require.ErrorIs(t, err, failed)
	assert.Equal(t, core.NBToolResponseStatusError, ora.Status)
	var envelope struct {
		OriginalError string `json:"original_error"`
	}
	require.NoError(t, json.Unmarshal([]byte(fmt.Sprint(ora.Data)), &envelope))
	assert.Equal(t, errText, envelope.OriginalError)

	empty, _ := postgresToolResponse(ctx, "", false, failed)
	assert.Equal(t, failed.Error(), empty.Data, "no output falls back to the error")
}

func TestSQLToolResponsesConvertRowsOnSuccess(t *testing.T) {
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin()}
	csv := "id,name\n1,alpha\n"

	pg, err := postgresToolResponse(ctx, csv, false, nil)
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, pg.Status)
	assert.Equal(t, convertCsvToJsonString(ctx, csv, ','), pg.Data)

	plan, err := postgresToolResponse(ctx, `"Seq Scan"`, true, nil)
	require.NoError(t, err)
	assert.Equal(t, `[{"plan": "Seq Scan"}]`, plan.Data)

	ora, err := oracleToolResponse(ctx, csv, nil)
	require.NoError(t, err)
	assert.Equal(t, convertCsvToJsonString(ctx, csv, ','), ora.Data)
}

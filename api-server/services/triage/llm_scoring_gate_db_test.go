package triage

import (
	"testing"

	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AI scoring is on by default: a tenant with no flag row gets it, a tenant with a 'disabled' row
// (every tenant that existed when the default flipped) keeps the legacy formula.
func TestLLMTriageScoringEnabled_DefaultOn_DB(t *testing.T) {
	db := signalClassTestDB(t)
	_, err := db.Exec(`CREATE TABLE feature_flag (
		id uuid PRIMARY KEY DEFAULT gen_random_uuid(), feature_id text NOT NULL, tenant_id uuid NOT NULL,
		status text NOT NULL, account_id uuid, feature_module_id text, created_at timestamptz NOT NULL DEFAULT now())`)
	require.NoError(t, err)
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: db}, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })
	prev := config.Config.FeatureLLMTriageScoringEnabled
	config.Config.FeatureLLMTriageScoringEnabled = false
	t.Cleanup(func() { config.Config.FeatureLLMTriageScoringEnabled = prev })

	newTenant, pinnedOff, switchedOn := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = db.Exec(`INSERT INTO feature_flag (feature_id, tenant_id, status) VALUES
		('TRIAGE_LLM_SCORING', $1, 'disabled'), ('TRIAGE_LLM_SCORING', $2, 'enabled')`, pinnedOff, switchedOn)
	require.NoError(t, err)

	assert.True(t, llmTriageScoringEnabled(&models.Event{Tenant: &newTenant}), "no row: on by default")
	assert.False(t, llmTriageScoringEnabled(&models.Event{Tenant: &pinnedOff}), "a 'disabled' row keeps it off")
	assert.True(t, llmTriageScoringEnabled(&models.Event{Tenant: &switchedOn}))
	assert.False(t, llmTriageScoringEnabled(&models.Event{}), "no tenant: off")
}

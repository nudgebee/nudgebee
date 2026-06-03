package slo

import (
	"log/slog"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSLOExecute(t *testing.T) {
	testenv.RequireMetastore(t)
	_, account, _ := testenv.RequireTenant(t)
	err := ExecuteSLO(account)
	assert.Nil(t, err)
}

func TestSLOList(t *testing.T) {
	testenv.RequireMetastore(t)
	tenant, account, user := testenv.RequireTenant(t)
	request := SLOListRequest{AccountId: account}
	ctxt := security.NewRequestContextForUserTenant(user, tenant, nil, nil, nil)
	data, err := GetSLOConfig(ctxt, request)
	assert.Nil(t, err)
	assert.NotNil(t, data)
}

func TestSLOCreateRequest(t *testing.T) {
	testenv.RequireMetastore(t)
	tenant, account, user := testenv.RequireTenant(t)
	configList := make([]SLOConfigRequest, 0)
	configRequest := SLOConfigRequest{Name: "Availablity", Goal: 0.99}
	configList = append(configList, configRequest)
	request := SLORequest{AccountId: account, WorkloadName: "app-dev", Namespace: "nudgebee", Config: configList}
	ctxt := security.NewRequestContextForUserTenant(user, tenant, nil, nil, nil)
	_, err := CreateOrUpdateSLOConfig(ctxt, request)
	assert.Nil(t, err)
}

func TestSLOConfigExecution(t *testing.T) {
	testenv.RequireMetastore(t)
	dbms, err := database.GetDatabaseManager(database.Metastore)
	assert.Nil(t, err)
	sloConfigId := "15edd4a6-a741-44f8-8e7d-925d173e60e0"
	rows, err := dbms.Db.Queryx(`select * from slo_config where enabled = true and id = $1`, sloConfigId)
	assert.Nil(t, err)

	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()

	sloConfigs := make([]DBSLOConfig, 0)
	for rows.Next() {
		config := DBSLOConfig{}
		err = rows.StructScan(&config)
		if err != nil {
			slog.Error("Error fetching slo config", "error", err, "config id", sloConfigId)
		}
		sloConfigs = append(sloConfigs, config)
	}
	dbSloConfig := sloConfigs[0]
	err = executeSLOConfig(dbSloConfig, dbSloConfig.CloudAccountId, dbms)
	assert.Nil(t, err)
}

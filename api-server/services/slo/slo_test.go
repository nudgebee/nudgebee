package slo

import (
	"log/slog"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSLOExecute(t *testing.T) {
	err := ExecuteSLO("0053b816-4b45-4dcd-a612-19545110f8aa")
	assert.Nil(t, err)
}

func TestSLOList(t *testing.T) {
	request := SLOListRequest{AccountId: "0053b816-4b45-4dcd-a612-19545110f8aa"}
	ctxt := security.NewRequestContextForUserTenant("af4cb6af-1254-421d-bfa5-ffcfe649017e", "0053b816-4b45-4dcd-a612-19545110f8aa", nil, nil, nil)
	data, err := GetSLOConfig(ctxt, request)
	assert.Nil(t, err)
	assert.NotNil(t, data)
}

func TestSLOCreateRequest(t *testing.T) {
	configList := make([]SLOConfigRequest, 0)
	configRequest := SLOConfigRequest{Name: "Availablity", Goal: 0.99}
	configList = append(configList, configRequest)
	request := SLORequest{AccountId: "0053b816-4b45-4dcd-a612-19545110f8aa", WorkloadName: "app-dev", Namespace: "nudgebee", Config: configList}
	ctxt := security.NewRequestContextForUserTenant("af4cb6af-1254-421d-bfa5-ffcfe649017e", "890cad87-c452-4aa7-b84a-742cee0454a1", nil, nil, nil)
	_, err := CreateOrUpdateSLOConfig(ctxt, request)
	assert.Nil(t, err)
}

func TestSLOConfigExecution(t *testing.T) {
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

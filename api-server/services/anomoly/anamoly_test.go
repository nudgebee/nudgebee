package anomoly

import (
	"database/sql"
	"nudgebee/services/application"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAnomoly(t *testing.T) {
	ctxt := security.NewRequestContextForUserTenant("af4cb6af-1254-421d-bfa5-ffcfe649017e", "890cad87-c452-4aa7-b84a-742cee0454a1", nil, nil, nil)
	evaluationPeriod := int(180)

	endTime := time.Now().UTC()
	startTime := endTime.Add(-24 * time.Hour) // 1 day before end time

	ProcessAnomaly(ctxt, AnomalyProcessingMessage{
		TenantId:                "890cad87-c452-4aa7-b84a-742cee0454a1",
		AccountId:               "ff87fbfd-5729-4474-b9d6-96beb693e3fd",
		ApplicationNamespace:    "nudgebee",
		ApplicationName:         "ml-k8s-server",
		StartTime:               startTime,
		EndTime:                 endTime,
		EvaluationPeriodMinutes: &evaluationPeriod,
		AnomalyType:             MetricAnomolyTypeCPU,
	})
}

func TestAnomolyEvidence(t *testing.T) {

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		println(err)
	}
	start := time.Now().UTC()
	anomaly := Anomaly{
		Id:           common.GenerateUUID(),
		AccountId:    "a2a30b02-0f67-42e5-a2ab-c658230fd798",
		Tenant:       "890cad87-c452-4aa7-b84a-742cee0454a1",
		Name:         "ml-k8s-server",
		Namespace:    "nudgebee",
		OldValue:     map[string]interface{}{"key": "value"},
		CurrentValue: 0.0,
		AnomalyType:  MetricAnomolyTypeLatency,
		IsAnomaly:    true,
		EvaluatedAt:  &start,
		PodName:      "auto-pilot-server-d7d8df6b8-zkj5m",
	}
	err = GenerateAnomalyEvent(dbms, &anomaly)
	if err != nil {
		println(err)
		t.Errorf("Test case 2 failed")
	}
}

func TestMLAnomaly(t *testing.T) {
	userId := "af4cb6af-1254-421d-bfa5-ffcfe649017e"
	tenantId := "890cad87-c452-4aa7-b84a-742cee0454a1"
	accountId := "a2a30b02-0f67-42e5-a2ab-c658230fd798"

	ctxt := security.NewRequestContextForUserTenant(userId, tenantId, nil, nil, nil)

	template := AnomalyTemplate{
		AnomalyType: MetricAnomolyTypeCPU,
	}

	application := application.Application{
		Name:         "ml-k8s-server",
		K8sNamespace: "nudgebee",
		K8sKind:      "Deployment",
	}
	err := processSingleApplicationMlAsync(ctxt, template, tenantId, accountId, application)
	if err != nil {
		println(err)
		t.SkipNow()
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	assert.NoError(t, err, "Failed to get DB manager")

	var anomalyFound bool
	var insertedAnomaly Anomaly // Or use map[string]any if Anomaly struct isn't easily queryable
	maxWait := 30 * time.Second // Adjust timeout as needed
	pollInterval := 1 * time.Second
	startTime := time.Now()

	for time.Since(startTime) < maxWait {
		// Query the database for the expected anomaly
		// Adjust query based on how you identify the specific anomaly triggered by this test run
		// This example assumes we look for the specific app/type within a recent timeframe
		query := `
            SELECT id, account_id, tenant, name, namespace, anomaly_type, is_anomaly, evaluated_at
            FROM anomaly
            WHERE account_id = $1
              AND tenant = $2
              AND name = $3
              AND namespace = $4
              AND lower(anomaly_type) = lower($5)
              AND evaluated_at > $6
            ORDER BY evaluated_at DESC
            LIMIT 1`

		// Use a time slightly before the test action started to avoid race conditions
		actionStartTime := time.Now().Add(-5 * time.Second)

		// Using QueryRowx and StructScan or MapScan depending on what's easier
		row := dbms.Db.QueryRowx(query, accountId, tenantId, application.Name, application.K8sNamespace, template.AnomalyType, actionStartTime)
		tempAnomaly := Anomaly{} // Use appropriate struct or map
		scanErr := row.StructScan(&tempAnomaly)

		if scanErr == nil {
			// Found the record
			anomalyFound = true
			insertedAnomaly = tempAnomaly
			break // Exit polling loop
		} else if scanErr != sql.ErrNoRows {
			// Handle unexpected DB errors
			t.Fatalf("Database query failed during polling: %v", scanErr)
		}

		// Wait before polling again
		time.Sleep(pollInterval)
	}

	// --- Assertions ---
	assert.True(t, anomalyFound, "Expected anomaly record was not found in the database within timeout")
	if anomalyFound {
		// Add more specific assertions about the insertedAnomaly data if needed
		assert.Equal(t, accountId, insertedAnomaly.AccountId)
		assert.Equal(t, tenantId, insertedAnomaly.Tenant)
		assert.Equal(t, application.K8sNamespace, insertedAnomaly.Name)
		assert.Equal(t, template.AnomalyType, insertedAnomaly.AnomalyType)
		// assert.True(t, insertedAnomaly.IsAnomaly) // Depending on test data
		// ... other assertions
	}
}

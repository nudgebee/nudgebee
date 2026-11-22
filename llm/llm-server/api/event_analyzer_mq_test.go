package api

import (
	"context"
	"encoding/json"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventMQRequeuesDatabaseSetupFailure(t *testing.T) {
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return nil, assert.AnError
	})
	t.Cleanup(func() { common.RegisterDatabaseManagerHook(common.Metastore, nil) })

	data, err := json.Marshal(EventAnalysisRequest{EventId: "event", AccountId: "account", TaskToken: "token"})
	require.NoError(t, err)
	require.ErrorIs(t, processTroubleshootingEventFromMq(context.Background(), data), assert.AnError)
}

func TestEventMQRequeuesAnalysisStatusReadFailure(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgres")}, nil
	})
	t.Cleanup(func() { common.RegisterDatabaseManagerHook(common.Metastore, nil) })

	const eventID, accountID, tenantID = "event-status-read-failure", "account-status-read-failure", "tenant-status-read-failure"
	mock.ExpectQuery("SELECT tenant FROM cloud_accounts WHERE id =").WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"tenant"}).AddRow(tenantID))
	mock.ExpectQuery("SELECT id::text FROM cloud_accounts WHERE tenant =").WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(accountID))
	mock.ExpectQuery("SELECT id, fingerprint, aggregation_key, created_at FROM events WHERE id =").WithArgs(eventID, accountID).
		WillReturnError(assert.AnError)

	data, err := json.Marshal(EventAnalysisRequest{EventId: eventID, AccountId: accountID, TaskToken: "token"})
	require.NoError(t, err)
	require.ErrorIs(t, processTroubleshootingEventFromMq(context.Background(), data), assert.AnError)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEventMQDoesNotRequeueMissingEvent(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgres")}, nil
	})
	t.Cleanup(func() { common.RegisterDatabaseManagerHook(common.Metastore, nil) })
	oldExchange, oldRoutingKey := config.Config.RabbitMqEventInvestigateCompletedExchange, config.Config.RabbitMqEventInvestigateCompletedRoutingKey
	config.Config.RabbitMqEventInvestigateCompletedExchange = ""
	config.Config.RabbitMqEventInvestigateCompletedRoutingKey = ""
	t.Cleanup(func() {
		config.Config.RabbitMqEventInvestigateCompletedExchange = oldExchange
		config.Config.RabbitMqEventInvestigateCompletedRoutingKey = oldRoutingKey
	})

	const eventID, accountID, tenantID = "event-not-found", "account-not-found", "tenant-not-found"
	mock.ExpectQuery("SELECT tenant FROM cloud_accounts WHERE id =").WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"tenant"}).AddRow(tenantID))
	mock.ExpectQuery("SELECT id::text FROM cloud_accounts WHERE tenant =").WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(accountID))
	mock.ExpectQuery("SELECT id, fingerprint, aggregation_key, created_at FROM events WHERE id =").WithArgs(eventID, accountID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "fingerprint", "aggregation_key", "created_at"}))

	data, err := json.Marshal(EventAnalysisRequest{EventId: eventID, AccountId: accountID, TaskToken: "token"})
	require.NoError(t, err)
	require.NoError(t, processTroubleshootingEventFromMq(context.Background(), data))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEventMQRequeuesWhenPendingTokenCannotBeStored(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgres")}, nil
	})
	t.Cleanup(func() { common.RegisterDatabaseManagerHook(common.Metastore, nil) })

	const eventID, accountID, tenantID = "event-pending-token-failure", "account-pending-token-failure", "tenant-pending-token-failure"
	mock.ExpectQuery("SELECT tenant FROM cloud_accounts WHERE id =").WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"tenant"}).AddRow(tenantID))
	mock.ExpectQuery("SELECT id::text FROM cloud_accounts WHERE tenant =").WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(accountID))
	mock.ExpectQuery("SELECT id, fingerprint, aggregation_key, created_at FROM events WHERE id =").WithArgs(eventID, accountID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "fingerprint", "aggregation_key", "created_at"}).AddRow(eventID, "fingerprint", "alert", time.Now()))
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping WHERE event_id=").WithArgs(eventID, events.AnalysisTypeLog).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}).AddRow("analysis-running"))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE id =").WithArgs("analysis-running", accountID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("analysis-running", "", string(events.AnalysisStatusInProgress), eventID, "", "", time.Now()))

	data, err := json.Marshal(EventAnalysisRequest{EventId: eventID, AccountId: accountID, TaskToken: "token"})
	require.NoError(t, err)
	err = processTroubleshootingEventFromMqWithRegister(context.Background(), data, func(context.Context, string, string) error {
		return assert.AnError
	})
	require.ErrorIs(t, err, assert.AnError)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The EVENT_AUTO_AI_SUMMARY kill switch must never reach a workflow-initiated
// investigation. When it did, processTroubleshootingEventFromMq set
// skipPublish and emitted a token-less envelope, runbook-server logged
// "investigation completion: empty task_token, dropping", and every
// llm.event_investigate node on an account with the flag off timed out on its
// StartToCloseTimeout.
func TestAutoAnalysisGateApplies(t *testing.T) {
	tests := []struct {
		name      string
		taskToken string
		want      bool
	}{
		{
			name:      "auto path (no token) is gated by the kill switch",
			taskToken: "",
			want:      true,
		},
		{
			name:      "workflow-initiated request bypasses the kill switch",
			taskToken: "dGFzay10b2tlbi1ieXRlcw==",
			want:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, autoAnalysisGateApplies(tt.taskToken))
		})
	}
}

func TestPublishAutoCompletion(t *testing.T) {
	cases := []struct {
		name        string
		taskToken   string
		ranAnalysis bool
		want        bool
	}{
		{"api-server request, ran here", "", true, true},
		{"api-server request, another worker's result", "", false, true},
		{"workflow request, ran here", "token", true, true},
		{"workflow request, another worker's result", "token", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, publishAutoCompletion(tc.taskToken, tc.ranAnalysis))
		})
	}
}

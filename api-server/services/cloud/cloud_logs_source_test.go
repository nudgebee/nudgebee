package cloud

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/config"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The log card names the log group (and streams) the collector read, so the
// reader of its lines knows where they came from.
func TestCloudLogsEvidenceNamesItsSource(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 4; i++ {
		mock.ExpectQuery("SELECT id FROM cloud_accounts WHERE tenant").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("account-1"))
	}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgresql")}, nil
	})
	t.Cleanup(func() {
		database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
			return nil, errors.New("no metastore in unit tests")
		})
		_ = rawDB.Close()
	})

	source := QueryLogResponse{Results: []LogMessage{{Message: "service started", Timestamp: 1}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": source})
	}))
	t.Cleanup(srv.Close)
	prevURL := config.Config.CloudCollectorServerUrl
	config.Config.CloudCollectorServerUrl = srv.URL
	t.Cleanup(func() { config.Config.CloudCollectorServerUrl = prevURL })

	ctx := playbooks.NewPlaybookActionContext("tenant-1", "account-1", slog.Default(), playbooks.PlaybookEvent{})
	params := map[string]any{"service_name": "AmazonEC2", "resource_id": "i-0123456789abcdef0", "region": "us-east-1"}

	source.LogGroupName, source.LogStreamPrefix = "EC2-Windows-Logs", "i-0123456789abcdef0"
	resp, err := (&cloudLogAction{}).Execute(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, "EC2-Windows-Logs", resp.GetAdditionalInfo()["log_group_name"])
	assert.Equal(t, "i-0123456789abcdef0", resp.GetAdditionalInfo()["log_stream_prefix"])

	// A collector that does not say where it read from leaves the card as before.
	source.LogGroupName, source.LogStreamPrefix = "", ""
	resp, err = (&cloudLogAction{}).Execute(ctx, params)
	require.NoError(t, err)
	assert.NotContains(t, resp.GetAdditionalInfo(), "log_group_name")
	assert.NotContains(t, resp.GetAdditionalInfo(), "log_stream_prefix")
}

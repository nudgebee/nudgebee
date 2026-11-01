package alertrule

import (
	"log/slog"
	"os"
	"testing"

	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestOpenObserveAlertLive exercises real provider CRUD against a disposable server.
func TestOpenObserveAlertLive(t *testing.T) {
	endpoint := os.Getenv("OPENOBSERVE_ALERT_TEST_URL")
	if endpoint == "" {
		t.Skip("set OPENOBSERVE_ALERT_TEST_URL, USER and PASSWORD for a disposable OpenObserve server")
	}
	username, password := os.Getenv("OPENOBSERVE_ALERT_TEST_USER"), os.Getenv("OPENOBSERVE_ALERT_TEST_PASSWORD")
	if username == "" || password == "" {
		t.Fatal("live alert test credentials are required")
	}
	ctx := security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil)
	mock := openObserveConfigDB(t)
	expectConfig := func() {
		rows := sqlmock.NewRows([]string{"id", "name", "value", "is_encrypted"})
		for _, entry := range [][2]string{{"openobserve_url", endpoint}, {"openobserve_org_id", "default"}, {"openobserve_username", username}, {"openobserve_password", password}, {"openobserve_log_stream", "logs"}} {
			rows.AddRow("live-test", entry[0], entry[1], false)
		}
		expectOpenObserveConfig(mock, ctx).WillReturnRows(rows)
	}
	for _, queryType := range []string{"sql", "promql"} {
		t.Run(queryType, func(t *testing.T) {
			cfg := openObserveTestConfig()
			cfg.AccountId = "account"
			cfg.Name = "PR864-synthetic-" + queryType + "-threshold"
			cfg.Enabled = false
			cfg.Query = "SELECT value FROM logs WHERE value >= 1"
			cfg.ProviderConfig = map[string]any{"destinations": []string{"pr864_destination"}, "query_type": queryType, "period": 1, "frequency": 1}
			if queryType == "promql" {
				cfg.AlertType = "metric"
				cfg.Query = "up"
				cfg.ProviderConfig["stream_name"] = "up"
			}
			source := &OpenObserveAlertRuleSource{}
			expectConfig()
			result, err := source.CreateAlertRule(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if result.ExternalRuleId == "" {
				t.Fatal("provider returned no alert ID")
			}
			deleted := false
			defer func() {
				if !deleted {
					expectConfig()
					if err := source.DeleteAlertRule(ctx, "account", result.ExternalRuleId); err != nil {
						t.Error(err)
					}
				}
			}()
			cfg.ProviderConfig["period"] = 2
			expectConfig()
			if _, err := source.UpdateAlertRule(ctx, result.ExternalRuleId, cfg); err != nil {
				t.Fatal(err)
			}
			expectConfig()
			if err := source.DeleteAlertRule(ctx, "account", result.ExternalRuleId); err != nil {
				t.Fatal(err)
			}
			deleted = true
			t.Logf("%s alert create/update/delete passed", queryType)
		})
	}
}

package alertrule

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func openObserveConfigDB(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	previous, ok := database.GetDatabaseManagerIfInitialized(database.Metastore)
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, nil
	})
	t.Cleanup(func() {
		database.RegisterDatabaseManagerHook(database.Metastore, nil)
		if ok {
			database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) { return previous, nil })
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	return mock
}

func openObserveConfigRows(endpoint string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"id", "name", "value", "is_encrypted"})
	for _, value := range [][2]string{{"openobserve_url", endpoint}, {"openobserve_org_id", "org"}, {"openobserve_username", "test"}, {"openobserve_password", "test-secret"}} {
		rows.AddRow("integration-1", value[0], value[1], false)
	}
	return rows
}

func expectOpenObserveConfig(mock sqlmock.Sqlmock, ctx *security.RequestContext) *sqlmock.ExpectedQuery {
	return mock.ExpectQuery("(?s)SELECT DISTINCT.*ica.tenant_id=i.tenant_id.*ca.tenant=i.tenant_id.*i.status != 'disabled'.*i.tenant_id=\\$1 AND ica.cloud_account_id=\\$2").
		WithArgs(ctx.GetSecurityContext().GetTenantId(), "account")
}

func TestOpenObserveAlertConfig(t *testing.T) {
	tests := []struct {
		name      string
		rows      *sqlmock.Rows
		dbError   error
		wantError string
	}{
		{"active account config", openObserveConfigRows("http://127.0.0.1:5080/browser/path"), nil, ""},
		{"none", sqlmock.NewRows([]string{"id", "name", "value", "is_encrypted"}), nil, "not found"},
		{"ambiguous", openObserveConfigRows("http://127.0.0.1:5080").AddRow("integration-2", "openobserve_url", "http://other", false), nil, "multiple active"},
		{"embedded credentials", openObserveConfigRows("http://user:pass@localhost:5080"), nil, "without embedded credentials"},
		{"read failure", nil, errors.New("database unavailable"), "list OpenObserve"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := openObserveConfigDB(t)
			ctx := security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil)
			expected := expectOpenObserveConfig(mock, ctx)
			if tc.dbError != nil {
				expected.WillReturnError(tc.dbError)
			} else {
				expected.WillReturnRows(tc.rows)
			}
			cfg, err := getOpenObserveAlertConfig(ctx, "account")
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v, want %q", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.URL != "http://127.0.0.1:5080" || cfg.LogStream != "default" || cfg.TraceStream != "default" {
				t.Errorf("config normalization = %#v", cfg)
			}
		})
	}
	if _, err := getOpenObserveAlertConfig(nil, ""); err == nil {
		t.Error("empty account accepted")
	}
}

func TestOpenObserveAlertSourceCRUD(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
			t.Run(method+"/"+http.StatusText(status), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != method {
						t.Errorf("method = %s", r.Method)
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte("{\"id\":\"alert-1\"}"))
				}))
				defer server.Close()
				ctx := security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil)
				mock := openObserveConfigDB(t)
				expectOpenObserveConfig(mock, ctx).WillReturnRows(openObserveConfigRows(server.URL))
				cfg := openObserveTestConfig()
				cfg.AccountId = "account"
				source := &OpenObserveAlertRuleSource{}
				var result *AlertRuleResult
				var err error
				switch method {
				case http.MethodPost:
					result, err = source.CreateAlertRule(ctx, cfg)
				case http.MethodPut:
					result, err = source.UpdateAlertRule(ctx, "alert-1", cfg)
				case http.MethodDelete:
					err = source.DeleteAlertRule(ctx, "account", "alert-1")
				}
				if status == http.StatusBadRequest {
					if err == nil {
						t.Fatal("API error accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if method != http.MethodDelete && (result == nil || result.ExternalRuleId != "alert-1") {
					t.Errorf("result = %#v", result)
				}
			})
		}
	}
}

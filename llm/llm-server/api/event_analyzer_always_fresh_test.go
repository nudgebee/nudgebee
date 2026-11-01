package api

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldForceFreshAnalysis(t *testing.T) {
	const eventID = "event-always-fresh"
	cases := []struct {
		name      string
		settings  string // cloud_account_attrs value; "" means no row
		namespace string // "" means the namespace is not looked up
		checkMap  bool
		mapped    bool
		want      bool
	}{
		{name: "no settings makes no further queries", want: false},
		{name: "malformed settings fall back to defaults", settings: "{not json", want: false},
		{name: "other namespace", settings: `{"always_fresh_namespaces":["demo"]}`, namespace: "prod", want: false},
		{name: "already analysed event reuses its run", settings: `{"always_fresh_namespaces":["demo"]}`, namespace: "demo", checkMap: true, mapped: true, want: false},
		{name: "new event in always-fresh namespace", settings: `{"always_fresh_namespaces":[" other ","demo"]}`, namespace: "demo", checkMap: true, want: true},
		{name: "reuse related off applies to every namespace", settings: `{"reuse_related":false}`, checkMap: true, want: true},
		{name: "reuse related off still reuses the event's own run", settings: `{"reuse_related":false}`, checkMap: true, mapped: true, want: false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A distinct account per case: settings are cached per account.
			accountID := fmt.Sprintf("account-always-fresh-%d", i)
			rawDB, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = rawDB.Close() })
			dbManager := &common.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgres")}
			ctx := security.NewRequestContextForSuperAdmin()

			settingsQuery := mock.ExpectQuery("SELECT value FROM cloud_account_attrs").WithArgs(accountID, events.EventAnalysisSettingsAttr)
			if tc.settings == "" {
				settingsQuery.WillReturnRows(sqlmock.NewRows([]string{"value"}))
			} else {
				settingsQuery.WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(tc.settings))
			}
			if tc.namespace != "" {
				mock.ExpectQuery("SELECT subject_namespace FROM events").WithArgs(eventID, accountID).
					WillReturnRows(sqlmock.NewRows([]string{"subject_namespace"}).AddRow(tc.namespace))
			}
			if tc.checkMap {
				mock.ExpectQuery("SELECT EXISTS").WithArgs(eventID).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(tc.mapped))
			}

			repo := events.NewEventAnalysisRepositoryForAccount(ctx, dbManager, accountID)
			assert.Equal(t, tc.want, shouldForceFreshAnalysis(ctx, repo, eventID, accountID))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestBindStagesToEventMapsEveryNonLogStage(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	repo := events.NewEventAnalysisRepository(&common.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgres")})

	const eventID = "event-bind-stages"
	for i, aType := range []events.EventAnalysisType{events.AnalysisTypeSummary, events.AnalysisTypeInvestigation, events.AnalysisTypeDetailedResponse} {
		analysisID := fmt.Sprintf("analysis-%d", i)
		mock.ExpectBegin()
		// No mapping yet: the stage must get its own row, never the related event's.
		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping").WithArgs(eventID, aType).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
		mock.ExpectQuery("INSERT INTO event_log_analysis").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(analysisID))
		mock.ExpectExec("INSERT INTO event_analysis_mapping").WithArgs(eventID, analysisID, aType).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
	}

	bindStagesToEvent(security.NewRequestContextForSuperAdmin(), repo, eventID, "fp", "account", "agg")
	require.NoError(t, mock.ExpectationsWereMet())
}

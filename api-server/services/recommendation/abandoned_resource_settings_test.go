package recommendation

import (
	"fmt"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAbandonedResourceSettings(t *testing.T) {
	defaults := abandonedResourceSettings{
		NetworkThreshold: abandonedResourceNetworkThresholdDefault,
		ObservationDays:  abandonedResourceObservationPeriodDaysDefault,
	}
	tests := []struct {
		name   string
		raw    string
		want   abandonedResourceSettings
		wantOK bool
	}{
		{"nothing saved", "", defaults, true},
		{"edited values are strings", `{"network_threshold":"1000","observation_days":"30"}`, abandonedResourceSettings{1000, 30}, true},
		{"loaded values are numbers", `{"network_threshold":2500,"observation_days":14}`, abandonedResourceSettings{2500, 14}, true},
		{"padded string", `{"network_threshold":" 800 ","observation_days":"10"}`, abandonedResourceSettings{800, 10}, true},
		{"only threshold saved", `{"network_threshold":"1000"}`, abandonedResourceSettings{1000, defaults.ObservationDays}, true},
		{"bad threshold keeps good days", `{"network_threshold":"abc","observation_days":"30"}`, abandonedResourceSettings{defaults.NetworkThreshold, 30}, false},
		{"bad days keeps good threshold", `{"network_threshold":"1000","observation_days":"6.5"}`, abandonedResourceSettings{1000, defaults.ObservationDays}, false},
		{"zero and negative ignored", `{"network_threshold":0,"observation_days":"-3"}`, defaults, false},
		{"fractional number ignored", `{"network_threshold":12.5,"observation_days":30}`, abandonedResourceSettings{defaults.NetworkThreshold, 30}, false},
		{"null fields ignored", `{"network_threshold":null,"observation_days":null}`, defaults, false},
		{"window below minimum is raised", `{"network_threshold":"1000","observation_days":"6"}`, abandonedResourceSettings{1000, abandonedResourceMinObservationDays}, false},
		{"one-day window is raised", `{"observation_days":1}`, abandonedResourceSettings{defaults.NetworkThreshold, abandonedResourceMinObservationDays}, false},
		{"window at minimum is kept", `{"observation_days":"7"}`, abandonedResourceSettings{defaults.NetworkThreshold, 7}, true},
		{"oversized values ignored", `{"network_threshold":"999999999999","observation_days":1e12}`, defaults, false},
		{"not JSON", `5000`, defaults, false},
		{"malformed JSON", `{"network_threshold":`, defaults, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseAbandonedResourceSettings(tt.raw)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantOK, ok)
		})
	}
}

func TestGetAbandonedResourceSettings(t *testing.T) {
	const accountID = "acc-1"
	tests := []struct {
		name    string
		setup   func(q *sqlmock.ExpectedQuery)
		want    abandonedResourceSettings
		wantErr bool
	}{
		{"saved settings", func(q *sqlmock.ExpectedQuery) {
			q.WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"network_threshold":"1000","observation_days":"30"}`))
		}, abandonedResourceSettings{1000, 30}, false},
		{"no row", func(q *sqlmock.ExpectedQuery) {
			q.WillReturnRows(sqlmock.NewRows([]string{"value"}))
		}, abandonedResourceSettings{abandonedResourceNetworkThresholdDefault, abandonedResourceObservationPeriodDaysDefault}, false},
		{"null value", func(q *sqlmock.ExpectedQuery) {
			q.WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(nil))
		}, abandonedResourceSettings{abandonedResourceNetworkThresholdDefault, abandonedResourceObservationPeriodDaysDefault}, false},
		{"query error stops the scan", func(q *sqlmock.ExpectedQuery) {
			q.WillReturnError(fmt.Errorf("connection reset"))
		}, abandonedResourceSettings{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := mockMetastore(t)
			tt.setup(mock.ExpectQuery(`SELECT value FROM cloud_account_attrs WHERE cloud_account_id = \$1 AND name = \$2`).
				WithArgs(accountID, abandonedResourceSettingsAttr))
			dbms, err := database.GetDatabaseManager(database.Metastore)
			require.NoError(t, err)

			got, err := getAbandonedResourceSettings(security.NewRequestContextForSuperAdmin(nil, nil, nil), dbms, accountID)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

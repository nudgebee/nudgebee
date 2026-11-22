package recommendation

import (
	"testing"

	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestInitialScanJobNames keeps initialScanJobNames in lockstep with the
// "nb" (server-side) entries of recommendationJobProviderMap: every initial-scan
// job must be an nb job, and every nb job must be in the initial-scan set. This
// fails the build if a new nb scanner is added but not wired into first-connect.
func TestInitialScanJobNames(t *testing.T) {
	inInitial := make(map[string]bool, len(initialScanJobNames))
	for _, name := range initialScanJobNames {
		if inInitial[name] {
			t.Errorf("initialScanJobNames contains duplicate %q", name)
		}
		inInitial[name] = true

		provider, ok := recommendationJobProviderMap[name]
		if !ok {
			t.Errorf("initial-scan job %q is not in recommendationJobProviderMap", name)
			continue
		}
		if provider != "nb" {
			t.Errorf("initial-scan job %q has provider %q, want \"nb\"", name, provider)
		}
	}

	for name, provider := range recommendationJobProviderMap {
		if provider == "nb" && !inInitial[name] {
			t.Errorf("nb job %q is missing from initialScanJobNames", name)
		}
	}
}

// An account that is not active when its agent first connects gets no scans and
// no marker, so it still gets the batch on a connect after it is re-enabled.
func TestTriggerInitialScans_AccountNotActiveLeavesNoMarker(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectQuery("SELECT value FROM cloud_account_attrs").
		WithArgs("account-1", initialScanMarkerName).
		WillReturnRows(sqlmock.NewRows([]string{"value"}))
	mock.ExpectQuery("FROM cloud_accounts WHERE id = \\$1").
		WithArgs("account-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant", "status", "account_type"}).
			AddRow("account-1", "tenant-1", "disabled", "kubernetes"))

	err := TriggerInitialScans(security.NewRequestContextForSuperAdmin(nil, nil, nil), "account-1", "tenant-1")

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

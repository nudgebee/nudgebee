package scan_orchestrator

import (
	"log/slog"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These two SQL predicates are what keep a user's dismissal alive across a
// re-scan. A dismiss writes status = 'Dismissed' + is_dismissed = true, but the
// read path filters only on status. If the archive step tombstones a Dismissed
// row (status != 'Archive' matched it) and the upsert then reclaims status with
// a blunt EXCLUDED.status, the finding reappears as Open with is_dismissed
// stranded at true — a row in a state no code expects. The archive must touch
// Open rows only, and the upsert must never move a row out of a user-owned
// state (Dismissed/snoozed, InProgress, Closed).

// mockMetastore points the metastore at a fresh sqlmock for one test. The
// manager is memoised by database.GetDatabaseManager on first resolution, so we
// resolve whichever manager the package ended up with and swap its handle,
// restoring it on cleanup rather than leaving a closed one behind.
func mockMetastore(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{}, nil
	})
	manager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	previous := manager.Db
	t.Cleanup(func() {
		_ = db.Close()
		manager.Db = previous
	})
	manager.Db = sqlx.NewDb(db, "postgres")
	return mock
}

func persistTestCtx() *security.RequestContext {
	return security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil)
}

// TestPersistImageScanKeepsUserDecisionsAcrossRescan pins the two predicates
// that make a dismissed image-scan CVE survive the next scan of its image.
//
// image_scanner is uncron'd, so Persist issues exactly two statements — the
// per-image archive then the batch upsert — with no schedule-state writes to
// mock. sqlmock verifies the SQL shape, not a live DB: it fails if either
// predicate regresses to the form that reopened dismissed findings.
func TestPersistImageScanKeepsUserDecisionsAcrossRescan(t *testing.T) {
	mock := mockMetastore(t)

	mock.ExpectBegin()

	// Archive must tombstone Open rows ONLY. `status = 'Open'` (not the old
	// `status != 'Archive'`) is what leaves a Dismissed/snoozed/InProgress row
	// untouched so the upsert below can preserve it.
	mock.ExpectExec(`category = 'Security'[\s\S]*AND status = 'Open'`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// The upsert may move a row between the scanner-owned Open/Archive states
	// but never out of a user-owned one. The CASE guard is the whole fix; a
	// bare `status = EXCLUDED.status` is what reopened dismissed findings.
	mock.ExpectExec(`status = CASE WHEN recommendation\.status NOT IN \('Open', 'Archive'\)`).
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectCommit()

	account := ScanAccount{
		AccountID:   "acct-1",
		TenantID:    "tenant-1",
		TargetImage: "registry.example.com/app:1.2.3",
	}
	recs := []Recommendation{{
		CloudAccountID:  "acct-1",
		TenantID:        "tenant-1",
		Category:        "Security",
		RuleName:        ImageScanRuleName,
		Recommendation:  `{"image_name":"registry.example.com/app:1.2.3","cve":"CVE-2024-0001"}`,
		Severity:        "High",
		Status:          "Open",
		AccountObjectID: "CVE-2024-0001",
	}}

	err := Persist(persistTestCtx(), account, "image_scanner", recs)

	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet(),
		"archive must tombstone Open rows only, and the upsert must preserve user-owned status")
}

// TestPersistImageScan_UpsertsVulnerabilityAndLinksID pins that a
// Recommendation carrying a Vulnerability gets it upserted into
// vulnerabilities BEFORE the recommendation upsert, and that the returned id
// is threaded into vulnerability_id on the recommendation row.
func TestPersistImageScan_UpsertsVulnerabilityAndLinksID(t *testing.T) {
	mock := mockMetastore(t)

	mock.ExpectBegin()
	mock.ExpectExec(`category = 'Security'[\s\S]*AND status = 'Open'`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`INSERT INTO vulnerabilities`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "source", "vuln_id", "package_name", "package_arch"}).
			AddRow("vuln-1", models.VulnerabilitySourceImageScan, "CVE-2024-0001", "openssl", nil))
	mock.ExpectExec(`INSERT INTO recommendation`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "vuln-1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	account := ScanAccount{AccountID: "acct-1", TenantID: "tenant-1", TargetImage: "registry.example.com/app:1.2.3"}
	recs := []Recommendation{{
		CloudAccountID:  "acct-1",
		TenantID:        "tenant-1",
		Category:        "Security",
		RuleName:        ImageScanRuleName,
		Recommendation:  `{"image_name":"registry.example.com/app:1.2.3","VulnerabilityID":"CVE-2024-0001"}`,
		Severity:        "High",
		Status:          "Open",
		AccountObjectID: "registry.example.com/app:1.2.3-openssl@3.1.4-r0-CVE-2024-0001",
		Vulnerability: &models.Vulnerability{
			Source:      models.VulnerabilitySourceImageScan,
			VulnId:      "CVE-2024-0001",
			PackageName: "openssl",
			Details:     models.NewJsonObject(map[string]any{}),
		},
	}}

	err := Persist(persistTestCtx(), account, "image_scanner", recs)

	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet(),
		"the vulnerabilities upsert must run before the recommendation upsert, and its id must land in vulnerability_id")
}

package vmpackage

import (
	"encoding/json"
	"testing"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/vulnmatcher"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildFindingRows(t *testing.T) {
	now := time.Now()
	pkg := Package{Type: PkgTypeRPM, Name: "openssl", Version: "3.0.7-24.el9", Arch: "x86_64", SourceName: "openssl"}
	pkgsByKey := map[string]Package{"pkg-1": pkg}

	findings := []vulnmatcher.Finding{
		{Key: "pkg-1", VulnID: "CVE-2024-0001", Severity: "high"},
		{Key: "pkg-1", VulnID: "CVE-2024-0001", Severity: "high"}, // duplicate finding for same package+CVE
		{Key: "unknown-key", VulnID: "CVE-2024-0002", Severity: "critical"},
	}

	rows, vulnRows, vulnKeys := buildFindingRows("tenant-1", "account-1", "resource-1", findings, pkgsByKey, now)

	// The unknown-key finding is dropped, and the duplicate collapses to one row.
	require.Len(t, rows, 1)
	require.Len(t, vulnRows, 1)
	require.Len(t, vulnKeys, 1)

	row := rows[0]
	assert.Equal(t, "tenant-1", row.TenantId)
	assert.Equal(t, "account-1", row.CloudAccountId)
	require.NotNil(t, row.ResourceId)
	assert.Equal(t, "resource-1", *row.ResourceId)
	assert.Equal(t, models.RecommendationStatusOpen, row.Status)
	assert.Equal(t, recommendationCategory, row.Category)
	assert.Equal(t, recommendationRuleName, row.RuleName)
	require.NotNil(t, row.Severity)
	assert.Equal(t, "High", *row.Severity)
	require.NotNil(t, row.AccountObjectId)
	assert.Equal(t, "openssl-3.0.7-24.el9-x86_64-CVE-2024-0001", *row.AccountObjectId)

	// recommendation.recommendation carries only fixed_version/fix_state/
	// package_version for this rule type — every other display field (CVE
	// id, package name, cvss, ...) now lives only on the linked
	// vulnerabilities row, asserted below.
	rec, err := json.Marshal(row.Recommendation)
	require.NoError(t, err)
	assert.JSONEq(t, `{"fixed_version":"","fix_state":"","package_version":"3.0.7-24.el9","vulnerable_packages":["openssl"]}`, string(rec))

	vulnRow := vulnRows[0]
	assert.Equal(t, models.VulnerabilitySourceVMPackage, vulnRow.Source)
	assert.Equal(t, "CVE-2024-0001", vulnRow.VulnId)
	assert.Equal(t, "openssl", vulnRow.PackageName)
	assert.Equal(t, models.VulnerabilityKey(models.VulnerabilitySourceVMPackage, "CVE-2024-0001", "openssl", nonEmptyPtr("x86_64")), vulnKeys[0])
}

func TestBuildFindingRows_GroupsBinaryPackagesBySourceAndCVE(t *testing.T) {
	now := time.Now()
	pkgsByKey := map[string]Package{
		"core": {
			Type: PkgTypeRPM, Name: "kernel-core", Version: "5.14.0-503.14.1.el9_5", Arch: "x86_64",
			SourceName: "kernel", SourceVersion: "5.14.0-503.14.1.el9_5",
		},
		"modules": {
			Type: PkgTypeRPM, Name: "kernel-modules", Version: "5.14.0-503.14.1.el9_5", Arch: "x86_64",
			SourceName: "kernel", SourceVersion: "5.14.0-503.14.1.el9_5",
		},
		"tools": {
			Type: PkgTypeRPM, Name: "kernel-tools", Version: "5.14.0-503.14.1.el9_5", Arch: "x86_64",
			SourceName: "kernel", SourceVersion: "5.14.0-503.14.1.el9_5",
		},
	}
	findings := []vulnmatcher.Finding{
		{Key: "core", VulnID: "CVE-2024-46713", Severity: "High", FixState: "not-fixed"},
		{Key: "modules", VulnID: "CVE-2024-46713", Severity: "High", FixState: "not-fixed"},
		{Key: "tools", VulnID: "CVE-2024-46713", Severity: "High", FixState: "not-fixed"},
	}

	rows, vulnRows, vulnKeys := buildFindingRows("tenant-1", "account-1", "resource-1", findings, pkgsByKey, now)

	require.Len(t, rows, 1)
	require.Len(t, vulnRows, 1)
	require.Len(t, vulnKeys, 1)
	require.NotNil(t, rows[0].AccountObjectId)
	assert.Equal(t, "kernel-5.14.0-503.14.1.el9_5-x86_64-CVE-2024-46713", *rows[0].AccountObjectId)

	rec, err := json.Marshal(rows[0].Recommendation)
	require.NoError(t, err)
	assert.JSONEq(t, `{"fixed_version":"","fix_state":"not-fixed","package_version":"5.14.0-503.14.1.el9_5","vulnerable_packages":["kernel-core","kernel-modules","kernel-tools"]}`, string(rec))
	assert.Equal(t, "kernel", vulnRows[0].PackageName)
}

func TestCanonicalSourceName_NormalizesCloudKernelVariants(t *testing.T) {
	for _, source := range []string{
		"linux-aws-6.8",
		"linux-azure-6.8",
		"linux-gcp-6.8",
		"linux-oracle-6.8",
		"linux-hwe-22.04",
		"linux-kvm",
		"linux-lowlatency",
		"linux-meta-aws-6.8",
		"linux-raspi",
		"linux-ibm",
		"linux-riscv",
		"linux-nvidia",
		"linux-realtime",
		"linux-intel",
	} {
		t.Run(source, func(t *testing.T) {
			assert.Equal(t, "linux", canonicalSourceName(source))
		})
	}
}

func TestCanonicalSourceName_DoesNotMergeNonKernelPackages(t *testing.T) {
	for _, source := range []string{"linux-atm", "linux-base", "linux-firmware", "kernel-srpm-macros"} {
		t.Run(source, func(t *testing.T) {
			assert.Equal(t, source, canonicalSourceName(source))
		})
	}
}

func TestCanonicalVulnerablePackage_FallsBackWithoutSourceVersion(t *testing.T) {
	pkg := Package{
		Type: PkgTypeRPM, Name: "nspr", Version: "4.35.0-14.el9_4", Arch: "x86_64", SourceName: "nss",
	}
	assert.Equal(t, pkg, canonicalVulnerablePackage(pkg))
}

func TestBuildFindingRows_SortsVulnerablePackages(t *testing.T) {
	pkg := func(name string) Package {
		return Package{Type: PkgTypeRPM, Name: name, Version: "1.0", Arch: "x86_64", SourceName: "demo", SourceVersion: "1.0"}
	}
	findings := []vulnmatcher.Finding{
		{Key: "z", VulnID: "CVE-2024-1"},
		{Key: "a", VulnID: "CVE-2024-1"},
	}
	rows, _, _ := buildFindingRows("t", "a", "r", findings, map[string]Package{"z": pkg("zeta"), "a": pkg("alpha")}, time.Now())
	require.Len(t, rows, 1)
	rec, err := json.Marshal(rows[0].Recommendation)
	require.NoError(t, err)
	assert.JSONEq(t, `{"fixed_version":"","fix_state":"","package_version":"1.0","vulnerable_packages":["alpha","zeta"]}`, string(rec))
}

func TestBuildFindingRows_Empty(t *testing.T) {
	rows, vulnRows, vulnKeys := buildFindingRows("t", "a", "r", nil, map[string]Package{}, time.Now())
	assert.Empty(t, rows)
	assert.Empty(t, vulnRows)
	assert.Empty(t, vulnKeys)
}

func TestMapSeverity(t *testing.T) {
	assert.Equal(t, "Critical", mapSeverity("critical"))
	assert.Equal(t, "High", mapSeverity("HIGH"))
	assert.Equal(t, "Medium", mapSeverity("Medium"))
	assert.Equal(t, "Low", mapSeverity("low"))
	assert.Equal(t, "Info", mapSeverity("negligible"))
	assert.Equal(t, "Info", mapSeverity(""))
}

// mockDBManager is registered once for the package's test binary.
// database.GetDatabaseManager caches the manager pointer returned by the
// hook on first resolution, so every test must reuse this same pointer and
// only swap its .Db field — registering a fresh *DatabaseManager per test
// (mirroring recommendation/list_recommendation_resolutions_test.go's single-
// var pattern) would be silently ignored after the first call and tests
// would hit the previous test's already-closed sqlmock connection.
var mockDBManager = &database.DatabaseManager{}

func init() {
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return mockDBManager, nil
	})
}

// withMockDB points the shared Metastore manager at a fresh sqlmock
// connection for the duration of the test.
func withMockDB(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mockDBManager.Db = sqlx.NewDb(db, "postgres")
	return mock
}

func TestUpsertPackages_ArchivesAndUpserts(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE vm_package SET is_active = false").
		WithArgs("resource-1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("INSERT INTO vm_package").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := upsertPackages("tenant-1", "account-1", "resource-1", "ubuntu", "22.04", []Package{
		{Type: PkgTypeDeb, Name: "bash", Version: "5.1-6ubuntu1", Arch: "amd64", SourceName: "bash"},
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpsertPackages_RefreshesSourceVersionOnConflict pins the second half of
// #36278. source_name is part of the conflict key but source_version is not, so
// unless the DO UPDATE list refreshes it, a row first written without one keeps
// the empty value through every future rescan — last_seen_at moves, the column
// never fills in. Proved against dev: a full 494-package rescan left all 494
// rows with source_version = ” until this column was added to the SET list.
func TestUpsertPackages_RefreshesSourceVersionOnConflict(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE vm_package SET is_active = false").
		WithArgs("resource-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// The upsert must carry source_version through to the conflict branch.
	mock.ExpectExec("ON CONFLICT .* DO UPDATE SET .*source_version = EXCLUDED.source_version").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := upsertPackages("tenant-1", "account-1", "resource-1", "amazonlinux", "2023", []Package{
		{Type: PkgTypeRPM, Name: "perl-B", Version: "1.80-477.amzn2023.0.9", Arch: "x86_64",
			SourceName: "perl", SourceVersion: "5.32.1-477.amzn2023.0.9"},
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertPackages_EmptyPackages_OnlyArchives(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE vm_package SET is_active = false").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := upsertPackages("tenant-1", "account-1", "resource-1", "ubuntu", "22.04", nil)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPersistFindings_ArchivesAndUpserts(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE recommendation SET status = 'Archive'").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectQuery("INSERT INTO vulnerabilities").
		WillReturnRows(sqlmock.NewRows([]string{"id", "source", "vuln_id", "package_name", "package_arch"}).
			AddRow("vuln-1", models.VulnerabilitySourceVMPackage, "CVE-2024-0001", "openssl", "x86_64"))
	mock.ExpectExec("INSERT INTO recommendation").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	pkg := Package{Type: PkgTypeRPM, Name: "openssl", Version: "3.0.7-24.el9", Arch: "x86_64", SourceName: "openssl"}
	err := persistFindings("tenant-1", "account-1", "resource-1",
		[]vulnmatcher.Finding{{Key: "pkg-1", VulnID: "CVE-2024-0001", Severity: "High"}},
		map[string]Package{"pkg-1": pkg},
	)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPersistFindings_NoFindings_OnlyArchives(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE recommendation SET status = 'Archive'").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := persistFindings("tenant-1", "account-1", "resource-1", nil, nil)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

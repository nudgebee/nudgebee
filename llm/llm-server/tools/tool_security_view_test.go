package tools

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

const (
	securityViewTestAccount = "550e8400-e29b-41d4-a716-446655440000"
	securityViewTestTenant  = "660e8400-e29b-41d4-a716-446655440000"
	securityViewTestImage   = "registry.example.com/web:1.0"
)

// Automation packs outside this repo query security_view by name, so its
// shape is a contract: renaming one of these must fail a test, not their runs.
var (
	// Columns the packs select, filter or group on.
	securityViewContractColumns = []string{"image", "namespace", "workload_name", "workload_type", "severity", "status", "category", "vulnerability_id", "recommendation"}
	// Keys the view rebuilds into `recommendation` from the linked
	// vulnerabilities row. FixedVersion and InstalledVersion are the finding's
	// own keys and survive because the view merges onto r.recommendation.
	securityViewContractRebuiltKeys = []string{"PkgName", "PkgIdentifier"}
)

// TestSecurityViewContractShape runs without a database, so CI catches a
// renamed column or key in the view's text.
func TestSecurityViewContractShape(t *testing.T) {
	// The view's output list: from the outer select to its FROM.
	outer := regexp.MustCompile(`(?is)\)\s*select\b(.*?)\bfrom\s+pod_container\b`).FindStringSubmatch(SecurityView)
	require.Len(t, outer, 2, "security_view's outer select not found")
	selectList := outer[1]
	for _, col := range securityViewContractColumns {
		// Either aliased ("... as image,") or a bare column ("r.status,").
		assert.Regexp(t, regexp.MustCompile(`(?i)(\bas\s+|\b\w+\.)"?`+col+`"?\s*(,|\n)`), selectList, "security_view must still return column %q", col)
	}
	for _, key := range securityViewContractRebuiltKeys {
		assert.Contains(t, SecurityView, "'"+key+"',", "security_view must still rebuild recommendation key %q", key)
	}
	assert.Contains(t, SecurityView, "r.recommendation || jsonb_build_object(", "the rebuilt keys must merge onto the finding's own body, which holds FixedVersion and InstalledVersion")
}

// setupSecurityViewDB points the metastore at a fresh schema holding the
// tables the view reads (only the columns it reads) and one running pod of
// securityViewTestImage. Set METASTORE_SQL_TEST_DB_URL to a disposable
// database to run the tests that use it; the schema is dropped afterwards.
func setupSecurityViewDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("METASTORE_SQL_TEST_DB_URL")
	if dsn == "" {
		t.Skip("METASTORE_SQL_TEST_DB_URL not set")
	}
	schema := fmt.Sprintf("security_view_test_%d", time.Now().UnixNano())
	admin, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	admin.MustExec("CREATE SCHEMA " + schema)
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	admin.MustExec(fmt.Sprintf(`
		CREATE TABLE %[1]s.k8s_pods (workload_name text, workload_type text, namespace text,
			cloud_account_id uuid, tenant_id uuid, meta jsonb, is_active boolean);
		CREATE TABLE %[1]s.vulnerabilities (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), vuln_id text,
			package_name text, severity text, description text, details jsonb);
		CREATE TABLE %[1]s.recommendation (id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			cloud_account_id uuid, tenant_id uuid, severity text, status text, category text,
			rule_name text, account_object_id text, recommendation jsonb, vulnerability_id uuid,
			created_at timestamptz DEFAULT now(), updated_at timestamptz DEFAULT now());`, schema))
	admin.MustExec(fmt.Sprintf(`INSERT INTO %s.k8s_pods VALUES ('web', 'Deployment', 'default', $1, $2,
		jsonb_build_object('config', jsonb_build_object('containers', jsonb_build_array(jsonb_build_object('image', $3::text)))), true)`, schema),
		securityViewTestAccount, securityViewTestTenant, securityViewTestImage)

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sqlx.Connect("postgres", dsn+sep+"search_path="+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	common.ResetDatabaseManager(common.Metastore)
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: db}, nil
	})
	t.Cleanup(func() { common.ResetDatabaseManager(common.Metastore) })
	return db
}

// insertSecurityViewFinding adds one finding on securityViewTestImage; body is
// its recommendation JSON without image_name.
func insertSecurityViewFinding(t *testing.T, db *sqlx.DB, status, ruleName, severity, objectID, body string, vulnerabilityID *string) {
	t.Helper()
	db.MustExec(`INSERT INTO recommendation
		(cloud_account_id, tenant_id, severity, status, category, rule_name, account_object_id, recommendation, vulnerability_id)
		VALUES ($1, $2, $3, $4, 'Security', $5, $6, $7::jsonb || jsonb_build_object('image_name', $8::text), $9)`,
		securityViewTestAccount, securityViewTestTenant, severity, status, ruleName, objectID, body, securityViewTestImage, vulnerabilityID)
}

func securityViewRows(t *testing.T, command string) []map[string]any {
	t.Helper()
	nbCtx := core.NbToolContext{AccountId: securityViewTestAccount, Ctx: security.NewRequestContext(context.Background(), nil, nil, nil, nil)}
	resp, err := SecurityExecuteTool{}.Call(nbCtx, core.NBToolCallRequest{Command: command})
	require.NoError(t, err)
	var rows []map[string]any
	if strings.Contains(resp.Data, `"rows":[]`) {
		return rows
	}
	require.NoError(t, common.UnmarshalJson([]byte(resp.Data), &rows), resp.Data)
	return rows
}

// TestSecurityViewListsOpenFindingsOnly: one image carries an open finding, a
// dismissed, an archived and an in-progress one, plus its scan-summary row.
// Only the open CVE may come back: the others are not open vulnerabilities,
// and an answer that counts them overstates what is left to fix.
func TestSecurityViewListsOpenFindingsOnly(t *testing.T) {
	db := setupSecurityViewDB(t)
	for _, f := range []struct{ status, rule, cve string }{
		{"Open", "image_scan", "CVE-2026-0001"},
		{"Dismissed", "image_scan", "CVE-2026-0002"},
		{"Archive", "image_scan", "CVE-2026-0003"},
		{"InProgress", "image_scan", "CVE-2026-0004"},
		{"Open", "image_scan_summary", ""},
	} {
		insertSecurityViewFinding(t, db, f.status, f.rule, "High", securityViewTestImage+"-"+f.cve, fmt.Sprintf(`{"VulnerabilityID": %q}`, f.cve), nil)
	}

	rows := securityViewRows(t, "select vulnerability_id, status, category from security_view order by vulnerability_id")
	require.Len(t, rows, 1, "only the open CVE is a finding: %v", rows)
	assert.Equal(t, "CVE-2026-0001", rows[0]["vulnerability_id"])
	assert.Equal(t, "Open", rows[0]["status"])
	assert.Equal(t, "image_scan", rows[0]["category"])
}

// TestSecurityViewContractQueries runs the queries an external automation pack
// sends, verbatim, against a finding linked to its vulnerabilities row: the
// columns and rebuilt keys they read must still resolve.
func TestSecurityViewContractQueries(t *testing.T) {
	db := setupSecurityViewDB(t)
	var vulnID string
	require.NoError(t, db.Get(&vulnID, `INSERT INTO vulnerabilities (vuln_id, package_name, severity, details)
		VALUES ('CVE-2026-0101', 'openssl', 'Critical', '{"pkg_id": "openssl@3.0.11-1~deb12u2",
			"pkg_identifier": {"PURL": "pkg:deb/debian/openssl@3.0.11-1~deb12u2?distro=debian-12"}}') RETURNING id`))
	insertSecurityViewFinding(t, db, "Open", "image_scan", "Critical", securityViewTestImage+"-openssl-CVE-2026-0101",
		`{"FixedVersion": "3.0.13-1~deb12u1", "InstalledVersion": "3.0.11-1~deb12u2"}`, &vulnID)

	q1 := `SELECT image, namespace, workload_name, workload_type, severity, count(DISTINCT vulnerability_id) FROM security_view WHERE category='image_scan' AND status='Open' AND severity IN ('Critical','High') AND recommendation::jsonb->>'FixedVersion' <> '' AND recommendation::jsonb->>'PkgIdentifier' ~ '"pkg:(deb|apk|rpm)/' GROUP BY 1,2,3,4,5 LIMIT 100`
	rows := securityViewRows(t, q1)
	require.Len(t, rows, 1, "Q1: %v", rows)
	assert.Equal(t, securityViewTestImage, rows[0]["image"])
	assert.Equal(t, "default", rows[0]["namespace"])
	assert.Equal(t, "web", rows[0]["workload_name"])
	assert.Equal(t, "Deployment", rows[0]["workload_type"])
	assert.Equal(t, "Critical", rows[0]["severity"])

	q2 := `SELECT recommendation::jsonb->>'PkgName' pkg, recommendation::jsonb->>'InstalledVersion' installed, string_agg(DISTINCT recommendation::jsonb->>'FixedVersion', ' | ') fixed, string_agg(DISTINCT vulnerability_id, ',') ids, max(CASE severity WHEN 'Critical' THEN 2 ELSE 1 END) sev FROM security_view WHERE image='` + securityViewTestImage + `' AND category='image_scan' AND status='Open' AND severity IN ('Critical','High') AND recommendation::jsonb->>'FixedVersion' <> '' AND recommendation::jsonb->>'PkgIdentifier' ~ '"pkg:(deb|apk|rpm)/' GROUP BY 1,2 LIMIT 100`
	rows = securityViewRows(t, q2)
	require.Len(t, rows, 1, "Q2: %v", rows)
	assert.Equal(t, "openssl", rows[0]["pkg"])
	assert.Equal(t, "3.0.11-1~deb12u2", rows[0]["installed"])
	assert.Equal(t, "3.0.13-1~deb12u1", rows[0]["fixed"])
	assert.Equal(t, "CVE-2026-0101", rows[0]["ids"])
}

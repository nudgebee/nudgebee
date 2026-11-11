package notification

import (
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Runs the "NudgeBee's own workloads" lookups against Postgres: hiding is on
// when an account has no setting, the agent's install namespace and the listed
// namespaces are hidden, turning it off hides nothing, and a malformed or
// unrelated attribute never breaks the query.
func TestOwnWorkloads_DB(t *testing.T) {
	url := os.Getenv("APP_DATABASE_URL")
	if url == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but APP_DATABASE_URL is empty")
		}
		t.Skip("skipping: APP_DATABASE_URL is not set")
	}
	db, err := sqlx.Connect("postgres", url)
	if err != nil {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatalf("REQUIRE_DB_TESTS is set but the database is not accessible: %v", err)
		}
		t.Skipf("skipping: database not accessible: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	const schema = "zz_own_workloads_test"
	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)
	mustExec(`CREATE SCHEMA ` + schema)
	mustExec(`SET search_path TO ` + schema)
	mustExec(`CREATE TABLE cloud_accounts (id uuid PRIMARY KEY, tenant uuid)`)
	mustExec(`CREATE TABLE agent (cloud_account_id uuid, type text, connection_status jsonb)`)
	mustExec(`CREATE TABLE cloud_account_attrs (cloud_account_id uuid, name text, value text)`)
	mustExec(`CREATE TABLE recommendation (id uuid PRIMARY KEY, tenant_id uuid, cloud_account_id uuid, rule_name text,
		account_object_id text, recommendation jsonb)`)
	mustExec(`CREATE TABLE k8s_pods (tenant_id uuid, cloud_account_id uuid, namespace text, is_active boolean, meta jsonb)`)
	mustExec(`CREATE TABLE events (id uuid PRIMARY KEY, cloud_account_id uuid, subject_namespace text)`)

	const tenant = "00000000-0000-0000-0000-0000000000f1"
	const otherTenant = "00000000-0000-0000-0000-0000000000f2"
	const (
		noSetting  = "00000000-0000-0000-0000-0000000000a1"
		turnedOff  = "00000000-0000-0000-0000-0000000000a2"
		withServer = "00000000-0000-0000-0000-0000000000a3"
		nullList   = "00000000-0000-0000-0000-0000000000a4"
		otherOrg   = "00000000-0000-0000-0000-0000000000b1"
	)
	mustExec(`INSERT INTO cloud_accounts VALUES ($1, $5), ($2, $5), ($3, $5), ($4, $5), ($6, $7)`,
		noSetting, turnedOff, withServer, nullList, tenant, otherOrg, otherTenant)
	mustExec(`INSERT INTO agent VALUES
		($1, 'k8s', '{"installationNamespace": "acme-observability"}'),
		($1, 'k8s', '{"installationNamespace": "acme-observability"}'),
		($2, 'k8s', '{"installationNamespace": "nudgebee-agent"}'),
		($3, 'k8s', '{"installationNamespace": "nudgebee-agent"}'),
		($4, 'k8s', '{"installationNamespace": "nb"}'),
		($4, 'AWS', '{"installationNamespace": "not-a-cluster"}'),
		($5, 'k8s', '{"installationNamespace": "nudgebee-agent"}')`,
		noSetting, turnedOff, withServer, nullList, otherOrg)
	mustExec(`INSERT INTO cloud_account_attrs VALUES
		($1, 'log_labels', 'not json at all'),
		($2, 'nudgebee_workloads', '{"hide_in_notifications": false, "namespaces": ["nudgebee"]}'),
		($3, 'nudgebee_workloads', '{"hide_in_notifications": true, "namespaces": ["acme-nudgebee"]}'),
		($4, 'nudgebee_workloads', '{"namespaces": null}')`,
		noSetting, turnedOff, withServer, nullList)

	own, err := OwnNamespacesByAccount(db, tenant)
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{
		noSetting:  {"acme-observability"},
		withServer: {"acme-nudgebee", "nudgebee-agent"},
		nullList:   {"nb"},
	}, own)

	for _, tc := range []struct {
		account, namespace string
		want               bool
	}{
		{noSetting, "acme-observability", true},
		{noSetting, "payments", false},
		{turnedOff, "nudgebee-agent", false},
		{turnedOff, "nudgebee", false},
		{withServer, "acme-nudgebee", true},
		{nullList, "not-a-cluster", false},
		{noSetting, "", false},
		{"", "acme-observability", false},
	} {
		got, err := isOwnWorkload(db, tc.account, tc.namespace)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got, "%s/%s", tc.account, tc.namespace)
	}

	mustExec(`INSERT INTO recommendation VALUES
		('00000000-0000-0000-0000-000000000001', $3, $1, 'pod_right_sizing', 'acme-observability/Deployment/agent', '{"agent": [{"add_info": {"estimated_savings": -9.5}}]}'),
		('00000000-0000-0000-0000-000000000002', $3, $1, 'replica_right_sizing', 'x', '{"metadata": {"namespace": "acme-observability"}}'),
		('00000000-0000-0000-0000-000000000003', $3, $1, 'workload_misconfigurations', 'acme-observability/Deployment/agent', '[{"level": "warning"}]'),
		('00000000-0000-0000-0000-000000000004', $3, $1, 'pod_right_sizing', 'payments/Deployment/api', '{"namespace": "payments"}'),
		('00000000-0000-0000-0000-000000000005', $3, $1, 'aws_ec2_underutilized', 'i-123', '{"cpu": 3}'),
		('00000000-0000-0000-0000-000000000006', $3, $2, 'pod_right_sizing', 'nudgebee-agent/Deployment/agent', '{"agent": []}'),
		('00000000-0000-0000-0000-000000000011', $3, $1, 'pv_rightsize', 'acme-observability/data-agent-0', '{}'),
		('00000000-0000-0000-0000-000000000012', $3, $1, 'aws_elb_idle', 'arn:aws:elasticloadbalancing:us-east-1:1:loadbalancer/app/acme-observability/x', '{}'),
		('00000000-0000-0000-0000-000000000013', $3, $1, 'pod_right_sizing', 'payments/Deployment/api', '{"api": []}')`,
		noSetting, turnedOff, tenant)

	// Image-scan findings name an image, not a namespace: one is NudgeBee's own
	// only when every active pod running the image is in an own namespace.
	pod := func(account, namespace string, active bool, images ...string) {
		containers := "["
		for i, image := range images {
			if i > 0 {
				containers += ","
			}
			containers += `{"image": "` + image + `"}`
		}
		mustExec(`INSERT INTO k8s_pods VALUES ($1, $2, $3, $4, jsonb_build_object('config', jsonb_build_object('containers', $5::jsonb)))`,
			tenant, account, namespace, active, containers+"]")
	}
	pod(noSetting, "acme-observability", true, "registry/agent:1", "registry/shared:1")
	pod(noSetting, "payments", true, "registry/shared:1")
	pod(noSetting, "payments", false, "registry/agent:1") // an old pod no longer running
	mustExec(`INSERT INTO k8s_pods VALUES ($1, $2, 'acme-observability', true, '{"config": {"containers": {"not": "a list"}}}')`, tenant, noSetting)
	mustExec(`INSERT INTO recommendation VALUES
		('00000000-0000-0000-0000-000000000007', $2, $1, 'image_scan', 'registry/agent:1', '{"image_name": "registry/agent:1"}'),
		('00000000-0000-0000-0000-000000000008', $2, $1, 'image_scan', 'registry/shared:1', '{"image_name": "registry/shared:1"}'),
		('00000000-0000-0000-0000-000000000009', $2, $1, 'image_scan', 'registry/gone:1', '{"image_name": "registry/gone:1"}'),
		('00000000-0000-0000-0000-000000000010', $2, $1, 'image_scan', 'x', '{}'),
		('00000000-0000-0000-0000-000000000014', $2, $1, 'image_scan_summary', 'registry/agent:1', '{"image_name": "registry/agent:1"}')`,
		noSetting, tenant)

	for id, want := range map[string]bool{
		"00000000-0000-0000-0000-000000000001": true,
		"00000000-0000-0000-0000-000000000002": true,
		"00000000-0000-0000-0000-000000000003": true,
		"00000000-0000-0000-0000-000000000004": false,
		"00000000-0000-0000-0000-000000000005": false,
		"00000000-0000-0000-0000-000000000006": false,
		"00000000-0000-0000-0000-000000000007": true,
		"00000000-0000-0000-0000-000000000008": false,
		"00000000-0000-0000-0000-000000000009": false,
		"00000000-0000-0000-0000-000000000010": false,
		"00000000-0000-0000-0000-000000000011": true,
		"00000000-0000-0000-0000-000000000012": false,
		"00000000-0000-0000-0000-000000000013": false,
		"00000000-0000-0000-0000-000000000014": true,
	} {
		got, err := isOwnRecommendation(db, id)
		require.NoError(t, err)
		assert.Equal(t, want, got, id)
	}

	mustExec(`INSERT INTO events VALUES
		('00000000-0000-0000-0000-000000000011', $1, 'acme-observability'),
		('00000000-0000-0000-0000-000000000012', $1, 'payments'),
		('00000000-0000-0000-0000-000000000013', $2, 'nudgebee-agent')`,
		noSetting, turnedOff)
	var kept []string
	require.NoError(t, db.Select(&kept, `SELECT e.subject_namespace FROM events e WHERE `+NotOwnEventSQL("e")+` ORDER BY 1`))
	assert.Equal(t, []string{"nudgebee-agent", "payments"}, kept)
}

// DatabaseManager's query helpers turn every "?" into a bind parameter, even
// inside a string literal, so a "?" in these fragments breaks the queries that
// embed them.
func TestOwnWorkloadFragments_NoQuestionMark(t *testing.T) {
	for name, fragment := range map[string]string{
		"NotOwnRecommendationSQL": NotOwnRecommendationSQL,
		"NotOwnImageSQL":          NotOwnImageSQL("$1"),
		"NotOwnEventSQL":          NotOwnEventSQL("e"),
	} {
		assert.NotContains(t, fragment, "?", name)
	}
}

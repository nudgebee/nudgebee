package query

import (
	"os"
	"strings"
	"testing"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// The recommendations agent reads the same namespace through its own copy of
// the fragment; a drift shows a finding's namespace in the UI but not to Nubi.
func TestPopeyeNamespaceSQLMatchesLLMServerCopy(t *testing.T) {
	raw, err := os.ReadFile("../../../llm/llm-server/tools/tool_recommendation.go")
	require.NoError(t, err)
	const marker = "const popeyeNamespaceSQL = `"
	src := string(raw)
	start := strings.Index(src, marker)
	require.GreaterOrEqual(t, start, 0, "llm-server no longer declares popeyeNamespaceSQL")
	rest := src[start+len(marker):]
	require.Equal(t, models.PopeyeNamespaceSQL, rest[:strings.Index(rest, "`")])
}

// TestPopeyeNamespaceSQL_DB evaluates the fragment on a real Postgres: the
// regex and split_part edge cases are invisible to Go.
func TestPopeyeNamespaceSQL_DB(t *testing.T) {
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

	cases := []struct {
		rule, objectID string
		want           *string
	}{
		{"deployments_misconfigurations", "shop/Deployment/web", ptr("shop")},
		{"secrets_misconfigurations", "shop/Secret/creds", ptr("shop")},
		{"namespaces_misconfigurations", "/Namespace/shop", ptr("shop")},
		{"clusterroles_misconfigurations", "/ClusterRole/admin", nil},
		{"nodes_misconfigurations", "/Node/node-1", nil},
		// Aggregate rows keyed on the bare kind carry no namespace.
		{"services_misconfigurations", "services", nil},
		{"misconfigurations", "shop/Pod/web-abc", nil},
		{"image_scan", "docker.io/library/nginx:1.25", nil},
		{"unused_pvc", "shop/PersistentVolumeClaim/data", nil},
	}
	for _, c := range cases {
		var got *string
		require.NoError(t, db.Get(&got,
			`SELECT `+models.PopeyeNamespaceSQL+` FROM (SELECT $1::text AS rule_name, $2::text AS account_object_id) r`,
			c.rule, c.objectID))
		require.Equal(t, c.want, got, "%s %s", c.rule, c.objectID)
	}
}

func ptr(s string) *string { return &s }

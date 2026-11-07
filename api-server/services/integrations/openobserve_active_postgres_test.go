package integrations

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"testing"
	"time"

	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// TestOpenObserveActiveLookupPostgres uses temporary tables on one connection.
// Set OO_TEST_PG_DSN to run it; the child process isolates the global DB hook.
func TestOpenObserveActiveLookupPostgres(t *testing.T) {
	dsn := os.Getenv("OO_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set OO_TEST_PG_DSN to run the PostgreSQL regression")
	}
	if os.Getenv("OO_ACTIVE_LOOKUP_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOpenObserveActiveLookupPostgres$", "-test.v")
		cmd.Env = append(os.Environ(), "OO_ACTIVE_LOOKUP_CHILD=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "isolated test failed:\n%s", output)
		return
	}

	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, err = db.Exec(`
CREATE TEMP TABLE cloud_accounts (id uuid PRIMARY KEY, tenant uuid NOT NULL);
CREATE TEMP TABLE integrations (id uuid PRIMARY KEY, tenant_id uuid NOT NULL, name text NOT NULL, labels jsonb NOT NULL, source text NOT NULL, type text NOT NULL, status text NOT NULL);
CREATE TEMP TABLE integrations_cloud_accounts (integration_id uuid NOT NULL, cloud_account_id uuid NOT NULL, tenant_id uuid NOT NULL, link_role text NOT NULL);
CREATE TEMP TABLE integration_config_values (integration_id uuid NOT NULL, name text NOT NULL, value text NOT NULL, is_encrypted boolean NOT NULL);
INSERT INTO cloud_accounts VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001'), ('20000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000002');
INSERT INTO integrations VALUES
 ('00000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','old','{}','user','openobserve','disabled'),
 ('00000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','replacement','{}','user','openobserve','enabled'),
 ('00000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000001','wrong-link-tenant','{}','user','openobserve','enabled'),
 ('00000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000001','wrong-account-tenant','{}','user','openobserve','enabled');
INSERT INTO integrations_cloud_accounts VALUES
 ('00000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','own'),
 ('00000000-0000-0000-0000-000000000002','20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','own'),
 ('00000000-0000-0000-0000-000000000002','20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','discovery_target'),
 ('00000000-0000-0000-0000-000000000003','20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000002','own'),
 ('00000000-0000-0000-0000-000000000004','20000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','own');
INSERT INTO integration_config_values VALUES
 ('00000000-0000-0000-0000-000000000001','openobserve_url','https://old.example',false),
 ('00000000-0000-0000-0000-000000000002','openobserve_url','https://replacement.example',false);`)
	require.NoError(t, err)
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: db}, nil
	})
	sc := security.NewSecurityContextForTenantAdmin("10000000-0000-0000-0000-000000000001")
	require.NotNil(t, sc)
	ctx := security.NewRequestContext(context.Background(), sc, slog.Default(), nil, nil)
	const account = "20000000-0000-0000-0000-000000000001"

	cfg, err := GetOpenObserveConfigs(ctx, account)
	require.NoError(t, err)
	require.Equal(t, "https://replacement.example", cfg.URL)
	_, err = GetOpenObserveConfigs(ctx, "20000000-0000-0000-0000-000000000002")
	require.Error(t, err, "a malformed cross-tenant account link must not resolve")

	preview := core.WithConfigOverride(ctx, core.ConfigOverride{
		AccountId: account, IntegrationName: IntegrationOpenObserve,
		Values: []core.IntegrationConfigValue{{Name: "openobserve_url", Value: "https://preview.example"}},
	})
	cfg, err = GetOpenObserveConfigs(preview, account)
	require.NoError(t, err)
	require.Equal(t, "https://preview.example", cfg.URL)
	_, err = GetOpenObserveConfigs(preview, "20000000-0000-0000-0000-000000000002")
	require.Error(t, err, "preview must not leak to another account")

	_, err = db.Exec(`UPDATE integrations_cloud_accounts SET tenant_id='10000000-0000-0000-0000-000000000001' WHERE integration_id='00000000-0000-0000-0000-000000000003'`)
	require.NoError(t, err)
	_, err = GetOpenObserveConfigs(ctx, account)
	require.ErrorContains(t, err, "multiple active")
	_, err = db.Exec(`UPDATE integrations SET status='disabled' WHERE name IN ('replacement','wrong-link-tenant')`)
	require.NoError(t, err)
	_, err = GetOpenObserveConfigs(ctx, account)
	require.ErrorContains(t, err, "not found")
}

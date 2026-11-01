package vmpackage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"nudgebee/services/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realDiscoveryInventoryResponse is a trimmed capture of a real
// discovery_inventory response (full package lists shortened to a few
// representative lines) — pins the actual wire shape rather than an
// invented one.
const realDiscoveryInventoryResponse = `{
  "status_code": 200,
  "request_id": "1bfaa67e-bcfe-441f-b07f-d78f91c034f0",
  "action": "discovery_inventory",
  "result": {
    "content_pack_version": 2,
    "targets": [
      {
        "host": "172.31.0.11",
        "status": "ok",
        "facts": {
          "arch": "x86_64",
          "os_family": "debian",
          "os_id": "ubuntu",
          "os_major": "22"
        },
        "collectors": {
          "hostname": "ip-172-31-0-11.ec2.internal\n",
          "machine-id": "ec2403e319a2f3f0ae53a05e3daf084b\n",
          "os-release": "PRETTY_NAME=\"Ubuntu 22.04.5 LTS\"\nNAME=\"Ubuntu\"\nVERSION_ID=\"22.04\"\nID=ubuntu\nID_LIKE=debian\n",
          "pkgs-dpkg": "acpid\t1:2.0.33-1ubuntu1\tamd64\tinstalled\tacpid\t1:2.0.33-1ubuntu1\nbash\t5.1-6ubuntu1.1\tamd64\tinstalled\tbash\t5.1-6ubuntu1.1\n"
        }
      }
    ]
  }
}`

func TestParseDiscoveryInventoryResult_RealResponseShape(t *testing.T) {
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(realDiscoveryInventoryResponse), &result))

	target, err := parseDiscoveryInventoryResult(result)
	require.NoError(t, err)

	assert.Equal(t, "172.31.0.11", target.Host)
	assert.Equal(t, "ok", target.Status)
	assert.Contains(t, target.Collectors["os-release"], "VERSION_ID=\"22.04\"")
	assert.Contains(t, target.Collectors["pkgs-dpkg"], "bash\t5.1-6ubuntu1.1")

	family, version, err := ParseOSRelease(target.Collectors["os-release"])
	require.NoError(t, err)
	assert.Equal(t, "ubuntu", family)
	assert.Equal(t, "22.04", version)

	pkgs, err := parsePackages(target.Collectors)
	require.NoError(t, err)
	require.Len(t, pkgs, 2)
	assert.Equal(t, "acpid", pkgs[0].Name)
}

func TestParseDiscoveryInventoryResult_NoTargets(t *testing.T) {
	_, err := parseDiscoveryInventoryResult(map[string]any{
		"result": map[string]any{"targets": []any{}},
	})
	assert.Error(t, err)
}

func TestParseDiscoveryInventoryResult_TargetNotOK(t *testing.T) {
	_, err := parseDiscoveryInventoryResult(map[string]any{
		"result": map[string]any{
			"targets": []any{
				map[string]any{"host": "10.0.0.5", "status": "unreachable"},
			},
		},
	})
	assert.Error(t, err)
}

func TestParsePackages_NoCollector(t *testing.T) {
	_, err := parsePackages(map[string]string{"os-release": "ID=ubuntu\nVERSION_ID=22.04\n"})
	assert.Error(t, err)
}

func TestParseDiscoveryInventoryResultBatch_MultipleTargets(t *testing.T) {
	targets, err := parseDiscoveryInventoryResultBatch(map[string]any{
		"result": map[string]any{
			"targets": []any{
				map[string]any{"host": "10.0.0.5", "status": "ok"},
				map[string]any{"host": "10.0.0.6", "status": "ssh-auth-failed", "error": "auth failed"},
				map[string]any{"host": "10.0.0.7", "status": "timeout"},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, targets, 3)
	assert.Equal(t, "ok", targets[0].Status)
	assert.Equal(t, "ssh-auth-failed", targets[1].Status)
	assert.Equal(t, "timeout", targets[2].Status)
}

func TestParseDiscoveryInventoryResultBatch_NoTargets(t *testing.T) {
	_, err := parseDiscoveryInventoryResultBatch(map[string]any{
		"result": map[string]any{"targets": []any{}},
	})
	assert.Error(t, err)
}

// realDiscoverySweepResponse is shaped after forager's SweepResult
// (pkg/proxy/discovery/sweep.go) as wrapped by the relay's ActionResponse
// envelope, mirroring realDiscoveryInventoryResponse above.
const realDiscoverySweepResponse = `{
  "status_code": 200,
  "request_id": "9f3a1c2e-1111-4444-8888-abcdefabcdef",
  "action": "discovery_sweep",
  "result": {
    "cidrs": ["172.31.0.0/28"],
    "addresses_scanned": 14,
    "rate_pps": 100,
    "addresses_excluded": 0,
    "hosts": [
      {"ip": "172.31.0.11", "mac": "02:42:ac:1f:00:0b", "rdns": "", "open_ports": [22], "sources": ["tcp"]},
      {"ip": "172.31.0.12", "open_ports": [3389], "sources": ["tcp"]}
    ],
    "duration_seconds": 1.2
  }
}`

func TestDiscoverySweepResponse_RealShape(t *testing.T) {
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(realDiscoverySweepResponse), &result))

	raw, err := json.Marshal(result)
	require.NoError(t, err)
	var resp discoverySweepResponse
	require.NoError(t, json.Unmarshal(raw, &resp))

	require.Len(t, resp.Result.Hosts, 2)
	assert.Equal(t, "172.31.0.11", resp.Result.Hosts[0].IP)
	assert.Equal(t, []int{22}, resp.Result.Hosts[0].OpenPorts)
	assert.Equal(t, "172.31.0.12", resp.Result.Hosts[1].IP)
	assert.Empty(t, resp.Result.Hosts[1].MAC)
}

const scanDatasourceID = "11111111-2222-4333-8444-555555555555"

// TestResolveScanDatasource_ScopesThroughAccountLinkTable pins that account
// scoping goes through integrations_cloud_accounts (own or discovery_target),
// and that the relay is routed through the agent's own account rather than
// the scanned one.
func TestResolveScanDatasource_ScopesThroughAccountLinkTable(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"datasource_key", "agent_account_id"}).
		AddRow("forager-discovery", "agent-account")
	mock.ExpectQuery(`JOIN integrations_cloud_accounts own ON own.integration_id = i.id AND own.link_role = 'own'(.|\n)*`+
		`link.cloud_account_id = \$2\s+AND link.link_role IN \('own', 'discovery_target'\)`).
		WithArgs(scanDatasourceID, "target-account", "tenant-1").
		WillReturnRows(rows)

	ds, err := resolveScanDatasource("target-account", "tenant-1", scanDatasourceID)
	require.NoError(t, err)
	assert.Equal(t, "forager-discovery", ds.Key)
	assert.Equal(t, "agent-account", ds.AgentAccountID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestResolveScanDatasource_NotLinkedIsBadRequest: a datasource that isn't
// linked to the account must fail with a clear 400, never fall back to the
// integration id — forager only knows keys, so that fallback is what produced
// "datasource <uuid> not found" for every single-VM scan.
func TestResolveScanDatasource_NotLinkedIsBadRequest(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("SELECT icv.value AS datasource_key").
		WithArgs(scanDatasourceID, "account-1", "tenant-1").
		WillReturnError(sql.ErrNoRows)

	ds, err := resolveScanDatasource("account-1", "tenant-1", scanDatasourceID)
	require.Error(t, err)
	var apiErr common.Error
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, 400, apiErr.Code)
	assert.Contains(t, err.Error(), "not an enabled discovery datasource linked to this account")
	assert.Empty(t, ds.Key)
}

func TestResolveScanDatasource_DBErrorNotMaskedAsNotLinked(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("SELECT icv.value AS datasource_key").
		WithArgs(scanDatasourceID, "account-1", "tenant-1").
		WillReturnError(errors.New("connection reset by peer"))

	_, err := resolveScanDatasource("account-1", "tenant-1", scanDatasourceID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection reset by peer")
	assert.NotContains(t, err.Error(), "linked to this account")
}

func TestResolveScanDatasource_NonUUIDIsAlreadyAKey(t *testing.T) {
	mock := withMockDB(t)

	ds, err := resolveScanDatasource("account-1", "tenant-1", "forager-discovery")
	require.NoError(t, err)
	assert.Equal(t, "forager-discovery", ds.Key)
	assert.Equal(t, "account-1", ds.AgentAccountID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestResolveScanDatasource_NoIntegrationsAccountIDColumn guards the root
// cause of single-VM scans always failing: integrations.account_id was dropped in V573, so filtering
// on it errors on every call — an error the old fallback swallowed.
func TestResolveScanDatasource_NoIntegrationsAccountIDColumn(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_, actual string) error {
		if strings.Contains(actual, "i.account_id") {
			return fmt.Errorf("query filters on dropped column integrations.account_id: %s", actual)
		}
		return nil
	})))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mockDBManager.Db = sqlx.NewDb(db, "postgres")
	mock.ExpectQuery("").
		WillReturnRows(sqlmock.NewRows([]string{"datasource_key", "agent_account_id"}).AddRow("k", "a"))

	_, err = resolveScanDatasource("account-1", "tenant-1", scanDatasourceID)
	require.NoError(t, err)
}

// TestListScanDatasources_ScopedLikeTheResolver pins that the single-VM scan
// dialog's picker is scoped exactly like resolveScanDatasource: caller's
// tenant, enabled discovery integrations only, and linked to the account as
// either its own agent or its scan target. A picker scoped any wider offers a
// datasource the scan RPC then rejects; any narrower hides one it accepts.
func TestListScanDatasources_ScopedLikeTheResolver(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"id", "name", "status"}).
		AddRow(scanDatasourceID, "aws-forager-discovery", "enabled").
		AddRow("22222222-3333-4444-8555-666666666666", "dc-forager-discovery", "enabled")
	mock.ExpectQuery(`WHERE i.tenant_id = \$1\s+AND i.type = 'discovery' AND i.status = 'enabled'(.|\n)*`+
		`icv.name = 'datasource_key'(.|\n)*`+
		`own.link_role = 'own'(.|\n)*`+
		`link.cloud_account_id = \$2\s+AND link.link_role IN \('own', 'discovery_target'\)`).
		WithArgs("tenant-1", "account-1").
		WillReturnRows(rows)

	options, err := ListScanDatasources("account-1", "tenant-1")
	require.NoError(t, err)
	require.Len(t, options, 2)
	assert.Equal(t, ScanDatasourceOption{ID: scanDatasourceID, Name: "aws-forager-discovery", Status: "enabled"}, options[0])
	assert.Equal(t, "dc-forager-discovery", options[1].Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestListScanDatasources_NoneIsEmptyNotNil: an account with no datasource
// must serialize as [] — the dialog reads "no rows" as "nothing configured".
func TestListScanDatasources_NoneIsEmptyNotNil(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("SELECT i.id::text AS id, i.name, i.status").
		WithArgs("tenant-1", "account-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "status"}))

	options, err := ListScanDatasources("account-1", "tenant-1")
	require.NoError(t, err)
	assert.NotNil(t, options)
	assert.Empty(t, options)
}

// TestListScanDatasources_DBErrorSurfaces: a failed lookup must reach the
// caller as an error, never as an empty list — an empty list tells the user
// to go configure a datasource that may already exist.
func TestListScanDatasources_DBErrorSurfaces(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("SELECT i.id::text AS id, i.name, i.status").
		WithArgs("tenant-1", "account-1").
		WillReturnError(errors.New("connection reset by peer"))

	options, err := ListScanDatasources("account-1", "tenant-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection reset by peer")
	assert.Nil(t, options)
}

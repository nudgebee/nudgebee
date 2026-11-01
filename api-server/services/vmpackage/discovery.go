package vmpackage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/relay"

	"github.com/google/uuid"
)

// scanDatasource is a caller-supplied datasource_id resolved for the manual
// single-VM scan: the forager-side datasource_key, and the account whose
// proxy agent serves it. The relay routes by AgentAccountID, which differs
// from the scanned account when the datasource reaches that account through
// a link_role='discovery_target' link — the same split ScanDiscoveryDatasource
// makes between ds.AccountID (relay) and ds.TargetAccountID (findings).
type scanDatasource struct {
	Key            string `db:"datasource_key"`
	AgentAccountID string `db:"agent_account_id"`
}

// resolveScanDatasource resolves a datasource identifier to its agent-side
// datasource_key, scoped to the calling account+tenant: datasourceID is
// caller-supplied RPC input, and without the scoping a caller could resolve
// another tenant's integration UUID to its datasource_key.
//
// Account scoping goes through integrations_cloud_accounts — integrations has
// no account_id column (dropped in V573). An integration matches when it is
// linked to accountID as either the agent's own account ('own') or its scan
// target ('discovery_target'), the same two roles the account-scan path
// (ListDiscoveryDatasourcesForAccount) accepts. When an integration has
// several 'own' links (same datasource name reported from several accounts),
// the one for accountID is preferred so the relay reaches the local agent.
//
// No silent fallback to the integration id: forager knows datasources only by
// key, so falling back just turns a lookup miss into forager's opaque
// "datasource <uuid> not found".
func resolveScanDatasource(accountID, tenantID, datasourceID string) (scanDatasource, error) {
	if _, err := uuid.Parse(datasourceID); err != nil {
		// Not a UUID — assume it's already a datasource_key, served by the
		// caller's own account's agent.
		return scanDatasource{Key: datasourceID, AgentAccountID: accountID}, nil
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return scanDatasource{}, fmt.Errorf("failed to get database manager: %w", err)
	}

	var ds scanDatasource
	err = dbms.Db.Get(&ds,
		`SELECT icv.value AS datasource_key, own.cloud_account_id::varchar AS agent_account_id
		 FROM integrations i
		 JOIN integration_config_values icv ON icv.integration_id = i.id AND icv.name = 'datasource_key'
		 JOIN integrations_cloud_accounts own ON own.integration_id = i.id AND own.link_role = 'own'
		 WHERE i.id = $1 AND i.tenant_id = $3
		   AND i.type = 'discovery' AND i.status = 'enabled'
		   AND EXISTS (
		       SELECT 1 FROM integrations_cloud_accounts link
		       WHERE link.integration_id = i.id AND link.cloud_account_id = $2
		         AND link.link_role IN ('own', 'discovery_target'))
		 ORDER BY (own.cloud_account_id = $2) DESC, own.id
		 LIMIT 1`,
		datasourceID, accountID, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return scanDatasource{}, common.ErrorBadRequest(fmt.Sprintf(
			"vmpackage: datasource %s is not an enabled discovery datasource linked to this account", datasourceID))
	}
	if err != nil {
		return scanDatasource{}, fmt.Errorf("vmpackage: resolve datasource %s: %w", datasourceID, err)
	}
	return ds, nil
}

// ScanDatasourceOption is one discovery datasource the single-VM scan dialog
// can offer for an account.
type ScanDatasourceOption struct {
	ID     string `db:"id"`
	Name   string `db:"name"`
	Status string `db:"status"`
}

// ListScanDatasources returns the datasources resolveScanDatasource accepts
// for accountID+tenantID, for the single-VM scan dialog to pick from. It
// carries the same conditions as that resolver (tenant, enabled discovery
// integration with a datasource_key and an 'own' link, linked to the account
// as 'own' or 'discovery_target') — keep the two in step, so the dialog never
// offers a datasource the scan then rejects.
//
// This exists because the dialog used to read the generic integrations list,
// which only tenant admins may call, while the scan itself is open to account
// and namespace admins: they got an empty picker for a datasource they are
// allowed to scan through.
func ListScanDatasources(accountID, tenantID string) ([]ScanDatasourceOption, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	options := []ScanDatasourceOption{}
	err = dbms.Db.Select(&options,
		`SELECT i.id::text AS id, i.name, i.status
		 FROM integrations i
		 WHERE i.tenant_id = $1
		   AND i.type = 'discovery' AND i.status = 'enabled'
		   AND EXISTS (
		       SELECT 1 FROM integration_config_values icv
		       WHERE icv.integration_id = i.id AND icv.name = 'datasource_key')
		   AND EXISTS (
		       SELECT 1 FROM integrations_cloud_accounts own
		       WHERE own.integration_id = i.id AND own.link_role = 'own')
		   AND EXISTS (
		       SELECT 1 FROM integrations_cloud_accounts link
		       WHERE link.integration_id = i.id AND link.cloud_account_id = $2
		         AND link.link_role IN ('own', 'discovery_target'))
		 ORDER BY i.name, i.id`,
		tenantID, accountID)
	if err != nil {
		return nil, fmt.Errorf("vmpackage: list scan datasources for account %s: %w", accountID, err)
	}
	return options, nil
}

// discoveryContentPackVersion pins the forager content pack this action
// requires — forager's discovery_inventory has no "latest" default and
// hard-errors without an explicit version. Used only by the manual
// single-target path; the cron sweep path pins whatever version the
// datasource's own metadata (integrations.labels.pack_versions) advertises,
// since a fleet-wide cron can't assume every datasource loaded the same pack.
const discoveryContentPackVersion = 2

const discoveryInventoryTimeoutSeconds = 90

// sweepInventoryTimeoutSeconds bounds the cron's batch discovery_inventory
// call, which can carry every VM a sweep just found on one network segment —
// far more targets than the manual single-VM path's 90s budget assumes.
const sweepInventoryTimeoutSeconds = 600

// discoverySweepTimeoutSeconds bounds a discovery_sweep call. Sweep rate is
// forager-side rate-limited (default 100pps, see forager's discovery README),
// so a full segment sweep can take longer than a single relay call normally
// would.
const discoverySweepTimeoutSeconds = 300

// discoveryTarget is one host's result inside a discovery_inventory
// response. Collectors holds raw command output keyed by collector name —
// "os-release", "pkgs-dpkg" (deb hosts) or "pkgs-rpm" (rpm hosts) are the
// ones this package reads; forager also sends identity collectors
// (hostname, kernel, machine-id, net-links) this slice doesn't use yet.
type discoveryTarget struct {
	Host       string            `json:"host"`
	Status     string            `json:"status"`
	Collectors map[string]string `json:"collectors"`
}

type discoveryInventoryResponse struct {
	Result struct {
		Targets []discoveryTarget `json:"targets"`
	} `json:"result"`
}

// SweepHost is one responder discovery_sweep found on the network — mirrors
// forager's own SweepHost (pkg/proxy/discovery/sweep.go). A sweep only
// establishes that something is at an address; discovery_inventory is what
// answers what's installed on it.
type SweepHost struct {
	IP        string   `json:"ip"`
	MAC       string   `json:"mac,omitempty"`
	RDNS      string   `json:"rdns,omitempty"`
	OpenPorts []int    `json:"open_ports,omitempty"`
	Sources   []string `json:"sources"`

	// CloudIdentity is forager's cloud-instance-identity probe output (raw,
	// unparsed multi-line key=value text — see resource_match.go's
	// parseCloudInstanceIdentity), populated when this datasource has SSH
	// credentials configured and the host answered on the SSH port. Empty
	// otherwise: no SSH credentials, host unreachable/not SSH, or a
	// non-cloud host that timed out on every provider's metadata service.
	CloudIdentity string `json:"cloud_identity,omitempty"`
}

type discoverySweepResponse struct {
	Result struct {
		Hosts []SweepHost `json:"hosts"`
	} `json:"result"`
}

// discoveryProxyCall runs one forager discovery action via the relay and
// returns its unwrapped response map. Shared by every discovery_* action —
// unlike fetchDiscoveryInventory, callers here already hold a resolved
// datasourceKey (the cron resolves it once per integration, not per call)
// rather than a caller-supplied datasource_id needing resolveScanDatasource's
// UUID-vs-key branching.
func discoveryProxyCall(accountID, datasourceKey, action string, params map[string]any, timeoutSeconds int) (map[string]any, error) {
	result, err := relay.ExecuteProxy(accountID, action, datasourceKey, params, timeoutSeconds)
	if err != nil {
		return nil, fmt.Errorf("vmpackage: %s failed: %w", action, err)
	}
	return result, nil
}

// fetchDiscoveryInventory runs forager's discovery_inventory content-pack
// action against ip and returns its one target entry. ds scopes the network
// (CIDR + SSH creds) the relay routes through; targets narrows forager's own
// multi-host scan down to the single VM this call is about, which is also
// what makes parseDiscoveryInventoryResult's Targets[0] valid.
func fetchDiscoveryInventory(ds scanDatasource, ip string) (discoveryTarget, error) {
	var target discoveryTarget

	result, err := discoveryProxyCall(ds.AgentAccountID, ds.Key, "discovery_inventory",
		map[string]any{
			"targets":              []string{ip},
			"content_pack_version": discoveryContentPackVersion,
		},
		discoveryInventoryTimeoutSeconds)
	if err != nil {
		return target, err
	}
	return parseDiscoveryInventoryResult(result)
}

// fetchDiscoveryInventoryBatch runs discovery_inventory against every ip in
// one relay call — used by the cron sweep path to inventory everything one
// discovery_sweep just found on a segment, rather than one relay round-trip
// per host. packVersion is pinned by the caller from the datasource's own
// advertised pack_versions (see integrations.labels), not the manual path's
// hardcoded discoveryContentPackVersion.
func fetchDiscoveryInventoryBatch(accountID, datasourceKey string, ips []string, packVersion int) ([]discoveryTarget, error) {
	result, err := discoveryProxyCall(accountID, datasourceKey, "discovery_inventory",
		map[string]any{
			"targets":              ips,
			"content_pack_version": packVersion,
		},
		sweepInventoryTimeoutSeconds)
	if err != nil {
		return nil, err
	}
	return parseDiscoveryInventoryResultBatch(result)
}

// fetchDiscoverySweep runs forager's discovery_sweep action against cidrs and
// returns every host it found. cidrs must fall within the datasource's own
// allowed_cidrs — forager enforces this itself and refuses anything outside
// it, so this trusts the caller to already be passing a datasource's own
// advertised scope (see integrations.labels.allowed_cidrs).
func fetchDiscoverySweep(accountID, datasourceKey string, cidrs []string) ([]SweepHost, error) {
	result, err := discoveryProxyCall(accountID, datasourceKey, "discovery_sweep",
		map[string]any{"cidrs": cidrs},
		discoverySweepTimeoutSeconds)
	if err != nil {
		return nil, err
	}

	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("vmpackage: re-marshal discovery_sweep response: %w", err)
	}
	var resp discoverySweepResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("vmpackage: parse discovery_sweep response: %w", err)
	}
	return resp.Result.Hosts, nil
}

// parseDiscoveryInventoryResult decodes relay.ExecuteProxy's already-unwrapped
// result map into the one target this package call scoped to, and validates
// it succeeded. Split out from fetchDiscoveryInventory so the decoding logic
// is unit-testable against a real captured response without a live relay.
func parseDiscoveryInventoryResult(result map[string]any) (discoveryTarget, error) {
	targets, err := parseDiscoveryInventoryResultBatch(result)
	if err != nil {
		return discoveryTarget{}, err
	}
	target := targets[0]
	if target.Status != "ok" {
		return target, fmt.Errorf("vmpackage: discovery_inventory target status %q for host %q", target.Status, target.Host)
	}
	return target, nil
}

// parseDiscoveryInventoryResultBatch decodes relay.ExecuteProxy's
// already-unwrapped result map into every target it returned, unfiltered by
// status — the cron sweep path needs to see (and log) unreachable/failed
// hosts too, not just the first one. Errors only when the response carries
// zero targets at all, since forager always returns one entry per requested
// target, success or failure.
func parseDiscoveryInventoryResultBatch(result map[string]any) ([]discoveryTarget, error) {
	// relay.ExecuteProxy already unwraps the proxy agent's {data: "<json>"}
	// envelope into a map — re-marshal/unmarshal into a typed struct rather
	// than manually walking map[string]any.
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("vmpackage: re-marshal discovery_inventory response: %w", err)
	}
	var resp discoveryInventoryResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("vmpackage: parse discovery_inventory response: %w", err)
	}

	if len(resp.Result.Targets) == 0 {
		return nil, fmt.Errorf("vmpackage: discovery_inventory returned no targets")
	}
	return resp.Result.Targets, nil
}

// parsePackages picks the populated package collector from a
// discovery_inventory target and parses it. Which collector ran is the
// content pack's own decision based on its own OS detection — services-server
// just reads whichever key is present rather than re-deciding by family.
func parsePackages(collectors map[string]string) ([]Package, error) {
	if raw, ok := collectors["pkgs-dpkg"]; ok {
		return ParseDpkgQuery(raw)
	}
	if raw, ok := collectors["pkgs-rpm"]; ok {
		return ParseRPMQA(raw)
	}
	return nil, fmt.Errorf("vmpackage: discovery_inventory response has no pkgs-dpkg or pkgs-rpm collector")
}

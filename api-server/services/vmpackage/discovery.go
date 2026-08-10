package vmpackage

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"nudgebee/services/internal/database"
	"nudgebee/services/relay"

	"github.com/google/uuid"
)

// resolveDatasourceKey resolves a datasource identifier to the agent-side
// datasource_key. Local copy of eventrule/playbooks/proxy_datasource.go's
// unexported function of the same name — not imported, since that package's
// init() registers every playbook action, unnecessary weight for one lookup
// query. Unlike the original, this scopes the lookup to the calling
// account+tenant via integrations: datasourceID is caller-supplied RPC
// input, and without this join a caller could resolve another tenant's
// integration UUID to its datasource_key.
func resolveDatasourceKey(logger *slog.Logger, accountID, tenantID, datasourceID string) (string, error) {
	if _, err := uuid.Parse(datasourceID); err != nil {
		// Not a UUID — assume it's already a datasource_key.
		return datasourceID, nil
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", fmt.Errorf("failed to get database manager: %w", err)
	}

	var datasourceKey string
	err = dbms.Db.Get(&datasourceKey,
		`SELECT icv.value
		 FROM integration_config_values icv
		 JOIN integrations i ON i.id = icv.integration_id
		 WHERE icv.integration_id = $1 AND icv.name = 'datasource_key'
		   AND i.account_id = $2 AND i.tenant_id = $3`,
		datasourceID, accountID, tenantID)
	if err != nil {
		// Fallback: use the integration ID itself as the datasource key.
		// Logged since this also masks real DB outages as confusing
		// downstream relay errors.
		logger.Warn("vmpackage: resolve datasource_key failed, falling back to integration id", "datasource_id", datasourceID, "error", err)
		return datasourceID, nil
	}
	return datasourceKey, nil
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
// rather than a caller-supplied datasource_id needing resolveDatasourceKey's
// UUID-vs-key branching.
func discoveryProxyCall(accountID, datasourceKey, action string, params map[string]any, timeoutSeconds int) (map[string]any, error) {
	result, err := relay.ExecuteProxy(accountID, action, datasourceKey, params, timeoutSeconds)
	if err != nil {
		return nil, fmt.Errorf("vmpackage: %s failed: %w", action, err)
	}
	return result, nil
}

// fetchDiscoveryInventory runs forager's discovery_inventory content-pack
// action against ip and returns its one target entry. datasourceID scopes
// the network (CIDR + SSH creds) the relay routes through; targets narrows
// forager's own multi-host scan down to the single VM this call is about,
// which is also what makes parseDiscoveryInventoryResult's Targets[0] valid.
func fetchDiscoveryInventory(logger *slog.Logger, accountID, tenantID, datasourceID, ip string) (discoveryTarget, error) {
	var target discoveryTarget

	datasourceKey, err := resolveDatasourceKey(logger, accountID, tenantID, datasourceID)
	if err != nil {
		return target, fmt.Errorf("vmpackage: resolve datasource: %w", err)
	}

	result, err := discoveryProxyCall(accountID, datasourceKey, "discovery_inventory",
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

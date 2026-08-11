// resource_match.go resolves a discovery-swept host against an existing (or
// newly-creatable) cloud-collector-shaped cloud_resourses row, so the daily
// discovery cron stops creating a duplicate bare "vm-<ip>" placeholder for a
// machine cloud-collector already knows about under its real AWS/Azure/GCP
// identity. See GitHub issue #36059.
//
// Resolution runs directly off discovery_sweep's response (SweepHost),
// immediately after the sweep — not after discovery_inventory, and not
// gated on it. IMDS is only reachable from the host itself, so forager's
// cloud-instance-identity probe runs over SSH during the sweep itself
// (pkg/proxy/discovery/cloud_identity.go in the forager repo, using
// whatever SSH credentials the datasource already has configured) and rides
// along in SweepHost.CloudIdentity — no content pack, no separate inventory
// round-trip, and no dependency on inventory ever running at all.
//
// Resolution tries three tiers per swept host, most confident first:
//
//  0. Reported identity (SweepHost.CloudIdentity) — an exact match against
//     an existing row wins outright; no match creates a properly-typed row
//     (not a bare placeholder) that cloud-collector's own sync will adopt
//     automatically on its next run — see resolveByIdentity's doc comment
//     for why no separate merge step is needed for that case.
//  1. MAC address (SweepHost.MAC) — already sent by forager's
//     discovery_sweep, previously read no further than the IP.
//  2. Private/public IP — last resort, since IPs can be reused across VPCs
//     or reassigned by DHCP.
//
// All three are scoped to the discovery datasource's associated target
// cloud account (DiscoveryDatasource.TargetAccountID), never the agent's
// own account — matching within the wrong account can never find the
// duplicate (see cron.go's ListDiscoveryDatasources doc comment). When a
// datasource has no target account associated yet, resolveTargets is a
// no-op and every host falls through to today's bare-placeholder behavior
// (still gated on discovery_inventory running, unchanged — a target account
// association is what turns real resolution on, not the identity probe by
// itself).
package vmpackage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"

	"github.com/jmoiron/sqlx"
)

// resolvedTarget is one swept host's outcome from resolveTargets. A zero
// value (CloudResourceID == "") tells scanTargetSafely that no tier matched
// or created anything — either the datasource has no target account
// associated, or none of tiers 0-2 found a match — and it should fall back
// to findOrCreateVMResource exactly as before this file existed.
type resolvedTarget struct {
	CloudResourceID string
	AccountID       string
}

// resolveTargets resolves every host discovery_sweep found against
// cloud_resourses under targetAccountID, in one pass right after the sweep
// (fixed query count per sweep, not per host) — independent of whether
// discovery_inventory subsequently runs for any of them. ownAccountID is
// only used to look up a possibly pre-existing bare placeholder to adopt
// (see adoptStalePlaceholder) — it is never the scope any tier matches
// within.
//
// A single host's resolution error is logged and skipped rather than
// aborting the whole datasource: that host falls through to
// findOrCreateVMResource's bare-placeholder behavior in scanTargetSafely,
// same as an unmatched host, while every other host on this sweep still
// gets resolved and scanned normally.
func resolveTargets(logger *slog.Logger, dbms *database.DatabaseManager, targetAccountID, ownAccountID, tenantID string, hosts []SweepHost) map[string]resolvedTarget {
	resolved := make(map[string]resolvedTarget, len(hosts))
	if targetAccountID == "" {
		return resolved
	}

	for _, host := range hosts {
		res, err := resolveOneHost(dbms, targetAccountID, ownAccountID, tenantID, host)
		if err != nil {
			logger.Error("vmpackage: resolve host against cloud_resourses failed", "host", host.IP, "error", err)
			continue
		}
		if res.CloudResourceID != "" {
			resolved[host.IP] = res
		}
	}
	return resolved
}

// resolveOneHost runs tiers 0-2 in priority order for one swept host,
// stopping at the first that produces a resource id.
func resolveOneHost(dbms *database.DatabaseManager, targetAccountID, ownAccountID, tenantID string, host SweepHost) (resolvedTarget, error) {
	if identity, ok := parseCloudInstanceIdentity(host.CloudIdentity); ok {
		res, err := resolveByIdentity(dbms, targetAccountID, ownAccountID, tenantID, identity, host.IP)
		if err != nil {
			return resolvedTarget{}, err
		}
		if res.CloudResourceID != "" {
			return res, nil
		}
	}

	if host.MAC != "" {
		id, ok, err := findExistingResourceByMAC(dbms, targetAccountID, tenantID, host.MAC)
		if err != nil {
			return resolvedTarget{}, fmt.Errorf("MAC lookup: %w", err)
		}
		if ok {
			if err := adoptStalePlaceholder(dbms, ownAccountID, tenantID, host.IP, id); err != nil {
				return resolvedTarget{}, err
			}
			return resolvedTarget{CloudResourceID: id, AccountID: targetAccountID}, nil
		}
	}

	id, ok, err := findExistingResourceByIP(dbms, targetAccountID, tenantID, host.IP)
	if err != nil {
		return resolvedTarget{}, fmt.Errorf("IP lookup: %w", err)
	}
	if ok {
		if err := adoptStalePlaceholder(dbms, ownAccountID, tenantID, host.IP, id); err != nil {
			return resolvedTarget{}, err
		}
		return resolvedTarget{CloudResourceID: id, AccountID: targetAccountID}, nil
	}

	return resolvedTarget{}, nil
}

// --- Tier 0: reported identity --------------------------------------------

// cloudInstanceIdentity is one host's parsed SweepHost.CloudIdentity.
// InstanceID is the AWS/GCP native instance id, or the Azure VM name (Azure
// has no single bare id — SubscriptionID/ResourceGroup/InstanceID together
// assemble the ARM resource id in resourceShape).
type cloudInstanceIdentity struct {
	Provider       string
	InstanceID     string
	Region         string
	PublicIP       string
	SubscriptionID string
	ResourceGroup  string
}

// parseCloudInstanceIdentity parses SweepHost.CloudIdentity's multi-line
// key=value output (see forager's pkg/proxy/discovery/cloud_identity.go)
// into a typed identity. ok=false when the probe never ran (no SSH
// credentials configured, or an older forager without this capability),
// timed out on every provider (non-cloud/bare-metal host), or reported an
// incomplete or unrecognized provider block.
func parseCloudInstanceIdentity(raw string) (cloudInstanceIdentity, bool) {
	fields := make(map[string]string)
	for line := range strings.SplitSeq(raw, "\n") {
		k, v, found := strings.Cut(strings.TrimSpace(line), "=")
		if found {
			fields[k] = v
		}
	}

	id := cloudInstanceIdentity{
		Provider:       fields["provider"],
		Region:         fields["region"],
		PublicIP:       fields["public_ip"],
		SubscriptionID: fields["subscription_id"],
		ResourceGroup:  fields["resource_group"],
	}

	switch id.Provider {
	case "aws":
		id.InstanceID = fields["instance_id"]
		if id.InstanceID == "" {
			return cloudInstanceIdentity{}, false
		}
	case "gcp":
		id.InstanceID = fields["instance_id"]
		if id.InstanceID == "" {
			return cloudInstanceIdentity{}, false
		}
		// zone -> region: strip the trailing "-<letter>" segment, e.g.
		// "us-central1-a" -> "us-central1".
		if zone := fields["zone"]; zone != "" {
			id.Region = zone
			if i := strings.LastIndex(zone, "-"); i > 0 {
				id.Region = zone[:i]
			}
		}
	case "azure":
		id.InstanceID = fields["name"]
		id.Region = fields["location"]
		if id.InstanceID == "" || id.SubscriptionID == "" || id.ResourceGroup == "" {
			return cloudInstanceIdentity{}, false
		}
	default:
		return cloudInstanceIdentity{}, false
	}

	if id.Region == "" {
		id.Region = "global"
	}
	return id, true
}

// cloudResourceShape is the exact (resourse_id, type, service_name,
// cloud_provider) tuple cloud-collector would write for this identity —
// confirmed against collector-server/cloud-collector/providers/{aws,azure,gcloud}.
// Getting these wrong silently breaks both the Tier-0 existing-row lookup
// below and, for AWS/GCP, cloud-collector's own reconcileExternalResourceIds
// convergence onto a freshly created row (see resolveByIdentity).
type cloudResourceShape struct {
	ResourseID string
	Type       string

	ServiceName   string
	CloudProvider string

	// ExternalResourceID is always set to ResourseID — for Azure that's
	// exactly the real value (external_resource_id is just the lowercased
	// resourse_id there), and for AWS/GCP it's a placeholder rather than
	// cloud-collector's synthesized ARN, but a safe one: cloud-collector's
	// existing reconcileExternalResourceIds pre-step overwrites whatever
	// external_resource_id currently holds (via IS DISTINCT FROM, not an
	// IS NULL check) once it matches on the natural key, so this gets
	// corrected on the next real sync the same way NULL would have been —
	// while avoiding a NULL external_resource_id on a row that otherwise
	// looks like a complete, real cloud-collector row.
	ExternalResourceID string
}

func (id cloudInstanceIdentity) resourceShape() cloudResourceShape {
	switch id.Provider {
	case "aws":
		return cloudResourceShape{ResourseID: id.InstanceID, Type: "compute-instance", ServiceName: "AmazonEC2", CloudProvider: "AWS", ExternalResourceID: id.InstanceID}
	case "gcp":
		return cloudResourceShape{ResourseID: id.InstanceID, Type: "compute.googleapis.com/Instance", ServiceName: "Compute Engine", CloudProvider: "GCP", ExternalResourceID: id.InstanceID}
	case "azure":
		armID := strings.ToLower(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s",
			id.SubscriptionID, id.ResourceGroup, id.InstanceID))
		return cloudResourceShape{ResourseID: armID, Type: "Microsoft.Compute/virtualMachines", ServiceName: "Microsoft.Compute/virtualMachines", CloudProvider: "Azure", ExternalResourceID: armID}
	default:
		return cloudResourceShape{}
	}
}

// resolveByIdentity looks up an existing cloud_resourses row matching
// identity's exact native resource id under targetAccountID. If none
// exists, it creates one — properly typed, not a bare placeholder.
//
// Why no explicit adopt-later step is needed for a freshly-created row:
// cloud_resourses.id never changes across cloud-collector's own
// ON CONFLICT (account, external_resource_id) DO UPDATE (neither its Azure
// nor its AWS/GCP branch's SET list touches id — confirmed against
// collector-server/cloud-collector/account/etl_resources.go). For Azure,
// external_resource_id is set to the real value right here, so
// cloud-collector's next sync converges on this row directly via that
// conflict target. For AWS/GCP, external_resource_id is set to a
// placeholder (see cloudResourceShape's doc comment), but cloud-collector's
// existing reconcileExternalResourceIds pre-step matches on the exact
// natural key (account, resourse_id case-insensitive, type, region,
// service_name) this insert sets correctly, and overwrites it with the real
// value before the main upsert runs. Either way vm_package/recommendation
// rows already attached to this id stay attached with no separate repoint.
func resolveByIdentity(dbms *database.DatabaseManager, targetAccountID, ownAccountID, tenantID string, identity cloudInstanceIdentity, host string) (resolvedTarget, error) {
	shape := identity.resourceShape()
	if shape.ResourseID == "" {
		return resolvedTarget{}, nil
	}

	const lookup = `
		SELECT id::text FROM cloud_resourses
		WHERE account = $1 AND tenant = $2 AND is_active IS NOT FALSE
		  AND cloud_provider ILIKE $3 AND resourse_id = $4`

	id, ok, err := queryOneString(dbms, lookup, targetAccountID, tenantID, shape.CloudProvider, shape.ResourseID)
	if err != nil {
		return resolvedTarget{}, fmt.Errorf("lookup by identity: %w", err)
	}
	if ok {
		if err := adoptStalePlaceholder(dbms, ownAccountID, tenantID, host, id); err != nil {
			return resolvedTarget{}, err
		}
		return resolvedTarget{CloudResourceID: id, AccountID: targetAccountID}, nil
	}

	meta, err := json.Marshal(map[string]string{"PrivateIpAddress": host, "PublicIpAddress": identity.PublicIP})
	if err != nil {
		return resolvedTarget{}, fmt.Errorf("marshal meta: %w", err)
	}

	newID := common.GenerateUUID()
	_, err = dbms.Db.Exec(`
		INSERT INTO public.cloud_resourses
		  (id, created_at, updated_at, resourse_id, "name", "type", status, account, cloud_provider,
		   region, arn, tenant, tags, meta, service_name, external_resource_id, is_active)
		VALUES ($1, now(), now(), $2, $3, $4, 'Active', $5, $6, $7, '', $8, '{}', $9, $10, $11, true)
		ON CONFLICT (account, resourse_id, type, region, service_name) DO NOTHING`,
		newID, shape.ResourseID, identity.InstanceID, shape.Type, targetAccountID, shape.CloudProvider,
		identity.Region, tenantID, string(meta), shape.ServiceName, shape.ExternalResourceID)
	if err != nil {
		return resolvedTarget{}, fmt.Errorf("create identity resource for %s: %w", shape.ResourseID, err)
	}

	// Race-tolerant: another sweep target/goroutine may have created the
	// same row between the lookup and this insert — re-read rather than
	// trust newID, same pattern findOrCreateVMResource already uses.
	id, ok, err = queryOneString(dbms, lookup, targetAccountID, tenantID, shape.CloudProvider, shape.ResourseID)
	if err != nil {
		return resolvedTarget{}, fmt.Errorf("read back identity resource for %s: %w", shape.ResourseID, err)
	}
	if !ok {
		return resolvedTarget{}, fmt.Errorf("identity resource for %s not found after insert", shape.ResourseID)
	}
	// A stale bare placeholder may already exist for this host from a sweep
	// before this identity collector was deployed, or before cloud-collector
	// had synced this account — adopt it onto the row just created, same as
	// the existing-row branch above.
	if err := adoptStalePlaceholder(dbms, ownAccountID, tenantID, host, id); err != nil {
		return resolvedTarget{}, err
	}
	return resolvedTarget{CloudResourceID: id, AccountID: targetAccountID}, nil
}

// --- Tiers 1-2: MAC / IP against existing cloud-collector rows only ------
//
// These only look up existing rows — a MAC or IP alone isn't enough
// information to construct a new properly-typed row the way Tier 0 can.

const (
	awsMACQuery = `
		SELECT cr.id::text FROM cloud_resourses cr
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(cr.meta->'NetworkInterfaces', '[]'::jsonb)) ni
		WHERE cr.account = $1 AND cr.tenant = $2 AND cr.is_active IS NOT FALSE
		  AND cr.cloud_provider ILIKE 'aws' AND cr.type = 'compute-instance' AND cr.service_name = 'AmazonEC2'
		  AND ni->>'MacAddress' = $3
		LIMIT 1`

	// GCP has no MAC tier: computepb.NetworkInterface carries no MAC field
	// at all (confirmed against the GCP SDK) — GCP falls straight to IP.

	azureMACQuery = `
		SELECT vm.id::text
		FROM cloud_resourses vm
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(vm.meta #> '{properties,networkProfile,networkInterfaces}', '[]'::jsonb)) nic_ref
		JOIN cloud_resourses nic
		  ON LOWER(nic.resourse_id) = LOWER(nic_ref->>'id')
		 AND nic.tenant = vm.tenant AND nic.cloud_provider ILIKE 'azure' AND nic.is_active IS NOT FALSE
		WHERE vm.account = $1 AND vm.tenant = $2 AND vm.is_active IS NOT FALSE
		  AND vm.cloud_provider ILIKE 'azure' AND vm.type = 'Microsoft.Compute/virtualMachines'
		  AND nic.meta #>> '{properties,macAddress}' = $3
		LIMIT 1`

	awsIPQuery = `
		SELECT cr.id::text FROM cloud_resourses cr
		WHERE cr.account = $1 AND cr.tenant = $2 AND cr.is_active IS NOT FALSE
		  AND cr.cloud_provider ILIKE 'aws' AND cr.type = 'compute-instance' AND cr.service_name = 'AmazonEC2'
		  AND (cr.meta->>'PrivateIpAddress' = $3 OR cr.meta->>'PublicIpAddress' = $3)
		LIMIT 1`

	gcpIPQuery = `
		SELECT cr.id::text FROM cloud_resourses cr
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(cr.meta->'network_interfaces', '[]'::jsonb)) ni
		WHERE cr.account = $1 AND cr.tenant = $2 AND cr.is_active IS NOT FALSE
		  AND cr.cloud_provider ILIKE 'gcp' AND cr.type = 'compute.googleapis.com/Instance'
		  AND (ni->>'network_i_p' = $3
		       OR EXISTS (
		         SELECT 1 FROM jsonb_array_elements(COALESCE(ni->'access_configs', '[]'::jsonb)) ac
		         WHERE ac->>'nat_i_p' = $3
		       ))
		LIMIT 1`

	azureIPQuery = `
		SELECT vm.id::text
		FROM cloud_resourses vm
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(vm.meta #> '{properties,networkProfile,networkInterfaces}', '[]'::jsonb)) nic_ref
		JOIN cloud_resourses nic
		  ON LOWER(nic.resourse_id) = LOWER(nic_ref->>'id')
		 AND nic.tenant = vm.tenant AND nic.cloud_provider ILIKE 'azure' AND nic.is_active IS NOT FALSE
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(nic.meta #> '{properties,ipConfigurations}', '[]'::jsonb)) ipcfg
		WHERE vm.account = $1 AND vm.tenant = $2 AND vm.is_active IS NOT FALSE
		  AND vm.cloud_provider ILIKE 'azure' AND vm.type = 'Microsoft.Compute/virtualMachines'
		  AND ipcfg #>> '{properties,privateIPAddress}' = $3
		LIMIT 1`
)

func findExistingResourceByMAC(dbms *database.DatabaseManager, accountID, tenantID, mac string) (string, bool, error) {
	if id, ok, err := queryOneString(dbms, awsMACQuery, accountID, tenantID, mac); err != nil || ok {
		return id, ok, err
	}
	return queryOneString(dbms, azureMACQuery, accountID, tenantID, mac)
}

func findExistingResourceByIP(dbms *database.DatabaseManager, accountID, tenantID, ip string) (string, bool, error) {
	if id, ok, err := queryOneString(dbms, awsIPQuery, accountID, tenantID, ip); err != nil || ok {
		return id, ok, err
	}
	if id, ok, err := queryOneString(dbms, gcpIPQuery, accountID, tenantID, ip); err != nil || ok {
		return id, ok, err
	}
	return queryOneString(dbms, azureIPQuery, accountID, tenantID, ip)
}

// queryOneString runs a query expected to return zero or one id::text column,
// translating sql.ErrNoRows into ("", false, nil) — every Tier 0-2 lookup
// here is a "does a match exist" check, not a required row.
func queryOneString(dbms *database.DatabaseManager, query string, args ...any) (string, bool, error) {
	var id string
	err := dbms.Db.Get(&id, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// --- Adopt-later: repoint a stale placeholder's history -------------------

// adoptStalePlaceholder repoints an old-style bare vm-<ip> placeholder's
// vm_package/recommendation history onto newResourceID and retires the
// placeholder, if one exists for host under ownAccountID — the account any
// placeholder created before a match was possible would have been filed
// under. No-op if none exists (the common case once a datasource has been
// associated and swept a few times).
func adoptStalePlaceholder(dbms *database.DatabaseManager, ownAccountID, tenantID, host, newResourceID string) error {
	oldID, ok, err := queryOneString(dbms,
		`SELECT id::text FROM cloud_resourses WHERE resourse_id = $1 AND account = $2 AND tenant = $3 AND is_active IS NOT FALSE`,
		vmResourceIDPrefix+host, ownAccountID, tenantID)
	if err != nil {
		return fmt.Errorf("lookup stale placeholder for %s: %w", host, err)
	}
	if !ok || oldID == newResourceID {
		return nil
	}
	return adoptPlaceholder(dbms, oldID, newResourceID)
}

// adoptPlaceholder repoints a retired placeholder's vm_package/recommendation
// history onto newResourceID via UPDATE, never DELETE — both tables FK to
// cloud_resourses(id) ON DELETE CASCADE, so deleting the placeholder would
// destroy the history this is trying to preserve — then marks the
// placeholder inactive, matching cloud-collector's own stale-row convention.
func adoptPlaceholder(dbms *database.DatabaseManager, oldPlaceholderID, newResourceID string) error {
	_, err := dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		if _, err := tx.Exec(`UPDATE vm_package SET cloud_resource_id = $1 WHERE cloud_resource_id = $2`, newResourceID, oldPlaceholderID); err != nil {
			return nil, fmt.Errorf("repoint vm_package: %w", err)
		}
		if _, err := tx.Exec(`UPDATE recommendation SET resource_id = $1 WHERE resource_id = $2`, newResourceID, oldPlaceholderID); err != nil {
			return nil, fmt.Errorf("repoint recommendation: %w", err)
		}
		if _, err := tx.Exec(`UPDATE cloud_resourses SET is_active = false WHERE id = $1`, oldPlaceholderID); err != nil {
			return nil, fmt.Errorf("retire placeholder: %w", err)
		}
		return nil, nil
	})
	return err
}

// --- verifyCloudResource fallback -----------------------------------------

// resolveResourceIP re-derives a cloud_resourses row's IP for the manual
// re-scan path (ScanPackages -> verifyCloudResource), for resources whose
// resourse_id doesn't carry the legacy "vm-<ip>" convention — i.e. anything
// resolveTargets matched or created. meta.PrivateIpAddress/PublicIpAddress
// covers every row this package itself writes (both the vm-<ip> fallback
// and Tier-0-created rows); the GCP/Azure fallbacks below cover genuine
// cloud-collector-originated rows that predate this fix.
func resolveResourceIP(dbms *database.DatabaseManager, resourceID, accountID, tenantID string) (string, error) {
	var row struct {
		CloudProvider string         `db:"cloud_provider"`
		Meta          sql.NullString `db:"meta"`
	}
	err := dbms.Db.Get(&row,
		`SELECT cloud_provider, meta::text AS meta FROM cloud_resourses WHERE id = $1 AND account = $2 AND tenant = $3`,
		resourceID, accountID, tenantID)
	if err != nil {
		return "", fmt.Errorf("vmpackage: load resource %s: %w", resourceID, err)
	}

	if row.Meta.Valid {
		var meta map[string]any
		if err := json.Unmarshal([]byte(row.Meta.String), &meta); err == nil {
			if ip, ok := meta["PrivateIpAddress"].(string); ok && ip != "" {
				return ip, nil
			}
			if ip, ok := meta["PublicIpAddress"].(string); ok && ip != "" {
				return ip, nil
			}
		}
	}

	if strings.EqualFold(row.CloudProvider, "gcp") {
		if ip, ok, err := queryOneString(dbms, `
			SELECT ni->>'network_i_p' FROM cloud_resourses cr
			CROSS JOIN LATERAL jsonb_array_elements(COALESCE(cr.meta->'network_interfaces', '[]'::jsonb)) ni
			WHERE cr.id = $1 AND ni->>'network_i_p' IS NOT NULL AND ni->>'network_i_p' != '' LIMIT 1`,
			resourceID); err == nil && ok {
			return ip, nil
		}
	}
	if strings.EqualFold(row.CloudProvider, "azure") {
		if ip, ok, err := queryOneString(dbms, `
			SELECT ipcfg #>> '{properties,privateIPAddress}'
			FROM cloud_resourses vm
			CROSS JOIN LATERAL jsonb_array_elements(COALESCE(vm.meta #> '{properties,networkProfile,networkInterfaces}', '[]'::jsonb)) nic_ref
			JOIN cloud_resourses nic ON LOWER(nic.resourse_id) = LOWER(nic_ref->>'id') AND nic.tenant = vm.tenant AND nic.cloud_provider ILIKE 'azure'
			CROSS JOIN LATERAL jsonb_array_elements(COALESCE(nic.meta #> '{properties,ipConfigurations}', '[]'::jsonb)) ipcfg
			WHERE vm.id = $1 AND ipcfg #>> '{properties,privateIPAddress}' IS NOT NULL LIMIT 1`,
			resourceID); err == nil && ok {
			return ip, nil
		}
	}

	return "", fmt.Errorf("vmpackage: resource %s has no recoverable IP", resourceID)
}

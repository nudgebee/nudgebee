// Package selfhosted implements the Knowledge Graph source for self-hosted VM
// accounts (cloud_accounts.cloud_provider='SelfHosted', account_type='vm').
//
// The forager proxy agent sweeps a network and vmpackage files one
// cloud_resourses row per host it scans. Where that row lives depends on the
// discovery datasource's target account: by default the VM account itself (a
// host matched to a cloud instance becomes an EC2/GCE/Azure-shaped row there),
// or, when a user points the datasource at a real cloud account, that
// account's existing instance row. This source emits one SelfHostedVM node per
// host the VM account scanned recently, wherever the row lives. The node is
// linked to its cloud instance node by the SAME_AS rules in
// core/default_relationships.json; nothing here touches another source's node.
package selfhosted

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

const (
	sourceName = "selfhosted"

	// SpecificTypeSelfHostedVM is the concrete type of every node this source emits.
	SpecificTypeSelfHostedVM = core.SpecificTypeSelfHostedVM

	// HostKindOnPrem is a host with no cloud identity (a vm-<ip> placeholder row).
	HostKindOnPrem = "on_prem"
	// HostKindCloudIdentified is a host vmpackage matched to a cloud instance.
	HostKindCloudIdentified = "cloud_identified"

	defaultScanFreshnessDays = 7
	placeholderIDPrefix      = "vm-"
)

func init() {
	sources.RegisterSourceFactory(sourceName, func(config sources.SourceConfig, logger *slog.Logger) (core.SourceInterface, error) {
		return NewSelfHostedSource(logger), nil
	}, "Self-hosted VM hosts scanned through a VM account")
}

// SelfHostedSource implements core.SourceInterface for one VM account.
type SelfHostedSource struct {
	sources.BaseSource
	logger  *slog.Logger
	enabled bool
}

// NewSelfHostedSource creates a new self-hosted VM source.
func NewSelfHostedSource(logger *slog.Logger) *SelfHostedSource {
	if logger == nil {
		logger = slog.Default()
	}
	return &SelfHostedSource{
		BaseSource: sources.NewBaseSource(sourceName),
		logger:     logger,
		enabled:    true,
	}
}

func (s *SelfHostedSource) GetName() string { return sourceName }

func (s *SelfHostedSource) IsEnabled() bool { return s.enabled }

func (s *SelfHostedSource) Validate() error { return nil }

// HostRow is one scanned host as read by fetchHostRows.
type HostRow struct {
	NBResourceID  string         `db:"nb_resource_id"`
	ResourceID    string         `db:"resourse_id"`
	Name          string         `db:"name"`
	CloudProvider string         `db:"cloud_provider"`
	Region        string         `db:"region"`
	PrivateIP     string         `db:"private_ip"`
	PublicIP      string         `db:"public_ip"`
	ScanStatus    sql.NullString `db:"scan_status"`
	PackageCount  sql.NullInt64  `db:"package_count"`
	FindingCount  sql.NullInt64  `db:"finding_count"`
	ScannedAt     time.Time      `db:"scanned_at"`
	OSFamily      string         `db:"os_family"`
	OSVersion     string         `db:"os_version"`
}

// BuildGraph emits one SelfHostedVM node per host the VM account scanned
// within the freshness window. A host that stops being scanned drops out of
// the result, so the ordinary static-source sweep tombstones its node — that
// is the only lifecycle these hosts have, since vmpackage never retires rows.
func (s *SelfHostedSource) BuildGraph(reqCtx *security.RequestContext, req *core.SourceBuildRequest) (*core.Graph, error) {
	ctx := reqCtx.GetContext()
	rows, err := fetchHostRows(ctx, req.TenantID, req.CloudAccountID, scanFreshnessDays())
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("selfhosted: failed to fetch hosts: %w", err)
	}

	nodes := ConvertHostRows(rows, req.TenantID, req.CloudAccountID)
	s.logger.Info("selfhosted: built hosts", "tenant_id", req.TenantID, "account_id", req.CloudAccountID, "rows", len(rows), "nodes", len(nodes))

	return &core.Graph{
		Nodes:          nodes,
		Edges:          []*core.DbEdge{},
		Source:         sourceName,
		TenantID:       req.TenantID,
		CloudAccountID: req.CloudAccountID,
	}, nil
}

func scanFreshnessDays() int {
	if config.Config.KGSelfHostedScanFreshness > 0 {
		return config.Config.KGSelfHostedScanFreshness
	}
	return defaultScanFreshnessDays
}

// hostRowsQuery selects every host the VM account ($2) scanned in the last $3
// days. The scope is the VM account itself plus the discovery-target account of
// each discovery datasource the VM account owns — the same own/discovery_target
// links vmpackage's ListDiscoveryDatasources reads. A target account that is no
// longer active counts as not scanned, so deactivating it retires these nodes
// on the next build. Rows come newest scan first; ConvertHostRows keeps the
// first row per node key.
const hostRowsQuery = `
WITH scope AS (
	SELECT $2::uuid AS account_id
	UNION
	SELECT target.cloud_account_id
	FROM integrations i
	JOIN integrations_cloud_accounts own
	  ON own.integration_id = i.id AND own.link_role = 'own'
	 AND own.cloud_account_id = $2::uuid AND own.tenant_id = $1::uuid
	JOIN LATERAL (
		SELECT ica.cloud_account_id
		FROM integrations_cloud_accounts ica
		WHERE ica.integration_id = i.id AND ica.link_role = 'discovery_target'
		ORDER BY ica.id DESC
		LIMIT 1
	) target ON true
	WHERE i.tenant_id = $1::uuid AND i.type = 'discovery'
)
SELECT cr.id::text                                       AS nb_resource_id,
       COALESCE(cr.resourse_id, '')                      AS resourse_id,
       COALESCE(cr.name, '')                             AS name,
       COALESCE(cr.cloud_provider, '')                   AS cloud_provider,
       COALESCE(NULLIF(cr.region, ''), 'global')         AS region,
       COALESCE(cr.meta->>'PrivateIpAddress', '')        AS private_ip,
       COALESCE(cr.meta->>'PublicIpAddress', '')         AS public_ip,
       vss.status                                        AS scan_status,
       vss.package_count,
       vss.finding_count,
       vss.scanned_at,
       COALESCE(os.os_family, '')                        AS os_family,
       COALESCE(os.os_version, '')                       AS os_version
FROM scope s
JOIN cloud_accounts ca
  ON ca.id = s.account_id AND ca.tenant = $1::uuid AND ca.status = 'active'
JOIN cloud_resourses cr
  ON cr.account = ca.id AND cr.tenant = $1::uuid AND cr.is_active IS NOT FALSE
JOIN vm_scan_status vss
  ON vss.cloud_resource_id = cr.id AND vss.tenant_id = $1::uuid
 AND vss.scanned_at > NOW() - ($3 * INTERVAL '1 day')
LEFT JOIN LATERAL (
	SELECT vp.os_family, vp.os_version
	FROM vm_package vp
	WHERE vp.cloud_resource_id = cr.id AND vp.is_active
	ORDER BY vp.last_seen_at DESC
	LIMIT 1
) os ON true
ORDER BY vss.scanned_at DESC, cr.id`

func fetchHostRows(ctx context.Context, tenantID, vmAccountID string, freshnessDays int) ([]HostRow, error) {
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}
	var rows []HostRow
	if err := dbManager.Db.SelectContext(ctx, &rows, hostRowsQuery, tenantID, vmAccountID, freshnessDays); err != nil {
		return nil, fmt.Errorf("failed to query scanned hosts: %w", err)
	}
	return rows, nil
}

// ConvertHostRows turns scanned-host rows into SelfHostedVM nodes owned by the
// VM account. Rows must be ordered newest scan first: after a datasource is
// re-pointed the same host can come back from both the VM account and the
// target account under one node key, and the newest scan wins.
//
// A cloud-identified host carries its instance id in instance_id, never in
// resource_id, so no index of ComputeInstance by resource_id (the K8s node →
// EC2 enricher, the provider-identifier resolver) mistakes it for the cloud
// instance node it is linked to.
func ConvertHostRows(rows []HostRow, tenantID, vmAccountID string) []*core.DbNode {
	nodes := make([]*core.DbNode, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.ResourceID == "" {
			continue
		}
		uniqueKey := core.BuildUniqueKey(core.CloudProviderSelfHosted, vmAccountID, row.Region,
			core.NodeTypeComputeInstance, "", keySafe(row.ResourceID))
		if _, dup := seen[uniqueKey]; dup {
			continue
		}
		seen[uniqueKey] = struct{}{}

		props := map[string]interface{}{
			"specific_type":  SpecificTypeSelfHostedVM,
			"name":           firstNonEmpty(row.Name, row.ResourceID),
			"region":         row.Region,
			"nb_resource_id": row.NBResourceID,
			"scanned_at":     row.ScannedAt.UTC().Format(time.RFC3339),
		}
		if provider, ok := cloudInstanceProvider(row); ok {
			props["host_kind"] = HostKindCloudIdentified
			props["instance_provider"] = provider
			props["instance_id"] = row.ResourceID
		} else {
			props["host_kind"] = HostKindOnPrem
			props["resource_id"] = row.ResourceID
		}
		setIfValidIP(props, "private_ip", row.PrivateIP)
		setIfValidIP(props, "public_ip", row.PublicIP)
		setIfNotEmpty(props, "os_family", row.OSFamily)
		setIfNotEmpty(props, "os_version", row.OSVersion)
		if row.ScanStatus.Valid {
			props["scan_status"] = row.ScanStatus.String
		}
		if row.PackageCount.Valid {
			props["package_count"] = row.PackageCount.Int64
		}
		if row.FindingCount.Valid {
			props["finding_count"] = row.FindingCount.Int64
		}

		nodes = append(nodes, core.NewNode(core.NodeTypeComputeInstance, uniqueKey, props, tenantID, vmAccountID, sourceName))
	}
	return nodes
}

// cloudInstanceProvider reports the lowercased cloud provider of a host
// vmpackage matched to a cloud instance. Placeholders (vm-<ip>) are on-prem
// whatever provider column they carry.
func cloudInstanceProvider(row HostRow) (string, bool) {
	if strings.HasPrefix(row.ResourceID, placeholderIDPrefix) {
		return "", false
	}
	provider := strings.ToLower(row.CloudProvider)
	switch provider {
	case core.CloudProviderAWS, core.CloudProviderGCP, core.CloudProviderAzure:
		return provider, true
	}
	return "", false
}

// keySafe keeps the 6-part unique key splittable: an IPv6 placeholder
// (vm-fe80::1) would otherwise add colons to the name segment.
func keySafe(id string) string {
	return strings.ReplaceAll(id, ":", "_")
}

// setIfValidIP drops anything that is not an IP. forager has stored an IMDS
// error page in PublicIpAddress, and that must not reach the graph.
func setIfValidIP(props map[string]interface{}, key, value string) {
	value = strings.TrimSpace(value)
	if net.ParseIP(value) != nil {
		props[key] = value
	}
}

func setIfNotEmpty(props map[string]interface{}, key, value string) {
	if value != "" {
		props[key] = value
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

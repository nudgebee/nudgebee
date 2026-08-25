package flow_sources

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"sort"
	"strings"
	"sync"
)

// CloudTopologyStore answers the cloud-topology lookups the knowledge graph used
// to answer by shelling out to `aws` / `az` / `gcloud` through cloud-collector.
//
// Every one of those CLI calls is a fresh interpreter process — two or three once
// GCP and Azure re-auth first — so a single lookup costs ~1.4 CPU-seconds. The
// same data is already in `cloud_resourses`, refreshed by daily discovery, with
// the provider's own payload preserved verbatim in `meta`. That verbatim payload
// is what makes this a drop-in: `network-interface.meta` *is* a
// describe-network-interfaces element, `hostedzone.meta.Id` *is* the
// `/hostedzone/Z...` form the CLI returns, so callers unmarshal store output into
// the structs they already had.
//
// Lookups return (value, hit). A miss means the table cannot answer — the row is
// absent, or present but lossy — and the caller runs its original CLI command
// unchanged. That keeps the migration behaviour-preserving: worst case we do
// exactly what we did before. A nil *CloudTopologyStore misses on every lookup,
// so callers that have no store need no nil checks of their own.
//
// The store is built once per knowledge-graph build and is read-only afterwards,
// so it is safe to share across goroutines. Only hit/miss counters are mutable.
type CloudTopologyStore struct {
	logger   *slog.Logger
	tenantID string

	// rowsByAccountType indexes every loaded row by (cloud account, type).
	rowsByAccountType map[string][]CloudResourceRow
	// enisByPrivateIP maps a private IPv4 address to the network-interface rows
	// carrying it — the index behind the per-IP describe-network-interfaces call.
	enisByPrivateIP map[string][]CloudResourceRow
	// lbByARN indexes load balancers by ARN, the identifier target-group and tag
	// lookups arrive with.
	lbByARN map[string]CloudResourceRow

	mu     sync.Mutex
	hits   map[string]int
	misses map[string]int
}

// Types this store indexes. These are the `cloud_resourses.type` values behind
// the CLI calls migrated in the first pass; GCP types are read straight from the
// rows the GCP source already loads, so they are not listed here.
const (
	topologyTypeNetworkInterface = "network-interface"
	topologyTypeHostedZone       = "hostedzone"
)

// topologyLoadBalancerTypes are every spelling of "load balancer" the collector
// has written. `loadbalancer` is the current canonical type; the underscored
// forms are older rows that were never rewritten.
var topologyLoadBalancerTypes = []string{
	"loadbalancer",
	"application_loadbalancer",
	"network_loadbalancer",
	"classic_loadbalancer",
}

// topologyStoreTypes is the full type filter for the loading query.
func topologyStoreTypes() []string {
	types := make([]string, 0, len(topologyLoadBalancerTypes)+2)
	types = append(types, topologyTypeNetworkInterface, topologyTypeHostedZone)
	types = append(types, topologyLoadBalancerTypes...)
	return types
}

// CloudTopologyFromDBEnabled reports whether topology lookups should consult the
// table at all. The kill switch exists so a bad mapper can be reverted to
// CLI-always without a deploy of application code.
func CloudTopologyFromDBEnabled() bool {
	return config.Config.FeatureKGCloudTopologyFromDB
}

// NewCloudTopologyStore loads and indexes one tenant's topology rows. Returns a
// nil store (and no error) when the feature is disabled, which makes every
// lookup a miss and every caller fall back to its CLI path.
func NewCloudTopologyStore(tenantID string, logger *slog.Logger) (*CloudTopologyStore, error) {
	if !CloudTopologyFromDBEnabled() {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	// Mirrors the SELECT in fetchCloudResourcesMap: same columns, same
	// is_active filter, same cloud_accounts join for the provider account number.
	query := `
		SELECT
			cr.id, cr.resourse_id, cr.name, cr.type, cr.status, cr.account, cr.tenant,
			cr.cloud_provider, cr.region, cr.arn, cr.tags, cr.meta, cr.service_name,
			cr.is_active, cr.external_resource_id,
			ca.account_number
		FROM cloud_resourses cr
		LEFT JOIN cloud_accounts ca ON cr.account = ca.id
		WHERE cr.tenant = $1
			AND cr.is_active = true
			AND cr.type = ANY($2)
	`

	var rows []CloudResourceRow
	if err := dbManager.Db.Select(&rows, query, tenantID, topologyStoreTypes()); err != nil {
		return nil, fmt.Errorf("failed to query cloud topology: %w", err)
	}

	return NewCloudTopologyStoreFromRows(tenantID, rows, logger), nil
}

// NewCloudTopologyStoreFromRows indexes rows the caller already holds. The cloud
// sources fetch every row for the account at the start of BuildGraph, so they use
// this rather than paying for a second query.
func NewCloudTopologyStoreFromRows(tenantID string, rows []CloudResourceRow, logger *slog.Logger) *CloudTopologyStore {
	if logger == nil {
		logger = slog.Default()
	}

	store := &CloudTopologyStore{
		logger:            logger,
		tenantID:          tenantID,
		rowsByAccountType: make(map[string][]CloudResourceRow),
		enisByPrivateIP:   make(map[string][]CloudResourceRow),
		lbByARN:           make(map[string]CloudResourceRow),
		hits:              make(map[string]int),
		misses:            make(map[string]int),
	}

	for _, row := range rows {
		key := accountTypeKey(row.Account, row.Type)
		store.rowsByAccountType[key] = append(store.rowsByAccountType[key], row)

		switch {
		case row.Type == topologyTypeNetworkInterface:
			store.indexENI(row)
		case isLoadBalancerType(row.Type):
			store.indexLoadBalancer(row)
		}
	}

	logger.Debug("cloud topology store loaded",
		"tenant_id", tenantID,
		"rows", len(rows),
		"eni_private_ips", len(store.enisByPrivateIP),
		"load_balancers", len(store.lbByARN))

	return store
}

func accountTypeKey(accountID, resourceType string) string {
	return accountID + "\x00" + resourceType
}

func isLoadBalancerType(resourceType string) bool {
	for _, t := range topologyLoadBalancerTypes {
		if resourceType == t {
			return true
		}
	}
	return false
}

// indexENI records every private IPv4 the interface carries. The primary address
// is repeated inside PrivateIpAddresses, so both are read and duplicates are
// harmless — the index is keyed by IP, and one interface never appears twice for
// the same IP because the outer loop visits each row once.
func (s *CloudTopologyStore) indexENI(row CloudResourceRow) {
	var meta struct {
		PrivateIPAddress   string `json:"PrivateIpAddress"`
		PrivateIPAddresses []struct {
			PrivateIPAddress string `json:"PrivateIpAddress"`
		} `json:"PrivateIpAddresses"`
	}
	if err := json.Unmarshal(row.Meta, &meta); err != nil {
		return
	}

	seen := make(map[string]bool, len(meta.PrivateIPAddresses)+1)
	add := func(ip string) {
		if ip == "" || seen[ip] {
			return
		}
		seen[ip] = true
		s.enisByPrivateIP[ip] = append(s.enisByPrivateIP[ip], row)
	}

	add(meta.PrivateIPAddress)
	for _, addr := range meta.PrivateIPAddresses {
		add(addr.PrivateIPAddress)
	}
}

func (s *CloudTopologyStore) indexLoadBalancer(row CloudResourceRow) {
	var meta struct {
		LoadBalancerArn string `json:"LoadBalancerArn"`
	}
	// A load balancer with unparseable meta is still indexable by its arn column.
	_ = json.Unmarshal(row.Meta, &meta)

	if meta.LoadBalancerArn != "" {
		s.lbByARN[meta.LoadBalancerArn] = row
	}
	if row.ARN != "" {
		s.lbByARN[row.ARN] = row
	}
}

// --- Lookups -----------------------------------------------------------------
//
// Each returns (value, hit). hit == false means "run the CLI".

// ENIMetaByPrivateIP returns the raw `meta` of every network interface carrying
// the given private IP. The payload is a describe-network-interfaces element, so
// callers unmarshal it with the struct they already use for the CLI response.
func (s *CloudTopologyStore) ENIMetaByPrivateIP(ipAddress string) ([]json.RawMessage, bool) {
	if s == nil {
		return nil, false
	}
	rows := s.enisByPrivateIP[ipAddress]
	if len(rows) == 0 {
		return nil, s.record(topologyTypeNetworkInterface, false)
	}

	metas := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		metas = append(metas, row.Meta)
	}
	return metas, s.record(topologyTypeNetworkInterface, true)
}

// HostedZones returns the Route 53 hosted zones for one AWS account, in the shape
// FetchHostedZones produces from the CLI. `meta.Id` already carries the
// `/hostedzone/Z...` form, which is what downstream record lookups pass back.
func (s *CloudTopologyStore) HostedZones(accountID string) ([]Route53HostedZone, bool) {
	if s == nil {
		return nil, false
	}
	rows := s.rowsByAccountType[accountTypeKey(accountID, topologyTypeHostedZone)]
	if len(rows) == 0 {
		return nil, s.record(topologyTypeHostedZone, false)
	}

	zones := make([]Route53HostedZone, 0, len(rows))
	for _, row := range rows {
		var meta struct {
			ID     string `json:"Id"`
			Name   string `json:"Name"`
			Config struct {
				PrivateZone bool `json:"PrivateZone"`
			} `json:"Config"`
		}
		if err := json.Unmarshal(row.Meta, &meta); err != nil {
			continue
		}
		id := meta.ID
		if id == "" {
			id = row.ResourceID
		}
		name := meta.Name
		if name == "" {
			name = row.Name
		}
		if id == "" || name == "" {
			continue
		}
		zones = append(zones, Route53HostedZone{ID: id, Name: name, PrivateZone: meta.Config.PrivateZone})
	}

	if len(zones) == 0 {
		return nil, s.record(topologyTypeHostedZone, false)
	}
	return zones, s.record(topologyTypeHostedZone, true)
}

// RecordSetsForZone returns a hosted zone's record sets. hostedZoneID is accepted
// in either the bare (`Z0123`) or path (`/hostedzone/Z0123`) form, because
// callers pass through whatever FetchHostedZones handed them.
//
// A zone row whose meta has no RecordSets key is a miss, not an empty result: the
// distinction between "this zone has no records" and "we did not collect them" is
// the difference between a correct graph and a silently empty one.
func (s *CloudTopologyStore) RecordSetsForZone(accountID, hostedZoneID string) ([]Route53RecordSet, bool) {
	if s == nil {
		return nil, false
	}
	wanted := normalizeHostedZoneID(hostedZoneID)
	if wanted == "" {
		return nil, s.record("hostedzone_records", false)
	}

	for _, row := range s.rowsByAccountType[accountTypeKey(accountID, topologyTypeHostedZone)] {
		var id string
		if raw, ok := metaField(row.Meta, "Id"); ok {
			_ = json.Unmarshal(raw, &id)
		}
		if normalizeHostedZoneID(id) != wanted && normalizeHostedZoneID(row.ResourceID) != wanted {
			continue
		}

		raw, ok := metaField(row.Meta, "RecordSets")
		if !ok {
			return nil, s.record("hostedzone_records", false)
		}
		var records []Route53RecordSet
		if err := json.Unmarshal(raw, &records); err != nil {
			return nil, s.record("hostedzone_records", false)
		}
		return records, s.record("hostedzone_records", true)
	}

	return nil, s.record("hostedzone_records", false)
}

// TargetGroupsForLB returns the raw `meta.TargetGroups` entries for a load
// balancer. Each element is a describe-target-groups TargetGroups[] element with
// the collector's nested TargetHealthDescriptions added, so callers unmarshal it
// with their existing struct.
//
// Note for callers: the nested health descriptions are a daily snapshot and must
// not be used as live health. Target *membership* is topology and is safe; target
// *health* flips minute to minute and still requires describe-target-health.
func (s *CloudTopologyStore) TargetGroupsForLB(loadBalancerARN string) ([]json.RawMessage, bool) {
	if s == nil {
		return nil, false
	}
	row, ok := s.lbByARN[loadBalancerARN]
	if !ok {
		return nil, s.record("target_groups", false)
	}
	raw, ok := metaField(row.Meta, "TargetGroups")
	if !ok {
		return nil, s.record("target_groups", false)
	}

	var groups []json.RawMessage
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, s.record("target_groups", false)
	}
	return groups, s.record("target_groups", true)
}

// TagsForLB returns a load balancer's tags as a flat map, replacing an
// `elbv2 describe-tags` call.
func (s *CloudTopologyStore) TagsForLB(loadBalancerARN string) (map[string]string, bool) {
	if s == nil {
		return nil, false
	}
	row, ok := s.lbByARN[loadBalancerARN]
	if !ok || len(row.Tags) == 0 {
		return nil, s.record("loadbalancer_tags", false)
	}

	// The tags column is the collector's key -> []value shape.
	var raw map[string][]string
	if err := json.Unmarshal(row.Tags, &raw); err != nil {
		return nil, s.record("loadbalancer_tags", false)
	}

	tags := make(map[string]string, len(raw))
	for key, values := range raw {
		if len(values) > 0 {
			tags[key] = values[0]
		} else {
			tags[key] = ""
		}
	}
	return tags, s.record("loadbalancer_tags", true)
}

// --- Instrumentation ---------------------------------------------------------

// record tallies the lookup and returns hit unchanged, so call sites read as
// `return value, s.record(kind, ok)`.
func (s *CloudTopologyStore) record(kind string, hit bool) bool {
	if s == nil {
		return hit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if hit {
		s.hits[kind]++
	} else {
		s.misses[kind]++
	}
	return hit
}

// LogStats emits one line per lookup kind. A high miss rate is the signal that a
// mapper is wrong or that discovery is not collecting a type on this account —
// without it a broken mapper is invisible, because the CLI fallback keeps the
// graph correct while the CPU saving silently never arrives.
func (s *CloudTopologyStore) LogStats(context string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	kinds := make([]string, 0, len(s.hits)+len(s.misses))
	for kind := range s.hits {
		kinds = append(kinds, kind)
	}
	for kind := range s.misses {
		if _, ok := s.hits[kind]; !ok {
			kinds = append(kinds, kind)
		}
	}
	sort.Strings(kinds)

	for _, kind := range kinds {
		hits, misses := s.hits[kind], s.misses[kind]
		s.logger.Info("cloud topology lookup stats",
			"context", context,
			"tenant_id", s.tenantID,
			"lookup", kind,
			"hits", hits,
			"misses", misses,
			"cli_fallbacks", misses)
	}
}

// --- helpers -----------------------------------------------------------------

// normalizeHostedZoneID reduces both `/hostedzone/Z0123` and `Z0123` to `Z0123`.
func normalizeHostedZoneID(id string) string {
	id = strings.TrimSpace(id)
	if idx := strings.LastIndex(id, "/"); idx >= 0 {
		id = id[idx+1:]
	}
	return id
}

// metaField returns a meta payload's raw value for a key, and whether the key was
// present at all. Presence is a different question from emptiness and the two
// must not be collapsed: an absent key means the collector never captured the
// field, so the caller has to fall back to the cloud CLI, while an empty array
// means it did capture it and the resource genuinely has none.
//
// Decoding shallowly into json.RawMessage also means the field's contents are
// parsed once, by the caller, and only when the key is actually there.
func metaField(meta json.RawMessage, key string) (json.RawMessage, bool) {
	if len(meta) == 0 {
		return nil, false
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(meta, &probe); err != nil {
		return nil, false
	}
	raw, ok := probe[key]
	return raw, ok
}

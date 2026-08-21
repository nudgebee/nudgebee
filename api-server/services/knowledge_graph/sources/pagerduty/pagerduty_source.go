// Package pagerduty implements a tenant-scoped Knowledge Graph source for
// PagerDuty: users, teams, and services. It reuses the existing `pagerduty`
// integration (api-server/services/integrations/pagerduty.go) for credentials —
// no new credential storage.
//
// Deliberately out of scope for v1 (see plan): schedules, schedule layers,
// escalation policies, escalation policy rules, vendors, per-service integrations,
// incidents/alerts/on-calls.
package pagerduty

import (
	"fmt"
	"log/slog"
	"strings"

	gopagerduty "github.com/PagerDuty/go-pagerduty"

	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

func init() {
	sources.RegisterSourceFactory("pagerduty", func(config sources.SourceConfig, logger *slog.Logger) (core.SourceInterface, error) {
		return NewPagerDutySource(logger), nil
	}, "PagerDuty source control (users, teams, services)")
}

// PagerDutySource implements core.SourceInterface + core.TenantScopedSource.
type PagerDutySource struct {
	sources.BaseSource
	logger  *slog.Logger
	enabled bool
}

// NewPagerDutySource creates a new PagerDuty source.
func NewPagerDutySource(logger *slog.Logger) *PagerDutySource {
	if logger == nil {
		logger = slog.Default()
	}
	return &PagerDutySource{
		BaseSource: sources.NewBaseSource("pagerduty"),
		logger:     logger,
		enabled:    true,
	}
}

func (s *PagerDutySource) GetName() string { return "pagerduty" }

func (s *PagerDutySource) IsEnabled() bool { return s.enabled }

func (s *PagerDutySource) Validate() error { return nil }

const pagerDutyDefaultURL = "https://api.pagerduty.com"

// integrationConfig is the decrypted config of one `type='pagerduty'` integrations row.
type integrationConfig struct {
	URL      string
	Password string // API key
}

// ListInstances discovers the tenant's configured PagerDuty integrations — there can
// be more than one. Mirrors the tenant+type+status lookup in the GitHub/GitLab
// sources' ListInstances.
func (s *PagerDutySource) ListInstances(ctx *security.RequestContext, tenantID string) ([]core.IntegrationInstance, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("pagerduty: failed to get database manager: %w", err)
	}

	rows, err := dbms.Db.Queryx(`
		SELECT id::text, name
		FROM integrations
		WHERE tenant_id = $1 AND type = $2 AND status = 'enabled'
	`, tenantID, integrations.IntegrationPagerDuty)
	if err != nil {
		return nil, fmt.Errorf("pagerduty: failed to list integrations: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			s.logger.Warn("pagerduty: failed to close integration rows", "error", cerr)
		}
	}()

	var results []core.IntegrationInstance
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("pagerduty: failed to scan integration row: %w", err)
		}
		results = append(results, core.IntegrationInstance{ID: id, Name: name})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pagerduty: error iterating integration rows: %w", err)
	}
	return results, nil
}

// loadIntegrationConfig reads and decrypts one integration's config_values.
func (s *PagerDutySource) loadIntegrationConfig(integrationID string) (*integrationConfig, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("pagerduty: failed to get database manager: %w", err)
	}

	rows, err := dbms.Db.Queryx(`
		SELECT name::text, value::text, is_encrypted
		FROM integration_config_values
		WHERE integration_id = $1
	`, integrationID)
	if err != nil {
		return nil, fmt.Errorf("pagerduty: failed to query config values: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			s.logger.Warn("pagerduty: failed to close config rows", "error", cerr)
		}
	}()

	values := make(map[string]string)
	for rows.Next() {
		var name, value string
		var isEncrypted bool
		if err := rows.Scan(&name, &value, &isEncrypted); err != nil {
			return nil, fmt.Errorf("pagerduty: failed to scan config value: %w", err)
		}
		if isEncrypted && value != "" {
			decrypted, decErr := common.Decrypt(value)
			if decErr != nil {
				return nil, fmt.Errorf("pagerduty: failed to decrypt config value %q: %w", name, decErr)
			}
			value = decrypted
		}
		values[name] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pagerduty: error iterating config rows: %w", err)
	}

	cfg := &integrationConfig{
		URL:      normalizeEndpoint(values[integrations.PagerDutyConfigUrl]),
		Password: values[integrations.PagerDutyConfigPassword],
	}
	if cfg.Password == "" {
		return nil, fmt.Errorf("pagerduty: integration %s is missing API key config", integrationID)
	}
	return cfg, nil
}

// normalizeEndpoint mirrors ticket-server/clients/pagerduty_client.go::NormalizePagerDutyEndpoint:
// defaults to the public PagerDuty API, adds a scheme if missing, and trims a trailing slash.
// Reimplemented locally since ticket-server is a separate Go module.
func normalizeEndpoint(rawURL string) string {
	if rawURL == "" {
		return pagerDutyDefaultURL
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	return strings.TrimSuffix(rawURL, "/")
}

// buildClient resolves a *pagerduty.Client for the given config. PagerDuty auth here
// is a single API token (integrations/pagerduty.go's ConfigSchema has no App/OAuth
// mode), so there is exactly one client-construction path.
func buildClient(cfg *integrationConfig) *gopagerduty.Client {
	return gopagerduty.NewClient(cfg.Password, gopagerduty.WithAPIEndpoint(cfg.URL))
}

// BuildGraph fetches one configured PagerDuty integration's users, teams, and
// services, and turns them into KG nodes/edges. req.CloudAccountID is the
// integrations.id for this instance (see ListInstances / core.TenantScopedSource) —
// a dispatch/partition key only, not a real cloud_accounts row.
//
// The team-member fetch below warn-and-continues on ordinary errors (a single team
// lacking permissions shouldn't blank the whole graph), but a context
// cancellation/timeout is different: since UserAccount/UserGroup/OnCallService are
// InfraAuthoritativeNodeTypes, a partial-but-"successful" graph would tombstone every
// real member/team/service that a timeout happened to cut off. ctx.Err() is checked
// after that fetch so a cancellation aborts the whole build instead (same pattern the
// GitHub/GitLab sources use).
func (s *PagerDutySource) BuildGraph(reqCtx *security.RequestContext, req *core.SourceBuildRequest) (*core.Graph, error) {
	cfg, err := s.loadIntegrationConfig(req.CloudAccountID)
	if err != nil {
		return nil, err
	}

	client := buildClient(cfg)
	ctx := reqCtx.GetContext()

	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	userNodesByID, err := s.fetchUsers(ctx, client, req)
	if err != nil {
		return nil, fmt.Errorf("pagerduty: failed to fetch users: %w", err)
	}
	for _, n := range userNodesByID {
		nodes = append(nodes, n)
	}

	// Teams require a PagerDuty plan/ability that not every account has (observed in
	// practice: a 402 "teams account ability is required" response) — warn-and-continue
	// so accounts without Teams still get Users + Services, matching the GitHub/GitLab
	// sources' tolerance for a single degraded sub-fetch not blanking the whole graph.
	teamNodesByID, teamEdges, err := s.fetchTeams(ctx, client, req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s.logger.Warn("pagerduty: failed to fetch teams", "error", err)
		teamNodesByID = map[string]*core.DbNode{}
	} else {
		for _, n := range teamNodesByID {
			nodes = append(nodes, n)
		}
		edges = append(edges, teamEdges...)
	}

	for teamID, teamNode := range teamNodesByID {
		memberEdges, err := s.fetchTeamMembers(ctx, client, teamID, teamNode, userNodesByID, req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			s.logger.Warn("pagerduty: failed to fetch team members", "team_id", teamID, "error", err)
			continue
		}
		edges = append(edges, memberEdges...)
	}

	serviceNodes, serviceEdges, err := s.fetchServices(ctx, client, teamNodesByID, req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s.logger.Warn("pagerduty: failed to fetch services", "error", err)
	} else {
		nodes = append(nodes, serviceNodes...)
		edges = append(edges, serviceEdges...)
	}

	return &core.Graph{
		Nodes:          nodes,
		Edges:          edges,
		Source:         "pagerduty",
		TenantID:       req.TenantID,
		CloudAccountID: req.CloudAccountID,
	}, nil
}

// newNode builds a node the same way every resource file in this package needs to:
// compute its unique key via the default BaseSource algorithm (properties["name"] +
// CloudAccountID), then hand off to core.NewNode for ID/query_attributes/ontology.
func (s *PagerDutySource) newNode(nodeType core.NodeType, properties map[string]interface{}, req *core.SourceBuildRequest) *core.DbNode {
	tempNode := &core.DbNode{
		NodeType:       nodeType,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)
	return core.NewNode(nodeType, uniqueKey, properties, req.TenantID, req.CloudAccountID, "pagerduty")
}

// newEdge builds an edge between two already-created nodes.
func (s *PagerDutySource) newEdge(source, target *core.DbNode, relType core.RelationshipType, properties map[string]interface{}, req *core.SourceBuildRequest) *core.DbEdge {
	if properties == nil {
		properties = map[string]interface{}{}
	}
	return core.NewEdge(source.ID, target.ID, relType, properties, req.TenantID, req.CloudAccountID, "pagerduty")
}

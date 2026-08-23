// Package gitlab implements a tenant-scoped Knowledge Graph source for GitLab:
// groups/personal namespaces, subgroups, projects, and users. It reuses the
// existing `gitlab` integration (api-server/services/integrations/gitlab.go) for
// credentials — no new credential storage.
//
// Deliberately out of scope for v1 (see plan): branches, commits, merge requests,
// CI/CD (runners/variables/environments/.gitlab-ci.yml), container registry,
// dependency/supply-chain data.
package gitlab

import (
	"fmt"
	"log/slog"

	golangGitlab "gitlab.com/gitlab-org/api/client-go"

	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

func init() {
	sources.RegisterSourceFactory("gitlab", func(config sources.SourceConfig, logger *slog.Logger) (core.SourceInterface, error) {
		return NewGitLabSource(logger), nil
	}, "GitLab source control (groups, subgroups, projects, users)")
}

// GitLabSource implements core.SourceInterface + core.TenantScopedSource.
type GitLabSource struct {
	sources.BaseSource
	logger  *slog.Logger
	enabled bool
}

// NewGitLabSource creates a new GitLab source.
func NewGitLabSource(logger *slog.Logger) *GitLabSource {
	if logger == nil {
		logger = slog.Default()
	}
	return &GitLabSource{
		BaseSource: sources.NewBaseSource("gitlab"),
		logger:     logger,
		enabled:    true,
	}
}

func (s *GitLabSource) GetName() string { return "gitlab" }

func (s *GitLabSource) IsEnabled() bool { return s.enabled }

func (s *GitLabSource) Validate() error { return nil }

const gitlabDefaultURL = "https://gitlab.com"

// integrationConfig is the decrypted config of one `type='gitlab'` integrations row.
type integrationConfig struct {
	URL      string
	Username string
	Password string // personal access token
	Group    string // optional: group id/full path to scope this instance to
}

// ListInstances discovers the tenant's configured GitLab integrations — there can be
// more than one (e.g. multiple connected groups/accounts). Mirrors the tenant+type+status
// lookup in api-server/services/account/adapter/gitlab.go::getGitLabCredentials, but
// returns every matching row instead of one by config name.
func (s *GitLabSource) ListInstances(ctx *security.RequestContext, tenantID string) ([]core.IntegrationInstance, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("gitlab: failed to get database manager: %w", err)
	}

	rows, err := dbms.Db.Queryx(`
		SELECT id::text, name
		FROM integrations
		WHERE tenant_id = $1 AND type = $2 AND status = 'enabled'
	`, tenantID, integrations.IntegrationGitlab)
	if err != nil {
		return nil, fmt.Errorf("gitlab: failed to list integrations: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			s.logger.Warn("gitlab: failed to close integration rows", "error", cerr)
		}
	}()

	var results []core.IntegrationInstance
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("gitlab: failed to scan integration row: %w", err)
		}
		results = append(results, core.IntegrationInstance{ID: id, Name: name})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("gitlab: error iterating integration rows: %w", err)
	}
	return results, nil
}

// loadIntegrationConfig reads and decrypts one integration's config_values, mirroring
// the read+decrypt loop in account/adapter/gitlab.go::getGitLabCredentials.
func (s *GitLabSource) loadIntegrationConfig(integrationID string) (*integrationConfig, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("gitlab: failed to get database manager: %w", err)
	}

	rows, err := dbms.Db.Queryx(`
		SELECT name::text, value::text, is_encrypted
		FROM integration_config_values
		WHERE integration_id = $1
	`, integrationID)
	if err != nil {
		return nil, fmt.Errorf("gitlab: failed to query config values: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			s.logger.Warn("gitlab: failed to close config rows", "error", cerr)
		}
	}()

	values := make(map[string]string)
	for rows.Next() {
		var name, value string
		var isEncrypted bool
		if err := rows.Scan(&name, &value, &isEncrypted); err != nil {
			return nil, fmt.Errorf("gitlab: failed to scan config value: %w", err)
		}
		if isEncrypted && value != "" {
			decrypted, decErr := common.Decrypt(value)
			if decErr != nil {
				return nil, fmt.Errorf("gitlab: failed to decrypt config value %q: %w", name, decErr)
			}
			value = decrypted
		}
		values[name] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("gitlab: error iterating config rows: %w", err)
	}

	cfg := &integrationConfig{
		URL:      values[integrations.GitlabConfigUrl],
		Username: values[integrations.GitlabConfigUsername],
		Password: values[integrations.GitlabConfigPassword],
		Group:    values[integrations.GitlabConfigGroup],
	}
	if cfg.URL == "" {
		cfg.URL = gitlabDefaultURL
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, fmt.Errorf("gitlab: integration %s is missing username/password config", integrationID)
	}
	return cfg, nil
}

// buildClient resolves a *gitlab.Client for the given config. GitLab auth here is
// personal-access-token only (integrations/gitlab.go's ConfigSchema has no App/OAuth
// mode), so there is exactly one client-construction path.
func buildClient(cfg *integrationConfig) (*golangGitlab.Client, error) {
	client, err := golangGitlab.NewClient(cfg.Password, golangGitlab.WithBaseURL(cfg.URL))
	if err != nil {
		return nil, fmt.Errorf("gitlab: failed to create client: %w", err)
	}
	return client, nil
}

// BuildGraph fetches one configured GitLab group/account's organization, subgroups,
// projects, and users, and turns them into KG nodes/edges. req.CloudAccountID is the
// integrations.id for this instance (see ListInstances / core.TenantScopedSource) —
// a dispatch/partition key only, not a real cloud_accounts row.
//
// The subgroup/member/project-member fetches below warn-and-continue on ordinary
// errors (a single subgroup lacking permissions shouldn't blank the whole graph), but
// a context cancellation/timeout is different: since SourceControlOrg/UserAccount/
// UserGroup are InfraAuthoritativeNodeTypes, a partial-but-"successful" graph would
// tombstone every real member/subgroup that a timeout happened to cut off. ctx.Err()
// is checked after each of those fetches so a cancellation aborts the whole build
// instead (same pattern the GitHub source uses).
func (s *GitLabSource) BuildGraph(reqCtx *security.RequestContext, req *core.SourceBuildRequest) (*core.Graph, error) {
	cfg, err := s.loadIntegrationConfig(req.CloudAccountID)
	if err != nil {
		return nil, err
	}

	client, err := buildClient(cfg)
	if err != nil {
		return nil, err
	}
	ctx := reqCtx.GetContext()

	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)
	userNodes := make(map[string]*core.DbNode) // username -> node, deduped across owner/members/project-members

	ownerNode, ownerGroupID, isGroup, err := s.fetchOwner(ctx, client, cfg, userNodes, req)
	if err != nil {
		return nil, fmt.Errorf("gitlab: failed to fetch owner for integration %q: %w", req.CloudAccountID, err)
	}
	nodes = append(nodes, ownerNode)

	var (
		projectNodes []*core.DbNode
		projectEdges []*core.DbEdge
		projectsByID map[int64]*core.DbNode
	)

	if isGroup {
		groupNodesByID := map[int64]*core.DbNode{ownerGroupID: ownerNode}

		subgroupNodes, subgroupEdges, err := s.fetchSubgroups(ctx, client, ownerGroupID, ownerNode, groupNodesByID, req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			s.logger.Warn("gitlab: failed to fetch subgroups", "group", cfg.Group, "error", err)
		} else {
			nodes = append(nodes, subgroupNodes...)
			edges = append(edges, subgroupEdges...)
		}

		projectNodes, projectEdges, projectsByID, err = s.fetchGroupProjects(ctx, client, ownerGroupID, groupNodesByID, ownerNode, req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			s.logger.Warn("gitlab: failed to fetch group projects", "group", cfg.Group, "error", err)
		}
		nodes = append(nodes, projectNodes...)
		edges = append(edges, projectEdges...)

		for groupID, groupNode := range groupNodesByID {
			memberNodes, memberEdges, err := s.fetchGroupMembers(ctx, client, groupID, groupNode, userNodes, req)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				s.logger.Warn("gitlab: failed to fetch group members", "group_id", groupID, "error", err)
				continue
			}
			nodes = append(nodes, memberNodes...)
			edges = append(edges, memberEdges...)
		}
	} else {
		projectNodes, projectEdges, projectsByID, err = s.fetchOwnedProjects(ctx, client, ownerNode, req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			s.logger.Warn("gitlab: failed to fetch owned projects", "username", cfg.Username, "error", err)
		}
		nodes = append(nodes, projectNodes...)
		edges = append(edges, projectEdges...)
	}

	memberNodes, memberEdges, err := s.fetchProjectMembers(ctx, client, projectsByID, userNodes, req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s.logger.Warn("gitlab: failed to fetch project members", "error", err)
	} else {
		nodes = append(nodes, memberNodes...)
		edges = append(edges, memberEdges...)
	}

	return &core.Graph{
		Nodes:          nodes,
		Edges:          edges,
		Source:         "gitlab",
		TenantID:       req.TenantID,
		CloudAccountID: req.CloudAccountID,
	}, nil
}

// newNode builds a node the same way every resource file in this package needs to:
// compute its unique key via the default BaseSource algorithm (properties["name"] +
// CloudAccountID), then hand off to core.NewNode for ID/query_attributes/ontology.
func (s *GitLabSource) newNode(nodeType core.NodeType, properties map[string]interface{}, req *core.SourceBuildRequest) *core.DbNode {
	tempNode := &core.DbNode{
		NodeType:       nodeType,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)
	return core.NewNode(nodeType, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gitlab")
}

// newEdge builds an edge between two already-created nodes.
func (s *GitLabSource) newEdge(source, target *core.DbNode, relType core.RelationshipType, properties map[string]interface{}, req *core.SourceBuildRequest) *core.DbEdge {
	if properties == nil {
		properties = map[string]interface{}{}
	}
	return core.NewEdge(source.ID, target.ID, relType, properties, req.TenantID, req.CloudAccountID, "gitlab")
}

// accessLevelRole maps GitLab's numeric access_level to the role string stored on
// MEMBER_OF edge properties.
func accessLevelRole(level golangGitlab.AccessLevelValue) string {
	switch level {
	case golangGitlab.OwnerPermissions:
		return "owner"
	case golangGitlab.MaintainerPermissions:
		return "maintainer"
	case golangGitlab.DeveloperPermissions:
		return "developer"
	case golangGitlab.ReporterPermissions:
		return "reporter"
	case golangGitlab.GuestPermissions:
		return "guest"
	default:
		return ""
	}
}

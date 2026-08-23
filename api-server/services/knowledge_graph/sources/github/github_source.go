// Package github implements a tenant-scoped Knowledge Graph source for GitHub:
// organizations/personal accounts, repositories, teams, and users. It reuses the
// existing `github` integration (api-server/services/integrations/github_issues.go)
// for credentials — no new credential storage.
//
// Deliberately out of scope for v1 (see plan): branches, commits, pull requests,
// GitHub Actions/workflows, packages, Dependabot alerts, supply-chain/SBOM data.
package github

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	gogithub "github.com/google/go-github/v61/github"

	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

func init() {
	sources.RegisterSourceFactory("github", func(config sources.SourceConfig, logger *slog.Logger) (core.SourceInterface, error) {
		return NewGitHubSource(logger), nil
	}, "GitHub source control (organizations, repositories, teams, users)")
}

// GitHubSource implements core.SourceInterface + core.TenantScopedSource.
type GitHubSource struct {
	sources.BaseSource
	logger  *slog.Logger
	enabled bool
}

// NewGitHubSource creates a new GitHub source.
func NewGitHubSource(logger *slog.Logger) *GitHubSource {
	if logger == nil {
		logger = slog.Default()
	}
	return &GitHubSource{
		BaseSource: sources.NewBaseSource("github"),
		logger:     logger,
		enabled:    true,
	}
}

func (s *GitHubSource) GetName() string { return "github" }

func (s *GitHubSource) IsEnabled() bool { return s.enabled }

func (s *GitHubSource) Validate() error { return nil }

// integrationConfig is the decrypted config of one `type='github'` integrations row.
type integrationConfig struct {
	Username string
	Password string
	AuthType string
}

// ListInstances discovers the tenant's configured GitHub integrations — there can be
// more than one (e.g. multiple connected orgs/accounts). Mirrors the tenant+type+status
// lookup in api-server/services/account/adapter/github.go::getGitCredentials, but
// returns every matching row instead of one by config name.
func (s *GitHubSource) ListInstances(ctx *security.RequestContext, tenantID string) ([]core.IntegrationInstance, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("github: failed to get database manager: %w", err)
	}

	rows, err := dbms.Db.Queryx(`
		SELECT id::text, name
		FROM integrations
		WHERE tenant_id = $1 AND type = $2 AND status = 'enabled'
	`, tenantID, integrations.IntegrationGithubIssues)
	if err != nil {
		return nil, fmt.Errorf("github: failed to list integrations: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			s.logger.Warn("github: failed to close integration rows", "error", cerr)
		}
	}()

	var results []core.IntegrationInstance
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("github: failed to scan integration row: %w", err)
		}
		results = append(results, core.IntegrationInstance{ID: id, Name: name})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("github: error iterating integration rows: %w", err)
	}
	return results, nil
}

// loadIntegrationConfig reads and decrypts one integration's config_values, mirroring
// the read+decrypt loop in account/adapter/github.go::getGitCredentials.
func (s *GitHubSource) loadIntegrationConfig(integrationID string) (*integrationConfig, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("github: failed to get database manager: %w", err)
	}

	rows, err := dbms.Db.Queryx(`
		SELECT name::text, value::text, is_encrypted
		FROM integration_config_values
		WHERE integration_id = $1
	`, integrationID)
	if err != nil {
		return nil, fmt.Errorf("github: failed to query config values: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			s.logger.Warn("github: failed to close config rows", "error", cerr)
		}
	}()

	values := make(map[string]string)
	for rows.Next() {
		var name, value string
		var isEncrypted bool
		if err := rows.Scan(&name, &value, &isEncrypted); err != nil {
			return nil, fmt.Errorf("github: failed to scan config value: %w", err)
		}
		if isEncrypted && value != "" {
			decrypted, decErr := common.Decrypt(value)
			if decErr != nil {
				return nil, fmt.Errorf("github: failed to decrypt config value %q: %w", name, decErr)
			}
			value = decrypted
		}
		values[name] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("github: error iterating config rows: %w", err)
	}

	cfg := &integrationConfig{
		Username: values[integrations.GithubConfigUsername],
		Password: values[integrations.GithubConfigPassword],
		AuthType: values[integrations.GithubConfigAuthType],
	}
	if cfg.AuthType == "" {
		cfg.AuthType = "token"
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, fmt.Errorf("github: integration %s is missing username/password config", integrationID)
	}
	return cfg, nil
}

// buildClient resolves a *github.Client for the given config. PAT (token) and GitHub
// App (application) auth both collapse to a single bearer token — WithAuthToken
// applies identically to either — so there is exactly one client-construction path.
func buildClient(ctx context.Context, cfg *integrationConfig) (*gogithub.Client, error) {
	token := cfg.Password
	if cfg.AuthType == "application" {
		installationID, err := strconv.ParseInt(cfg.Password, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("github: invalid installation ID: %w", err)
		}
		token, err = common.GetGithubAppInstallationToken(ctx, installationID)
		if err != nil {
			return nil, fmt.Errorf("github: failed to exchange installation token: %w", err)
		}
	}
	return gogithub.NewClient(nil).WithAuthToken(token), nil
}

// BuildGraph fetches one configured GitHub org/account's organization, repositories,
// teams, and users, and turns them into KG nodes/edges. req.CloudAccountID is the
// integrations.id for this instance (see ListInstances / core.TenantScopedSource) —
// a dispatch/partition key only, not a real cloud_accounts row.
//
// The members/teams/collaborators fetches below warn-and-continue on ordinary errors
// (a single team lacking permissions shouldn't blank the whole graph), but a context
// cancellation/timeout is different: since SourceControlOrg/UserAccount/UserGroup are
// InfraAuthoritativeNodeTypes, a partial-but-"successful" graph would tombstone every
// real member/team/collaborator that a timeout happened to cut off. ctx.Err() is
// checked after each of those fetches so a cancellation aborts the whole build instead.
func (s *GitHubSource) BuildGraph(reqCtx *security.RequestContext, req *core.SourceBuildRequest) (*core.Graph, error) {
	cfg, err := s.loadIntegrationConfig(req.CloudAccountID)
	if err != nil {
		return nil, err
	}

	ctx := reqCtx.GetContext()
	client, err := buildClient(ctx, cfg)
	if err != nil {
		return nil, err
	}

	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)
	userNodes := make(map[string]*core.DbNode) // login -> node, deduped across owner/members/teams/collaborators

	ownerNode, isOrg, err := s.fetchOwner(ctx, client, cfg.Username, userNodes, req)
	if err != nil {
		return nil, fmt.Errorf("github: failed to fetch owner %q: %w", cfg.Username, err)
	}
	nodes = append(nodes, ownerNode)

	repoNodes, repoEdges, repoByName, err := s.fetchRepos(ctx, client, cfg.Username, isOrg, ownerNode, req)
	if err != nil {
		return nil, fmt.Errorf("github: failed to fetch repos for %q: %w", cfg.Username, err)
	}
	nodes = append(nodes, repoNodes...)
	edges = append(edges, repoEdges...)

	if isOrg {
		memberNodes, memberEdges, err := s.fetchOrgMembers(ctx, client, cfg.Username, ownerNode, userNodes, req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			s.logger.Warn("github: failed to fetch org members", "org", cfg.Username, "error", err)
		} else {
			nodes = append(nodes, memberNodes...)
			edges = append(edges, memberEdges...)
		}

		teamNodes, teamEdges, err := s.fetchTeams(ctx, client, cfg.Username, ownerNode, repoByName, userNodes, req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			s.logger.Warn("github: failed to fetch teams", "org", cfg.Username, "error", err)
		} else {
			nodes = append(nodes, teamNodes...)
			edges = append(edges, teamEdges...)
		}
	}

	collabNodes, collabEdges, err := s.fetchCollaborators(ctx, client, cfg.Username, repoByName, userNodes, req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s.logger.Warn("github: failed to fetch collaborators", "owner", cfg.Username, "error", err)
	} else {
		nodes = append(nodes, collabNodes...)
		edges = append(edges, collabEdges...)
	}

	return &core.Graph{
		Nodes:          nodes,
		Edges:          edges,
		Source:         "github",
		TenantID:       req.TenantID,
		CloudAccountID: req.CloudAccountID,
	}, nil
}

// newNode builds a node the same way every resource file in this package needs to:
// compute its unique key via the default BaseSource algorithm (properties["name"] +
// CloudAccountID), then hand off to core.NewNode for ID/query_attributes/ontology.
func (s *GitHubSource) newNode(nodeType core.NodeType, properties map[string]interface{}, req *core.SourceBuildRequest) *core.DbNode {
	tempNode := &core.DbNode{
		NodeType:       nodeType,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)
	return core.NewNode(nodeType, uniqueKey, properties, req.TenantID, req.CloudAccountID, "github")
}

// newEdge builds an edge between two already-created nodes.
func (s *GitHubSource) newEdge(source, target *core.DbNode, relType core.RelationshipType, properties map[string]interface{}, req *core.SourceBuildRequest) *core.DbEdge {
	if properties == nil {
		properties = map[string]interface{}{}
	}
	return core.NewEdge(source.ID, target.ID, relType, properties, req.TenantID, req.CloudAccountID, "github")
}

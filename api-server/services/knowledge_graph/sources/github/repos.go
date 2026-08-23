package github

import (
	"context"
	"fmt"
	"strings"
	"time"

	gogithub "github.com/google/go-github/v61/github"

	"nudgebee/services/knowledge_graph/core"
)

// githubRepositorySchema is the concrete property schema for the GitHubRepository
// specific_type. Names are the node.Properties keys written by newRepoNode.
var githubRepositorySchema = core.SpecificTypeSchema{
	SpecificType: "GitHubRepository",
	NodeType:     core.NodeTypeRepository,
	Properties: []core.PropertyDef{
		{Name: "full_name", Indexed: true},
		{Name: "language", Indexed: true},
		{Name: "private", Indexed: true},
		{Name: "archived", Indexed: true},
		{Name: "disabled"},
		{Name: "fork"},
		{Name: "default_branch"},
		{Name: "url", Indexed: true},
		{Name: "description"},
		{Name: "topics"},
		{Name: "created_at"},
		{Name: "updated_at"},
		{Name: "pushed_at"},
	},
}

func init() { core.RegisterSpecificTypeSchema(githubRepositorySchema) }

// fetchRepos lists every repository owned directly by the integration's configured
// org/account (not repos merely accessible via team/collaborator grants — those are
// modeled as HAS_ACCESS_TO edges onto these same nodes) and returns Repository nodes
// + an owner OWNS repo edge for each. repoNodes is keyed by the repo's short name,
// unique within one owner's repos — the scope every repo in this call shares, since
// one integration instance is configured against exactly one org/user.
func (s *GitHubSource) fetchRepos(
	ctx context.Context,
	client *gogithub.Client,
	username string,
	isOrg bool,
	ownerNode *core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode, error) {
	repos, err := listRepos(ctx, client, username, isOrg)
	if err != nil {
		return nil, nil, nil, err
	}

	nodes := make([]*core.DbNode, 0, len(repos))
	edges := make([]*core.DbEdge, 0, len(repos))
	repoNodes := make(map[string]*core.DbNode, len(repos))

	for _, repo := range repos {
		if repo == nil || repo.Name == nil {
			continue
		}
		node := s.newRepoNode(repo, req)
		nodes = append(nodes, node)
		repoNodes[*repo.Name] = node
		edges = append(edges, s.newEdge(ownerNode, node, core.RelationshipOwns, nil, req))
	}

	return nodes, edges, repoNodes, nil
}

func listRepos(ctx context.Context, client *gogithub.Client, username string, isOrg bool) ([]*gogithub.Repository, error) {
	var out []*gogithub.Repository

	if isOrg {
		opts := &gogithub.RepositoryListByOrgOptions{ListOptions: gogithub.ListOptions{PerPage: 100}}
		for {
			repos, resp, err := retryOnRateLimit(ctx, func() ([]*gogithub.Repository, *gogithub.Response, error) {
				return client.Repositories.ListByOrg(ctx, username, opts)
			})
			if err != nil {
				return nil, fmt.Errorf("list org repos: %w", err)
			}
			out = append(out, repos...)
			if resp.NextPage == 0 {
				break
			}
			opts.Page = resp.NextPage
		}
		return out, nil
	}

	// Personal account: scope strictly to repos the configured user owns — not
	// every repo the token happens to have collaborator/org access to.
	opts := &gogithub.RepositoryListByAuthenticatedUserOptions{
		Affiliation: "owner",
		ListOptions: gogithub.ListOptions{PerPage: 100},
	}
	for {
		repos, resp, err := retryOnRateLimit(ctx, func() ([]*gogithub.Repository, *gogithub.Response, error) {
			return client.Repositories.ListByAuthenticatedUser(ctx, opts)
		})
		if err != nil {
			return nil, fmt.Errorf("list user repos: %w", err)
		}
		out = append(out, repos...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (s *GitHubSource) newRepoNode(repo *gogithub.Repository, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"specific_type": "GitHubRepository",
		"name":          *repo.Name,
	}
	if repo.FullName != nil {
		properties["full_name"] = *repo.FullName
	}
	if repo.Description != nil {
		properties["description"] = *repo.Description
	}
	if repo.Language != nil {
		properties["language"] = *repo.Language
	}
	if repo.HTMLURL != nil {
		properties["url"] = *repo.HTMLURL
	}
	if repo.DefaultBranch != nil {
		properties["default_branch"] = *repo.DefaultBranch
	}
	if repo.Private != nil {
		properties["private"] = *repo.Private
	}
	if repo.Archived != nil {
		properties["archived"] = *repo.Archived
	}
	if repo.Disabled != nil {
		properties["disabled"] = *repo.Disabled
	}
	if repo.Fork != nil {
		properties["fork"] = *repo.Fork
	}
	if len(repo.Topics) > 0 {
		properties["topics"] = strings.Join(repo.Topics, ",")
	}
	if repo.CreatedAt != nil {
		properties["created_at"] = repo.CreatedAt.Format(time.RFC3339)
	}
	if repo.UpdatedAt != nil {
		properties["updated_at"] = repo.UpdatedAt.Format(time.RFC3339)
	}
	if repo.PushedAt != nil {
		properties["pushed_at"] = repo.PushedAt.Format(time.RFC3339)
	}
	return s.newNode(core.NodeTypeRepository, properties, req)
}

package github

import (
	"context"
	"fmt"

	gogithub "github.com/google/go-github/v61/github"

	"nudgebee/services/knowledge_graph/core"
)

// githubTeamSchema is the concrete property schema for the GitHubTeam specific_type.
// Names are the node.Properties keys written by newTeamNode.
var githubTeamSchema = core.SpecificTypeSchema{
	SpecificType: "GitHubTeam",
	NodeType:     core.NodeTypeUserGroup,
	Properties: []core.PropertyDef{
		{Name: "slug", Indexed: true},
		{Name: "description"},
		{Name: "privacy"},
		{Name: "url"},
	},
}

func init() { core.RegisterSpecificTypeSchema(githubTeamSchema) }

// fetchTeams lists the org's teams and returns UserGroup nodes plus:
//   - Team --BELONGS_TO--> Org
//   - child Team --MEMBER_OF--> parent Team (nested teams)
//   - User --MEMBER_OF--> Team (members/maintainers, via fetchTeamMembers)
//   - Team --HAS_ACCESS_TO--> Repository (team repo permissions, via fetchTeamRepos)
func (s *GitHubSource) fetchTeams(
	ctx context.Context,
	client *gogithub.Client,
	org string,
	orgNode *core.DbNode,
	repoNodes map[string]*core.DbNode,
	userNodes map[string]*core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbNode, []*core.DbEdge, error) {
	teams, err := listTeams(ctx, client, org)
	if err != nil {
		return nil, nil, err
	}

	nodes := make([]*core.DbNode, 0, len(teams))
	edges := make([]*core.DbEdge, 0)
	teamNodesBySlug := make(map[string]*core.DbNode, len(teams))

	for _, team := range teams {
		if team == nil || team.Slug == nil {
			continue
		}
		node := s.newTeamNode(team, req)
		nodes = append(nodes, node)
		teamNodesBySlug[*team.Slug] = node
		edges = append(edges, s.newEdge(node, orgNode, core.RelationshipBelongsTo, nil, req))
	}

	// Second pass: nested-team edges, once every team node exists.
	for _, team := range teams {
		if team == nil || team.Slug == nil || team.Parent == nil || team.Parent.Slug == nil {
			continue
		}
		childNode, childOK := teamNodesBySlug[*team.Slug]
		parentNode, parentOK := teamNodesBySlug[*team.Parent.Slug]
		if childOK && parentOK {
			edges = append(edges, s.newEdge(childNode, parentNode, core.RelationshipMemberOf, nil, req))
		}
	}

	for _, team := range teams {
		if team == nil || team.Slug == nil {
			continue
		}
		teamNode := teamNodesBySlug[*team.Slug]

		memberNodes, memberEdges, mErr := s.fetchTeamMembers(ctx, client, org, *team.Slug, teamNode, userNodes, req)
		if mErr != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			s.logger.Warn("github: failed to fetch team members", "team", *team.Slug, "error", mErr)
		} else {
			nodes = append(nodes, memberNodes...)
			edges = append(edges, memberEdges...)
		}

		repoEdges, rErr := s.fetchTeamRepos(ctx, client, org, *team.Slug, teamNode, repoNodes, req)
		if rErr != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			s.logger.Warn("github: failed to fetch team repos", "team", *team.Slug, "error", rErr)
		} else {
			edges = append(edges, repoEdges...)
		}
	}

	return nodes, edges, nil
}

func listTeams(ctx context.Context, client *gogithub.Client, org string) ([]*gogithub.Team, error) {
	var out []*gogithub.Team
	opts := &gogithub.ListOptions{PerPage: 100}
	for {
		teams, resp, err := retryOnRateLimit(ctx, func() ([]*gogithub.Team, *gogithub.Response, error) {
			return client.Teams.ListTeams(ctx, org, opts)
		})
		if err != nil {
			return nil, fmt.Errorf("list teams: %w", err)
		}
		out = append(out, teams...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (s *GitHubSource) newTeamNode(team *gogithub.Team, req *core.SourceBuildRequest) *core.DbNode {
	name := *team.Slug
	if team.Name != nil && *team.Name != "" {
		name = *team.Name
	}
	properties := map[string]interface{}{
		"specific_type": "GitHubTeam",
		"name":          name,
		"slug":          *team.Slug,
	}
	if team.Description != nil {
		properties["description"] = *team.Description
	}
	if team.Privacy != nil {
		properties["privacy"] = *team.Privacy
	}
	if team.HTMLURL != nil {
		properties["url"] = *team.HTMLURL
	}
	return s.newNode(core.NodeTypeUserGroup, properties, req)
}

// fetchTeamMembers fetches maintainers first (to build a role lookup), then all
// members once, so each member gets exactly one MEMBER_OF edge with the correct role
// instead of a separate call per user.
func (s *GitHubSource) fetchTeamMembers(
	ctx context.Context,
	client *gogithub.Client,
	org, slug string,
	teamNode *core.DbNode,
	userNodes map[string]*core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbNode, []*core.DbEdge, error) {
	maintainers := make(map[string]bool)
	maintainerUsers, err := listTeamMembers(ctx, client, org, slug, "maintainer")
	if err != nil {
		return nil, nil, fmt.Errorf("list team maintainers: %w", err)
	}
	for _, u := range maintainerUsers {
		if u.Login != nil {
			maintainers[*u.Login] = true
		}
	}

	allMembers, err := listTeamMembers(ctx, client, org, slug, "all")
	if err != nil {
		return nil, nil, fmt.Errorf("list team members: %w", err)
	}

	nodes := make([]*core.DbNode, 0, len(allMembers))
	edges := make([]*core.DbEdge, 0, len(allMembers))
	for _, u := range allMembers {
		userNode, isNew := s.newUserNode(u, userNodes, req)
		if userNode == nil {
			continue
		}
		if isNew {
			nodes = append(nodes, userNode)
		}
		role := "member"
		if u.Login != nil && maintainers[*u.Login] {
			role = "maintainer"
		}
		edges = append(edges, s.newEdge(userNode, teamNode, core.RelationshipMemberOf,
			map[string]interface{}{"role": role}, req))
	}
	return nodes, edges, nil
}

func listTeamMembers(ctx context.Context, client *gogithub.Client, org, slug, role string) ([]*gogithub.User, error) {
	var out []*gogithub.User
	opts := &gogithub.TeamListTeamMembersOptions{Role: role, ListOptions: gogithub.ListOptions{PerPage: 100}}
	for {
		members, resp, err := retryOnRateLimit(ctx, func() ([]*gogithub.User, *gogithub.Response, error) {
			return client.Teams.ListTeamMembersBySlug(ctx, org, slug, opts)
		})
		if err != nil {
			return nil, err
		}
		out = append(out, members...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// fetchTeamRepos returns Team --HAS_ACCESS_TO--> Repository edges for repos the team
// has been granted access to. Repos outside this integration's own repo set (e.g. a
// team granted access to a repo under a different org) are skipped — repoNodes only
// contains nodes already built by fetchRepos for req.CloudAccountID.
func (s *GitHubSource) fetchTeamRepos(
	ctx context.Context,
	client *gogithub.Client,
	org, slug string,
	teamNode *core.DbNode,
	repoNodes map[string]*core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbEdge, error) {
	var repos []*gogithub.Repository
	opts := &gogithub.ListOptions{PerPage: 100}
	for {
		page, resp, err := retryOnRateLimit(ctx, func() ([]*gogithub.Repository, *gogithub.Response, error) {
			return client.Teams.ListTeamReposBySlug(ctx, org, slug, opts)
		})
		if err != nil {
			return nil, fmt.Errorf("list team repos: %w", err)
		}
		repos = append(repos, page...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	edges := make([]*core.DbEdge, 0, len(repos))
	for _, repo := range repos {
		if repo == nil || repo.Name == nil {
			continue
		}
		repoNode, ok := repoNodes[*repo.Name]
		if !ok {
			continue
		}
		edges = append(edges, s.newEdge(teamNode, repoNode, core.RelationshipHasAccessTo,
			map[string]interface{}{"permission": derivePermission(nil, repo.Permissions)}, req))
	}
	return edges, nil
}

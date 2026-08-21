package github

import (
	"context"
	"fmt"

	gogithub "github.com/google/go-github/v61/github"

	"nudgebee/services/knowledge_graph/core"
)

// githubUserSchema is the concrete property schema for the GitHubUser specific_type.
// Names are the node.Properties keys written by newUserNode.
var githubUserSchema = core.SpecificTypeSchema{
	SpecificType: "GitHubUser",
	NodeType:     core.NodeTypeUserAccount,
	Properties: []core.PropertyDef{
		{Name: "username", Indexed: true},
		{Name: "email"},
		{Name: "company"},
		{Name: "url"},
	},
}

func init() { core.RegisterSpecificTypeSchema(githubUserSchema) }

// newUserNode returns the (possibly cached) node for a GitHub user, and whether the
// node is new this call — callers only append genuinely-new nodes to the graph,
// since the same person can be seen as the owner, an org member, a team member, and
// a repo collaborator within a single BuildGraph run.
func (s *GitHubSource) newUserNode(user *gogithub.User, userNodes map[string]*core.DbNode, req *core.SourceBuildRequest) (*core.DbNode, bool) {
	if user == nil || user.Login == nil || *user.Login == "" {
		return nil, false
	}
	login := *user.Login
	if existing, ok := userNodes[login]; ok {
		return existing, false
	}

	name := login
	if user.Name != nil && *user.Name != "" {
		name = *user.Name
	}
	properties := map[string]interface{}{
		"specific_type": "GitHubUser",
		"name":          name,
		"username":      login,
	}
	if user.Email != nil {
		properties["email"] = *user.Email
	}
	if user.Company != nil {
		properties["company"] = *user.Company
	}
	if user.HTMLURL != nil {
		properties["url"] = *user.HTMLURL
	}

	node := s.newNode(core.NodeTypeUserAccount, properties, req)
	userNodes[login] = node
	return node, true
}

// derivePermission resolves a repo-access level from either the explicit role name
// (present on collaborator-list responses) or the boolean permission map (present on
// both collaborator and team-repo responses), in descending order of access.
func derivePermission(roleName *string, perms map[string]bool) string {
	if roleName != nil && *roleName != "" {
		return *roleName
	}
	for _, p := range []string{"admin", "maintain", "push", "triage", "pull"} {
		if perms[p] {
			return p
		}
	}
	return ""
}

// fetchOrgMembers lists the org's members (admins + regular members) and returns
// UserAccount nodes + MEMBER_OF edges to the org, with a `role` edge property.
func (s *GitHubSource) fetchOrgMembers(
	ctx context.Context,
	client *gogithub.Client,
	org string,
	orgNode *core.DbNode,
	userNodes map[string]*core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbNode, []*core.DbEdge, error) {
	admins := make(map[string]bool)
	adminUsers, err := listOrgMembers(ctx, client, org, "admin")
	if err != nil {
		return nil, nil, fmt.Errorf("list org admins: %w", err)
	}
	for _, u := range adminUsers {
		if u.Login != nil {
			admins[*u.Login] = true
		}
	}

	allMembers, err := listOrgMembers(ctx, client, org, "all")
	if err != nil {
		return nil, nil, fmt.Errorf("list org members: %w", err)
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
		if u.Login != nil && admins[*u.Login] {
			role = "admin"
		}
		edges = append(edges, s.newEdge(userNode, orgNode, core.RelationshipMemberOf,
			map[string]interface{}{"role": role}, req))
	}
	return nodes, edges, nil
}

func listOrgMembers(ctx context.Context, client *gogithub.Client, org, role string) ([]*gogithub.User, error) {
	var out []*gogithub.User
	opts := &gogithub.ListMembersOptions{Role: role, ListOptions: gogithub.ListOptions{PerPage: 100}}
	for {
		users, resp, err := retryOnRateLimit(ctx, func() ([]*gogithub.User, *gogithub.Response, error) {
			return client.Organizations.ListMembers(ctx, org, opts)
		})
		if err != nil {
			return nil, err
		}
		out = append(out, users...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// fetchCollaborators lists direct (non-team-inherited) collaborators for every repo
// and returns UserAccount nodes + HAS_ACCESS_TO edges carrying the permission level.
// "direct" affiliation is used deliberately — it excludes access inherited via team
// or org membership (already modeled by MEMBER_OF + team HAS_ACCESS_TO edges), so a
// repo doesn't get one HAS_ACCESS_TO edge per org member.
func (s *GitHubSource) fetchCollaborators(
	ctx context.Context,
	client *gogithub.Client,
	owner string,
	repoNodes map[string]*core.DbNode, // keyed by repo short name
	userNodes map[string]*core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbNode, []*core.DbEdge, error) {
	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	for repoName, repoNode := range repoNodes {
		opts := &gogithub.ListCollaboratorsOptions{
			Affiliation: "direct",
			ListOptions: gogithub.ListOptions{PerPage: 100},
		}
		for {
			collabs, resp, err := retryOnRateLimit(ctx, func() ([]*gogithub.User, *gogithub.Response, error) {
				return client.Repositories.ListCollaborators(ctx, owner, repoName, opts)
			})
			if err != nil {
				if ctx.Err() != nil {
					return nil, nil, ctx.Err()
				}
				s.logger.Warn("github: failed to list collaborators (may lack permission)", "repo", repoName, "error", err)
				break
			}
			for _, u := range collabs {
				userNode, isNew := s.newUserNode(u, userNodes, req)
				if userNode == nil {
					continue
				}
				if isNew {
					nodes = append(nodes, userNode)
				}
				edges = append(edges, s.newEdge(userNode, repoNode, core.RelationshipHasAccessTo,
					map[string]interface{}{"permission": derivePermission(u.RoleName, u.Permissions)}, req))
			}
			if resp.NextPage == 0 {
				break
			}
			opts.Page = resp.NextPage
		}
	}
	return nodes, edges, nil
}

package gitlab

import (
	"context"
	"fmt"

	golangGitlab "gitlab.com/gitlab-org/api/client-go"

	"nudgebee/services/knowledge_graph/core"
)

// gitlabUserSchema is the concrete property schema for the GitLabUser specific_type.
// Names are the node.Properties keys written by newUserNode.
var gitlabUserSchema = core.SpecificTypeSchema{
	SpecificType: "GitLabUser",
	NodeType:     core.NodeTypeUserAccount,
	Properties: []core.PropertyDef{
		{Name: "username", Indexed: true},
		{Name: "email"},
		{Name: "url"},
	},
}

func init() { core.RegisterSpecificTypeSchema(gitlabUserSchema) }

// newUserNode returns the (possibly cached) node for a GitLab user, and whether the
// node is new this call — callers only append genuinely-new nodes to the graph,
// since the same person can be seen as the owner, a group member, and a project
// member within a single BuildGraph run.
func (s *GitLabSource) newUserNode(username, name, email, webURL string, userNodes map[string]*core.DbNode, req *core.SourceBuildRequest) (*core.DbNode, bool) {
	if username == "" {
		return nil, false
	}
	if existing, ok := userNodes[username]; ok {
		return existing, false
	}

	if name == "" {
		name = username
	}
	properties := map[string]interface{}{
		"specific_type": "GitLabUser",
		"name":          name,
		"username":      username,
	}
	if email != "" {
		properties["email"] = email
	}
	if webURL != "" {
		properties["url"] = webURL
	}

	node := s.newNode(core.NodeTypeUserAccount, properties, req)
	userNodes[username] = node
	return node, true
}

// fetchGroupMembers lists one group's direct members (not members inherited from a
// parent group — inherited access is already implied by the BELONGS_TO/MEMBER_OF
// subgroup chain, so listing direct-only avoids one redundant edge per ancestor
// level) and returns UserAccount nodes + MEMBER_OF edges to that group, with a
// `role` edge property derived from GitLab's numeric access_level.
func (s *GitLabSource) fetchGroupMembers(
	ctx context.Context,
	client *golangGitlab.Client,
	groupID int64,
	groupNode *core.DbNode,
	userNodes map[string]*core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbNode, []*core.DbEdge, error) {
	var members []*golangGitlab.GroupMember
	opts := &golangGitlab.ListGroupMembersOptions{ListOptions: golangGitlab.ListOptions{PerPage: 100}}
	for {
		page, resp, err := client.Groups.ListGroupMembers(groupID, opts, golangGitlab.WithContext(ctx))
		if err != nil {
			return nil, nil, fmt.Errorf("list group members: %w", err)
		}
		members = append(members, page...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	nodes := make([]*core.DbNode, 0, len(members))
	edges := make([]*core.DbEdge, 0, len(members))
	for _, m := range members {
		userNode, isNew := s.newUserNode(m.Username, m.Name, m.Email, m.WebURL, userNodes, req)
		if userNode == nil {
			continue
		}
		if isNew {
			nodes = append(nodes, userNode)
		}
		role := accessLevelRole(m.AccessLevel)
		edges = append(edges, s.newEdge(userNode, groupNode, core.RelationshipMemberOf,
			map[string]interface{}{"role": role}, req))
	}
	return nodes, edges, nil
}

// fetchProjectMembers lists direct (non-group-inherited) members for every project
// and returns UserAccount nodes + HAS_ACCESS_TO edges carrying the access level.
// "direct" is used deliberately — it excludes access inherited via group membership
// (already modeled by MEMBER_OF edges), so a project doesn't get one HAS_ACCESS_TO
// edge per group member.
func (s *GitLabSource) fetchProjectMembers(
	ctx context.Context,
	client *golangGitlab.Client,
	projectsByID map[int64]*core.DbNode,
	userNodes map[string]*core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbNode, []*core.DbEdge, error) {
	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	for projectID, projectNode := range projectsByID {
		opts := &golangGitlab.ListProjectMembersOptions{ListOptions: golangGitlab.ListOptions{PerPage: 100}}
		for {
			members, resp, err := client.ProjectMembers.ListProjectMembers(projectID, opts, golangGitlab.WithContext(ctx))
			if err != nil {
				if ctx.Err() != nil {
					return nil, nil, ctx.Err()
				}
				s.logger.Warn("gitlab: failed to list project members (may lack permission)", "project_id", projectID, "error", err)
				break
			}
			for _, m := range members {
				userNode, isNew := s.newUserNode(m.Username, m.Name, m.Email, m.WebURL, userNodes, req)
				if userNode == nil {
					continue
				}
				if isNew {
					nodes = append(nodes, userNode)
				}
				edges = append(edges, s.newEdge(userNode, projectNode, core.RelationshipHasAccessTo,
					map[string]interface{}{"permission": accessLevelRole(m.AccessLevel)}, req))
			}
			if resp.NextPage == 0 {
				break
			}
			opts.Page = resp.NextPage
		}
	}
	return nodes, edges, nil
}

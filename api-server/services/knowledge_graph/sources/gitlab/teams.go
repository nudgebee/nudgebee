package gitlab

import (
	"context"
	"fmt"

	golangGitlab "gitlab.com/gitlab-org/api/client-go"

	"nudgebee/services/knowledge_graph/core"
)

// gitlabGroupSchema is the concrete property schema for the GitLabGroup specific_type
// (a subgroup nested under the integration's root group). Names are the
// node.Properties keys written by newGroupNode.
var gitlabGroupSchema = core.SpecificTypeSchema{
	SpecificType: "GitLabGroup",
	NodeType:     core.NodeTypeUserGroup,
	Properties: []core.PropertyDef{
		{Name: "url", Indexed: true},
		{Name: "description"},
	},
}

func init() { core.RegisterSpecificTypeSchema(gitlabGroupSchema) }

// fetchSubgroups lists every descendant of the integration's root group and returns
// UserGroup nodes plus:
//   - subgroup --BELONGS_TO--> root org (universal, like GitHub Team --> Org)
//   - child subgroup --MEMBER_OF--> immediate parent subgroup (only when the parent
//     isn't the root org itself, mirroring GitHub's nested child-team --> parent-team edge)
//
// groupNodesByID is mutated in place (keyed by GitLab's numeric group ID) so callers
// can subsequently fetch projects/members for every group, root included.
func (s *GitLabSource) fetchSubgroups(
	ctx context.Context,
	client *golangGitlab.Client,
	rootGroupID int64,
	rootOrgNode *core.DbNode,
	groupNodesByID map[int64]*core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbNode, []*core.DbEdge, error) {
	descendants, err := listDescendantGroups(ctx, client, rootGroupID)
	if err != nil {
		return nil, nil, err
	}

	nodes := make([]*core.DbNode, 0, len(descendants))
	edges := make([]*core.DbEdge, 0, len(descendants))
	parentByID := make(map[int64]int64, len(descendants))

	for _, group := range descendants {
		if group == nil {
			continue
		}
		node := s.newGroupNode(group, req)
		nodes = append(nodes, node)
		groupNodesByID[group.ID] = node
		parentByID[group.ID] = group.ParentID
		edges = append(edges, s.newEdge(node, rootOrgNode, core.RelationshipBelongsTo, nil, req))
	}

	for childID, parentID := range parentByID {
		if parentID == rootGroupID {
			continue
		}
		childNode, childOK := groupNodesByID[childID]
		parentNode, parentOK := groupNodesByID[parentID]
		if childOK && parentOK {
			edges = append(edges, s.newEdge(childNode, parentNode, core.RelationshipMemberOf, nil, req))
		}
	}

	return nodes, edges, nil
}

func listDescendantGroups(ctx context.Context, client *golangGitlab.Client, rootGroupID int64) ([]*golangGitlab.Group, error) {
	var out []*golangGitlab.Group
	opts := &golangGitlab.ListDescendantGroupsOptions{ListOptions: golangGitlab.ListOptions{PerPage: 100}}
	for {
		groups, resp, err := client.Groups.ListDescendantGroups(rootGroupID, opts, golangGitlab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("list descendant groups: %w", err)
		}
		out = append(out, groups...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (s *GitLabSource) newGroupNode(group *golangGitlab.Group, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"specific_type": "GitLabGroup",
		"name":          group.FullPath,
	}
	if group.WebURL != "" {
		properties["url"] = group.WebURL
	}
	if group.Description != "" {
		properties["description"] = group.Description
	}
	return s.newNode(core.NodeTypeUserGroup, properties, req)
}

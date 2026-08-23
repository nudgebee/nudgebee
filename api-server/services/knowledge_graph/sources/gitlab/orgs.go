package gitlab

import (
	"context"
	"fmt"

	golangGitlab "gitlab.com/gitlab-org/api/client-go"

	"nudgebee/services/knowledge_graph/core"
)

// gitlabOrganizationSchema is the concrete property schema for the GitLabOrganization
// specific_type. Names are the node.Properties keys written by fetchOwner.
var gitlabOrganizationSchema = core.SpecificTypeSchema{
	SpecificType: "GitLabOrganization",
	NodeType:     core.NodeTypeSourceControlOrg,
	Properties: []core.PropertyDef{
		{Name: "type", Indexed: true},
		{Name: "url", Indexed: true},
		{Name: "description"},
	},
}

func init() { core.RegisterSpecificTypeSchema(gitlabOrganizationSchema) }

// fetchOwner resolves the integration's configured `group` — a GitLab top-level
// group — or, when no group is configured, the authenticated user's own profile
// (personal-namespace fallback, matching how GitHub treats a user's own repos as
// owned by their user account rather than an org). Groups become a SourceControlOrg
// node (ownerGroupID is that group's numeric GitLab ID, used to key groupNodesByID
// for subgroup/project attribution); personal accounts become a UserAccount node
// (reusing the GitLabUser schema/dedup map) with isGroup=false.
func (s *GitLabSource) fetchOwner(
	ctx context.Context,
	client *golangGitlab.Client,
	cfg *integrationConfig,
	userNodes map[string]*core.DbNode,
	req *core.SourceBuildRequest,
) (ownerNode *core.DbNode, ownerGroupID int64, isGroup bool, err error) {
	if cfg.Group == "" {
		user, _, err := client.Users.CurrentUser(golangGitlab.WithContext(ctx))
		if err != nil {
			return nil, 0, false, fmt.Errorf("get current user: %w", err)
		}
		node, _ := s.newUserNode(user.Username, user.Name, user.Email, user.WebURL, userNodes, req)
		if node == nil {
			return nil, 0, false, fmt.Errorf("current user has no username")
		}
		return node, 0, false, nil
	}

	group, _, err := client.Groups.GetGroup(cfg.Group, &golangGitlab.GetGroupOptions{}, golangGitlab.WithContext(ctx))
	if err != nil {
		return nil, 0, false, fmt.Errorf("get group %q: %w", cfg.Group, err)
	}

	properties := map[string]interface{}{
		"specific_type": "GitLabOrganization",
		"name":          group.FullPath,
		"type":          "Group",
	}
	if group.WebURL != "" {
		properties["url"] = group.WebURL
	}
	if group.Description != "" {
		properties["description"] = group.Description
	}

	return s.newNode(core.NodeTypeSourceControlOrg, properties, req), group.ID, true, nil
}

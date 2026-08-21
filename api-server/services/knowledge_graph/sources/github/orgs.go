package github

import (
	"context"
	"fmt"

	gogithub "github.com/google/go-github/v61/github"

	"nudgebee/services/knowledge_graph/core"
)

// githubOrganizationSchema is the concrete property schema for the GitHubOrganization
// specific_type. Names are the node.Properties keys written by fetchOwner.
var githubOrganizationSchema = core.SpecificTypeSchema{
	SpecificType: "GitHubOrganization",
	NodeType:     core.NodeTypeSourceControlOrg,
	Properties: []core.PropertyDef{
		{Name: "type", Indexed: true},
		{Name: "url", Indexed: true},
		{Name: "description"},
		{Name: "company"},
		{Name: "email"},
	},
}

func init() { core.RegisterSpecificTypeSchema(githubOrganizationSchema) }

// fetchOwner resolves the integration's configured `username` — a GitHub org or a
// personal account — into its owning node. Orgs become a SourceControlOrg node;
// personal accounts become a UserAccount node (reusing the GitHubUser schema/dedup
// map), matching how GitHub itself treats a user's own repos as owned by their user
// account rather than an org.
func (s *GitHubSource) fetchOwner(
	ctx context.Context,
	client *gogithub.Client,
	username string,
	userNodes map[string]*core.DbNode,
	req *core.SourceBuildRequest,
) (ownerNode *core.DbNode, isOrg bool, err error) {
	user, _, err := retryOnRateLimit(ctx, func() (*gogithub.User, *gogithub.Response, error) {
		return client.Users.Get(ctx, username)
	})
	if err != nil {
		return nil, false, fmt.Errorf("get user/org %q: %w", username, err)
	}

	if user.Type == nil || *user.Type != "Organization" {
		node, _ := s.newUserNode(user, userNodes, req)
		if node == nil {
			return nil, false, fmt.Errorf("user %q has no login", username)
		}
		return node, false, nil
	}

	org, _, err := retryOnRateLimit(ctx, func() (*gogithub.Organization, *gogithub.Response, error) {
		return client.Organizations.Get(ctx, username)
	})
	if err != nil {
		return nil, false, fmt.Errorf("get organization %q: %w", username, err)
	}

	properties := map[string]interface{}{
		"specific_type": "GitHubOrganization",
		"name":          username,
		"type":          "Organization",
	}
	if org.HTMLURL != nil {
		properties["url"] = *org.HTMLURL
	}
	if org.Description != nil {
		properties["description"] = *org.Description
	}
	if org.Company != nil {
		properties["company"] = *org.Company
	}
	if org.Email != nil {
		properties["email"] = *org.Email
	}

	return s.newNode(core.NodeTypeSourceControlOrg, properties, req), true, nil
}

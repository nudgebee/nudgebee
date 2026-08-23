package pagerduty

import (
	"context"
	"fmt"

	gopagerduty "github.com/PagerDuty/go-pagerduty"

	"nudgebee/services/knowledge_graph/core"
)

// pagerdutyUserSchema is the concrete property schema for the PagerDutyUser
// specific_type. Names are the node.Properties keys written by newUserNode.
var pagerdutyUserSchema = core.SpecificTypeSchema{
	SpecificType: "PagerDutyUser",
	NodeType:     core.NodeTypeUserAccount,
	Properties: []core.PropertyDef{
		{Name: "username", Indexed: true},
		{Name: "email"},
		{Name: "role"},
		{Name: "url"},
	},
}

func init() { core.RegisterSpecificTypeSchema(pagerdutyUserSchema) }

// newUserNode builds a UserAccount node for one PagerDuty user. `username` is set to
// the PagerDuty user's own ID (not the email) — this is the value Identity Sync
// records as integration_user_accounts.external_user_id (see
// integrations/pagerduty.go::ListUsers, which maps core.ExternalUser{ID: u.ID, ...}),
// and it's what sources/identity_enricher.go matches SAME_AS edges against. `name`
// stays the human-readable display name since it drives the node's unique key
// (BaseSource.GenerateUniqueKey hashes on properties["name"]).
func (s *PagerDutySource) newUserNode(user gopagerduty.User, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"specific_type": "PagerDutyUser",
		"name":          user.Name,
		"username":      user.ID,
	}
	if user.Email != "" {
		properties["email"] = user.Email
	}
	if user.Role != "" {
		properties["role"] = user.Role
	}
	if user.HTMLURL != "" {
		properties["url"] = user.HTMLURL
	}
	return s.newNode(core.NodeTypeUserAccount, properties, req)
}

// fetchUsers lists every user visible to the integration's API token and returns
// UserAccount nodes keyed by the PagerDuty user ID, mirroring the pagination loop
// already used by integrations/pagerduty.go::ListUsers (Limit 100, safety-capped
// offset).
func (s *PagerDutySource) fetchUsers(ctx context.Context, client *gopagerduty.Client, req *core.SourceBuildRequest) (map[string]*core.DbNode, error) {
	nodesByID := make(map[string]*core.DbNode)
	var offset uint
	for {
		resp, err := retryOnRateLimit(ctx, func() (*gopagerduty.ListUsersResponse, error) {
			return client.ListUsersWithContext(ctx, gopagerduty.ListUsersOptions{Limit: 100, Offset: offset})
		})
		if err != nil {
			return nil, fmt.Errorf("list users: %w", err)
		}
		for _, u := range resp.Users {
			if u.ID == "" {
				continue
			}
			nodesByID[u.ID] = s.newUserNode(u, req)
		}
		if !resp.More {
			break
		}
		offset += 100
		if offset > 10000 { // safety cap, matches integrations/pagerduty.go::ListUsers
			break
		}
	}
	return nodesByID, nil
}

package pagerduty

import (
	"context"
	"fmt"

	gopagerduty "github.com/PagerDuty/go-pagerduty"

	"nudgebee/services/knowledge_graph/core"
)

// pagerdutyTeamSchema is the concrete property schema for the PagerDutyTeam
// specific_type. Names are the node.Properties keys written by newTeamNode.
var pagerdutyTeamSchema = core.SpecificTypeSchema{
	SpecificType: "PagerDutyTeam",
	NodeType:     core.NodeTypeUserGroup,
	Properties: []core.PropertyDef{
		{Name: "url", Indexed: true},
		{Name: "description"},
	},
}

func init() { core.RegisterSpecificTypeSchema(pagerdutyTeamSchema) }

func (s *PagerDutySource) newTeamNode(team gopagerduty.Team, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"specific_type": "PagerDutyTeam",
		"name":          team.Name,
	}
	if team.Description != "" {
		properties["description"] = team.Description
	}
	if team.HTMLURL != "" {
		properties["url"] = team.HTMLURL
	}
	return s.newNode(core.NodeTypeUserGroup, properties, req)
}

// fetchTeams lists every team visible to the integration's API token and returns
// UserGroup nodes keyed by PagerDuty team ID, plus child-team --MEMBER_OF--> parent-team
// edges (from Team.Parent, only when non-nil) once every team node exists — mirroring
// the nested-group second-pass pattern used by sources/github/teams.go and
// sources/gitlab/teams.go.
func (s *PagerDutySource) fetchTeams(ctx context.Context, client *gopagerduty.Client, req *core.SourceBuildRequest) (map[string]*core.DbNode, []*core.DbEdge, error) {
	var teams []gopagerduty.Team
	var offset uint
	for {
		resp, err := retryOnRateLimit(ctx, func() (*gopagerduty.ListTeamResponse, error) {
			return client.ListTeamsWithContext(ctx, gopagerduty.ListTeamOptions{Limit: 100, Offset: offset})
		})
		if err != nil {
			return nil, nil, fmt.Errorf("list teams: %w", err)
		}
		teams = append(teams, resp.Teams...)
		if !resp.More {
			break
		}
		offset += 100
	}

	nodesByID := make(map[string]*core.DbNode, len(teams))
	for _, team := range teams {
		if team.ID == "" {
			continue
		}
		nodesByID[team.ID] = s.newTeamNode(team, req)
	}

	edges := make([]*core.DbEdge, 0)
	for _, team := range teams {
		if team.ID == "" || team.Parent == nil || team.Parent.ID == "" {
			continue
		}
		childNode, childOK := nodesByID[team.ID]
		parentNode, parentOK := nodesByID[team.Parent.ID]
		if childOK && parentOK {
			edges = append(edges, s.newEdge(childNode, parentNode, core.RelationshipMemberOf, nil, req))
		}
	}

	return nodesByID, edges, nil
}

// fetchTeamMembers lists one team's members and returns User --MEMBER_OF--> Team
// edges with a `role` property (PagerDuty's admin/manager/responder/observer).
// Member nodes themselves are not built here — every user was already built by
// fetchUsers, keyed by ID in userNodesByID.
func (s *PagerDutySource) fetchTeamMembers(
	ctx context.Context,
	client *gopagerduty.Client,
	teamID string,
	teamNode *core.DbNode,
	userNodesByID map[string]*core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbEdge, error) {
	members, err := retryOnRateLimit(ctx, func() ([]gopagerduty.Member, error) {
		return client.ListTeamMembersPaginated(ctx, teamID)
	})
	if err != nil {
		return nil, fmt.Errorf("list team members: %w", err)
	}

	edges := make([]*core.DbEdge, 0, len(members))
	for _, m := range members {
		userNode, ok := userNodesByID[m.User.ID]
		if !ok {
			continue
		}
		properties := map[string]interface{}{}
		if m.Role != "" {
			properties["role"] = m.Role
		}
		edges = append(edges, s.newEdge(userNode, teamNode, core.RelationshipMemberOf, properties, req))
	}
	return edges, nil
}

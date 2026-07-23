package pagerduty

import (
	"context"
	"fmt"

	gopagerduty "github.com/PagerDuty/go-pagerduty"

	"nudgebee/services/knowledge_graph/core"
)

// pagerdutyServiceSchema is the concrete property schema for the PagerDutyService
// specific_type. Names are the node.Properties keys written by newServiceNode.
var pagerdutyServiceSchema = core.SpecificTypeSchema{
	SpecificType: "PagerDutyService",
	NodeType:     core.NodeTypeOnCallService,
	Properties: []core.PropertyDef{
		{Name: "status", Indexed: true},
		{Name: "url", Indexed: true},
		{Name: "description"},
	},
}

func init() { core.RegisterSpecificTypeSchema(pagerdutyServiceSchema) }

func (s *PagerDutySource) newServiceNode(service gopagerduty.Service, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"specific_type": "PagerDutyService",
		"name":          service.Name,
	}
	if service.Description != "" {
		properties["description"] = service.Description
	}
	if service.Status != "" {
		properties["status"] = service.Status
	}
	if service.HTMLURL != "" {
		properties["url"] = service.HTMLURL
	}
	return s.newNode(core.NodeTypeOnCallService, properties, req)
}

// fetchServices lists every service visible to the integration's API token
// (Includes: []string{"teams"} so each service's associated teams come back inline,
// no per-service extra call needed) and returns OnCallService nodes plus
// Team --OWNS--> Service edges for each of a service's associated teams — matched
// against the already-built team nodes in teamNodesByID.
func (s *PagerDutySource) fetchServices(
	ctx context.Context,
	client *gopagerduty.Client,
	teamNodesByID map[string]*core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbNode, []*core.DbEdge, error) {
	services, err := retryOnRateLimit(ctx, func() ([]gopagerduty.Service, error) {
		return client.ListServicesPaginated(ctx, gopagerduty.ListServiceOptions{
			Includes: []string{"teams"},
		})
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list services: %w", err)
	}

	nodes := make([]*core.DbNode, 0, len(services))
	edges := make([]*core.DbEdge, 0)
	for _, svc := range services {
		if svc.ID == "" {
			continue
		}
		node := s.newServiceNode(svc, req)
		nodes = append(nodes, node)

		for _, team := range svc.Teams {
			teamNode, ok := teamNodesByID[team.ID]
			if !ok {
				continue
			}
			edges = append(edges, s.newEdge(teamNode, node, core.RelationshipOwns, nil, req))
		}
	}

	return nodes, edges, nil
}

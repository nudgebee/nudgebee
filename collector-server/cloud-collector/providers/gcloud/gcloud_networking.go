package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const (
	ServiceNameNetworking = "Networking"
)

type networkingService struct{}

func (s *networkingService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	resources := []providers.Resource{}

	// VPCs are global resources — always query regardless of region.
	// Deduplication in ListResources prevents duplicates across region iterations.
	vpcs, err := s.listVPCs(ctx, session.ProjectId, session.Opts)
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		if isGCPPermissionOrNotFoundError(err) {
			ctx.GetLogger().Warn("skipping VPCs — API disabled or permission denied", "error", err)
		} else {
			ctx.GetLogger().Error("failed to list VPCs", "error", err)
		}
	} else {
		resources = append(resources, vpcs...)
	}

	// Subnets are regional — skip when called with "global" or empty region
	if region != "global" && region != "" {
		subnets, err := s.listSubnets(ctx, session.ProjectId, region, session.Opts)
		if err != nil {
			// Credential/permission errors are account config issues, not collector bugs
			if _, _, _, isPermErr := IsGCPPermissionError(err); isPermErr || strings.Contains(err.Error(), "could not find default credentials") {
				ctx.GetLogger().Warn("failed to list subnets", "error", err, "region", region)
			} else {
				ctx.GetLogger().Error("failed to list subnets", "error", err, "region", region)
			}
			RecordGCPPermissionError(ctx, err)
		} else {
			resources = append(resources, subnets...)
		}
	}

	// Firewall rules are global — always query regardless of region.
	// Deduplication in ListResources prevents duplicates across region iterations.
	firewalls, err := s.listFirewallRules(ctx, session.ProjectId, session.Opts)
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		if isGCPPermissionOrNotFoundError(err) {
			ctx.GetLogger().Warn("skipping firewall rules — API disabled or permission denied", "error", err)
		} else {
			ctx.GetLogger().Error("failed to list firewall rules", "error", err)
		}
	} else {
		resources = append(resources, firewalls...)
	}

	return resources, nil
}

func (s *networkingService) listVPCs(ctx providers.CloudProviderContext, projectID string, opts []option.ClientOption) ([]providers.Resource, error) {
	client, err := compute.NewNetworksRESTClient(ctx.GetContext(), opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create networks client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close networks client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListNetworksRequest{
		Project: projectID,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		network, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list networks: %w", err)
		}

		resources = append(resources, providers.Resource{
			Id:          fmt.Sprintf("%s/networks/%s", projectID, network.GetName()),
			Name:        network.GetName(),
			Type:        "vpc-network",
			Arn:         fmt.Sprintf("%s/networks/%s", projectID, network.GetName()),
			Region:      "global",
			ServiceName: ServiceNameNetworking,
			Status:      providers.ResourceStatusActive,
			Tags:        map[string][]string{},
			Meta:        map[string]any{},
			CreatedAt:   time.Now(),
		})
	}

	return resources, nil
}

func (s *networkingService) listSubnets(ctx providers.CloudProviderContext, projectID, region string, opts []option.ClientOption) ([]providers.Resource, error) {
	client, err := compute.NewSubnetworksRESTClient(ctx.GetContext(), opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create subnetworks client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close subnetworks client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListSubnetworksRequest{
		Project: projectID,
		Region:  region,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		subnet, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list subnets: %w", err)
		}

		resources = append(resources, providers.Resource{
			Id:          fmt.Sprintf("%s/regions/%s/subnetworks/%s", projectID, region, subnet.GetName()),
			Name:        subnet.GetName(),
			Type:        "subnet",
			Arn:         fmt.Sprintf("%s/regions/%s/subnetworks/%s", projectID, region, subnet.GetName()),
			Region:      region,
			ServiceName: ServiceNameNetworking,
			Status:      providers.ResourceStatusActive,
			Tags:        map[string][]string{},
			Meta:        map[string]any{},
			CreatedAt:   time.Now(),
		})
	}

	return resources, nil
}

func (s *networkingService) listFirewallRules(ctx providers.CloudProviderContext, projectID string, opts []option.ClientOption) ([]providers.Resource, error) {
	client, err := compute.NewFirewallsRESTClient(ctx.GetContext(), opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create firewalls client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close firewalls client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListFirewallsRequest{
		Project: projectID,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		firewall, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list firewall rules: %w", err)
		}

		resources = append(resources, providers.Resource{
			Id:          fmt.Sprintf("%s/global/firewalls/%s", projectID, firewall.GetName()),
			Name:        firewall.GetName(),
			Type:        "firewall-rule",
			Arn:         fmt.Sprintf("%s/global/firewalls/%s", projectID, firewall.GetName()),
			Region:      "global",
			ServiceName: ServiceNameNetworking,
			Status:      providers.ResourceStatusActive,
			Tags:        map[string][]string{},
			Meta:        map[string]any{},
			CreatedAt:   time.Now(),
		})
	}

	return resources, nil
}

func (s *networkingService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Networking metrics would come from Cloud Monitoring
	return providers.QueryMetricsResponse{
		Items: []providers.MetricItem{},
	}, fmt.Errorf("networking metrics not implemented")
}

func (s *networkingService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	// No specific recommendations for networking resources yet
	return []providers.Recommendation{}, nil
}

func (s *networkingService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return fmt.Errorf("networking service does not support recommendations")
}

func (s *networkingService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, fmt.Errorf("networking service does not support commands")
}

func (s *networkingService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}

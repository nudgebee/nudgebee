package aws

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/directconnect"
	"github.com/aws/aws-sdk-go-v2/service/directconnect/types"
)

func directConnectConnectionStateToNbStatus(state types.ConnectionState) providers.ResourceStatus {
	switch state {
	case types.ConnectionStateAvailable, types.ConnectionStateOrdering, types.ConnectionStatePending, types.ConnectionStateRequested:
		return providers.ResourceStatusActive
	case types.ConnectionStateDeleted, types.ConnectionStateDeleting:
		return providers.ResourceStatusDeleted
	case types.ConnectionStateDown, types.ConnectionStateRejected, types.ConnectionStateUnknown:
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

func directConnectVirtualInterfaceStateToNbStatus(state types.VirtualInterfaceState) providers.ResourceStatus {
	switch state {
	case types.VirtualInterfaceStateAvailable, types.VirtualInterfaceStateConfirming, types.VirtualInterfaceStatePending, types.VirtualInterfaceStateVerifying:
		return providers.ResourceStatusActive
	case types.VirtualInterfaceStateDeleted, types.VirtualInterfaceStateDeleting:
		return providers.ResourceStatusDeleted
	case types.VirtualInterfaceStateDown, types.VirtualInterfaceStateRejected:
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

type awsDirectConnect struct {
	DefaultAwsServiceImpl
}

func (a *awsDirectConnect) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsDirectConnect) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsDirectConnect) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsDirectConnect) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
	// Direct Connect doesn't have direct CloudWatch Logs integration
	return "", errors.New("direct Connect does not have CloudWatch Logs integration")
}

func (a *awsDirectConnect) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return []providers.Resource{}, err
	}
	cfg.Region = region
	svc := directconnect.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// List all Direct Connect connections
	connectionsResult, err := svc.DescribeConnections(context.TODO(), &directconnect.DescribeConnectionsInput{})
	if err != nil {
		ctx.GetLogger().Error("failed to fetch direct connect connections", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return resources, err
	}

	for _, conn := range connectionsResult.Connections {
		if conn.ConnectionId == nil || conn.ConnectionName == nil {
			ctx.GetLogger().Warn("Skipping Direct Connect connection due to missing ID or name")
			continue
		}

		tags := make(map[string][]string)

		// Get tags for the connection
		if conn.ConnectionId != nil {
			tagsResult, err := svc.DescribeTags(context.TODO(), &directconnect.DescribeTagsInput{
				ResourceArns: []string{fmt.Sprintf("arn:aws:directconnect:%s:%s:dxcon/%s", region, account.AccountNumber, *conn.ConnectionId)},
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch direct connect tags", "error", err, "connectionId", *conn.ConnectionId)
			} else if len(tagsResult.ResourceTags) > 0 {
				for _, tag := range tagsResult.ResourceTags[0].Tags {
					if tag.Key != nil && tag.Value != nil {
						tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
					}
				}
			}
		}

		metaMap := structToMap(conn)

		// Get virtual interfaces for this connection
		vifsResult, err := svc.DescribeVirtualInterfaces(context.TODO(), &directconnect.DescribeVirtualInterfacesInput{
			ConnectionId: conn.ConnectionId,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to fetch virtual interfaces", "error", err, "connectionId", *conn.ConnectionId)
		} else if vifsResult.VirtualInterfaces != nil {
			vifs := []map[string]any{}
			for _, vif := range vifsResult.VirtualInterfaces {
				vifs = append(vifs, structToMap(vif))
			}
			metaMap["VirtualInterfaces"] = vifs
			metaMap["VirtualInterfaceCount"] = len(vifsResult.VirtualInterfaces)
		}

		// Get LOA (Letter of Authorization) status
		loaResult, err := svc.DescribeLoa(context.TODO(), &directconnect.DescribeLoaInput{
			ConnectionId: conn.ConnectionId,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to fetch LOA", "error", err, "connectionId", *conn.ConnectionId)
		} else {
			metaMap["LOA"] = structToMap(loaResult)
		}

		createdAt := time.Time{}
		status := providers.ResourceStatusUnknown
		if conn.ConnectionState != "" {
			status = directConnectConnectionStateToNbStatus(conn.ConnectionState)
		}

		connectionArn := fmt.Sprintf("arn:aws:directconnect:%s:%s:dxcon/%s", region, account.AccountNumber, *conn.ConnectionId)

		resource := providers.Resource{
			Id:          *conn.ConnectionId,
			ServiceName: ServiceNameDirectConnect,
			Name:        *conn.ConnectionName,
			Status:      status,
			Region:      region,
			Tags:        tags,
			Meta:        metaMap,
			Arn:         connectionArn,
			CreatedAt:   createdAt,
			Type:        getAwsServiceResourceType(ServiceNameDirectConnect, "connection"),
		}
		resources = append(resources, resource)
	}

	// Also get LAGs (Link Aggregation Groups)
	lagsResult, err := svc.DescribeLags(context.TODO(), &directconnect.DescribeLagsInput{})
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch direct connect LAGs", "error", err, "accountNumber", account.AccountNumber, "region", region)
	} else {
		for _, lag := range lagsResult.Lags {
			if lag.LagId == nil || lag.LagName == nil {
				ctx.GetLogger().Warn("Skipping Direct Connect LAG due to missing ID or name")
				continue
			}

			tags := make(map[string][]string)

			// Get tags for the LAG
			if lag.LagId != nil {
				tagsResult, err := svc.DescribeTags(context.TODO(), &directconnect.DescribeTagsInput{
					ResourceArns: []string{fmt.Sprintf("arn:aws:directconnect:%s:%s:dxlag/%s", region, account.AccountNumber, *lag.LagId)},
				})
				if err != nil {
					ctx.GetLogger().Warn("failed to fetch direct connect LAG tags", "error", err, "lagId", *lag.LagId)
				} else if len(tagsResult.ResourceTags) > 0 {
					for _, tag := range tagsResult.ResourceTags[0].Tags {
						if tag.Key != nil && tag.Value != nil {
							tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
						}
					}
				}
			}

			metaMap := structToMap(lag)

			status := providers.ResourceStatusUnknown
			if lag.LagState != "" {
				// LagState uses the same enum values as ConnectionState
				status = directConnectConnectionStateToNbStatus(types.ConnectionState(lag.LagState))
			}

			lagArn := fmt.Sprintf("arn:aws:directconnect:%s:%s:dxlag/%s", region, account.AccountNumber, *lag.LagId)

			resource := providers.Resource{
				Id:          *lag.LagId,
				ServiceName: ServiceNameDirectConnect,
				Name:        *lag.LagName,
				Status:      status,
				Region:      region,
				Tags:        tags,
				Meta:        metaMap,
				Arn:         lagArn,
				CreatedAt:   time.Time{},
				Type:        getAwsServiceResourceType(ServiceNameDirectConnect, "lag"),
			}
			resources = append(resources, resource)
		}
	}

	// Get Virtual Interfaces (VIFs) as separate resources
	allVifsResult, err := svc.DescribeVirtualInterfaces(context.TODO(), &directconnect.DescribeVirtualInterfacesInput{})
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch all virtual interfaces", "error", err, "accountNumber", account.AccountNumber, "region", region)
	} else {
		for _, vif := range allVifsResult.VirtualInterfaces {
			if vif.VirtualInterfaceId == nil || vif.VirtualInterfaceName == nil {
				ctx.GetLogger().Warn("Skipping Virtual Interface due to missing ID or name")
				continue
			}

			tags := make(map[string][]string)

			// Get tags for the VIF
			if vif.VirtualInterfaceId != nil {
				tagsResult, err := svc.DescribeTags(context.TODO(), &directconnect.DescribeTagsInput{
					ResourceArns: []string{fmt.Sprintf("arn:aws:directconnect:%s:%s:dxvif/%s", region, account.AccountNumber, *vif.VirtualInterfaceId)},
				})
				if err != nil {
					ctx.GetLogger().Warn("failed to fetch virtual interface tags", "error", err, "vifId", *vif.VirtualInterfaceId)
				} else if len(tagsResult.ResourceTags) > 0 {
					for _, tag := range tagsResult.ResourceTags[0].Tags {
						if tag.Key != nil && tag.Value != nil {
							tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
						}
					}
				}
			}

			metaMap := structToMap(vif)

			status := providers.ResourceStatusUnknown
			if vif.VirtualInterfaceState != "" {
				status = directConnectVirtualInterfaceStateToNbStatus(vif.VirtualInterfaceState)
			}

			vifArn := fmt.Sprintf("arn:aws:directconnect:%s:%s:dxvif/%s", region, account.AccountNumber, *vif.VirtualInterfaceId)

			resource := providers.Resource{
				Id:          *vif.VirtualInterfaceId,
				ServiceName: ServiceNameDirectConnect,
				Name:        *vif.VirtualInterfaceName,
				Status:      status,
				Region:      region,
				Tags:        tags,
				Meta:        metaMap,
				Arn:         vifArn,
				CreatedAt:   time.Time{},
				Type:        getAwsServiceResourceType(ServiceNameDirectConnect, "virtualinterface"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *awsDirectConnect) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check for connections in DOWN state
		if resource.Type == getAwsServiceResourceType(ServiceNameDirectConnect, "connection") {
			if connectionState, ok := meta["ConnectionState"].(string); ok {
				if strings.ToUpper(connectionState) == "DOWN" {
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "aws_directconnect_connection_down",
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      0,
						Data: map[string]any{
							"message":         "Direct Connect connection is in DOWN state",
							"connectionState": connectionState,
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					}
					recommendations = append(recommendations, recommendation)
				}
			}

			// Check for connections without redundancy (no LAG)
			if vifCount, ok := meta["VirtualInterfaceCount"].(int); ok && vifCount > 0 {
				// Check if connection is part of a LAG
				if lagId, ok := meta["LagId"].(string); !ok || lagId == "" {
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "aws_directconnect_no_redundancy",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      0,
						Data: map[string]any{
							"message":        "Direct Connect connection is not part of a LAG",
							"recommendation": "Consider using Link Aggregation Groups (LAG) for redundancy",
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					}
					recommendations = append(recommendations, recommendation)
				}
			}

			// Check for connections with no virtual interfaces
			if vifCount, ok := meta["VirtualInterfaceCount"].(int); ok && vifCount == 0 {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryRightSizing,
					RuleName:     "aws_directconnect_no_virtual_interfaces",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"message":        "Direct Connect connection has no virtual interfaces",
						"recommendation": "Delete unused Direct Connect connections to save on port hour charges",
					},
					Action:              providers.RecommendationActionDelete,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}
		}

		// Check for LAGs with insufficient connections
		if resource.Type == getAwsServiceResourceType(ServiceNameDirectConnect, "lag") {
			if numberOfConnections, ok := meta["NumberOfConnections"].(int32); ok {
				if minimumLinks, ok := meta["MinimumLinks"].(int32); ok {
					if numberOfConnections < minimumLinks {
						recommendation := providers.Recommendation{
							CategoryName: providers.RecommendationCategoryConfiguration,
							RuleName:     "aws_directconnect_lag_below_minimum",
							Severity:     providers.RecommendationSeverityHigh,
							Savings:      0,
							Data: map[string]any{
								"message":             "LAG has fewer connections than minimum required",
								"numberOfConnections": numberOfConnections,
								"minimumLinks":        minimumLinks,
								"recommendation":      "Add more connections to meet minimum link requirements",
							},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						}
						recommendations = append(recommendations, recommendation)
					}
				}
			}

			// Check LAG state
			if lagState, ok := meta["LagState"].(string); ok {
				if strings.ToUpper(lagState) == "DOWN" {
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "aws_directconnect_lag_down",
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      0,
						Data: map[string]any{
							"message":  "LAG is in DOWN state",
							"lagState": lagState,
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					}
					recommendations = append(recommendations, recommendation)
				}
			}
		}

		// Check for virtual interfaces in DOWN state
		if resource.Type == getAwsServiceResourceType(ServiceNameDirectConnect, "virtualinterface") {
			if vifState, ok := meta["VirtualInterfaceState"].(string); ok {
				if strings.ToUpper(vifState) == "DOWN" {
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "aws_directconnect_vif_down",
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      0,
						Data: map[string]any{
							"message":               "Virtual Interface is in DOWN state",
							"virtualInterfaceState": vifState,
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					}
					recommendations = append(recommendations, recommendation)
				}
			}

			// Check for BGP peer status
			if bgpPeers, ok := meta["BgpPeers"].([]any); ok {
				for _, peerAny := range bgpPeers {
					if peerMap, ok := peerAny.(map[string]any); ok {
						if bgpStatus, ok := peerMap["BgpStatus"].(string); ok {
							if strings.ToUpper(bgpStatus) != "UP" {
								recommendation := providers.Recommendation{
									CategoryName: providers.RecommendationCategoryConfiguration,
									RuleName:     "aws_directconnect_bgp_peer_down",
									Severity:     providers.RecommendationSeverityHigh,
									Savings:      0,
									Data: map[string]any{
										"message":   "BGP peer is not in UP state",
										"bgpStatus": bgpStatus,
										"bgpPeer":   peerMap,
									},
									Action:              providers.RecommendationActionModify,
									ResourceServiceName: resource.ServiceName,
									ResourceId:          resource.Id,
									ResourceType:        resource.Type,
									ResourceRegion:      resource.Region,
								}
								recommendations = append(recommendations, recommendation)
								break
							}
						}
					}
				}
			}
		}
	}

	return recommendations, nil
}

func (a *awsDirectConnect) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "directconnect",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	return app, nil
}

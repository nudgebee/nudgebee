package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/configservice"
	"github.com/aws/smithy-go"
	"go.uber.org/multierr"
)

// serviceNameFromResourceType extracts a simplified service name from a full AWS resource type string.
// For example, "AWS::EC2::Instance" becomes "ec2".
func serviceNameFromResourceType(rt string) string {
	parts := strings.Split(rt, "::")
	if len(parts) > 1 {
		return strings.ToLower(parts[1])
	}
	return ""
}

// isConfigServiceNotEnabledError checks for errors that indicate AWS Config is not available.
// This is a placeholder and needs to be implemented by checking the actual errors from AWS.
func isConfigServiceNotEnabledError(err error) bool {
	var apiErr *smithy.GenericAPIError
	if errors.As(err, &apiErr) {
		// Error codes can vary, this covers common cases for when a service is not set up.
		// "AuthFailure", "AccessDeniedException", "UnrecognizedClientException"
		// Or a validation exception on the query if no recorder is set up.
		return apiErr.ErrorCode() == "AuthFailure" || strings.Contains(apiErr.ErrorMessage(), "is not authorized to perform") || strings.Contains(apiErr.ErrorMessage(), "No configuration recorder found")
	} else if strings.Contains(err.Error(), "AccessDeniedException") {
		return true
	}
	return false
}

func (a *awsProvider) queryServiceMapWithConfig(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryServiceMapRequest) (providers.QueryServiceMapResponse, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		return providers.QueryServiceMapResponse{}, fmt.Errorf("failed to create aws session: %w", err)
	}

	region := query.Region
	if region == "" && account.Region != nil {
		region = *account.Region
	}
	if region == "" {
		return providers.QueryServiceMapResponse{}, errors.New("region is required for QueryServiceMap")
	}

	cfgSvc := configservice.NewFromConfig(cfg)

	var applications []providers.ServiceMapApplication
	var mu sync.Mutex
	var wg sync.WaitGroup
	errChan := make(chan error, len(query.Resources)*2)

	identifiedResources := map[string]providers.ServiceMapApplication{}

	for _, resRequest := range query.Resources {
		wg.Add(1)
		go func(res providers.QueryServiceMapResourceRequest) {
			defer wg.Done()

			resourceArn := res.Resource // Assuming this is an ARN

			// Upstream query - find resources that have relationships pointing to this resource
			upstreamExpression := fmt.Sprintf("SELECT resourceId, resourceType, awsRegion, accountId, tags WHERE relationships.resourceId = '%s'", resourceArn)
			upstreamInput := &configservice.SelectResourceConfigInput{Expression: aws.String(upstreamExpression)}
			upstreamOutput, err := cfgSvc.SelectResourceConfig(context.TODO(), upstreamInput)
			if err != nil {
				errChan <- fmt.Errorf("failed to query upstream dependencies for %s: %w", resourceArn, err)
				return
			}

			// Downstream query - get the resource itself with its relationships
			downstreamExpression := fmt.Sprintf("SELECT resourceId, resourceType, awsRegion, accountId, relationships, tags WHERE resourceId = '%s'", resourceArn)
			downstreamInput := &configservice.SelectResourceConfigInput{Expression: aws.String(downstreamExpression)}
			downstreamOutput, err := cfgSvc.SelectResourceConfig(context.TODO(), downstreamInput)
			if err != nil {
				errChan <- fmt.Errorf("failed to query downstream dependencies for %s: %w", resourceArn, err)
				return
			}

			app := providers.ServiceMapApplication{
				Id: providers.ServiceApplicationId{
					Name:      resourceArn,
					Kind:      res.ServiceName,
					Namespace: region,
				},
				Upstreams:   []providers.UpstreamLink{},
				Downstreams: []providers.DownstreamLink{},
				Status:      "Unknown", // We might be able to get this from the resource config
			}

			// Process upstream
			for _, result := range upstreamOutput.Results {
				var item struct {
					ResourceType string `json:"resourceType"`
					ResourceId   string `json:"resourceId"`
					AWSRegion    string `json:"awsRegion"`
				}
				if err := json.Unmarshal([]byte(result), &item); err == nil {
					link := providers.ServiceApplicationLink{
						Id: providers.ServiceApplicationId{
							Name:      item.ResourceId,
							Kind:      serviceNameFromResourceType(item.ResourceType),
							Namespace: item.AWSRegion,
						},
					}
					app.Upstreams = append(app.Upstreams, link.ToUpstreamLink())
					mu.Lock()
					if _, ok := identifiedResources[link.Key()]; !ok {
						identifiedResources[link.Key()] = providers.ServiceMapApplication{
							Id:          link.Id,
							Upstreams:   []providers.UpstreamLink{},
							Downstreams: []providers.DownstreamLink{},
							Status:      "running",
						}
					}
					mu.Unlock()
				}
			}

			// Process downstream
			if len(downstreamOutput.Results) > 0 {
				var item struct {
					Relationships []struct {
						ResourceType     string `json:"resourceType"`
						ResourceId       string `json:"resourceId"`
						RelationshipName string `json:"relationshipName"`
					} `json:"relationships"`
				}
				if err := json.Unmarshal([]byte(downstreamOutput.Results[0]), &item); err == nil {
					for _, rel := range item.Relationships {
						link := providers.ServiceApplicationLink{
							Id: providers.ServiceApplicationId{
								Name:      rel.ResourceId,
								Kind:      serviceNameFromResourceType(rel.ResourceType),
								Namespace: region,
							},
						}
						app.Downstreams = append(app.Downstreams, link.ToDownstreamLink())
						if _, ok := identifiedResources[link.Key()]; !ok {
							identifiedResources[link.Key()] = providers.ServiceMapApplication{
								Id:          link.Id,
								Upstreams:   []providers.UpstreamLink{},
								Downstreams: []providers.DownstreamLink{},
								Status:      "running",
							}
						}
					}
				}
			}

			mu.Lock()
			applications = append(applications, app)
			mu.Unlock()

		}(resRequest)
	}

	wg.Wait()
	close(errChan)

	var allErrors error
	for err := range errChan {
		allErrors = multierr.Append(allErrors, err)
	}

	for _, v := range identifiedResources {
		applications = append(applications, v)
	}

	if allErrors != nil {
		return providers.QueryServiceMapResponse{}, allErrors
	}

	return providers.QueryServiceMapResponse{Applications: applications}, nil
}

func (a *awsProvider) queryServiceMapWithFallback(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryServiceMapRequest) (providers.QueryServiceMapResponse, error) {
	ctx.GetLogger().Info("Executing QueryServiceMap with service-specific fallback logic.")

	region := query.Region
	if region == "" && account.Region != nil {
		region = *account.Region
	}
	if region == "" {
		return providers.QueryServiceMapResponse{}, errors.New("region is required for QueryServiceMap in fallback")
	}

	var applications []providers.ServiceMapApplication

	errs := []string{}
	for _, resRequest := range query.Resources {
		// Normalize service name
		requestedServiceName := strings.TrimPrefix(strings.ToLower(resRequest.ServiceName), "amazon")
		requestedServiceName = strings.TrimPrefix(requestedServiceName, "aws")

		if service, ok := GetAwsService(requestedServiceName); ok {
			resourceId := resRequest.Resource
			app, err := service.GetServiceMap(ctx, account, region, resourceId)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			applications = append(applications, app)
		} else {
			return providers.QueryServiceMapResponse{}, errors.New("unsupported service - " + requestedServiceName)
		}
	}

	// add linked apps.
	missingApps := map[string]providers.ServiceApplicationId{}

	existingKeys := map[string]bool{}

	for _, app := range applications {
		existingKeys[app.Id.Key()] = true
	}

	for _, app := range applications {
		for _, upstream := range app.Upstreams {
			// upstream.Id is a string in format "name:kind:namespace"
			if existingKeys[upstream.Id] {
				continue
			}
			// Parse the string ID back to ServiceApplicationId
			parts := strings.Split(upstream.Id, ":")
			if len(parts) == 3 {
				missingApps[upstream.Id] = providers.ServiceApplicationId{
					Name:      parts[0],
					Kind:      parts[1],
					Namespace: parts[2],
				}
			}
		}
		for _, downstream := range app.Downstreams {
			// downstream.Id is a ServiceApplicationId object
			key := downstream.Id.Key()
			if existingKeys[key] {
				continue
			}
			missingApps[key] = downstream.Id
		}
	}
	for _, appId := range missingApps {
		applications = append(applications, providers.ServiceMapApplication{
			Id:          appId,
			Status:      "active",
			Upstreams:   []providers.UpstreamLink{},
			Downstreams: []providers.DownstreamLink{},
		})
	}

	return providers.QueryServiceMapResponse{Applications: applications, Errors: errs}, nil
}

// getClusterAndServiceNameFromArn parses the cluster and service name from an ECS Service ARN.
// Example ARN: arn:aws:ecs:us-east-1:123456789012:service/my-cluster/my-service
func getClusterAndServiceNameFromArn(arn string) (string, string) {
	parts := strings.Split(arn, "/")
	// e.g. arn:aws:ecs:region:account-id:service/cluster-name/service-name
	if len(parts) >= 3 {
		return parts[len(parts)-2], parts[len(parts)-1]
	}
	// Fallback for simpler ARN formats, though less common for user-created services.
	if len(parts) == 2 {
		return "default", parts[len(parts)-1] // Assume default cluster if not specified
	}
	return "", ""
}

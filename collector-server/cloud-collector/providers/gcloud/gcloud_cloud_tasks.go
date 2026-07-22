package gcloud

import (
	"fmt"
	"strings"
	"sync"
	"time"

	cloudtasks "cloud.google.com/go/cloudtasks/apiv2"
	"cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
	"google.golang.org/api/iterator"
	locationpb "google.golang.org/genproto/googleapis/cloud/location"

	"nudgebee/collector/cloud/providers"
)

const ServiceNameCloudTasks = "Cloud Tasks"

// cloudTasksAnchorTTL no-ops repeated per-region GetResources calls within one refresh:
// queues are regional but the store loop scans ~40 regions, and creating a gRPC client
// per region exhausts the request deadline. Instead we enumerate the service's own
// locations once (with a single client) and list queues across all of them.
const cloudTasksAnchorTTL = 1 * time.Minute

// cloudTasksService lists Cloud Tasks queues across all of the project's Cloud Tasks
// locations in a single scan (see cloudTasksAnchorTTL).
type cloudTasksService struct {
	scanned sync.Map // projectID -> time.Time of last scan
}

func (s *cloudTasksService) GetMetrices(_ providers.CloudProviderContext, _ providers.Account, _ providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return providers.QueryMetricsResponse{}, nil
}

func (s *cloudTasksService) GetResources(ctx providers.CloudProviderContext, account providers.Account, _ string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	now := time.Now()
	if last, ok := s.scanned.Load(session.ProjectId); ok {
		if t, ok := last.(time.Time); ok && now.Sub(t) < cloudTasksAnchorTTL {
			return nil, nil
		}
	}
	s.scanned.Store(session.ProjectId, now)

	client, err := cloudtasks.NewClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create cloud tasks client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close cloud tasks client", "error", cerr)
		}
	}()

	// Enumerate the project's Cloud Tasks locations, then list queues in each with the
	// same client (ListQueues has no "-" location wildcard).
	resources := []providers.Resource{}
	locIt := client.ListLocations(ctx.GetContext(), &locationpb.ListLocationsRequest{
		Name: fmt.Sprintf("projects/%s", session.ProjectId),
	})
	for {
		loc, err := locIt.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			if isGCPPermissionOrNotFoundError(err) {
				ctx.GetLogger().Warn("skipping Cloud Tasks — API unavailable (disabled, permission, or deadline)", "error", err)
				return resources, nil
			}
			return resources, fmt.Errorf("failed to list cloud tasks locations: %w", err)
		}
		resources = append(resources, s.listQueuesInLocation(ctx, client, session.ProjectId, loc.GetLocationId())...)
	}

	if len(resources) > 0 {
		ctx.GetLogger().Info("fetched Cloud Tasks queues", "count", len(resources), "projectId", session.ProjectId)
	}
	return resources, nil
}

func (s *cloudTasksService) listQueuesInLocation(ctx providers.CloudProviderContext, client *cloudtasks.Client, projectID, location string) []providers.Resource {
	var resources []providers.Resource
	it := client.ListQueues(ctx.GetContext(), &cloudtaskspb.ListQueuesRequest{
		Parent: fmt.Sprintf("projects/%s/locations/%s", projectID, location),
	})
	for {
		queue, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			ctx.GetLogger().Warn("skipping Cloud Tasks queues in location", "error", err, "location", location)
			return resources
		}
		resources = append(resources, cloudTasksQueueToResource(queue, location))
	}
	return resources
}

func cloudTasksQueueToResource(queue *cloudtaskspb.Queue, region string) providers.Resource {
	// queue.Name is "projects/{project}/locations/{location}/queues/{queue}"
	parts := strings.Split(queue.GetName(), "/")
	queueID := parts[len(parts)-1]

	meta := map[string]interface{}{
		"selfLink": queue.GetName(),
		"state":    queue.GetState().String(),
	}
	if rc := queue.GetRateLimits(); rc != nil {
		meta["max_dispatches_per_second"] = rc.GetMaxDispatchesPerSecond()
		meta["max_concurrent_dispatches"] = rc.GetMaxConcurrentDispatches()
	}

	return providers.Resource{
		Id:          queueID,
		Name:        queueID,
		Type:        "cloud-tasks",
		Arn:         queue.GetName(),
		ServiceName: ServiceNameCloudTasks,
		Status:      providers.ResourceStatusActive,
		Region:      region,
		Tags:        map[string][]string{},
		Meta:        meta,
	}
}

func (s *cloudTasksService) GetRecommendations(_ providers.CloudProviderContext, _ providers.Account, _ providers.ListRecommendationsRequest, _ []providers.Resource) ([]providers.Recommendation, error) {
	return nil, nil
}

func (s *cloudTasksService) ApplyRecommendation(_ providers.CloudProviderContext, _ providers.Account, _ providers.Recommendation) error {
	return fmt.Errorf("apply recommendation not supported for Cloud Tasks")
}

func (s *cloudTasksService) ApplyCommand(_ providers.CloudProviderContext, _ providers.Account, _ providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, fmt.Errorf("apply command not supported for Cloud Tasks")
}

func (s *cloudTasksService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}

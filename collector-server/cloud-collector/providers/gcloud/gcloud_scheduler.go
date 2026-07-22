package gcloud

import (
	"fmt"
	"strings"

	scheduler "cloud.google.com/go/scheduler/apiv1"
	"cloud.google.com/go/scheduler/apiv1/schedulerpb"
	"google.golang.org/api/iterator"

	"nudgebee/collector/cloud/providers"
)

const ServiceNameScheduler = "Cloud Scheduler"

// schedulerService lists Cloud Scheduler jobs (regional cron jobs). GetResources
// lists jobs for the given region; the store loop iterates regions.
type schedulerService struct{}

func (s *schedulerService) GetMetrices(_ providers.CloudProviderContext, _ providers.Account, _ providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return providers.QueryMetricsResponse{}, nil
}

func (s *schedulerService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	if region == "" || strings.EqualFold(region, "global") {
		return nil, nil
	}

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := scheduler.NewCloudSchedulerClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create cloud scheduler client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close cloud scheduler client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	it := client.ListJobs(ctx.GetContext(), &schedulerpb.ListJobsRequest{
		Parent: fmt.Sprintf("projects/%s/locations/%s", session.ProjectId, region),
	})
	for {
		job, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			if isGCPPermissionOrNotFoundError(err) {
				ctx.GetLogger().Warn("skipping Cloud Scheduler jobs — API disabled, permission denied, or region unsupported", "error", err, "region", region)
				return resources, nil
			}
			return resources, fmt.Errorf("failed to list cloud scheduler jobs: %w", err)
		}
		resources = append(resources, schedulerJobToResource(job, region))
	}

	if len(resources) > 0 {
		ctx.GetLogger().Info("fetched Cloud Scheduler jobs", "count", len(resources), "region", region)
	}
	return resources, nil
}

func schedulerJobToResource(job *schedulerpb.Job, region string) providers.Resource {
	// job.Name is "projects/{project}/locations/{location}/jobs/{job}"
	parts := strings.Split(job.GetName(), "/")
	jobID := parts[len(parts)-1]

	meta := map[string]interface{}{
		"selfLink": job.GetName(),
		"schedule": job.GetSchedule(),
		"timezone": job.GetTimeZone(),
		"state":    job.GetState().String(),
	}
	// Capture the job's target so the KG can later link the cron to what it invokes.
	if t := job.GetPubsubTarget(); t != nil {
		meta["target_type"] = "pubsub"
		meta["target"] = t.GetTopicName()
	} else if t := job.GetHttpTarget(); t != nil {
		meta["target_type"] = "http"
		meta["target"] = t.GetUri()
	} else if t := job.GetAppEngineHttpTarget(); t != nil {
		meta["target_type"] = "appengine"
		meta["target"] = t.GetRelativeUri()
	}

	return providers.Resource{
		Id:          jobID,
		Name:        jobID,
		Type:        "cloud-scheduler",
		Arn:         job.GetName(),
		ServiceName: ServiceNameScheduler,
		Status:      providers.ResourceStatusActive,
		Region:      region,
		Tags:        map[string][]string{},
		Meta:        meta,
	}
}

func (s *schedulerService) GetRecommendations(_ providers.CloudProviderContext, _ providers.Account, _ providers.ListRecommendationsRequest, _ []providers.Resource) ([]providers.Recommendation, error) {
	return nil, nil
}

func (s *schedulerService) ApplyRecommendation(_ providers.CloudProviderContext, _ providers.Account, _ providers.Recommendation) error {
	return fmt.Errorf("apply recommendation not supported for Cloud Scheduler")
}

func (s *schedulerService) ApplyCommand(_ providers.CloudProviderContext, _ providers.Account, _ providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, fmt.Errorf("apply command not supported for Cloud Scheduler")
}

func (s *schedulerService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}

package cloudfoundry

import (
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"sync"
	"time"
)

const ServiceNameApps = "apps"

type cfAppsService struct{}

func (s *cfAppsService) GetResources(ctx providers.CloudProviderContext, client *cfClient, orgName string) ([]providers.Resource, error) {
	apps, err := getPaginated[cfApp](client, "/v3/apps?per_page=200")
	if err != nil {
		return nil, fmt.Errorf("failed to list apps: %w", err)
	}

	// Build space/org lookup maps for enrichment
	spaces, orgs := buildLookupMaps(ctx, client)

	// Build process lookup map for app allocation data (memory, disk, instances)
	processMap := buildProcessMap(ctx, client)

	// Build per-instance stats map for live instance data (CPU, memory, disk, uptime)
	instanceStatsMap := buildInstanceStatsMap(ctx, client, processMap)

	var resources []providers.Resource
	for _, app := range apps {
		resource := appToResource(app, spaces, orgs, processMap, instanceStatsMap)
		resources = append(resources, resource)
	}

	return resources, nil
}

// buildProcessMap fetches all processes and builds a map keyed by app GUID.
// Each entry aggregates the "web" process allocation (instances, memory, disk).
func buildProcessMap(ctx providers.CloudProviderContext, client *cfClient) map[string]cfProcess {
	processes, err := getPaginated[cfProcess](client, "/v3/processes?per_page=200")
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch processes for app enrichment", "error", err)
		return make(map[string]cfProcess)
	}

	processMap := make(map[string]cfProcess)
	for _, p := range processes {
		appGUID := p.Relations.App.Data.GUID
		if appGUID == "" {
			continue
		}
		// Prefer the "web" process type; fall back to first process seen
		if existing, ok := processMap[appGUID]; ok {
			if p.Type == "web" && existing.Type != "web" {
				processMap[appGUID] = p
			}
		} else {
			processMap[appGUID] = p
		}
	}
	return processMap
}

// buildInstanceStatsMap fetches process stats for each app's web process and returns
// per-instance data (CPU, memory, disk, uptime) keyed by app GUID.
// Uses a worker pool to fetch stats concurrently and avoid N+1 sequential API calls.
func buildInstanceStatsMap(ctx providers.CloudProviderContext, client *cfClient, processMap map[string]cfProcess) map[string][]map[string]any {
	const workerCount = 10

	type statsResult struct {
		appGUID   string
		instances []map[string]any
	}

	jobs := make(chan struct {
		appGUID string
		proc    cfProcess
	}, len(processMap))
	results := make(chan statsResult, len(processMap))

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				statsPath := fmt.Sprintf("/v3/processes/%s/stats", job.proc.GUID)
				statsBody, err := client.get(statsPath)
				if err != nil {
					ctx.GetLogger().Warn("failed to get process stats for instance enrichment", "app_guid", job.appGUID, "error", err)
					continue
				}

				var stats cfProcessStats
				if err := json.Unmarshal(statsBody, &stats); err != nil {
					ctx.GetLogger().Warn("failed to parse process stats for instance enrichment", "app_guid", job.appGUID, "error", err)
					continue
				}

				var instances []map[string]any
				for _, inst := range stats.Resources {
					instData := map[string]any{
						"index":      inst.Index,
						"state":      inst.State,
						"cpu":        inst.Usage.CPU * 100, // Convert to percentage
						"mem":        inst.Usage.Mem,
						"mem_quota":  inst.MemQuota,
						"disk":       inst.Usage.Disk,
						"disk_quota": inst.DiskQuota,
						"uptime":     inst.Uptime,
					}
					instances = append(instances, instData)
				}
				if len(instances) > 0 {
					results <- statsResult{appGUID: job.appGUID, instances: instances}
				}
			}
		}()
	}

	for appGUID, proc := range processMap {
		jobs <- struct {
			appGUID string
			proc    cfProcess
		}{appGUID: appGUID, proc: proc}
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	statsMap := make(map[string][]map[string]any)
	for r := range results {
		statsMap[r.appGUID] = r.instances
	}

	return statsMap
}

func appToResource(app cfApp, spaces map[string]cfSpace, orgs map[string]cfOrg, processMap map[string]cfProcess, instanceStatsMap map[string][]map[string]any) providers.Resource {
	spaceGUID := app.Relations.Space.Data.GUID
	spaceName := spaceGUID
	orgName := ""
	orgGUID := ""

	if space, ok := spaces[spaceGUID]; ok {
		spaceName = space.Name
		orgGUID = space.Relations.Organization.Data.GUID
		if org, ok := orgs[orgGUID]; ok {
			orgName = org.Name
		}
	}

	status := providers.ResourceStatusUnknown
	switch app.State {
	case "STARTED":
		status = providers.ResourceStatusActive
	case "STOPPED":
		status = providers.ResourceStatusInactive
	}

	tags := make(map[string][]string)
	tags["space"] = []string{spaceName}
	if orgName != "" {
		tags["org"] = []string{orgName}
	}
	// Include CF labels as tags
	for k, v := range app.Metadata.Labels {
		tags[k] = []string{v}
	}

	// Synthetic ARN-like identifier
	arn := fmt.Sprintf("cf://%s/%s/%s", orgName, spaceName, app.Name)

	meta := map[string]any{
		"space_guid":     spaceGUID,
		"org_guid":       orgGUID,
		"lifecycle_type": app.Lifecycle.Type,
		"state":          app.State,
	}
	if len(app.Lifecycle.Data.Buildpacks) > 0 {
		meta["buildpacks"] = app.Lifecycle.Data.Buildpacks
	}
	if app.Lifecycle.Data.Stack != "" {
		meta["stack"] = app.Lifecycle.Data.Stack
	}

	// Enrich with process allocation data (instances, memory, disk, health check, command)
	if proc, ok := processMap[app.GUID]; ok {
		meta["instances"] = proc.Instances
		meta["memory_in_mb"] = proc.MemoryInMB
		meta["disk_in_mb"] = proc.DiskInMB
		if proc.Command != "" {
			meta["command"] = proc.Command
		}
		if proc.HealthCheck.Type != "" {
			meta["health_check_type"] = proc.HealthCheck.Type
			if proc.HealthCheck.Data.Endpoint != "" {
				meta["health_check_endpoint"] = proc.HealthCheck.Data.Endpoint
			}
			if proc.HealthCheck.Data.Timeout > 0 {
				meta["health_check_timeout"] = proc.HealthCheck.Data.Timeout
			}
		}
	}

	// Enrich with per-instance stats (CPU, memory, disk, uptime per instance)
	if instStats, ok := instanceStatsMap[app.GUID]; ok {
		meta["instance_stats"] = instStats
	}

	// App updated_at timestamp
	if !app.UpdatedAt.IsZero() {
		meta["updated_at"] = app.UpdatedAt.Format(time.RFC3339)
	}

	return providers.Resource{
		Id:          app.GUID,
		Name:        app.Name,
		Type:        "app",
		Arn:         arn,
		ServiceName: ServiceNameApps,
		Status:      status,
		Region:      orgName,
		Tags:        tags,
		Meta:        meta,
		CreatedAt:   app.CreatedAt,
	}
}

// getAppMetrics fetches process stats for an app and converts to MetricItems.
func getAppMetrics(ctx providers.CloudProviderContext, client *cfClient, appGUID string, serviceName string) ([]providers.MetricItem, error) {
	// First get the processes for this app
	path := fmt.Sprintf("/v3/apps/%s/processes", appGUID)
	body, err := client.get(path)
	if err != nil {
		return nil, fmt.Errorf("failed to get processes for app %s: %w", appGUID, err)
	}

	var processesResp struct {
		Resources []cfProcess `json:"resources"`
	}
	if err := json.Unmarshal(body, &processesResp); err != nil {
		return nil, fmt.Errorf("failed to parse processes response: %w", err)
	}

	now := time.Now()
	var metrics []providers.MetricItem

	for _, process := range processesResp.Resources {
		// Get stats for each process
		statsPath := fmt.Sprintf("/v3/processes/%s/stats", process.GUID)
		statsBody, err := client.get(statsPath)
		if err != nil {
			ctx.GetLogger().Warn("failed to get process stats", "process_guid", process.GUID, "error", err)
			continue
		}

		var stats cfProcessStats
		if err := json.Unmarshal(statsBody, &stats); err != nil {
			ctx.GetLogger().Warn("failed to parse process stats", "process_guid", process.GUID, "error", err)
			continue
		}

		if len(stats.Resources) == 0 {
			continue
		}

		// Aggregate metrics across instances
		var totalCPU float64
		var totalMem, totalDisk int64
		var totalMemQuota, totalDiskQuota int64
		runningInstances := 0

		for _, inst := range stats.Resources {
			if inst.State == "RUNNING" {
				totalCPU += inst.Usage.CPU
				totalMem += inst.Usage.Mem
				totalDisk += inst.Usage.Disk
				totalMemQuota += inst.MemQuota
				totalDiskQuota += inst.DiskQuota
				runningInstances++
			}
		}

		if runningInstances == 0 {
			continue
		}

		avgCPU := totalCPU / float64(runningInstances) * 100 // Convert to percentage

		timestamps := []time.Time{now}

		metrics = append(metrics,
			providers.MetricItem{
				Name:        "cpu_usage",
				Statistics:  "Average",
				ResourceId:  appGUID,
				Values:      []float64{avgCPU},
				Timestamps:  timestamps,
				ServiceName: serviceName,
			},
			providers.MetricItem{
				Name:        "memory_usage_bytes",
				Statistics:  "Average",
				ResourceId:  appGUID,
				Values:      []float64{float64(totalMem) / float64(runningInstances)},
				Timestamps:  timestamps,
				ServiceName: serviceName,
			},
			providers.MetricItem{
				Name:        "disk_usage_bytes",
				Statistics:  "Average",
				ResourceId:  appGUID,
				Values:      []float64{float64(totalDisk) / float64(runningInstances)},
				Timestamps:  timestamps,
				ServiceName: serviceName,
			},
			providers.MetricItem{
				Name:        "instance_count",
				Statistics:  "Sum",
				ResourceId:  appGUID,
				Values:      []float64{float64(runningInstances)},
				Timestamps:  timestamps,
				ServiceName: serviceName,
			},
		)

		// Calculate percentage metrics if quotas are available
		if totalMemQuota > 0 {
			avgMemPct := (float64(totalMem) / float64(totalMemQuota)) * 100
			metrics = append(metrics, providers.MetricItem{
				Name:        "memory_usage_percent",
				Statistics:  "Average",
				ResourceId:  appGUID,
				Values:      []float64{avgMemPct},
				Timestamps:  timestamps,
				ServiceName: serviceName,
			})
		}
		if totalDiskQuota > 0 {
			avgDiskPct := (float64(totalDisk) / float64(totalDiskQuota)) * 100
			metrics = append(metrics, providers.MetricItem{
				Name:        "disk_usage_percent",
				Statistics:  "Average",
				ResourceId:  appGUID,
				Values:      []float64{avgDiskPct},
				Timestamps:  timestamps,
				ServiceName: serviceName,
			})
		}
	}

	return metrics, nil
}

// buildLookupMaps fetches spaces and orgs for enriching app resources.
func buildLookupMaps(ctx providers.CloudProviderContext, client *cfClient) (map[string]cfSpace, map[string]cfOrg) {
	spacesMap := make(map[string]cfSpace)
	orgsMap := make(map[string]cfOrg)

	spaces, err := getPaginated[cfSpace](client, "/v3/spaces?per_page=200")
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch spaces for lookup", "error", err)
	} else {
		for _, s := range spaces {
			spacesMap[s.GUID] = s
		}
	}

	orgs, err := getPaginated[cfOrg](client, "/v3/organizations?per_page=200")
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch orgs for lookup", "error", err)
	} else {
		for _, o := range orgs {
			orgsMap[o.GUID] = o
		}
	}

	return spacesMap, orgsMap
}

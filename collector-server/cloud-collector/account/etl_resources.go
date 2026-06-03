package account

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/samber/lo"
	"golang.org/x/exp/maps"
)

func getResourcesInternal(ctx *security.RequestContext, accountId string, request providers.ListResourceRequest) (providers.ListResourcesResponse, providers.Account, error) {
	if request.ServiceName == "" || accountId == "" {
		return providers.ListResourcesResponse{}, providers.Account{}, fmt.Errorf("invalid request")
	}
	account, provider, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch account", "error", err, "accountId", accountId)
		return providers.ListResourcesResponse{}, providers.Account{}, err
	}
	cloudProvider, ok := providers.GetProvider(provider)
	if !ok {
		return providers.ListResourcesResponse{}, providers.Account{}, fmt.Errorf("provider not found")
	}
	resources, err := cloudProvider.ListResources(ctx, account, request)
	return resources, account, err
}

// this function results in deadlocks :P as its called from both cost discovery and event discovery
// this is current workaround to avoid deadlocks,
// i guess eventually its better to have mu based on accountId + serviceName + region to avoid unnecessary locks
var storeResourcesMutex sync.Mutex

var providerDefaultServices = map[string][]string{
	"aws": {
		"AmazonEC2",
		"AmazonRDS",
		"AmazonS3",
		"AWSLambda",
		"AmazonECR",
		"AmazonSNS",
		"AmazonSES",
		"AWSCloudTrail",
		"AWSQueueService",
		"AmazonECRPublic",
		"AmazonVPC",
		"AWSELB",
		"AWSKMS",
		"AWSSecretsManager",
		"AmazonCloudWatch",
		"AmazonEKS",
		"AWSCodeArtifact",
		"AmazonElastiCache",
		"AWSSecurityHub",
		"AmazonMSK",
		"AmazonSageMaker",
		"AmazonRedshift",
		"AmazonES",
		"AmazonEFS",
		"AmazonBedrock",
		"AWSCloudFormation",
		"AmazonXray",
		"AWSBackup",
		"AWSCloudShell",
		"AmazonCloudFront",
		"AmazonECS",
		"AmazonDynamoDB",
		"AmazonGuardDuty",
		"AWSIAM",
		"AWSElasticBeanstalk",
		"AWSStepFunctions",
		"AWSDirectConnect",
		"AWSWAF",
		"AmazonInspector",
		"AWSConfig",
		"AWSSystemsManager",
		"AWSFargate",
		"AWSECS",
	},
	"azure": {
		"microsoft.compute/virtualmachines",
		"microsoft.sql/servers",
		"microsoft.compute/disks",
		"microsoft.storage/storageaccounts",
		"microsoft.compute/virtualmachinescalesets",
		"microsoft.app/containerapps",
		"microsoft.machinelearningservices/workspaces",
		"microsoft.network/publicipaddresses",
		"microsoft.botservice/botservices",
		"microsoft.containerregistry/registries",
		"microsoft.network/loadbalancers",
		"microsoft.operationalinsights/workspaces",
		"microsoft.insights/metricalerts",
		"microsoft.web/sites",
		"microsoft.keyvault/vaults",
		"microsoft.documentdb/databaseaccounts",
		"microsoft.network/virtualnetworks",
		"microsoft.containerservice/managedclusters",
		"microsoft.network/applicationgateways",
		"microsoft.authorization/roleassignments",
		"microsoft.cache/redis",
		"microsoft.dbformariadb/servers",
		"microsoft.dbformysql/flexibleservers",
		"microsoft.dbforpostgresql/flexibleservers",
		"microsoft.network/azurefirewalls",
		"microsoft.network/dnszones",
		"microsoft.network/expressroutecircuits",
		"microsoft.network/frontdoors",
		"microsoft.web/sites/functions",
		"microsoft.storage/storageaccounts/fileservices",
		"microsoft.network/ddosprotectionplans",
		"microsoft.security/pricings",
		"microsoft.securityinsights",
		"microsoft.hybridcompute/machines",
		"microsoft.insights",
		"microsoft.authorization/policyassignments",
		"microsoft.devops/projects",
		"microsoft.logic/workflows",
		"microsoft.devops/pipelines",
		"microsoft.eventgrid/topics",
		"microsoft.cdn/profiles",
		"microsoft.network/networksecuritygroups",
		"microsoft.network/networkinterfaces",
		"microsoft.recoveryservices/vaults",
		"microsoft.web/serverfarms",
		"microsoft.network/networkwatchers",
		"microsoft.compute/sshpublickeys",
		"microsoft.managedidentity/userassignedidentities",
	},
	"gcp":          {"Compute Engine", "Cloud Storage", "BigQuery", "Cloud SQL", "Kubernetes Engine", "Cloud Pub/Sub", "Cloud Functions", "Cloud Run", "Cloud Monitoring", "Networking", "VM Manager", "Vertex AI", "Gemini API", "Cloud Load Balancing", "compute.googleapis.com/Disk", "compute.googleapis.com/NetworkInterface"},
	"cloudfoundry": {"apps", "spaces", "organizations", "routes", "service_instances", "builds", "deployments", "tasks", "service_credential_bindings"},
}

func discoverAndStoreResources(ctx *security.RequestContext, account providers.Account, accountId string) {
	for _, serviceName := range providerDefaultServices[strings.ToLower(account.CloudProvider)] {
		_, err := StoreResources(ctx, accountId, serviceName)
		if err != nil {
			ctx.GetLogger().Error("unable to discover service resources", "error", err, "service", serviceName)
		}
	}
}

func StoreResourcesAll(ctx *security.RequestContext, accountId string) (StoreResourcesResponse, error) {
	t0 := time.Now()
	availableServices, err := getAllServices(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch available services", "error", err)
	}
	ctx.GetLogger().Info("fetched available services", "count", len(availableServices), "time", time.Since(t0).String())
	cnt := 0
	errs := []string{}
	for _, serviceName := range availableServices {
		d, err := StoreResources(ctx, accountId, serviceName)
		if err != nil {
			if !errors.Is(err, errors.ErrUnsupported) {
				ctx.GetLogger().Error("unable to store resources", "error", err, "serviceName", serviceName)
				errs = append(errs, err.Error())
			}
			continue
		}
		cnt += d.Count
		ctx.GetLogger().Info("stored resources", "count", d.Count, "time", time.Since(t0).String())
	}
	return StoreResourcesResponse{
		Count:    cnt,
		Duration: time.Since(t0),
		Errors:   errs,
	}, nil

}

// regionlessProviders are cloud providers whose APIs don't use regions for resource listing.
// These providers use alternative groupings (e.g., CloudFoundry uses org names stored in the
// "region" column, but the CF API itself doesn't filter by region). Skipping region-bootstrap
// logic for these prevents bootstrap deadlocks on fresh accounts.
var regionlessProviders = map[string]bool{
	"cloudfoundry": true,
}

// alwaysSelfDiscoverRegionsServices are services that must always defer region
// discovery to the provider (via the provider's own listAllRegions API). The
// usual flow constrains region scans to whatever regions are already represented
// in cloud_resourses for this account+service — which silently locks future
// refreshes to a subset when the FIRST scan was partial. Per-region resources
// like GCP subnets (one default subnet auto-created per region) hit this lock-in
// pathologically. See issue #31101 gap #5: only 1 of 44 GCP subnets emitted
// because the first refresh recorded only us-central1, then every subsequent
// refresh re-derived `regions = ['us-central1']` from that single row.
var alwaysSelfDiscoverRegionsServices = map[string]bool{
	"networking": true, // GCP networking (subnets are per-region)
}

// Global services that don't have region-specific resources (legitimate empty region case)
var globalServices = map[string]bool{
	"awsiam":           true,
	"amazons3":         true, // S3 buckets are global (though they have a region attribute)
	"amazonroute53":    true,
	"awscloudfront":    true,
	"awswaf":           true,
	"awsorganizations": true,
	"awsconfig":        true, // AWS Config is account-level, not region-scoped
}

// isGlobalAwsService checks if a service name corresponds to a global AWS service
// by normalizing the service name (lowercase, no spaces) and checking against the globalServices map
func isGlobalAwsService(serviceName string) bool {
	normalizedName := strings.ToLower(strings.ReplaceAll(serviceName, " ", ""))
	return globalServices[normalizedName]
}

func StoreResources(ctx *security.RequestContext, accountId string, serviceName string, regions ...string) (StoreResourcesResponse, error) {
	t0 := time.Now()

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("unable to get dbms", "error", err)
		return StoreResourcesResponse{
			Count:    0,
			Duration: time.Since(t0),
		}, err
	}

	// Fetch account early to determine provider type for region logic.
	// Regionless providers (e.g., CloudFoundry) don't use AWS-style regions;
	// their APIs return all resources regardless of region parameter.
	accountInfo, _, accErr := getAccount(ctx, accountId)
	isRegionless := accErr == nil && regionlessProviders[strings.ToLower(accountInfo.CloudProvider)]

	// Services in alwaysSelfDiscoverRegionsServices skip the DB-history derivation
	// and let the provider list every region itself, breaking the partial-first-scan
	// lock-in described above.
	selfDiscoverRegions := alwaysSelfDiscoverRegionsServices[strings.ToLower(serviceName)]

	if len(regions) == 0 && !isRegionless && !selfDiscoverRegions {
		query := `select distinct region from cloud_resourses where account = $1 and lower(service_name) = lower($2) and region is not null and region != ''`
		err := dbms.QueryAndScan(&regions, query, accountId, serviceName)
		if err != nil {
			ctx.GetLogger().Error("unable to fetch regions from database", "error", err, "service", serviceName)
			return StoreResourcesResponse{
				Count:    0,
				Duration: time.Since(t0),
			}, err
		}

		// If no regions found for this service, try regions from other services in the account.
		// This helps when a new service is added but other services already have region data.
		if len(regions) == 0 {
			if !isGlobalAwsService(serviceName) {
				ctx.GetLogger().Warn("no existing regions found for service, attempting fallback region discovery",
					"service", serviceName,
					"account", accountId)

				fallbackQuery := `select distinct region from cloud_resourses
								   where account = $1
								   and region is not null
								   and region != ''
								   and is_active = true
								   order by region
								   limit 50`
				err := dbms.QueryAndScan(&regions, fallbackQuery, accountId)
				if err != nil {
					ctx.GetLogger().Error("unable to fetch fallback regions", "error", err, "service", serviceName)
					return StoreResourcesResponse{
						Count:    0,
						Duration: time.Since(t0),
					}, fmt.Errorf("unable to fetch fallback regions: %w", err)
				}

				if len(regions) > 0 {
					ctx.GetLogger().Info("using fallback regions from other active services",
						"service", serviceName,
						"account", accountId,
						"region_count", len(regions),
						"regions", regions)
				} else {
					// Fresh account bootstrap: no regions in DB at all.
					// All providers (AWS, Azure, GCP) can discover regions via their own API
					// when ListResources receives empty regions, so let them handle it.
					ctx.GetLogger().Info("no DB regions found - letting provider handle region discovery",
						"service", serviceName,
						"account", accountId)
				}
			}
		}
	} else if isRegionless {
		ctx.GetLogger().Debug("regionless provider - skipping region bootstrap logic",
			"service", serviceName,
			"account", accountId,
			"provider", accountInfo.CloudProvider)
	}

	// For global AWS services, the regions list above is a stale cache of where
	// resources have been seen historically — using it as a filter would resurrect
	// the original bug (a bucket in a "new" region gets dropped because the region
	// isn't in the cached set). Strip it before passing to the provider; awsProvider
	// short-circuits global services to a single account-wide call regardless. The
	// archival path below uses isGlobalAwsService directly and is unaffected.
	providerRegions := regions
	if isGlobalAwsService(serviceName) {
		providerRegions = nil
	}

	resources, account, err := getResourcesInternal(ctx, accountId, providers.ListResourceRequest{
		ServiceName: serviceName,
		Regions:     providerRegions,
	})

	// Update agent status on every exit path so the UI always reflects the
	// latest sync attempt. Previously this defer lived after the error/empty
	// checks, so failed fetches (e.g. GCP SERVICE_DISABLED) never wrote a
	// "resources" entry to connection_status, leaving the UI stuck on
	// "Disconnected" with no last-sync time.
	defer func() {
		msg := ""
		if err != nil && !errors.Is(err, errors.ErrUnsupported) {
			msg = err.Error()
		}
		// Skip updating connection_status for unsupported services — they are
		// expected gaps (e.g. servicebus/namespaces with no provider impl) and
		// should not overwrite a previous successful sync entry.
		if errors.Is(err, errors.ErrUnsupported) {
			return
		}
		if uerr := updateOrCreateAgentStatus(ctx, accountId, AgentStatusConnected, msg, true, map[string]any{
			"account_number": account.AccountNumber,
			"resources": map[string]any{
				"updated_at": time.Now().UTC().Format(time.RFC3339),
				"last_job": map[string]any{
					"service_name": serviceName,
					"regions":      regions,
				},
				"err": msg,
			},
		}); uerr != nil {
			ctx.GetLogger().Error("Failed to update agent status", "error", uerr.Error())
		}
	}()

	if err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			ctx.GetLogger().Debug("service does not support resource listing", "serviceName", serviceName)
		} else {
			ctx.GetLogger().Error("unable to fetch resources", "error", err, "serviceName", serviceName, "regions", regions)
		}
		return StoreResourcesResponse{
			Count:    0,
			Duration: time.Since(t0),
		}, err
	}
	ctx.GetLogger().Info("pulled resources", "time", time.Since(t0).String(), "count", len(resources.Items), "regions_queried", len(regions))
	if len(resources.Items) == 0 {
		// SAFEGUARD: Only archive if we actually queried specific regions OR it's a known global/regionless service.
		// When regions=[] and provider self-discovered regions but returned 0 items, a provider-level
		// failure (e.g., Azure getRegions timeout) could silently return 0 items without error,
		// causing unscoped archival of all existing resources for this service.
		if len(regions) == 0 && !isGlobalAwsService(serviceName) && !isRegionless {
			ctx.GetLogger().Warn("refusing to archive service data when zero regions were queried - possible bootstrap or provider failure",
				"service", serviceName,
				"account", accountId,
				"action", "skipping archival to prevent data loss")
			return StoreResourcesResponse{
				Count:    0,
				Arns:     []string{},
				Duration: time.Since(t0),
			}, nil
		}

		// Global services (S3, IAM, Route53, CloudFront, WAF, …) are account-wide,
		// so a region-scoped archive would leak rows whose region wasn't iterated.
		// Treat archival as unscoped for them — same logic as in storeResourcesInsert.
		archiveScopeRegions := regions
		if isGlobalAwsService(serviceName) {
			archiveScopeRegions = nil
		}

		var archiveResourcesQuery string
		var args []any
		if len(archiveScopeRegions) > 0 {
			placeholders := make([]string, len(archiveScopeRegions))
			for i := range archiveScopeRegions {
				placeholders[i] = fmt.Sprintf("$%d", i+3)
			}
			archiveResourcesQuery = fmt.Sprintf(`update cloud_resourses set is_active = false, status = 'Deleted' where account = $1 and lower(service_name) = $2 and (meta->>'nb_source' IS NULL OR meta->>'nb_source' != 'billing') and region in (%s)`, strings.Join(placeholders, ", "))
			args = append([]any{accountId, strings.ToLower(serviceName)}, lo.ToAnySlice(archiveScopeRegions)...)
		} else {
			archiveResourcesQuery = `update cloud_resourses set is_active = false, status = 'Deleted' where account = $1 and lower(service_name) = $2 and (meta->>'nb_source' IS NULL OR meta->>'nb_source' != 'billing')`
			args = []any{accountId, strings.ToLower(serviceName)}
		}
		updatedResources, err := dbms.Exec(archiveResourcesQuery, args...)
		if err != nil {
			ctx.GetLogger().Error("unable to archive resources", "error", err, "service", serviceName)
			return StoreResourcesResponse{
				Count:    0,
				Arns:     []string{},
				Duration: time.Since(t0),
			}, err
		}
		if c, err := updatedResources.RowsAffected(); err == nil {
			ctx.GetLogger().Info("archived resources", "count", c, "service", serviceName, "regions_queried", archiveScopeRegions)
		}

		// Also deactivate billing-sourced resources for these regions.
		// When the API returns zero items, any billing resource in those regions no longer exists in AWS.
		var billingArchiveQuery string
		var billingArgs []any
		if len(archiveScopeRegions) > 0 {
			bPlaceholders := make([]string, len(archiveScopeRegions))
			for i := range archiveScopeRegions {
				bPlaceholders[i] = fmt.Sprintf("$%d", i+3)
			}
			billingArchiveQuery = fmt.Sprintf(`update cloud_resourses set is_active = false, status = 'Deleted' where account = $1 and lower(service_name) = $2 and meta->>'nb_source' = 'billing' and region in (%s)`, strings.Join(bPlaceholders, ", "))
			billingArgs = append([]any{accountId, strings.ToLower(serviceName)}, lo.ToAnySlice(archiveScopeRegions)...)
		} else {
			billingArchiveQuery = `update cloud_resourses set is_active = false, status = 'Deleted' where account = $1 and lower(service_name) = $2 and meta->>'nb_source' = 'billing'`
			billingArgs = []any{accountId, strings.ToLower(serviceName)}
		}
		if billingResult, billingErr := dbms.Exec(billingArchiveQuery, billingArgs...); billingErr != nil {
			ctx.GetLogger().Error("unable to archive billing resources", "error", billingErr, "service", serviceName)
		} else if c, billingErr := billingResult.RowsAffected(); billingErr == nil {
			ctx.GetLogger().Info("archived billing resources (zero-items path)", "count", c, "service", serviceName, "regions_queried", regions)
		}

		return StoreResourcesResponse{
			Count:    0,
			Arns:     []string{},
			Duration: time.Since(t0),
		}, nil
	}

	resourceMap := map[string]map[string]any{}
	t := time.Now().UTC().Format(time.RFC3339)
	var nilString *string
	isAzure := strings.EqualFold(account.CloudProvider, "azure")
	for _, item := range resources.Items {
		// Azure ARM resource IDs are case-insensitive. Normalize before storing
		// so this row collides with the realtime upsert (which also lowercases)
		// instead of creating a duplicate row with different casing.
		if isAzure {
			item.Id = strings.ToLower(item.Id)
			item.Arn = strings.ToLower(item.Arn)
		}
		resourceMapKey := buildExternalResourceId(account.CloudProvider, account.AccountNumber, item.Region, item.ServiceName, item.Type, item.Id, "")
		if _, ok := resourceMap[resourceMapKey]; ok {
			continue
		}
		tagsStr := []byte("{}")
		if len(item.Tags) > 0 {
			tagsStr, err = common.MarshalJson(item.Tags)
			if err != nil {
				ctx.GetLogger().Error("unable to marshal tags", "error", err)
				return StoreResourcesResponse{
					Count:    0,
					Duration: time.Since(t0),
				}, err
			}
		}

		if len(item.Meta) == 0 {
			ctx.GetLogger().Warn("resource has empty meta",
				"resource_id", item.Id, "service_name", item.ServiceName, "name", item.Name)
		}
		if item.Meta == nil {
			item.Meta = map[string]any{}
		}
		item.Meta["nb_source"] = "api"
		metaStr, err := common.MarshalJson(item.Meta)
		if err != nil {
			ctx.GetLogger().Error("unable to marshal meta", "error", err)
			return StoreResourcesResponse{
				Count:    0,
				Duration: time.Since(t0),
			}, err
		}
		resourceDbData := map[string]any{
			"id":                   uuid.New().String(),
			"created_at":           t,
			"created_by":           nilString,
			"updated_at":           t,
			"updated_by":           nilString,
			"resourse_id":          item.Id,
			"name":                 item.Name,
			"type":                 item.Type,
			"status":               lo.Ternary(item.Status != "", item.Status, providers.ResourceStatusActive),
			"resourse_created_on":  item.CreatedAt.UTC(),
			"account":              accountId,
			"cloud_provider":       account.CloudProvider,
			"region":               item.Region,
			"arn":                  item.Arn,
			"tenant":               ctx.GetSecurityContext().GetTenantId(),
			"tags":                 string(tagsStr),
			"meta":                 string(metaStr),
			"service_name":         item.ServiceName,
			"first_seen":           t,
			"last_seen":            t,
			"is_active":            true,
			"external_resource_id": resourceMapKey,
		}

		resourceMap[resourceMapKey] = resourceDbData
	}

	err = storeResourcesInsert(ctx, dbms, accountId, serviceName, resourceMap, regions)
	if err != nil {
		ctx.GetLogger().Error("unable to insert resources", "error", err)
		return StoreResourcesResponse{
			Count:    len(resourceMap),
			Arns:     maps.Keys(resourceMap),
			Duration: time.Since(t0),
		}, err
	}

	return StoreResourcesResponse{
		Count:    len(resourceMap),
		Arns:     maps.Keys(resourceMap),
		Duration: time.Since(t0),
	}, nil
}

func storeResourcesInsert(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId string, serviceName string, resourceMap map[string]map[string]any, regions []string) error {
	if len(resourceMap) == 0 {
		return nil
	}
	storeResourcesMutex.Lock()
	defer storeResourcesMutex.Unlock()

	// Collect all unique regions from the resourceMap to handle region-specific archiving.
	// For services flagged as global (IAM, S3, Route53, CloudFront, WAF, etc.) the API
	// is account-wide, so we deliberately archive without a region filter — region-scoped
	// archival would silently leak rows whose region wasn't in the iterated set
	// (e.g. an S3 bucket whose location moved out of the bootstrap regions list, or
	// "global"-tagged IAM/Route53 rows when the iteration only produced regional names).
	var allRegions []string
	if isGlobalAwsService(serviceName) {
		allRegions = nil
	} else if len(regions) > 0 {
		allRegions = regions
	} else {
		regionSet := make(map[string]struct{})
		for _, item := range resourceMap {
			if region, ok := item["region"].(string); ok && region != "" {
				regionSet[region] = struct{}{}
			}
		}
		allRegions = maps.Keys(regionSet)
	}

	_, err := dbms.DoInTransaction(func(tx common.DatabaseManagerTx) (any, error) {
		// Archive resources for this service and account (and regions if applicable)
		// This marks ALL resources for the service/account/region(s) as deleted
		// The subsequent UPSERT will reactivate only the resources that currently exist in AWS
		var archiveResourcesQuery string
		var archiveArgs []any

		if len(allRegions) > 0 {
			// Region-specific archiving: only archive resources in the regions being synced
			// This prevents marking resources in other regions as deleted
			placeholders := make([]string, len(allRegions))
			for i := range allRegions {
				placeholders[i] = fmt.Sprintf("$%d", i+3) // $3, $4, $5...
			}
			inClause := strings.Join(placeholders, ", ")
			archiveResourcesQuery = fmt.Sprintf(`update cloud_resourses set is_active = false, status = 'Deleted' where account = $1 and lower(service_name) = $2 and (meta->>'nb_source' IS NULL OR meta->>'nb_source' != 'billing') and region in (%s)`, inClause)
			archiveArgs = []any{accountId, strings.ToLower(serviceName)}
			for _, region := range allRegions {
				archiveArgs = append(archiveArgs, region)
			}
		} else {
			// No regions in resourceMap (e.g., global services like IAM, S3)
			// Archive all resources for this service/account
			archiveResourcesQuery = `update cloud_resourses set is_active = false, status = 'Deleted' where account = $1 and lower(service_name) = $2 and (meta->>'nb_source' IS NULL OR meta->>'nb_source' != 'billing')`
			archiveArgs = []any{accountId, strings.ToLower(serviceName)}
		}

		// Execute archiveResourcesQuery inside the transaction
		result, err := tx.Exec(archiveResourcesQuery, archiveArgs...)
		if err != nil {
			return nil, err
		}

		if rowsAffected, err := result.RowsAffected(); err == nil {
			// Note: This log will show the count of resources marked as deleted
			// Some of these will be reactivated by the subsequent UPSERT if they still exist
			ctx.GetLogger().Info("Archived resources before UPSERT",
				"service", serviceName,
				"account", accountId,
				"regions", allRegions,
				"archived_count", rowsAffected)
		}

		batches := lo.Chunk(maps.Values(resourceMap), 100)

		// Determine cloud provider from first resource in map
		var cloudProvider string
		for _, resource := range resourceMap {
			if cp, ok := resource["cloud_provider"].(string); ok {
				cloudProvider = cp
				break
			}
		}

		for _, batch := range batches {
			batchMap := lo.SliceToMap(batch, func(item map[string]any) (string, map[string]any) {
				return item["external_resource_id"].(string), item
			})

			// Choose conflict strategy based on cloud provider
			// Azure: Use (account, external_resource_id) because Azure external_resource_id = resource_id (full path)
			// AWS/GCP: Use 5-column constraint to handle cases where external_resource_id varies (e.g., :data-transfer suffix)
			baseQuery := `INSERT INTO cloud_resourses (id, created_at, created_by, updated_at, updated_by, resourse_id, name, type, status, resourse_created_on, account, cloud_provider, region, arn, tenant, tags, meta, service_name, first_seen, last_seen, is_active, external_resource_id)
											values (:id, :created_at, :created_by, :updated_at, :updated_by, :resourse_id, :name, :type, :status, :resourse_created_on, :account, :cloud_provider, :region, :arn, :tenant, :tags, :meta, :service_name, :first_seen, :last_seen, :is_active, :external_resource_id)`
			var conflictClause string
			if strings.EqualFold(cloudProvider, "azure") {
				conflictClause = `
								 on conflict (account, external_resource_id)
								 	do update set
												last_seen = EXCLUDED.last_seen,
												status = EXCLUDED.status,
												meta = EXCLUDED.meta,
												is_active = EXCLUDED.is_active,
												tags = EXCLUDED.tags,
												arn = EXCLUDED.arn,
												resourse_created_on = EXCLUDED.resourse_created_on,
												resourse_id = EXCLUDED.resourse_id,
												name = EXCLUDED.name,
												type = EXCLUDED.type,
												region = EXCLUDED.region,
												service_name = EXCLUDED.service_name`
			} else {
				// AWS, GCP, and others use 5-column constraint
				conflictClause = `
								 on conflict (account, resourse_id, type, region, service_name)
								 	do update set
												last_seen = EXCLUDED.last_seen,
												status = EXCLUDED.status,
												meta = EXCLUDED.meta,
												is_active = EXCLUDED.is_active,
												tags = EXCLUDED.tags,
												arn = EXCLUDED.arn,
												resourse_created_on = EXCLUDED.resourse_created_on,
												name = EXCLUDED.name,
												external_resource_id = EXCLUDED.external_resource_id`
			}
			query := baseQuery + conflictClause

			_, err = tx.NamedExec(query, maps.Values(batchMap))
			if err != nil {
				return nil, err
			}
		}

		// Reconcile billing-sourced resources: API discovery is source of truth.
		// Deactivate billing resources for this service/account/regions that were NOT returned by the API.
		fetchedIDs := make([]string, 0, len(resourceMap))
		for _, item := range resourceMap {
			if id, ok := item["resourse_id"].(string); ok && id != "" {
				fetchedIDs = append(fetchedIDs, id)
			}
		}
		if len(fetchedIDs) > 0 {
			var billingReconcileQuery string
			var billingReconcileArgs []any
			if len(allRegions) > 0 {
				regionPlaceholders := make([]string, len(allRegions))
				for i := range allRegions {
					regionPlaceholders[i] = fmt.Sprintf("$%d", i+3)
				}
				idPlaceholders := make([]string, len(fetchedIDs))
				for i := range fetchedIDs {
					idPlaceholders[i] = fmt.Sprintf("$%d", len(allRegions)+3+i)
				}
				billingReconcileQuery = fmt.Sprintf(
					`update cloud_resourses set is_active = false, status = 'Deleted' where account = $1 and lower(service_name) = $2 and status != 'Deleted' and meta->>'nb_source' = 'billing' and region in (%s) and resourse_id not in (%s)`,
					strings.Join(regionPlaceholders, ", "),
					strings.Join(idPlaceholders, ", "),
				)
				billingReconcileArgs = []any{accountId, strings.ToLower(serviceName)}
				for _, r := range allRegions {
					billingReconcileArgs = append(billingReconcileArgs, r)
				}
				for _, id := range fetchedIDs {
					billingReconcileArgs = append(billingReconcileArgs, id)
				}
			} else {
				idPlaceholders := make([]string, len(fetchedIDs))
				for i := range fetchedIDs {
					idPlaceholders[i] = fmt.Sprintf("$%d", i+3)
				}
				billingReconcileQuery = fmt.Sprintf(
					`update cloud_resourses set is_active = false, status = 'Deleted' where account = $1 and lower(service_name) = $2 and status != 'Deleted' and meta->>'nb_source' = 'billing' and resourse_id not in (%s)`,
					strings.Join(idPlaceholders, ", "),
				)
				billingReconcileArgs = []any{accountId, strings.ToLower(serviceName)}
				for _, id := range fetchedIDs {
					billingReconcileArgs = append(billingReconcileArgs, id)
				}
			}
			if result, billingErr := tx.Exec(billingReconcileQuery, billingReconcileArgs...); billingErr != nil {
				ctx.GetLogger().Error("billing reconciliation failed", "error", billingErr, "service", serviceName)
			} else if c, billingErr := result.RowsAffected(); billingErr == nil && c > 0 {
				ctx.GetLogger().Info("deactivated stale billing resources", "count", c, "service", serviceName, "regions", allRegions)
			} else {
				ctx.GetLogger().Debug("billing reconciliation: no stale resources found", "service", serviceName, "regions", allRegions, "fetchedIDs_count", len(fetchedIDs))
			}
		}

		return nil, nil
	})

	if err != nil {
		return err
	}

	return nil
}

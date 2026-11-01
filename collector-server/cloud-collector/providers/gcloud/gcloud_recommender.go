package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"regexp"
	"sort"
	"strings"
	"sync"

	recommender "cloud.google.com/go/recommender/apiv1"
	recommenderpb "cloud.google.com/go/recommender/apiv1/recommenderpb"
	"golang.org/x/sync/semaphore"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const ServiceNameRecommender = "Recommender"

// recommenderConcurrency bounds parallel (recommender, location) fetches, the
// same way the AWS resource crawl bounds its per-region fan-out (providers/aws
// main.go). Each fetch is an independent blocking round-trip; iterating them
// sequentially serialized into tens of seconds once every recommender was
// queried across the account's real locations.
const recommenderConcurrency = 8

// locationScope is a bitmask describing which location classes a recommender's
// recommendations live under. GCP scopes each recommender to the location of
// the resource it describes: VM/disk recommenders live under zones
// (us-central1-a), Cloud SQL under regions, IAM/image under "global". Querying
// the wrong class returns nothing, so each recommenderTypes entry declares its
// verified scope (per that recommender's page under
// https://cloud.google.com/recommender/docs/recommenders).
type locationScope uint8

const (
	scopeGlobal locationScope = 1 << iota
	scopeRegional
	scopeZonal
)

// recommenderTypes lists the GCP recommender IDs to query.
// See https://cloud.google.com/recommender/docs/recommenders
var recommenderTypes = []struct {
	id       string
	category providers.RecommendationCategory
	scope    locationScope
}{
	// Cost
	{"google.compute.instance.IdleResourceRecommender", providers.RecommendationCategoryRightSizing, scopeZonal},
	{"google.compute.instance.MachineTypeRecommender", providers.RecommendationCategoryRightSizing, scopeZonal},
	{"google.compute.disk.IdleResourceRecommender", providers.RecommendationCategoryRightSizing, scopeZonal},
	// Regional external IPs live under regions, global IPs under "global".
	{"google.compute.address.IdleResourceRecommender", providers.RecommendationCategoryRightSizing, scopeRegional | scopeGlobal},
	{"google.compute.image.IdleResourceRecommender", providers.RecommendationCategoryRightSizing, scopeGlobal},
	{"google.cloudsql.instance.IdleRecommender", providers.RecommendationCategoryRightSizing, scopeRegional},
	{"google.cloudsql.instance.OverprovisionedRecommender", providers.RecommendationCategoryRightSizing, scopeRegional},

	// Security
	{"google.iam.policy.Recommender", providers.RecommendationCategorySecurity, scopeGlobal},
	{"google.cloudsql.instance.SecurityRecommender", providers.RecommendationCategorySecurity, scopeRegional},

	// Performance / Reliability
	// GKE recommendations live under the cluster's location: a region for
	// regional clusters, a zone for zonal ones — so both classes are queried.
	{"google.container.DiagnosisRecommender", providers.RecommendationCategoryConfiguration, scopeRegional | scopeZonal},
	{"google.cloudsql.instance.UnderprovisionedRecommender", providers.RecommendationCategoryConfiguration, scopeRegional},

	// BigQuery — recommendations live under the dataset location, which the
	// collected BigQuery resources carry in Region (including multi-regions
	// like "us"), so the derived region set covers them.
	{"google.bigquery.table.PartitionClusterRecommender", providers.RecommendationCategoryRightSizing, scopeRegional},

	// --- New GCP recommender types ---

	// Cost
	{"google.run.service.CostRecommender", providers.RecommendationCategoryRightSizing, scopeRegional},
	{"google.storage.bucket.SoftDeleteRecommender", providers.RecommendationCategoryRightSizing, scopeRegional},

	// Security
	{"google.run.service.IdentityRecommender", providers.RecommendationCategorySecurity, scopeRegional},
	{"google.run.service.SecurityRecommender", providers.RecommendationCategorySecurity, scopeRegional},
	{"google.resourcemanager.projectUtilization.Recommender", providers.RecommendationCategorySecurity, scopeGlobal},
	{"google.firestore.database.FirebaseRulesRecommender", providers.RecommendationCategorySecurity, scopeRegional},

	// Performance — folds into Configuration, matching the existing
	// PERFORMANCE->Configuration impact mapping.
	{"google.storage.bucket.AnywhereCacheRecommender", providers.RecommendationCategoryConfiguration, scopeRegional},
	{"google.compute.instanceGroupManager.MachineTypeRecommender", providers.RecommendationCategoryConfiguration, scopeRegional | scopeZonal},
	{"google.cloudfunctions.PerformanceRecommender", providers.RecommendationCategoryConfiguration, scopeRegional},
	{"google.cloudsql.instance.PerformanceRecommender", providers.RecommendationCategoryConfiguration, scopeRegional},

	// Reliability — no dedicated category, folded into Configuration (#35141).
	{"google.resourcemanager.serviceLimit.Recommender", providers.RecommendationCategoryConfiguration, scopeGlobal},
	{"google.cloudsql.instance.OutOfDiskRecommender", providers.RecommendationCategoryConfiguration, scopeRegional},
	{"google.cloudsql.instance.ReliabilityRecommender", providers.RecommendationCategoryConfiguration, scopeRegional},
	{"google.firestore.database.ReliabilityRecommender", providers.RecommendationCategoryConfiguration, scopeRegional},
	{"google.compute.ReliabilityRiskRecommender", providers.RecommendationCategoryConfiguration, scopeGlobal},

	// Maintenance (Manageability) — folded into Configuration (#35141).
	{"google.clouderrorreporting.Recommender", providers.RecommendationCategoryConfiguration, scopeGlobal},
	{"google.logging.productSuggestion.ContainerRecommender", providers.RecommendationCategoryConfiguration, scopeGlobal},

	// Deliberately deferred — chatty on active projects; review the noise against
	// a real account before enabling (#35141 Drawbacks):
	//   google.iam.policy.ChangeRiskRecommender
	//   google.iam.serviceAccount.ChangeRiskRecommender
	//   google.resourcemanager.project.ChangeRiskRecommender
	//   google.cloud.RecentChangeRecommender
	// Not included: idle/underutilized reservations and advisory notifications
	// (no distinct recommender id), and deprecation notices
	// (google.cloud.deprecation.GeneralRecommender is rejected by the live
	// Recommender API with INVALID_ARGUMENT — the correct id is unconfirmed, so
	// it is left out until verified). Commitment recommenders are excluded by the
	// ticket as separate work.
}

// fallbackRecommenderRegions is used only when the account has no collected
// resources yet (e.g. the first sync ordering) so region-scoped recommenders
// still get queried somewhere. It mirrors the historical hardcoded list.
var fallbackRecommenderRegions = []string{
	"us-central1",
	"us-east1",
	"us-west1",
	"europe-west1",
	"asia-east1",
	"asia-southeast1",
}

// zoneFromLink extracts the zone segment from resource selfLinks/ARNs, which
// carry it in both URL ("https://.../zones/us-central1-a/instances/x") and
// path ("projects/p/zones/us-central1-a/instances/x") forms.
var zoneFromLink = regexp.MustCompile(`/zones/([a-z0-9-]+)`)

// resolveRecommenderLocations derives the regions and zones the account
// actually uses from its already-collected resources. Regions come from the
// Region field; zones only survive collection inside selfLink/ARN (collectors
// normalize Region to the enclosing region), so they are parsed back out.
// Recommendations always attach to a resource, so a location with no
// collected resources cannot yield one — skipping it loses nothing.
func resolveRecommenderLocations(resources []providers.Resource) (regions, zones []string) {
	regionSet := map[string]struct{}{}
	zoneSet := map[string]struct{}{}

	for _, r := range resources {
		// Global resources (IAM, images, …) store "global" in Region; that
		// location class is owned by scopeGlobal, not the derived region set.
		if region := strings.ToLower(strings.TrimSpace(r.Region)); region != "" && region != "global" {
			regionSet[region] = struct{}{}
		}
		if m := zoneFromLink.FindStringSubmatch(r.Arn); m != nil {
			zoneSet[m[1]] = struct{}{}
		}
		if m := zoneFromLink.FindStringSubmatch(stringMeta(r.Meta, "selfLink")); m != nil {
			zoneSet[m[1]] = struct{}{}
		}
	}

	for region := range regionSet {
		regions = append(regions, region)
	}
	for zone := range zoneSet {
		zones = append(zones, zone)
	}
	sort.Strings(regions)
	sort.Strings(zones)
	return regions, zones
}

func stringMeta(meta map[string]interface{}, key string) string {
	if meta == nil {
		return ""
	}
	s, _ := meta[key].(string)
	return s
}

// normalizeGCPResourcePath reduces the shapes a GCP resource reference takes —
// https selfLink ("https://www.googleapis.com/compute/v1/projects/p/zones/z/instances/x"),
// recommendation operation resource ("//compute.googleapis.com/projects/p/..."),
// or bare path — to the shared "projects/..." suffix, lowercased, so references
// from different APIs land on the same index key. The project segment is
// canonicalized to "-" because the APIs disagree on it for the same resource
// (selfLinks carry the project ID, Recommender targets the project number) and
// the index is built per account, so it carries no information here.
func normalizeGCPResourcePath(ref string) string {
	idx := strings.Index(ref, "projects/")
	if idx < 0 {
		return ""
	}
	path := strings.ToLower(ref[idx:])
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 3 {
		return path
	}
	return "projects/-/" + parts[2]
}

// overviewTargetClusterURIs pulls clusterUri values out of the recommendation's
// content overview. GKE DiagnosisRecommender recommendations carry no operation
// groups at all — their targets live in the overview (and in the top-level
// target_resources field, which recommender v1.18.0's proto does not expose yet).
func overviewTargetClusterURIs(rec *recommenderpb.Recommendation) []string {
	overview := rec.GetContent().GetOverview()
	if overview == nil {
		return nil
	}
	var uris []string
	for _, tc := range overview.GetFields()["targetClusters"].GetListValue().GetValues() {
		if uri := tc.GetStructValue().GetFields()["clusterUri"].GetStringValue(); uri != "" {
			uris = append(uris, uri)
		}
	}
	return uris
}

// buildRecommenderResourceIndex maps every normalized reference the collectors
// store for a resource (Arn, Meta selfLink) to that resource, so a
// recommendation's operation target can be resolved to the collected row it is
// about. First writer wins on a key collision, keeping resolution deterministic.
func buildRecommenderResourceIndex(resources []providers.Resource) map[string]providers.Resource {
	index := make(map[string]providers.Resource, len(resources)*2)
	for _, r := range resources {
		for _, ref := range []string{r.Arn, stringMeta(r.Meta, "selfLink")} {
			if key := normalizeGCPResourcePath(ref); key != "" {
				if _, exists := index[key]; !exists {
					index[key] = r
				}
			}
		}
	}
	return index
}

// resolveRecommenderTarget finds the collected resource a recommendation acts
// on. Every operation is tried in order and the first whose resource resolves
// against the index wins — "first operation with a resource" alone would be
// wrong, e.g. an idle-disk recommendation leads with a create-snapshot
// operation whose snapshot does not exist yet; resolve-first skips phantoms.
func resolveRecommenderTarget(rec *recommenderpb.Recommendation, index map[string]providers.Resource) (providers.Resource, bool) {
	for _, group := range rec.GetContent().GetOperationGroups() {
		for _, op := range group.GetOperations() {
			if r, ok := lookupGCPResource(index, op.GetResource()); ok {
				return r, true
			}
		}
	}
	for _, uri := range overviewTargetClusterURIs(rec) {
		if r, ok := lookupGCPResource(index, uri); ok {
			return r, true
		}
	}
	return providers.Resource{}, false
}

func lookupGCPResource(index map[string]providers.Resource, ref string) (providers.Resource, bool) {
	key := normalizeGCPResourcePath(ref)
	if key == "" {
		return providers.Resource{}, false
	}
	r, ok := index[key]
	return r, ok
}

// linkRecommenderTarget stamps ExternalResourceId from the resolved target's
// own stored fields — the identical construction etl_resources uses when
// writing cloud_resourses — so the ingestion's preferred linking path
// populates resource_id and the UI shows the target's name (#39727). The
// construction cannot be done from the recommendation alone: compute instances
// are stored under their numeric ID, which the recommendation payload never
// carries. Mirrors aws_compute_optimizer. ResourceId/account_object_id stay
// untouched: the upsert conflict key and the alarm creator depend on them.
func linkRecommenderTarget(mapped providers.Recommendation, rec *recommenderpb.Recommendation, account providers.Account, index map[string]providers.Resource) providers.Recommendation {
	target, ok := resolveRecommenderTarget(rec, index)
	if !ok {
		return mapped
	}
	mapped.ExternalResourceId = common.BuildExternalResourceId(account.CloudProvider, account.AccountNumber, target.Region, target.ServiceName, target.Type, target.Id, "")
	if mapped.Data != nil {
		mapped.Data["target_resource_name"] = target.Name
	}
	return mapped
}

// locationsForScope expands a recommender's scope bitmask into the concrete
// locations to query.
func locationsForScope(scope locationScope, regions, zones []string) []string {
	var locations []string
	if scope&scopeGlobal != 0 {
		locations = append(locations, "global")
	}
	if scope&scopeRegional != 0 {
		locations = append(locations, regions...)
	}
	if scope&scopeZonal != 0 {
		locations = append(locations, zones...)
	}
	return locations
}

type gcloudRecommenderService struct{}

func (s *gcloudRecommenderService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return providers.QueryMetricsResponse{}, nil
}

func (s *gcloudRecommenderService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	return nil, nil
}

func (s *gcloudRecommenderService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		ctx.GetLogger().Error("failed to get gcloud session for recommender", "error", err)
		return nil, err
	}

	// Determine project ID: prefer session credentials, fall back to account number
	projectId := session.ProjectId
	if projectId == "" {
		projectId = account.AccountNumber
	}

	// Add quota project for proper billing attribution
	opts := append(session.Opts, option.WithQuotaProject(projectId))

	client, err := recommender.NewClient(ctx.GetContext(), opts...)
	if err != nil {
		ctx.GetLogger().Error("failed to create recommender client", "error", err)
		return nil, err
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close recommender client", "error", cerr)
		}
	}()

	regions, zones := resolveRecommenderLocations(existingResources)
	if len(regions) == 0 {
		regions = fallbackRecommenderRegions
	}
	ctx.GetLogger().Info("resolved recommender locations", "regions", regions, "zones", zones)

	// Read-only after this point, so safe to share across the fetch goroutines.
	resourceIndex := buildRecommenderResourceIndex(existingResources)

	// Flatten each recommender into its scoped (recommender, location) fetches,
	// then run them in parallel. The recommender client is safe for concurrent
	// use, and a bounded semaphore keeps the fan-out from opening an unbounded
	// number of connections on accounts spread across many regions.
	type recWork struct {
		id       string
		category providers.RecommendationCategory
		location string
	}
	var work []recWork
	for _, rt := range recommenderTypes {
		for _, location := range locationsForScope(rt.scope, regions, zones) {
			work = append(work, recWork{id: rt.id, category: rt.category, location: location})
		}
	}

	var (
		recommendations []providers.Recommendation
		mu              sync.Mutex
		wg              sync.WaitGroup
	)
	sem := semaphore.NewWeighted(recommenderConcurrency)

	for _, w := range work {
		if err := sem.Acquire(ctx.GetContext(), 1); err != nil {
			// Context cancelled/timed out — stop scheduling and let in-flight
			// fetches drain via wg.Wait below.
			ctx.GetLogger().Warn("failed to acquire recommender semaphore", "error", err)
			break
		}
		wg.Add(1)
		go func(w recWork) {
			defer wg.Done()
			defer sem.Release(1)

			parent := fmt.Sprintf("projects/%s/locations/%s/recommenders/%s", projectId, w.location, w.id)
			recs, err := fetchRecommendations(ctx, client, parent, w.id, w.category, w.location, account, resourceIndex)
			if err != nil {
				// Permission denied or recommender not available in this location — skip
				if isGCPPermissionOrNotFoundError(err) {
					RecordGCPPermissionError(ctx, err)
					return
				}
				ctx.GetLogger().Warn("failed to list recommendations", "recommender", w.id, "location", w.location, "error", err)
				return
			}
			mu.Lock()
			recommendations = append(recommendations, recs...)
			mu.Unlock()
		}(w)
	}
	wg.Wait()

	ctx.GetLogger().Info("fetched GCP recommender recommendations", "count", len(recommendations))
	return recommendations, nil
}

func fetchRecommendations(ctx providers.CloudProviderContext, client *recommender.Client, parent, recommenderID string, category providers.RecommendationCategory, location string, account providers.Account, resourceIndex map[string]providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation

	req := &recommenderpb.ListRecommendationsRequest{
		Parent:   parent,
		PageSize: 100,
	}

	it := client.ListRecommendations(ctx.GetContext(), req)
	for {
		rec, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		// Only process ACTIVE recommendations
		if rec.StateInfo != nil && rec.StateInfo.State != recommenderpb.RecommendationStateInfo_ACTIVE {
			continue
		}

		mapped := mapGCPRecommendation(rec, recommenderID, category, location)
		mapped = linkRecommenderTarget(mapped, rec, account, resourceIndex)
		recommendations = append(recommendations, mapped)
	}

	return recommendations, nil
}

func mapGCPRecommendation(rec *recommenderpb.Recommendation, recommenderID string, category providers.RecommendationCategory, location string) providers.Recommendation {
	severity := mapGCPPriorityToSeverity(rec.Priority)
	savings := extractGCPSavings(rec)

	// Override category from impact if available
	if rec.PrimaryImpact != nil {
		category = mapGCPImpactCategory(rec.PrimaryImpact.Category, category)
	}

	// If there are cost savings, override severity based on amount
	if savings > 0 {
		monthlySavings := savings
		severity = mapGCPSavingsToSeverity(monthlySavings)
	}

	ruleName := fmt.Sprintf("gcp_native_%s", sanitizeRecommenderID(recommenderID))

	data := map[string]any{
		"source":              "gcp",
		"recommender_id":      recommenderID,
		"recommendation_name": rec.Name,
		"description":         rec.Description,
		"subtype":             rec.RecommenderSubtype,
		"priority":            rec.Priority.String(),
	}

	if rec.PrimaryImpact != nil {
		data["impact_category"] = rec.PrimaryImpact.Category.String()
		if costProj := rec.PrimaryImpact.GetCostProjection(); costProj != nil {
			if costProj.Cost != nil {
				// Cost is negative for savings
				monthlyCost := float64(costProj.Cost.Units) + float64(costProj.Cost.Nanos)/1e9
				data["cost_projection_monthly"] = monthlyCost
				data["currency_code"] = costProj.Cost.CurrencyCode
			}
			if costProj.CostInLocalCurrency != nil {
				localCost := float64(costProj.CostInLocalCurrency.Units) + float64(costProj.CostInLocalCurrency.Nanos)/1e9
				data["cost_in_local_currency"] = localCost
			}
		}
	}

	if savings > 0 {
		data["estimated_monthly_savings"] = savings
	}

	if rec.StateInfo != nil {
		data["state"] = rec.StateInfo.State.String()
	}

	// Extract resource ID from recommendation name
	// Format: projects/{project}/locations/{location}/recommenders/{recommender}/recommendations/{id}
	resourceId := extractRecommendationID(rec.Name)
	resourceType := recommenderID

	return providers.Recommendation{
		CategoryName:        category,
		RuleName:            ruleName,
		Severity:            severity,
		Savings:             savings,
		Action:              mapGCPRecommenderAction(recommenderID),
		Data:                data,
		ResourceServiceName: ServiceNameRecommender,
		ResourceId:          resourceId,
		ResourceType:        resourceType,
		ResourceRegion:      location,
	}
}

func mapGCPPriorityToSeverity(priority recommenderpb.Recommendation_Priority) providers.RecommendationSeverity {
	switch priority {
	case recommenderpb.Recommendation_P1:
		return providers.RecommendationSeverityHigh
	case recommenderpb.Recommendation_P2:
		return providers.RecommendationSeverityHigh
	case recommenderpb.Recommendation_P3:
		return providers.RecommendationSeverityMedium
	case recommenderpb.Recommendation_P4:
		return providers.RecommendationSeverityLow
	default:
		return providers.RecommendationSeverityMedium
	}
}

func mapGCPSavingsToSeverity(monthlySavings float64) providers.RecommendationSeverity {
	switch {
	case monthlySavings >= 100:
		return providers.RecommendationSeverityHigh
	case monthlySavings >= 20:
		return providers.RecommendationSeverityMedium
	default:
		return providers.RecommendationSeverityLow
	}
}

func mapGCPImpactCategory(impact recommenderpb.Impact_Category, fallback providers.RecommendationCategory) providers.RecommendationCategory {
	switch impact {
	case recommenderpb.Impact_COST:
		return providers.RecommendationCategoryRightSizing
	case recommenderpb.Impact_SECURITY:
		return providers.RecommendationCategorySecurity
	case recommenderpb.Impact_PERFORMANCE:
		return providers.RecommendationCategoryConfiguration
	case recommenderpb.Impact_RELIABILITY:
		return providers.RecommendationCategoryConfiguration
	default:
		return fallback
	}
}

func mapGCPRecommenderAction(recommenderID string) providers.RecommendationAction {
	if strings.Contains(recommenderID, "Idle") || strings.Contains(recommenderID, "Shutdown") {
		return providers.RecommendationActionDelete
	}
	return providers.RecommendationActionModify
}

func extractGCPSavings(rec *recommenderpb.Recommendation) float64 {
	if rec.PrimaryImpact == nil {
		return 0
	}
	costProj := rec.PrimaryImpact.GetCostProjection()
	if costProj == nil || costProj.Cost == nil {
		return 0
	}

	// Cost is negative for savings (money saved), positive for cost increase
	totalCost := float64(costProj.Cost.Units) + float64(costProj.Cost.Nanos)/1e9

	// Duration is typically 2592000s (30 days). Normalize to monthly.
	if totalCost < 0 {
		// Negative cost = savings. Return as positive monthly savings.
		durationSecs := float64(0)
		if costProj.Duration != nil {
			durationSecs = float64(costProj.Duration.Seconds)
		}
		if durationSecs > 0 {
			// Normalize to 30-day month
			return -totalCost * (30 * 24 * 3600) / durationSecs
		}
		return -totalCost
	}
	return 0
}

// sanitizeRecommenderID converts "google.compute.instance.IdleResourceRecommender" to "compute_instance_idle_resource"
func sanitizeRecommenderID(id string) string {
	// Remove "google." prefix and "Recommender" suffix
	id = strings.TrimPrefix(id, "google.")
	id = strings.TrimSuffix(id, "Recommender")
	id = strings.TrimRight(id, ".")
	// Replace dots with underscores
	id = strings.ReplaceAll(id, ".", "_")
	// Convert camelCase to snake_case
	result := strings.Builder{}
	for i, r := range id {
		if r >= 'A' && r <= 'Z' {
			if i > 0 && id[i-1] != '_' {
				result.WriteByte('_')
			}
			result.WriteRune(r + 32) // toLower
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// extractRecommendationID extracts the recommendation UUID from the full resource name.
func extractRecommendationID(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return name
}

func isGCPPermissionOrNotFoundError(err error) bool {
	errStr := err.Error()
	return strings.Contains(errStr, "PermissionDenied") ||
		strings.Contains(errStr, "PERMISSION_DENIED") ||
		strings.Contains(errStr, "NotFound") ||
		strings.Contains(errStr, "NOT_FOUND") ||
		strings.Contains(errStr, "InvalidArgument") ||
		strings.Contains(errStr, "INVALID_ARGUMENT") ||
		strings.Contains(errStr, "403") ||
		strings.Contains(errStr, "404") ||
		strings.Contains(errStr, "has not enabled") ||
		strings.Contains(errStr, "has not been used in project") ||
		strings.Contains(errStr, "is disabled")
}

func (s *gcloudRecommenderService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return nil
}

func (s *gcloudRecommenderService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, nil
}

func (s *gcloudRecommenderService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}

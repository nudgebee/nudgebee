package recommendation

import (
	"fmt"
	"log/slog"
	"nudgebee/services/account"
	"nudgebee/services/account/adapter"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/internal/annotations"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/ml"
	"nudgebee/services/notification"
	"nudgebee/services/observability"
	"nudgebee/services/scan_orchestrator"
	"nudgebee/services/security"
	"slices"
	"strings"
	"time"

	"github.com/samber/lo"
)

func GetRecommendation(context *security.RequestContext, id string) (models.Recommendation, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return models.Recommendation{}, err
	}
	r := databaseManager.Db.QueryRowx(`SELECT id, created_at, updated_at, tenant_id, cloud_account_id, resource_id,
			recommendation, recommendation_action, severity, estimated_savings, status, category,
			rule_name, account_object_id, note, dismissed_reason, is_dismissed, updated_by,
			finops_score, finops_band, finops_score_breakdown, last_nudged_at, dedupe_group
		FROM recommendation WHERE id = $1`, id)
	if r.Err() != nil {
		return models.Recommendation{}, r.Err()
	}
	recommendation := models.Recommendation{}
	err = r.StructScan(&recommendation)
	return recommendation, err
}

func ListRecommendationResolutions(context *security.RequestContext, rescommendationId string, resolutionType string, resolverType models.RecommendationResolutionResolverType, resolverId string) ([]models.RecommendationResolution, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return []models.RecommendationResolution{}, err
	}

	// Check for InProgress resolutions for THIS specific resolver
	// Also exclude any stuck > 2 hours (safety mechanism to prevent duplicate creation)
	r, err := databaseManager.Db.Queryx(`
		SELECT id, created_at, updated_at, recommendation_id, type, data, status, type_reference_id,
			resolver_type, resolver_id, status_message, pr_iteration_count, pr_lifecycle_state, last_pr_check_at
		FROM recommendation_resolution
		WHERE recommendation_id = $1
		AND type = $2
		AND resolver_type = $3
		AND resolver_id = $4
		AND status = 'InProgress'
		AND created_at > NOW() - INTERVAL '2 hours'
		ORDER BY created_at DESC
	`, rescommendationId, resolutionType, resolverType, resolverId)
	if err != nil {
		return []models.RecommendationResolution{}, err
	}

	resolutions := []models.RecommendationResolution{}
	for r.Next() {
		resolution := models.RecommendationResolution{}
		err = r.StructScan(&resolution)
		if err != nil {
			return []models.RecommendationResolution{}, err
		}
		resolutions = append(resolutions, resolution)
	}
	return resolutions, nil
}

func hasInProgressRecommendation(existingRecommendations []models.RecommendationResolution) (bool, string) {
	recommendationResolutionId := common.GenerateUUID()
	for _, resolution := range existingRecommendations {
		if resolution.Status == models.RecommendationResolutionStatusInProgress {
			return true, resolution.Id
		}
	}
	return false, recommendationResolutionId
}

func ApplyRecommendation(ctx *security.RequestContext, query RecommendationApplyRequest) (RecommendationApplyResponse, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(query.AccountId, security.SecurityAccessTypeCreate) {
		return RecommendationApplyResponse{}, common.ErrorUnauthorized("error: account access not found")
	}
	if query.ProviderConfig == nil {
		query.ProviderConfig = map[string]any{}
	}
	query.ProviderConfig["recommendation_source"] = "recommendation"

	r, err := GetRecommendation(ctx, query.RecommendationId)
	if err != nil {
		ctx.GetLogger().Error("error getting recommendation", "error", err)
		return RecommendationApplyResponse{}, err
	}
	if r.Id == "" {
		return RecommendationApplyResponse{}, fmt.Errorf("recommendation: recommendation not found - %s", query.RecommendationId)
	}

	a, err := account.GetAccount(ctx, query.AccountId)
	if err != nil {
		ctx.GetLogger().Error("error getting account", "error", err)
		return RecommendationApplyResponse{}, err
	}
	if a.Id == "" {
		return RecommendationApplyResponse{}, fmt.Errorf("recommendation: account not found - %s", query.AccountId)
	}
	if a.AccountAccess != nil && *a.AccountAccess == "readonly" {
		return RecommendationApplyResponse{}, fmt.Errorf("recommendation: cannot apply recommendation on read-only account %s", query.AccountId)
	}

	var cr models.Resource
	if r.ResourceId != nil {
		cr, err = account.GetResource(ctx, *r.ResourceId)
		if err != nil {
			ctx.GetLogger().Error("error getting resource", "error", err)
			return RecommendationApplyResponse{}, err
		}
	} else {
		cr = models.Resource{}
	}

	// Preflight: if the recommendation references a resource that has been
	// soft-deleted (or vanished entirely) since the recommendation was generated,
	// fail fast with a friendly message and archive the rec instead of letting
	// the cloud SDK return a raw 404 (e.g. ResourceGroupNotFound).
	if r.ResourceId != nil && (cr.Id == "" || strings.ToLower(cr.Status) != "active") {
		ctx.GetLogger().Info("recommendation target resource no longer active, archiving",
			"recommendation_id", r.Id,
			"resource_id", *r.ResourceId,
			"resource_status", cr.Status)
		dbms, dbErr := database.GetDatabaseManager(database.Metastore)
		if dbErr == nil {
			now := time.Now().Format(time.RFC3339)
			userId := ctx.GetSecurityContext().GetUserId()
			var archiveErr error
			if userId != "" {
				_, archiveErr = dbms.Db.Exec(
					"UPDATE recommendation SET status = $1, updated_at = $2, updated_by = $3 WHERE id = $4",
					models.RecommendationStatusArchive, now, userId, r.Id,
				)
			} else {
				_, archiveErr = dbms.Db.Exec(
					"UPDATE recommendation SET status = $1, updated_at = $2 WHERE id = $3",
					models.RecommendationStatusArchive, now, r.Id,
				)
			}
			if archiveErr != nil {
				ctx.GetLogger().Warn("failed to archive stale recommendation", "error", archiveErr, "recommendation_id", r.Id)
			}
		}
		return RecommendationApplyResponse{}, fmt.Errorf("recommendation: target resource no longer exists in this account; the recommendation has been archived. Refresh to see updated recommendations")
	}

	// Determine the provider based on query.Provider or account type
	var providerName string
	if query.Provider == "git" {
		// "git" means: raise a PR, auto-detect whether GitHub or GitLab from annotation
		ctx.GetLogger().Info("DEBUG: provider=git, attempting auto-detection",
			"resource_id", cr.Id,
			"cloud_account_id", r.CloudAccountId,
			"recommendation_id", r.Id)
		if cr.Id == "" {
			return RecommendationApplyResponse{}, fmt.Errorf("recommendation: resource not found, cannot detect git provider")
		}
		gitRepoURL, gitErr := adapter.GetGitRepoURLFromWorkload(ctx, cr, r.CloudAccountId)
		ctx.GetLogger().Info("DEBUG: GetGitRepoURLFromWorkload result",
			"git_repo_url", gitRepoURL,
			"error", gitErr,
			"recommendation_id", r.Id)
		if gitErr != nil {
			ctx.GetLogger().Error("error getting git repo URL from workload", "error", gitErr)
			return RecommendationApplyResponse{}, fmt.Errorf("recommendation: failed to get git repo annotation - %w", gitErr)
		}
		if gitRepoURL == "" {
			return RecommendationApplyResponse{}, fmt.Errorf("recommendation: %s annotation not found on workload", annotations.CIGitRepo)
		}
		detected := adapter.DetectGitProviderFromURL(gitRepoURL)
		ctx.GetLogger().Info("DEBUG: provider detection result",
			"detected_provider", detected,
			"git_repo_url", gitRepoURL,
			"recommendation_id", r.Id)
		if detected == "" {
			return RecommendationApplyResponse{}, fmt.Errorf("recommendation: unable to detect git provider from URL - %s", gitRepoURL)
		}
		providerName = detected
		ctx.GetLogger().Info("detected git provider from annotation", "provider", detected, "repo_url", gitRepoURL)
	} else if query.Provider != "" {
		providerName = query.Provider
	} else {
		// Map account type to provider name
		switch a.AccountType {
		case "kubernetes":
			providerName = "kubernetes"
		case "aws":
			providerName = "aws"
		case "azure":
			providerName = "azure"
		case "gcp":
			providerName = "gcp"
		case "cloud":
			// For generic "cloud" account type, use cloud_provider field
			switch a.CloudProvider {
			case "AWS":
				providerName = "aws"
			case "Azure":
				providerName = "azure"
			case "GCP":
				providerName = "gcp"
			default:
				return RecommendationApplyResponse{}, fmt.Errorf("recommendation: cloud provider not supported - %s", a.CloudProvider)
			}
		default:
			return RecommendationApplyResponse{}, fmt.Errorf("recommendation: account type not supported - %s", a.AccountType)
		}
	}

	recommendationRequest := adapter.ApplyRecommendationRequest{
		Data:           query.Data.(map[string]any),
		Recommendation: r,
		Resource:       cr,
		ProviderConfig: query.ProviderConfig,
	}

	// Determine resolution type based on provider and recommendation category
	resolutionType := models.RecommendationResolutionTypePullRequest
	switch providerName {
	case "kubernetes":
		switch recommendationRequest.Recommendation.Category {
		case "RightSizing":
			resolutionType = models.RecommendationResolutionTypeDeploymentChange
		case "EventResolution":
			resolutionType = models.RecommendationEventResolutionType
		}
	case "aws", "azure", "gcp":
		// For cloud providers, use CloudResource for all recommendation types
		resolutionType = models.RecommendationResolutionTypeCloudResource
	}

	// Set resolver defaults before checking for existing resolutions
	if query.ResolverType == "" {
		query.ResolverType = models.RecommendationResolutionResolverTypeUser
	}
	if query.ResolverId == "" {
		query.ResolverId = ctx.GetSecurityContext().GetUserId()
	}

	ctx.GetLogger().Info("DEBUG: calling adapter",
		"provider_name", providerName,
		"recommendation_id", r.Id,
		"category", r.Category,
		"rule_name", r.RuleName,
		"resolution_type", resolutionType)
	adptr := adapter.GetAdapter(providerName)
	existingRecommendations, err := ListRecommendationResolutions(ctx, recommendationRequest.Recommendation.Id, string(resolutionType), query.ResolverType, query.ResolverId)

	if err != nil {
		ctx.GetLogger().Error("failed to fetch existing recommendations", "error", err)
		return RecommendationApplyResponse{}, err
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return RecommendationApplyResponse{}, err
	}

	// insert resolution
	statusMessage := "Configuring"
	found, recommendationResolutionId := hasInProgressRecommendation(existingRecommendations)
	resolutionCreatedAt := time.Now()

	resolution := models.RecommendationResolution{
		Id:               recommendationResolutionId,
		CreatedAt:        &resolutionCreatedAt,
		UpdatedAt:        &resolutionCreatedAt,
		RecommendationId: r.Id,
		Type:             resolutionType,
		Data:             models.NewJsonObject(query),
		Status:           models.RecommendationResolutionStatusInProgress,
		TypeReferenceId:  "",
		ResolverType:     query.ResolverType,
		ResolverId:       query.ResolverId,
		StatusMessage:    &statusMessage,
	}
	if !found {
		_, err = dbms.Db.Exec(`INSERT INTO recommendation_resolution (id, created_at, updated_at, recommendation_id, type, data, status, type_reference_id, resolver_type, resolver_id, status_message) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			resolution.Id, resolutionCreatedAt.Format(time.RFC3339), resolutionCreatedAt.Format(time.RFC3339), resolution.RecommendationId, resolution.Type, resolution.Data, resolution.Status, resolution.TypeReferenceId, resolution.ResolverType, resolution.ResolverId, resolution.StatusMessage)
		if err != nil {
			ctx.GetLogger().Error("error inserting recommendation resolution", "error", err, "recommendation_id", resolution.RecommendationId, "resolution_id", resolution.Id)
			return RecommendationApplyResponse{}, common.ErrorInternal(fmt.Sprintf("error inserting recommendation resolution: %s", err.Error()))
		}
	}

	resp, err := adptr.ApplyRecommendation(ctx, recommendationRequest, existingRecommendations, recommendationResolutionId)

	if err != nil {
		ctx.GetLogger().Error("error applying recommendation", "error", err)
		_, err1 := dbms.Db.Exec(`UPDATE recommendation_resolution SET status = $1, updated_at = $2, status_message = $4 WHERE id = $3`,
			models.RecommendationResolutionStatusFailed, time.Now(), resolution.Id, err.Error())
		if err1 != nil {
			ctx.GetLogger().Error("error updating recommendation resolution after fail in applying recommendation", "error", err1)
		}
		return RecommendationApplyResponse{}, err
	}

	_, err1 := dbms.Db.Exec(`UPDATE recommendation_resolution SET type_reference_id = $1, updated_at = $2 WHERE id = $3`,
		resp.ResolutionTypeRefrenceId, time.Now(), resolution.Id)
	if err1 != nil {
		ctx.GetLogger().Error("error updating recommendation resolution after fail in applying recommendation", "error", err1)
	}

	ctx.GetLogger().Info("Recommendation applied", "response", slog.AnyValue(resp.Data))

	recommendationStatus := models.RecommendationStatusInProgress
	switch resp.Status {
	case adapter.RecommendationResolutionStatusSuccess:
		recommendationStatus = models.RecommendationStatusClosed
	case adapter.RecommendationResolutionStatusFailed:
		recommendationStatus = models.RecommendationStatusDismissed
	case adapter.RecommendationResolutionStatusInProgress:
		recommendationStatus = models.RecommendationStatusInProgress
	}

	userId := ctx.GetSecurityContext().GetUserId()
	if userId != "" {
		_, err = dbms.Db.Exec("UPDATE recommendation SET status = $3, updated_at = $2, updated_by = $4 WHERE id = $1", r.Id, time.Now().Format(time.RFC3339), recommendationStatus, userId)
	} else {
		_, err = dbms.Db.Exec("UPDATE recommendation SET status = $3, updated_at = $2 WHERE id = $1", r.Id, time.Now().Format(time.RFC3339), recommendationStatus)
	}

	if err != nil {
		ctx.GetLogger().Error("error closing recommendation", "error", err, "recommendation_id", r.Id, "status", recommendationStatus, "user_id", userId)
		return RecommendationApplyResponse{}, common.ErrorInternal(fmt.Sprintf("error closing recommendation: %s", err.Error()))
	}

	// trigger notification
	err = notification.TriggerNotificationForRecommendationResolution(ctx, r, resolution)
	if err != nil {
		ctx.GetLogger().Error("error triggering notification", "error", err)
	}
	return RecommendationApplyResponse{
		Data:       []any{resp.Data},
		Resolution: resolution,
		Status:     models.RecommendationStatus(string(recommendationStatus)),
	}, nil
}

// RetryRecommendationResolution retries a failed recommendation resolution
func RetryRecommendationResolution(ctx *security.RequestContext, query RetryRecommendationResolutionRequest) (RetryRecommendationResolutionResponse, error) {
	// if !ctx.GetSecurityContext().HasAccountAccess(query.AccountId, security.SecurityAccessTypeCreate) {
	// 	return RetryRecommendationResolutionResponse{}, common.ErrorUnauthorized("error: account access not found")
	// }

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return RetryRecommendationResolutionResponse{}, err
	}

	// Get the existing resolution
	var resolution models.RecommendationResolution
	row := dbms.Db.QueryRowx(`SELECT id, created_at, updated_at, recommendation_id, type, data, status, type_reference_id,
			resolver_type, resolver_id, status_message, pr_iteration_count, pr_lifecycle_state, last_pr_check_at
		FROM recommendation_resolution WHERE id = $1`, query.ResolutionId)
	if err := row.StructScan(&resolution); err != nil {
		ctx.GetLogger().Error("error getting recommendation resolution", "error", err)
		return RetryRecommendationResolutionResponse{}, fmt.Errorf("resolution not found: %s", query.ResolutionId)
	}

	// Verify the resolution is in Failed status
	if resolution.Status != models.RecommendationResolutionStatusFailed {
		return RetryRecommendationResolutionResponse{
			Resolution: resolution,
			Status:     "error",
			Message:    fmt.Sprintf("Cannot retry resolution with status: %s. Only failed resolutions can be retried.", resolution.Status),
		}, nil
	}

	// Get the recommendation
	r, err := GetRecommendation(ctx, resolution.RecommendationId)
	if err != nil {
		ctx.GetLogger().Error("error getting recommendation", "error", err)
		return RetryRecommendationResolutionResponse{}, err
	}

	// Get the account
	a, err := account.GetAccount(ctx, query.AccountId)
	if err != nil {
		ctx.GetLogger().Error("error getting account", "error", err)
		return RetryRecommendationResolutionResponse{}, err
	}

	// Determine provider from stored resolution data or resolution type
	var providerName string
	if resolution.Data.IsObject() {
		if data, ok := resolution.Data.Object().(map[string]any); ok {
			if provider, ok := data["provider"].(string); ok && provider != "" {
				providerName = provider
			}
		}
	}
	// If provider not found in data, determine from resolution type
	if providerName == "" {
		if resolution.Type == models.RecommendationResolutionTypePullRequest {
			providerName = "github"
		} else {
			// Fall back to account type
			switch a.AccountType {
			case "kubernetes":
				providerName = "kubernetes"
			case "aws":
				providerName = "aws"
			case "azure":
				providerName = "azure"
			case "gcp":
				providerName = "gcp"
			case "cloud":
				switch a.CloudProvider {
				case "AWS":
					providerName = "aws"
				case "Azure":
					providerName = "azure"
				case "GCP":
					providerName = "gcp"
				default:
					return RetryRecommendationResolutionResponse{}, fmt.Errorf("cloud provider not supported: %s", a.CloudProvider)
				}
			default:
				return RetryRecommendationResolutionResponse{}, fmt.Errorf("account type not supported: %s", a.AccountType)
			}
		}
	}

	// Reset resolution status to InProgress
	statusMessage := "Retrying"
	now := time.Now()
	_, err = dbms.Db.Exec(`UPDATE recommendation_resolution SET status = $1, updated_at = $2, status_message = $3 WHERE id = $4`,
		models.RecommendationResolutionStatusInProgress, now, statusMessage, resolution.Id)
	if err != nil {
		ctx.GetLogger().Error("error updating recommendation resolution status", "error", err)
		return RetryRecommendationResolutionResponse{}, err
	}

	// Update the recommendation status to InProgress
	_, err = dbms.Db.Exec("UPDATE recommendation SET status = $1 WHERE id = $2", models.RecommendationStatusInProgress, r.Id)
	if err != nil {
		ctx.GetLogger().Error("error updating recommendation status", "error", err)
	}

	// Get resource if exists
	var cr models.Resource
	if r.ResourceId != nil {
		cr, err = account.GetResource(ctx, *r.ResourceId)
		if err != nil {
			ctx.GetLogger().Error("error getting resource", "error", err)
		}
	}

	// Build the request from the stored resolution data
	var providerConfig map[string]any
	if resolution.Data.IsObject() {
		if data, ok := resolution.Data.Object().(map[string]any); ok {
			if pc, ok := data["provider_config"].(map[string]any); ok {
				providerConfig = pc
			}
		}
	}
	if providerConfig == nil {
		providerConfig = map[string]any{}
	}
	providerConfig["recommendation_source"] = "recommendation_retry"

	recommendationRequest := adapter.ApplyRecommendationRequest{
		Data:           map[string]any{},
		Recommendation: r,
		Resource:       cr,
		ProviderConfig: providerConfig,
	}

	// Get adapter and apply
	adptr := adapter.GetAdapter(providerName)
	resp, err := adptr.ApplyRecommendation(ctx, recommendationRequest, []models.RecommendationResolution{}, resolution.Id)

	if err != nil {
		ctx.GetLogger().Error("error retrying recommendation", "error", err)
		_, err1 := dbms.Db.Exec(`UPDATE recommendation_resolution SET status = $1, updated_at = $2, status_message = $3 WHERE id = $4`,
			models.RecommendationResolutionStatusFailed, time.Now(), err.Error(), resolution.Id)
		if err1 != nil {
			ctx.GetLogger().Error("error updating recommendation resolution after retry fail", "error", err1)
		}
		return RetryRecommendationResolutionResponse{
			Resolution: resolution,
			Status:     "error",
			Message:    fmt.Sprintf("Retry failed: %s", err.Error()),
		}, nil
	}

	// Update resolution with new reference ID
	_, err1 := dbms.Db.Exec(`UPDATE recommendation_resolution SET type_reference_id = $1, updated_at = $2 WHERE id = $3`,
		resp.ResolutionTypeRefrenceId, time.Now(), resolution.Id)
	if err1 != nil {
		ctx.GetLogger().Error("error updating recommendation resolution after retry", "error", err1)
	}

	// Refresh resolution data
	row = dbms.Db.QueryRowx(`SELECT id, created_at, updated_at, recommendation_id, type, data, status, type_reference_id,
			resolver_type, resolver_id, status_message, pr_iteration_count, pr_lifecycle_state, last_pr_check_at
		FROM recommendation_resolution WHERE id = $1`, resolution.Id)
	_ = row.StructScan(&resolution)

	ctx.GetLogger().Info("Recommendation resolution retried successfully", "resolution_id", resolution.Id)
	return RetryRecommendationResolutionResponse{
		Resolution: resolution,
		Status:     "success",
		Message:    "Resolution retry initiated successfully",
	}, nil
}

func ScanImage(ctx *security.RequestContext, query RecommendationScanImageRequest) (RecommendationApplyResponse, error) {

	if !ctx.GetSecurityContext().HasAccountAccess(query.AccountId, security.SecurityAccessTypeCreate) {
		return RecommendationApplyResponse{}, common.ErrorUnauthorized("unauthorized")
	}

	a, err := account.GetAccount(ctx, query.AccountId)
	if err != nil {
		ctx.GetLogger().Error("error getting account", "error", err)
		return RecommendationApplyResponse{}, err
	}
	if a.Id == "" {
		return RecommendationApplyResponse{}, fmt.Errorf("recommendation: account not found - %s", query.AccountId)
	}

	if a.Tenant != ctx.GetSecurityContext().GetTenantId() {
		return RecommendationApplyResponse{}, fmt.Errorf("recommendation: account not found in tenant - %s", query.AccountId)
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return RecommendationApplyResponse{}, err
	}
	rows, err := dbms.Db.Queryx(`
	select distinct container->>'image' as image, cr.cloud_account_id , cr.tenant_id , cr.name, cr.namespace, cr.workload_name
	from k8s_pods cr, 
		lateral jsonb_array_elements(cr.meta->'config'->'containers') as container 
	where cr.is_active is not false 
		and cr.status ='Running' 
		and cr.cloud_account_id = $1 
		and cr.namespace = $2
		and cr.workload_name = $3
	limit 5`, query.AccountId, query.Namespace, query.Workload)

	if err != nil {
		ctx.GetLogger().Error("error getting image scanner recommendations", "error", err)
		return RecommendationApplyResponse{}, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()

	pendingImages := make([]map[string]any, 0)
	images := make([]string, 0)
	for rows.Next() {
		d := make(map[string]any)
		err = rows.MapScan(d)
		if err != nil {
			ctx.GetLogger().Error("error scanning image scanner recommendations", "error", err)
			return RecommendationApplyResponse{}, err
		}
		if slices.Contains(images, d["image"].(string)) {
			continue
		}
		pendingImages = append(pendingImages, d)
		images = append(images, d["image"].(string))
	}

	tasks := make([]map[string]any, 0)
	for _, image := range pendingImages {
		payload := map[string]any{
			"sinks":         nil,
			"no_sinks":      false,
			"sync_response": false,
			"origin":        "callback",
			"timestamp":     time.Now(),
			"action_name":   "image_scanner",
			"action_params": map[string]any{
				"name":       image["name"],
				"namespace":  image["namespace"],
				"image_name": image["image"],
			},
		}
		payloadStr, err := common.MarshalJson(payload)
		if err != nil {
			ctx.GetLogger().Error("error marshalling image scanner recommendations", "error", err)
			return RecommendationApplyResponse{}, err
		}
		task := map[string]any{
			"cloud_account_id": query.AccountId,
			"tenant":           image["tenant_id"],
			"action":           "image_scanner",
			"payload":          string(payloadStr),
			"status":           "TODO",
			"source":           "recommendation",
		}
		tasks = append(tasks, task)
	}

	ctx.GetLogger().Info("Inserting image scanner tasks", "tasks", len(tasks))

	if (len(tasks)) == 0 {
		return RecommendationApplyResponse{}, common.ErrorBadRequest("no images to scan")
	}

	_, err = dbms.Db.NamedExec(`INSERT INTO agent_task (cloud_account_id, tenant, action, payload, status, source)
		VALUES (:cloud_account_id, :tenant, :action, :payload, :status, :source)
		ON CONFLICT (cloud_account_id, (payload->'action_params'->>'image_name')) WHERE action = 'image_scanner'
		DO UPDATE SET status = 'TODO', payload = EXCLUDED.payload, updated_at = NOW()
		WHERE agent_task.status NOT IN ('TODO', 'PROCESSING')`, tasks)
	if err != nil {
		ctx.GetLogger().Error("error inserting image scanner tasks", "error", err)
		return RecommendationApplyResponse{}, err
	}

	return RecommendationApplyResponse{
		Data: lo.Map(images, func(image string, index int) any {
			return map[string]any{
				"image": image,
			}
		}),
	}, nil
}

var recommendationJobProviderMap = map[string]string{
	"abandoned_workload_scan": "nb",
	"spot_scan":               "nb",
	// Server-orchestrated scanners (Stage 3 / Wave 2). The "nb" branch routes
	// these into scan_orchestrator.RunOne when the tenant has
	// SERVER_ORCHESTRATED_SCANNERS enabled, falling back to the legacy
	// agent_task insert otherwise. After every tenant is migrated the legacy
	// branch + the agent_task path go away in Wave 3.
	"popeye_scan":        "nb",
	"trivy_cis_scan":     "nb",
	"kube_bench_scan":    "nb",
	"helm_chart_upgrade": "nb",
	// volume_analyzer used to dispatch the Robusta `volume_analyzer` action
	// to the agent. With the Robusta agent gone, ml-k8s-server now owns
	// volume rightsizing for every metrics provider — see
	// services/ml/service.go:TriggerVolumeRightsizing. Routed via "nb".
	"volume_analyzer": "nb",
	// Not yet migrated — these stay on the legacy agent_task path.
	// image_scanner: needs per-image orchestration (Phase 2b).
	// krr_scan / unused_pv: ml-k8s-server owns rightsizing already; agent_task
	//   path is dead but the entry is kept until Wave 3 cleans up.
	// k8s_version_upgrade / certificate_scanner: not Job-based — Robusta
	//   implemented them as in-process K8s API calls; api-server will
	//   reimplement using existing get_resource primitives in a follow-up.
	"image_scanner":       "agent",
	"krr_scan":            "agent",
	"unused_pv":           "agent",
	"k8s_version_upgrade": "agent",
	"certificate_scanner": "agent",
}

func CreateRecommendationJob(ctx *security.RequestContext, query RecommendationJobCreateRequest) (RecommendationJobCreateResponse, error) {
	ctx.GetLogger().Info("Creating recommendation job", "query", slog.AnyValue(query))

	if !ctx.GetSecurityContext().HasAccountAccess(query.AccountId, security.SecurityAccessTypeCreate) {
		return RecommendationJobCreateResponse{}, common.ErrorUnauthorized("error: account access not found")
	}

	a, err := account.GetAccount(ctx, query.AccountId)
	if err != nil {
		ctx.GetLogger().Error("error getting account", "error", err)
		return RecommendationJobCreateResponse{}, err
	}
	if a.Id == "" {
		return RecommendationJobCreateResponse{}, fmt.Errorf("recommendation: account not found - %s", query.AccountId)
	}

	if a.AccountType != "kubernetes" {
		return RecommendationJobCreateResponse{}, fmt.Errorf("recommendation: account type not supported - %s", a.AccountType)
	}

	jobProvider := recommendationJobProviderMap[query.JobName]
	if jobProvider == "" {
		return RecommendationJobCreateResponse{}, fmt.Errorf("recommendation: job name not supported - %s", query.JobName)
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return RecommendationJobCreateResponse{}, err
	}

	switch jobProvider {
	case "agent":
		if resp, err := enqueueLegacyAgentTask(ctx, dbms, query.AccountId, a.Tenant, query.JobName); err != nil || len(resp.Data) > 0 {
			return resp, err
		}
	case "nb":
		switch query.JobName {
		case "spot_scan":
			go func() {
				err := processSpotInstanceRecommendations(ctx, query.AccountId, dbms)
				if err != nil {
					ctx.GetLogger().Error("error processing spot instance recommendations", "error", err)
				}
			}()
		case "abandoned_workload_scan":
			go func() {
				mp, _, _ := observability.GetLogsMetricsTracesProvider(ctx, query.AccountId, "", "metrics", "")
				err := processAbandonedRecommendations(ctx, query.AccountId, dbms, mp)
				if err != nil {
					ctx.GetLogger().Error("error processing abandoned workload recommendations", "error", err)
				}
			}()
		case "volume_analyzer":
			// Synchronous: ml-k8s-server is async itself (returns 202 and
			// queues the work) so this returns quickly without blocking the UI.
			mp, _, _ := observability.GetLogsMetricsTracesProvider(ctx, query.AccountId, "", "metrics", "")
			req := ml.VolumeRightsizingRequest{
				AccountId:             query.AccountId,
				TenantId:              a.Tenant,
				PersistRecommendation: true,
				MetricsProvider:       mp,
			}
			if mp == "datadog" {
				apiKey, appKey, site, ddErr := integrations.GetDatadogConfigs(ctx, query.AccountId)
				if ddErr != nil {
					ctx.GetLogger().Error("volume_analyzer: error getting datadog configs", "error", ddErr, "account_id", query.AccountId)
					return RecommendationJobCreateResponse{}, ddErr
				}
				req.DatadogApiKey = apiKey
				req.DatadogAppKey = appKey
				req.DatadogSite = site
			}
			if _, err := ml.TriggerVolumeRightsizing(ctx, req); err != nil {
				ctx.GetLogger().Error("volume_analyzer: error triggering ml-k8s-server", "error", err, "account_id", query.AccountId, "metrics_provider", mp)
				return RecommendationJobCreateResponse{}, err
			}
		case "popeye_scan", "trivy_cis_scan", "kube_bench_scan", "helm_chart_upgrade":
			// Server-orchestrated scanners. UI-triggered single-scanner runs go
			// through scan_orchestrator.RunOne which schedules the Job, polls,
			// fetches logs, parses, and UPSERTs into the recommendation table.
			//
			// Gate on the per-tenant feature flag — for tenants who haven't
			// opted in, fall back to the legacy agent_task insert path so the
			// UI button keeps working (the agent's per-scanner action is gone
			// post-PR #34, so the task will sit in TODO indefinitely; the same
			// behaviour as today).
			if !scan_orchestrator.IsEnabledForTenant(ctx, a.Tenant) {
				return enqueueLegacyAgentTask(ctx, dbms, query.AccountId, a.Tenant, query.JobName)
			}
			account := scan_orchestrator.ScanAccount{
				AccountID: query.AccountId,
				TenantID:  a.Tenant,
			}
			go func() {
				defer func() {
					if r := recover(); r != nil {
						ctx.GetLogger().Error("scan_orchestrator: RunOne panicked", "scanner", query.JobName, "panic", r)
					}
				}()
				if err := scan_orchestrator.RunOne(ctx, account, query.JobName, nil); err != nil {
					ctx.GetLogger().Error("scan_orchestrator: RunOne failed", "scanner", query.JobName, "error", err)
				}
			}()
		}
	default:
		return RecommendationJobCreateResponse{}, fmt.Errorf("recommendation: job provider not supported - %s", jobProvider)
	}

	return RecommendationJobCreateResponse{
		Data: []any{},
	}, nil
}

func UpdateResolutionStatus(ctx *security.RequestContext) error {
	t0 := time.Now()
	defer func() {
		ctx.GetLogger().Info("UpdateResolutionStatus", "time", time.Since(t0))
	}()
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("error getting database manager", "error", err)
		return err
	}

	// Open recommendations that are in progress and have failed resolutions
	_, err = dbms.Db.Exec(fmt.Sprintf("update recommendation set status = '%s' where status = '%s' and id in (select recommendation_id from recommendation_resolution where status = '%s')", models.RecommendationStatusOpen, models.RecommendationStatusInProgress, models.RecommendationResolutionStatusFailed))
	if err != nil {
		ctx.GetLogger().Error("error updating recommendation status", "error", err)
	}

	// only check resolutions that are in progress and are user resolutions
	// only check resolutions that are in progress and are user or auto-optimize resolutions
	rows, err := dbms.Db.Queryx("select * from recommendation_resolution where status = $1 and resolver_type IN ($2, $3)", models.RecommendationResolutionStatusInProgress, models.RecommendationResolutionResolverTypeUser, models.RecommendationResolutionResolverTypeAutoOptimize)
	if err != nil {
		ctx.GetLogger().Error("error getting recommendation resolutions", "error", err)
		return err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()

	recommendationResolutionsToCheck := make([]models.RecommendationResolution, 0)
	recommendations := make(map[string]models.Recommendation)
	recommendationIds := make([]any, 0)

	for rows.Next() {
		resolution := models.RecommendationResolution{}
		err = rows.StructScan(&resolution)
		if err != nil {
			ctx.GetLogger().Error("error scanning recommendation resolutions", "error", err)
			return err
		}
		recommendationResolutionsToCheck = append(recommendationResolutionsToCheck, resolution)
		recommendationIds = append(recommendationIds, resolution.RecommendationId)
	}

	if len(recommendationResolutionsToCheck) == 0 {
		return nil
	}

	rows1, err := dbms.Query("select * from recommendation where id in (?)", recommendationIds)
	if err != nil {
		ctx.GetLogger().Error("error getting recommendations", "error", err)
		return err
	}
	defer func() {
		err := rows1.Close()
		if err != nil {
			ctx.GetLogger().Error("error closing rows", "error", err)
		}
	}()

	for rows1.Next() {
		recommendation := models.Recommendation{}
		err = rows1.StructScan(&recommendation)
		if err != nil {
			ctx.GetLogger().Error("error scanning recommendations", "error", err)
			return err
		}
		recommendations[recommendation.Id] = recommendation
	}

	ctx.GetLogger().Info("Checking resolutions", "resolutions", len(recommendationResolutionsToCheck))

	for _, resolution := range recommendationResolutionsToCheck {
		recommendation := recommendations[resolution.RecommendationId]
		if recommendation.Id == "" {
			ctx.GetLogger().Error("error getting recommendation", "error", fmt.Errorf("recommendation not found - %s", resolution.RecommendationId))
			continue
		}

		// if recommendation is closed, then close the resolution
		if recommendation.Status == models.RecommendationStatusClosed {
			ctx.GetLogger().Info("Closing resolution as recommendation is already closed", "resolution", resolution.Id)
			_, err = dbms.Db.Exec("UPDATE recommendation_resolution SET status = $2, updated_at = $3 WHERE id = $1", resolution.Id, models.RecommendationResolutionStatusSuccess, time.Now().Format(time.RFC3339))
			if err != nil {
				ctx.GetLogger().Error("error updating recommendation resolution", "error", err)
				return err
			}
		}

		adptr := adapter.GetAdapterFromResolutionProvider(resolution.Type)
		if adptr == nil {
			ctx.GetLogger().Error("error getting adapter", "error", fmt.Errorf("adapter not found - %s", resolution.Type))
			continue
		}

		adapterContext := security.NewRequestContext(ctx.GetContext(), security.NewSecurityContextForTenantAdmin(recommendation.TenantId), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
		var statusMessage string
		if resolution.StatusMessage != nil {
			statusMessage = *resolution.StatusMessage
		}
		resp, err := adptr.GetRecommendationResolutionStatus(adapterContext, recommendation, resolution.TypeReferenceId, resolution.Data, statusMessage)
		var status models.RecommendationResolutionStatus
		var statusMsg string
		if err != nil {
			ctx.GetLogger().Error("error getting recommendation resolution status", "error", err)
			statusMsg = fmt.Sprintf("error getting recommendation resolution status - %s", err.Error())
			status = models.RecommendationResolutionStatusFailed
		} else {
			status = models.RecommendationResolutionStatus(string(resp.Status))
			statusMsg = resp.StatusMessage
		}
		if status == models.RecommendationResolutionStatusFailed {
			// resetting recommendation to open status if resolution failed
			ctx.GetLogger().Error("resolution failed", "resolution", resolution.Id, "status", statusMsg)
			_, err = dbms.Db.Exec("UPDATE recommendation SET status = $1 where id = $2 and cloud_account_id = $3 and tenant_id = $4", models.RecommendationStatusOpen, recommendation.Id, recommendation.CloudAccountId, recommendation.TenantId)
			if err != nil {
				ctx.GetLogger().Error("error updating recommendation", "error", err)
				return err
			}
		}

		_, err = dbms.Db.Exec("UPDATE recommendation_resolution SET status = $2, updated_at = $3, status_message = $4 WHERE id = $1", resolution.Id, status, time.Now().Format(time.RFC3339), statusMsg)
		if err != nil {
			ctx.GetLogger().Error("error updating recommendation resolution", "error", err)
			return err
		}

		// update recommendation if issue is resolved
		recommendationStatus := recommendation.Status
		switch status {
		case models.RecommendationResolutionStatusSuccess:
			recommendationStatus = models.RecommendationStatusClosed
		case models.RecommendationResolutionStatusFailed:
			recommendationStatus = models.RecommendationStatusDismissed
		}
		if recommendation.Status != recommendationStatus {
			_, err = dbms.Db.Exec("UPDATE recommendation SET status = $2, updated_at = $3 WHERE id = $1", recommendation.Id, models.RecommendationStatusClosed, time.Now().Format(time.RFC3339))
			if err != nil {
				ctx.GetLogger().Error("error updating recommendation", "error", err)
				return err
			}
		}

	}
	return nil
}

// enqueueLegacyAgentTask is the legacy "agent" path: insert an agent_task row
// the (now-decommissioned) Robusta runner used to pick up. Kept as a fallback
// for tenants who haven't been opted in to SERVER_ORCHESTRATED_SCANNERS yet —
// the row sits in TODO status until either the feature flag flips or Wave 3
// removes the legacy path entirely.
//
// Returns RecommendationJobCreateResponse{Data: ["already submitted..."]} when
// a non-terminal task for the same action already exists, so the caller can
// short-circuit without a separate "in progress" code path.
func enqueueLegacyAgentTask(ctx *security.RequestContext, dbms *database.DatabaseManager, accountID, tenantID, jobName string) (RecommendationJobCreateResponse, error) {
	rows, err := dbms.Db.Queryx("select count(*) as cnt from agent_task where cloud_account_id = $1 and tenant = $2 and action = $3 and status not in ('FAILED', 'COMPLETED', 'TIMEOUT')", accountID, tenantID, jobName)
	if err != nil {
		ctx.GetLogger().Error("error finding existing agent_tasks", "error", err)
		return RecommendationJobCreateResponse{}, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("error closing agent_task rows", "error", err)
		}
	}()
	for rows.Next() {
		cnt := 0
		if err := rows.Scan(&cnt); err != nil {
			ctx.GetLogger().Error("error scanning existing agent_tasks", "error", err)
			return RecommendationJobCreateResponse{}, err
		}
		if cnt > 0 {
			return RecommendationJobCreateResponse{
				Data: []any{
					map[string]any{"message": fmt.Sprintf("job already submitted - %s", jobName)},
				},
			}, nil
		}
	}

	payload := map[string]any{
		"sinks":         nil,
		"no_sinks":      false,
		"sync_response": false,
		"origin":        "callback",
		"timestamp":     time.Now(),
		"action_name":   jobName,
		"action_params": map[string]any{"a": "b"},
	}
	if jobName == "popeye_scan" {
		payload["action_params"] = map[string]any{
			"spinach": "popeye:\n  excludes:\n    v1/pods:\n      - name: rx:kube-system\n",
		}
	}
	payloadStr, err := common.MarshalJson(payload)
	if err != nil {
		ctx.GetLogger().Error("error marshalling agent_task payload", "error", err)
		return RecommendationJobCreateResponse{}, err
	}
	task := map[string]any{
		"cloud_account_id": accountID,
		"tenant":           tenantID,
		"action":           jobName,
		"payload":          string(payloadStr),
		"status":           "TODO",
		"source":           "recommendation",
	}
	if _, err := dbms.Db.NamedExec(`INSERT INTO agent_task (cloud_account_id, tenant, action, payload, status, source) values (:cloud_account_id, :tenant, :action, :payload, :status, :source)`, []map[string]any{task}); err != nil {
		ctx.GetLogger().Error("error inserting agent_task", "error", err)
		return RecommendationJobCreateResponse{}, err
	}
	return RecommendationJobCreateResponse{}, nil
}

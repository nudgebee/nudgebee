package recommendation

import (
	"context"
	"database/sql"
	"errors"
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
	"nudgebee/services/query"
	"nudgebee/services/recommendation/coordinator"
	"nudgebee/services/scan_orchestrator"
	"nudgebee/services/security"
	"strings"
	"time"

	"github.com/samber/lo"
)

func GetRecommendation(context *security.RequestContext, id string) (models.Recommendation, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return models.Recommendation{}, err
	}
	// recommendation.recommendation is trimmed for vm_package_vulnerability/image_scan
	// rows (see V867 migration) — the shared CVE+package fields now live on the
	// linked vulnerabilities row. Rebuild the legacy full payload via the same
	// reconstruction consumers like the LLM security tool and the security
	// code-agent PR flow (ApplySecurityRecommendationUsingCodeAgent -> formatCVELogs)
	// expect, so callers of GetRecommendation keep seeing CVE id/package/severity
	// instead of the trimmed husk.
	r := databaseManager.Db.QueryRowx(`SELECT r.id, r.created_at, r.updated_at, r.tenant_id, r.cloud_account_id, r.resource_id,
			`+query.VulnerabilityRecommendationSQL("r", "v.")+` AS recommendation, r.recommendation_action, r.severity, r.estimated_savings, r.status, r.category,
			r.rule_name, r.account_object_id, r.note, r.dismissed_reason, r.is_dismissed, r.updated_by,
			r.finops_score, r.finops_band, r.finops_score_breakdown, r.last_nudged_at, r.dedupe_group, r.snoozed_until
		FROM recommendation r
		LEFT JOIN vulnerabilities v ON v.id = r.vulnerability_id
		WHERE r.id = $1`, id)
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
			resolver_type, resolver_id, status_message, pr_iteration_count, pr_lifecycle_state, last_pr_check_at,
			value_refresh_count, last_value_refresh_at
		FROM recommendation_resolution
		WHERE recommendation_id = $1
		AND type = $2
		AND resolver_type = $3
		AND resolver_id = $4
		AND status = 'InProgress'
		AND created_at > NOW() - INTERVAL '2 hours'
		ORDER BY created_at DESC
	`, rescommendationId, resolutionType, resolverType, resolverId)
	defer func() {
		if r != nil {
			if cerr := r.Close(); cerr != nil {
				slog.Error("recommendation: error closing rows", "error", cerr)
			}
		}
	}()
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
	if err := r.Err(); err != nil {
		return []models.RecommendationResolution{}, fmt.Errorf("recommendation: iterate recommendation_resolution rows: %w", err)
	}
	return resolutions, nil
}

// findOpenPRResolution returns the most recent PullRequest resolution for a
// recommendation whose PR is still open on the remote, or nil when none exists.
//
// A resolution counts as "open" when it carries a real PR URL (type_reference_id
// like http...), is still InProgress, and its pr_lifecycle_state is not one of
// the terminal states ('merged' / 'closed' / 'unresolvable') that the reconciler
// in account/adapter/pr_lifecycle.go drives rows to once a PR is resolved. Failed
// PR-creation attempts leave type_reference_id empty, so they are ignored. NULL
// lifecycle rows with a URL are treated as open (a PR was raised but the
// reconciler has not classified it yet) to stay on the safe side of not opening
// a duplicate.
//
// The status filter is not redundant with the lifecycle filter: it keeps this
// query's idea of "open" the same as the reconciler's, which selects on
// status = 'InProgress' throughout. A row the reconciler will never look at
// again must not be able to block a recommendation here, because nothing can
// ever release it — a row stuck at ('Success', 'created') pinned llm-server and
// relay-server to PRs that had already merged, and no new PR was raised for
// either for a month. status only moves off InProgress via prTerminalFields,
// i.e. the PR really did merge or close, so this cannot hide a live PR.
func findOpenPRResolution(recommendationId string) (*models.RecommendationResolution, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	row := databaseManager.Db.QueryRowx(`
		SELECT id, created_at, updated_at, recommendation_id, type, data, status, type_reference_id,
			resolver_type, resolver_id, status_message, pr_iteration_count, pr_lifecycle_state, last_pr_check_at,
			value_refresh_count, last_value_refresh_at
		FROM recommendation_resolution
		WHERE recommendation_id = $1
		AND type = $2
		AND type_reference_id LIKE 'http%'
		AND status = $3
		AND (pr_lifecycle_state IS NULL OR pr_lifecycle_state NOT IN ('merged', 'closed', 'unresolvable'))
		ORDER BY created_at DESC
		LIMIT 1
	`, recommendationId, string(models.RecommendationResolutionTypePullRequest),
		string(models.RecommendationResolutionStatusInProgress))
	resolution := models.RecommendationResolution{}
	if err := row.StructScan(&resolution); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("error scanning open PR resolution: %w", err)
	}
	return &resolution, nil
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
			now := time.Now().UTC().Format(time.RFC3339)
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
		case "Configuration":
			// requests-unset pod recs live under Configuration but apply the
			// same way as any other pod_right_sizing rec
			if recommendationRequest.Recommendation.RuleName == "pod_right_sizing" {
				resolutionType = models.RecommendationResolutionTypeDeploymentChange
			}
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

	// Guard against opening duplicate PRs for the same recommendation. Autopilot
	// re-applies the same rightsizing recommendation on every scheduled run with a
	// fresh resolver_id (a new auto_pilot_task per run), so the resolver-scoped
	// InProgress check in ListRecommendationResolutions can never see the prior
	// run — each run would otherwise raise a brand-new PR for a recommendation
	// that already has one open. If a non-terminal PR already exists, return it
	// instead of raising another. Once the reconciler / GitHub webhook drives that
	// PR to a terminal state (merged/closed/unresolvable), a fresh PR is allowed.
	if resolutionType == models.RecommendationResolutionTypePullRequest {
		existingPR, err := findOpenPRResolution(r.Id)
		if err != nil {
			ctx.GetLogger().Error("error checking for existing open PR", "error", err, "recommendation_id", r.Id)
			return RecommendationApplyResponse{}, err
		}
		if existingPR != nil {
			lifecycleState := ""
			if existingPR.PRLifecycleState != nil {
				lifecycleState = *existingPR.PRLifecycleState
			}

			// The open PR stays the only PR — but it should not be allowed to go on
			// proposing numbers that are no longer true. When the recommendation has
			// moved materially since this PR was raised, rewrite it in place rather
			// than leaving it stale (#34959). Anything the refresh cannot establish
			// leaves the PR exactly as it was.
			decision := maybeRefreshOpenPR(ctx, existingPR, newValuesFromApplyRequest(query), query.RefreshOpenPRChangePct)

			ctx.GetLogger().Info("recommendation already has an open PR",
				"recommendation_id", r.Id,
				"existing_resolution_id", existingPR.Id,
				"pr_url", existingPR.TypeReferenceId,
				"pr_lifecycle_state", lifecycleState,
				"refreshed", decision.Refreshed,
				"decision", decision.Reason)

			message := fmt.Sprintf("a pull request is already open for this recommendation - %s", existingPR.TypeReferenceId)
			action := PRActionUnchanged
			if decision.Refreshed {
				message = fmt.Sprintf("%s - %s", existingPR.TypeReferenceId, decision.Reason)
				action = PRActionRefreshed
			}

			return RecommendationApplyResponse{
				Data: []any{map[string]any{
					"message": message,
					"pr_url":  existingPR.TypeReferenceId,
				}},
				Resolution: *existingPR,
				Status:     models.RecommendationStatusInProgress,
				PRAction:   action,
			}, nil
		}
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
	resolutionCreatedAt := time.Now().UTC()

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
			models.RecommendationResolutionStatusFailed, time.Now().UTC(), resolution.Id, err.Error())
		if err1 != nil {
			ctx.GetLogger().Error("error updating recommendation resolution after fail in applying recommendation", "error", err1)
		}
		return RecommendationApplyResponse{}, err
	}

	_, err1 := dbms.Db.Exec(`UPDATE recommendation_resolution SET type_reference_id = $1, updated_at = $2 WHERE id = $3`,
		resp.ResolutionTypeRefrenceId, time.Now().UTC(), resolution.Id)
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
	nowStr := time.Now().UTC().Format(time.RFC3339)
	if userId != "" {
		_, err = dbms.Db.Exec("UPDATE recommendation SET status = $3, updated_at = $2, updated_by = $4 WHERE id = $1", r.Id, nowStr, recommendationStatus, userId)
	} else {
		_, err = dbms.Db.Exec("UPDATE recommendation SET status = $3, updated_at = $2 WHERE id = $1", r.Id, nowStr, recommendationStatus)
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
	applyResponse := RecommendationApplyResponse{
		Data:       []any{resp.Data},
		Resolution: resolution,
		Status:     models.RecommendationStatus(string(recommendationStatus)),
	}
	if resolutionType == models.RecommendationResolutionTypePullRequest {
		applyResponse.PRAction = PRActionCreated
	}
	return applyResponse, nil
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
			resolver_type, resolver_id, status_message, pr_iteration_count, pr_lifecycle_state, last_pr_check_at,
			value_refresh_count, last_value_refresh_at
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
	now := time.Now().UTC()
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
			models.RecommendationResolutionStatusFailed, time.Now().UTC(), err.Error(), resolution.Id)
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
		resp.ResolutionTypeRefrenceId, time.Now().UTC(), resolution.Id)
	if err1 != nil {
		ctx.GetLogger().Error("error updating recommendation resolution after retry", "error", err1)
	}

	// Refresh resolution data
	row = dbms.Db.QueryRowx(`SELECT id, created_at, updated_at, recommendation_id, type, data, status, type_reference_id,
			resolver_type, resolver_id, status_message, pr_iteration_count, pr_lifecycle_state, last_pr_check_at,
			value_refresh_count, last_value_refresh_at
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
	// Distinct running images of the workload, plus the node each runs on and a
	// pod name — needed for the node-pinned fs-scan and pull-secret sourcing.
	rows, err := dbms.Db.Queryx(`
	select distinct container->>'image' as image, cr.name, cr.namespace, cr.meta->>'node' as node
	from k8s_pods cr,
		lateral jsonb_array_elements(cr.meta->'config'->'containers') as container
	where cr.is_active is not false
		and cr.status ='Running'
		and cr.cloud_account_id = $1
		and cr.namespace = $2
		and cr.workload_name = $3
	limit 5`, query.AccountId, query.Namespace, query.Workload)
	if err != nil {
		ctx.GetLogger().Error("error getting workload images for scan", "error", err)
		return RecommendationApplyResponse{}, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()

	type pendingImage struct{ image, pod, namespace, node string }
	seen := map[string]struct{}{}
	pending := make([]pendingImage, 0)
	images := make([]string, 0)
	for rows.Next() {
		d := make(map[string]any)
		if err := rows.MapScan(d); err != nil {
			ctx.GetLogger().Error("error scanning workload image row", "error", err)
			return RecommendationApplyResponse{}, err
		}
		img, _ := d["image"].(string)
		if img == "" {
			continue
		}
		if _, ok := seen[img]; ok {
			continue
		}
		seen[img] = struct{}{}
		node, _ := d["node"].(string)
		if node == "" {
			// fs-scan pins the Job to the node; skip images whose pod has no node.
			ctx.GetLogger().Warn("image_scanner: skipping image with no node", "image", img)
			continue
		}
		pod, _ := d["name"].(string)
		ns, _ := d["namespace"].(string)
		pending = append(pending, pendingImage{image: img, pod: pod, namespace: ns, node: node})
		images = append(images, img)
	}

	if len(pending) == 0 {
		return RecommendationApplyResponse{}, common.ErrorBadRequest("no images to scan")
	}

	// Server-orchestrated: schedule a trivy fs Job per image, poll, parse, UPSERT.
	// Replaces the legacy agent_task dispatch (the current agent has no
	// image_scanner action). Detached + bounded so the UI returns immediately;
	// results land in the recommendation table for the Image Scan tab.
	detachedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), 30*time.Minute)
	scanCtx := security.NewRequestContext(detachedCtx, ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
	go func() {
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				ctx.GetLogger().Error("image_scanner: manual scan panicked", "panic", r)
			}
		}()
		for _, p := range pending {
			if err := scan_orchestrator.RunOne(scanCtx, scan_orchestrator.ScanAccount{
				AccountID: query.AccountId,
				// Use the account's tenant (already fetched + validated against the
				// caller's tenant above). Scanning it from the per-pod k8s_pods row
				// failed: tenant_id is a uuid column, so MapScan hands back a
				// non-string value and the `.(string)` assertion yielded "" — which
				// tripped scan_orchestrator's "TenantID is required" guard and made
				// every manual "Scan Image" no-op before a Job was ever scheduled.
				TenantID:        a.Tenant,
				TargetImage:     p.image,
				TargetNode:      p.node,
				TargetNamespace: p.namespace,
				TargetPodName:   p.pod,
			}, "image_scanner", nil); err != nil {
				ctx.GetLogger().Error("image_scanner: manual per-image scan failed", "image", p.image, "error", err)
			}
		}
	}()

	ctx.GetLogger().Info("image_scanner: manual scan started", "images", len(images), "account_id", query.AccountId)
	return RecommendationApplyResponse{
		Data: lo.Map(images, func(image string, index int) any {
			return map[string]any{"image": image}
		}),
	}, nil
}

var recommendationJobProviderMap = map[string]string{
	"abandoned_workload_scan": "nb",
	"spot_scan":               "nb",
	// Server-orchestrated scanners. The "nb" branch routes these into
	// scan_orchestrator.RunOne which schedules a generic schedule_k8s_job
	// task on the agent, polls for completion, fetches logs, parses, and
	// UPSERTs recommendation rows.
	"popeye_scan":        "nb",
	"trivy_cis_scan":     "nb",
	"kube_bench_scan":    "nb",
	"helm_chart_upgrade": "nb",
	// volume_analyzer used to dispatch the Robusta `volume_analyzer` action
	// to the agent. With the Robusta agent gone, ml-k8s-server now owns
	// volume rightsizing for every metrics provider — see
	// services/ml/service.go:TriggerVolumeRightsizing. Routed via "nb".
	"volume_analyzer": "nb",
	// image_scanner is server-orchestrated (per-image): the "nb" branch runs
	// runImageScannerServerOrchestrated (schedule trivy fs Job per pending image,
	// poll, parse, UPSERT). The legacy "agent" path dispatched an image_scanner
	// agent_task the current agent doesn't implement ("action not registered").
	"image_scanner": "nb",
	// krr_scan (pod/vertical rightsizing) is owned by ml-k8s-server — see
	// services/ml/service.go:TriggerVerticalRightsizing (also driven by the
	// daily "Vertical Rightsizing Refresh" cron). The legacy agent_task path
	// is dead (nothing consumes krr_scan since the legacy agent was removed),
	// so the UI Refresh would enqueue a task that only fails. Routed via "nb"
	// so on-demand refresh triggers the server-side generator directly.
	"krr_scan": "nb",
	// Not yet migrated — these stay on the legacy agent_task path.
	// unused_pv: ml-k8s-server owns rightsizing already; agent_task path is
	//   dead but the entry is kept until Wave 3 cleans up.
	// k8s_version_upgrade / certificate_scanner: not Job-based — the legacy agent
	//   implemented them as in-process K8s API calls; api-server will reimplement
	//   using existing get_resource primitives in a follow-up.
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
				// Detach from the request context (which is cancelled when the HTTP handler
				// returns) but bound the work with an absolute timeout so a hung run can't leak
				// the goroutine or the in-flight job guard.
				detachedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), 15*time.Minute)
				defer cancel()
				taskCtx := security.NewRequestContext(detachedCtx, ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
				if err := processAbandonedRecommendations(taskCtx, query.AccountId, dbms); err != nil {
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
			if mp == "ES" {
				esCfg, esErr := observability.ElasticsearchRightsizingConfig(ctx, query.AccountId)
				if esErr != nil {
					ctx.GetLogger().Error("volume_analyzer: error getting elasticsearch config", "error", esErr, "account_id", query.AccountId)
					return RecommendationJobCreateResponse{}, esErr
				}
				req.Elasticsearch = esCfg
			}
			if _, err := ml.TriggerVolumeRightsizing(ctx, req); err != nil {
				ctx.GetLogger().Error("volume_analyzer: error triggering ml-k8s-server", "error", err, "account_id", query.AccountId, "metrics_provider", mp)
				return RecommendationJobCreateResponse{}, err
			}
		case "krr_scan":
			// Pod/vertical rightsizing. ml-k8s-server owns generation (same path
			// as the daily "Vertical Rightsizing Refresh" cron). Synchronous:
			// ml-k8s-server is async itself (returns 202 and queues the work) so
			// this returns quickly without blocking the UI.
			mp, _, _ := observability.GetLogsMetricsTracesProvider(ctx, query.AccountId, "", "metrics", "")
			req := ml.VerticalRightsizingRequest{
				AccountId:             query.AccountId,
				TenantId:              a.Tenant,
				PersistRecommendation: true,
				BatchByNamespace:      true,
				MetricsProvider:       mp,
			}
			if mp == "datadog" {
				apiKey, appKey, site, ddErr := integrations.GetDatadogConfigs(ctx, query.AccountId)
				if ddErr != nil {
					ctx.GetLogger().Error("krr_scan: error getting datadog configs", "error", ddErr, "account_id", query.AccountId)
					return RecommendationJobCreateResponse{}, ddErr
				}
				req.DatadogApiKey = apiKey
				req.DatadogAppKey = appKey
				req.DatadogSite = site
			}
			if mp == "ES" {
				esCfg, esErr := observability.ElasticsearchRightsizingConfig(ctx, query.AccountId)
				if esErr != nil {
					ctx.GetLogger().Error("krr_scan: error getting elasticsearch config", "error", esErr, "account_id", query.AccountId)
					return RecommendationJobCreateResponse{}, esErr
				}
				req.Elasticsearch = esCfg
			}
			if _, err := ml.TriggerVerticalRightsizing(ctx, req); err != nil {
				ctx.GetLogger().Error("krr_scan: error triggering ml-k8s-server", "error", err, "account_id", query.AccountId, "metrics_provider", mp)
				return RecommendationJobCreateResponse{}, err
			}
		case "popeye_scan", "trivy_cis_scan", "kube_bench_scan", "helm_chart_upgrade":
			// Server-orchestrated scanners. UI-triggered single-scanner runs go
			// through scan_orchestrator.RunOne which schedules the Job, polls,
			// fetches logs, parses, and UPSERTs into the recommendation table.
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
		case "image_scanner":
			// Per-image: scan the account's pending images server-side. Detached
			// from the request context (cancelled when the handler returns) but
			// bounded so a hung run can't leak the goroutine.
			go func() {
				defer func() {
					if r := recover(); r != nil {
						ctx.GetLogger().Error("image_scanner: server-orchestrated run panicked", "panic", r)
					}
				}()
				detachedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), 30*time.Minute)
				defer cancel()
				taskCtx := security.NewRequestContext(detachedCtx, ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
				if err := runImageScannerServerOrchestrated(taskCtx, query.AccountId, a.Tenant, dbms); err != nil {
					ctx.GetLogger().Error("image_scanner: server-orchestrated run failed", "account_id", query.AccountId, "error", err)
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

// reopenOrphanedInProgressRecommendations returns InProgress recommendations that have
// no resolution behind them to Open. Every writer that claims a recommendation inserts
// its recommendation_resolution row before flipping the status (ApplyRecommendation,
// CreateTicketResolution), so InProgress with no resolution at all means nobody is
// working it. Such a row is unreachable: the settle statement in UpdateResolutionStatus
// joins recommendation_resolution and never sees it, and every retirement path skips
// InProgress — so it stays visible in Optimise indefinitely, outliving the workload it
// points at. Open is the safe landing state; it hands the row back to its producer's
// normal archive cycle, which retires it when the resource is gone.
func reopenOrphanedInProgressRecommendations(ctx *security.RequestContext, dbms *database.DatabaseManager) error {
	res, err := dbms.Db.Exec(`update recommendation r
	set
		status = $1,
		updated_at = NOW()
	where
		r.status = $2
		and not exists (select 1 from recommendation_resolution rr where rr.recommendation_id = r.id)`,
		models.RecommendationStatusOpen, models.RecommendationStatusInProgress)
	if err != nil {
		return err
	}
	if count, err := res.RowsAffected(); err == nil && count > 0 {
		ctx.GetLogger().Info("reopened orphaned in-progress recommendations", "count", count)
	}

	return nil
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

	// The pull request webhook and the followup cron retire resolution rows
	// directly, so the per-resolution loop below — which only reads InProgress
	// rows — never sees those outcomes. The set-based reconcile that settles
	// their recommendations lives with the coordinator.
	if err := coordinator.ReconcileSettledRecommendations(ctx); err != nil {
		ctx.GetLogger().Error("error reconciling settled recommendations", "error", err)
	}

	// Ticket delegations settle from the local tickets table — the poll below
	// deliberately skips Ticket rows because no adapter can poll a ticket.
	if err := SyncTicketResolutions(ctx); err != nil {
		ctx.GetLogger().Error("error syncing ticket resolutions", "error", err)
	}

	if _, err := coordinator.ExpireSnoozes(ctx); err != nil {
		ctx.GetLogger().Error("error expiring snoozed recommendations", "error", err)
	}

	if err := reopenOrphanedInProgressRecommendations(ctx, dbms); err != nil {
		ctx.GetLogger().Error("error reopening orphaned in-progress recommendations", "error", err)
	}

	// Poll every in-progress resolution regardless of who raised it — agent
	// (NBLLM) and runbook (AutoRunbook) rows previously never advanced because
	// only User/AutoOptimize were selected here, leaving their recommendations
	// stuck InProgress with the webhook as the only way out. Ticket resolutions
	// are excluded instead: no adapter can poll a ticket's state, so they would
	// only log an adapter-not-found error every run; they settle via the
	// ticket-status sync.
	rows, err := dbms.Db.Queryx("select * from recommendation_resolution where status = $1 and type <> $2", models.RecommendationResolutionStatusInProgress, models.RecommendationResolutionTypeTicket)
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
			if _, err := coordinator.SettleResolution(ctx, resolution.Id, models.RecommendationResolutionStatusSuccess, "recommendation already closed", coordinator.SourcePoll); err != nil {
				ctx.GetLogger().Error("error settling resolution for closed recommendation", "error", err)
				return err
			}
			// Settled — polling the adapter now could only overwrite this Success
			// (and a Failed poll result would even reopen the Closed recommendation).
			continue
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
		if status == models.RecommendationResolutionStatusInProgress {
			// Not a transition — refresh the polled message only. Guarded on the
			// row still being InProgress so a webhook that settled it between our
			// read and this write is not overwritten back to InProgress.
			_, err = dbms.Db.Exec("UPDATE recommendation_resolution SET updated_at = $3, status_message = $4 WHERE id = $1 AND status = $2", resolution.Id, models.RecommendationResolutionStatusInProgress, time.Now().UTC().Format(time.RFC3339), statusMsg)
			if err != nil {
				ctx.GetLogger().Error("error updating recommendation resolution", "error", err)
				return err
			}
			continue
		}

		if status == models.RecommendationResolutionStatusFailed {
			ctx.GetLogger().Error("resolution failed", "resolution", resolution.Id, "status", statusMsg)
		}
		if _, err := coordinator.SettleResolution(ctx, resolution.Id, status, statusMsg, coordinator.SourcePoll); err != nil {
			ctx.GetLogger().Error("error settling recommendation resolution", "error", err)
			return err
		}
	}
	return nil
}

// enqueueLegacyAgentTask is the legacy "agent" path: insert an agent_task row
// the (now-decommissioned) Robusta runner used to pick up. Still used by the
// "agent"-provider scanners (image_scanner, krr_scan, unused_pv,
// k8s_version_upgrade, certificate_scanner) that haven't been wired to the
// scan_orchestrator entrypoints yet; the row sits in TODO status until that
// migration lands.
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

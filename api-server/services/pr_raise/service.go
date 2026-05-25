package pr_raise

import (
	"fmt"
	"log/slog"
	"nudgebee/services/account"
	"nudgebee/services/account/adapter"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
	"time"
)

func GetEvent(context *security.RequestContext, id string) (models.Event, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return models.Event{}, err
	}
	r := databaseManager.Db.QueryRowx(`SELECT id, created_at, updated_at, finding_id, title, description, source, aggregation_key,
			failure, finding_type, category, priority, subject_type, subject_name, subject_namespace,
			subject_node, service_key, cluster, ends_at, starts_at, fingerprint, evidences, tenant,
			cloud_account_id, cloud_resource_id, status, nb_status, nb_status_changed_at,
			nb_status_changed_by, snoozed_until, principal, subject_owner, subject_owner_kind, labels,
			urgency, computed_score, computed_priority, score_factors, score_confidence
		FROM events WHERE id = $1`, id)
	if r.Err() != nil {
		return models.Event{}, r.Err()
	}
	recommendation := models.Event{}
	err = r.StructScan(&recommendation)
	return recommendation, err
}

func ListEventResolutions(context *security.RequestContext, rescommendationId string) ([]models.EventResolution, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return []models.EventResolution{}, err
	}
	r, err := databaseManager.Db.Queryx(`SELECT id, recommendation_id AS event_id, created_at, updated_at, type, data, status,
			type_reference_id, resolver_type, resolver_id, status_message,
			pr_iteration_count, pr_lifecycle_state, last_pr_check_at
		FROM recommendation_resolution WHERE recommendation_id = $1`, rescommendationId)
	if err != nil {
		return []models.EventResolution{}, err
	}

	resolutions := []models.EventResolution{}
	for r.Next() {
		resolution := models.EventResolution{}
		err = r.StructScan(&resolution)
		if err != nil {
			return []models.EventResolution{}, err
		}
		resolutions = append(resolutions, resolution)
	}
	return resolutions, nil
}

func ApplyResolution(ctx *security.RequestContext, query PRraiseRequest) (EventRecommendationApplyResponse, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(query.AccountId, security.SecurityAccessTypeCreate) {
		return EventRecommendationApplyResponse{}, common.ErrorUnauthorized("error: account access not found")
	}

	queryData := query.Data.(map[string]any)
	a, err := account.GetAccount(ctx, query.AccountId)
	if err != nil {
		ctx.GetLogger().Error("error getting account", "error", err)
		return EventRecommendationApplyResponse{}, err
	}
	if a.Id == "" {
		return EventRecommendationApplyResponse{}, fmt.Errorf("recommendation: account not found - %s", query.AccountId)
	}
	if query.ResourceId == nil {
		return EventRecommendationApplyResponse{}, fmt.Errorf("recommendation: resource id is required")
	}

	var cr models.Resource
	cr, err = account.GetResource(ctx, *query.ResourceId)
	if err != nil {
		ctx.GetLogger().Error("error getting resource", "error", err)
		return EventRecommendationApplyResponse{}, err
	}

	if a.AccountType != "kubernetes" {
		return EventRecommendationApplyResponse{}, fmt.Errorf("recommendation: account type not supported - %s", a.AccountType)
	}

	if query.Provider == "" {
		query.Provider = "kubernetes"
	}

	adptr := adapter.GetAdapter(query.Provider)

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return EventRecommendationApplyResponse{}, err
	}

	// insert resolution
	statusMessage := "Configuring"
	resolutionCreatedAt := time.Now()
	var resolverType models.RecommendationResolutionResolverType
	if query.ResolverType == string(models.RecommendationResolutionResolverTypeAutoRunbook) {
		resolverType = models.RecommendationResolutionResolverTypeAutoRunbook
	} else if query.ResolverType == string(models.RecommendationResolutionResolverTypeAutoOptimize) {
		resolverType = models.RecommendationResolutionResolverTypeAutoOptimize
	}

	resolution := models.EventResolution{
		Id:            common.GenerateUUID(),
		CreatedAt:     &resolutionCreatedAt,
		UpdatedAt:     &resolutionCreatedAt,
		EventId:       query.ResolverID,
		Type:          models.RecommendationResolutionTypePullRequest,
		Data:          models.NewJsonObject(query),
		Status:        models.RecommendationResolutionStatusInProgress,
		ResolverType:  resolverType,
		ResolverId:    query.ResolverID,
		StatusMessage: &statusMessage,
	}
	_, err = dbms.Db.Exec(`INSERT INTO event_resolution (id, created_at, updated_at, event_id, type, data, status, type_reference_id, resolver_type, resolver_id, status_message) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		resolution.Id,
		resolutionCreatedAt.Format(time.RFC3339),
		resolutionCreatedAt.Format(time.RFC3339),
		resolution.EventId,
		resolution.Type,
		resolution.Data,
		resolution.Status,
		resolution.TypeReferenceId,
		resolution.ResolverType,
		resolution.ResolverId,
		resolution.StatusMessage)
	if err != nil {
		ctx.GetLogger().Error("error inserting PR resolution", "error", err)
		return EventRecommendationApplyResponse{}, common.ErrorInternal("error inserting recommendation resolution")
	}

	type ResourceData struct {
		Request string `json:"request" mapstructure:"request"`
		Limit   string `json:"limit" mapstructure:"limit"`
	}

	type PodRightsizingRequest struct {
		Cpu    ResourceData `json:"cpu" mapstructure:"cpu"`
		Memory ResourceData `json:"memory" mapstructure:"memory"`
	}

	convertedRecommendationResolution := []models.RecommendationResolution{}
	var recommendationRequest adapter.ApplyRecommendationRequest

	if query.ChangeType == "VerticalRightsize" {
		recommendationData := map[string]any{}
		resourceMeta := cr.Meta.Object().(map[string]any)
		resourceConfigs := resourceMeta["config"].(map[string]any)
		resourceContainers := resourceConfigs["containers"].([]any)
		for containerName, containerData := range queryData {
			var podRightsizingRequest PodRightsizingRequest
			err = common.UnmarshalMapToStruct(containerData.(map[string]interface{}), &podRightsizingRequest)
			if err != nil {
				return EventRecommendationApplyResponse{}, fmt.Errorf("error parsing query data: %v", err)
			}
			containerExists := false
			for _, rc := range resourceContainers {
				if rc.(map[string]any)["name"] == containerName {
					containerExists = true
					break
				}
			}
			if containerExists {
				containerValues := map[string]any{
					"cpu":    podRightsizingRequest.Cpu,
					"memory": podRightsizingRequest.Memory,
				}
				recommendationData[containerName] = containerValues
				break
			}
		}
		if len(recommendationData) == 0 {
			return EventRecommendationApplyResponse{}, fmt.Errorf("no container values found for container ")
		}

		if query.ProviderConfig == nil {
			query.ProviderConfig = map[string]any{}
		}
		query.ProviderConfig["recommendation_source"] = "event"

		id := common.GenerateUUID()
		recommendationRequest = adapter.ApplyRecommendationRequest{
			Data: query.Data.(map[string]any),
			Recommendation: models.Recommendation{
				Category:       "RightSizing",
				RuleName:       "pod_right_sizing",
				Id:             id,
				CloudAccountId: query.AccountId,
				TenantId:       query.TenantId,
				Recommendation: models.NewJsonObject(recommendationData),
			},
			Resource:          cr,
			ProviderConfig:    query.ProviderConfig,
			ResolverType:      query.ResolverType,
			ReferenceLink:     query.ReferenceLink,
			IsEventResolution: true,
		}

	} else if query.ChangeType == "HorizontalRightsize" {
		replicaCount := int(queryData["replica_count"].(float64))
		id := common.GenerateUUID()
		resourceMeta := cr.Meta.Object().(map[string]any)
		recommendationRequest = adapter.ApplyRecommendationRequest{
			Data: query.Data.(map[string]any),
			Recommendation: models.Recommendation{
				Category:       "RightSizing",
				RuleName:       "replica_right_sizing",
				Id:             id,
				CloudAccountId: query.AccountId,
				TenantId:       query.TenantId,
				Recommendation: models.NewJsonObject(map[string]any{
					"account_id":  query.AccountId,
					"action_name": "replica_right_sizing",
					"action_params": map[string]any{
						"name":          cr.Name,
						"namespace":     resourceMeta["namespace"],
						"kind":          resourceMeta["kind"],
						"replica_count": replicaCount,
					},
				}),
				AccountObjectId: &id,
			},
			Resource:          cr,
			ProviderConfig:    query.ProviderConfig,
			ResolverType:      query.ResolverType,
			ReferenceLink:     query.ReferenceLink,
			IsEventResolution: true,
		}
	} else {
		return EventRecommendationApplyResponse{}, fmt.Errorf("recommendation: change type not supported - %s", query.ChangeType)
	}
	resp, err := adptr.ApplyRecommendation(ctx, recommendationRequest, convertedRecommendationResolution, resolution.Id)

	if err != nil {
		ctx.GetLogger().Error("error applying recommendation", "error", err)
		_, err = dbms.Db.Exec("UPDATE event_resolution SET status = $2, updated_at = $3, status_message = $4 WHERE id = $1", resolution.Id, models.RecommendationResolutionStatusFailed, time.Now().Format(time.RFC3339), err.Error())
		if err != nil {
			ctx.GetLogger().Error("error updating event resolution", "error", err)
		}
		return EventRecommendationApplyResponse{}, err
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

	return EventRecommendationApplyResponse{
		Data:       []any{resp.Data},
		Resolution: resolution,
		Status:     models.RecommendationStatus(string(recommendationStatus)),
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

	// only check resolutions that are in progress and are user resolutions
	rows, err := dbms.Db.Queryx("select * from event_resolution where status = $1 and resolver_type = $2", models.RecommendationResolutionStatusInProgress, models.RecommendationResolutionResolverTypeAutoRunbook)
	if err != nil {
		ctx.GetLogger().Error("error getting event resolutions", "error", err)
		return err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("error closing rows", "error", err)
		}
	}()

	eventResolutionsToCheck := make([]models.EventResolution, 0)
	for rows.Next() {
		resolution := models.EventResolution{}
		err = rows.StructScan(&resolution)
		if err != nil {
			ctx.GetLogger().Error("error scanning event resolutions", "error", err)
			return err
		}
		eventResolutionsToCheck = append(eventResolutionsToCheck, resolution)
	}

	ctx.GetLogger().Info("Checking resolutions", "resolutions", len(eventResolutionsToCheck))

	for _, resolution := range eventResolutionsToCheck {
		adptr := adapter.GetAdapterFromResolutionProvider(resolution.Type)
		if adptr == nil {
			ctx.GetLogger().Error("error getting adapter", "error", fmt.Errorf("adapter not found - %s", resolution.Type))
			continue
		}

		var dataMap map[string]any
		dataBytes, err := common.MarshalJson(resolution.Data)
		if err != nil {
			ctx.GetLogger().Error("error marshalling resolution data", "error", err)
			continue
		}
		err = common.UnmarshalJson(dataBytes, &dataMap)
		accountId, ok := dataMap["account_id"].(string)
		if !ok {
			ctx.GetLogger().Error("error getting account id from resolution data")
			continue
		}
		tenantId, ok := dataMap["tenant_id"].(string)
		if !ok {
			ctx.GetLogger().Error("error getting tenant id from resolution data")
			continue
		}
		adapterContext := security.NewRequestContext(ctx.GetContext(), security.NewSecurityContextForTenantAdmin(tenantId), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
		if err != nil {
			ctx.GetLogger().Error("error unmarshalling resolution data", "error", err)
			continue
		}
		resp, err := adptr.GetRecommendationResolutionStatus(adapterContext, models.Recommendation{
			Category:       "RightSizing",
			RuleName:       "pod_right_sizing",
			Id:             resolution.ResolverId,
			CloudAccountId: accountId,
			TenantId:       tenantId,
			Recommendation: models.NewJsonObject(map[string]any{}),
		}, resolution.TypeReferenceId, resolution.Data, *resolution.StatusMessage)
		var status models.RecommendationResolutionStatus
		var statusMsg string
		if err != nil {
			ctx.GetLogger().Error("error getting event resolution status", "error", err)
			statusMsg = fmt.Sprintf("error getting event resolution status - %s", err.Error())
			status = models.RecommendationResolutionStatusFailed
		} else {
			status = models.RecommendationResolutionStatus(string(resp.Status))
			statusMsg = resp.StatusMessage
		}

		_, err = dbms.Db.Exec("UPDATE event_resolution SET status = $2, updated_at = $3, status_message = $4 WHERE id = $1", resolution.Id, status, time.Now().Format(time.RFC3339), statusMsg)
		if err != nil {
			ctx.GetLogger().Error("error updating event resolution", "error", err)
			return err
		}
	}

	return nil
}

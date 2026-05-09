package account

import (
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"strings"
	"time"

	"github.com/google/uuid"
)

func StoreRecommendationsAll(ctx *security.RequestContext, accountId string) (StoreRecommendationResponse, error) {
	t0 := time.Now()
	availableServices, err := getAllServices(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch available services", "error", err)
	}
	ctx.GetLogger().Info("fetched available services", "count", len(availableServices), "time", time.Since(t0).String())
	cnt := 0
	errs := []string{}
	for _, serviceName := range availableServices {
		d, err := StoreRecommendations(ctx, accountId, providers.ListRecommendationsRequest{
			ServiceName: serviceName,
		})
		if err != nil {
			if !errors.Is(err, errors.ErrUnsupported) {
				ctx.GetLogger().Error("unable to store recommendations", "error", err, "serviceName", serviceName)
				errs = append(errs, err.Error())
			}
			continue
		}
		cnt += d.Count
		ctx.GetLogger().Info("stored recommendations", "count", d.Count, "time", time.Since(t0).String())
	}
	// Collect native cloud provider recommendations (account-level services not in cloud_resourses)
	// AWS: costoptimizationhub, costexplorer, computeoptimizer, trustedadvisor
	// GCP: recommender
	// Azure: advisor
	// Services not found for a given cloud provider are silently skipped (return empty).
	nativeServices := []string{"costoptimizationhub", "costexplorer", "computeoptimizer", "trustedadvisor", "recommender", "advisor"}
	for _, svc := range nativeServices {
		d, err := StoreRecommendations(ctx, accountId, providers.ListRecommendationsRequest{
			ServiceName: svc,
		})
		if err != nil {
			ctx.GetLogger().Error("unable to store native recommendations", "service", svc, "error", err)
			errs = append(errs, err.Error())
			continue
		}
		cnt += d.Count
		ctx.GetLogger().Info("stored native recommendations", "service", svc, "count", d.Count, "time", time.Since(t0).String())
	}

	// Final reconciliation: archive any Open rec whose target resource is no
	// longer Active. The per-service archive in StoreRecommendations only runs
	// when getRecommendationsInternal succeeds, so a transient provider error
	// (or a service whose live resources are all deleted) leaves orphans behind.
	if swept, sweepErr := sweepOrphanedRecommendations(ctx, accountId); sweepErr != nil {
		ctx.GetLogger().Error("orphan recommendation sweep failed", "error", sweepErr)
		errs = append(errs, sweepErr.Error())
	} else if swept > 0 {
		ctx.GetLogger().Info("orphan recommendations archived", "count", swept, "time", time.Since(t0).String())
	}

	return StoreRecommendationResponse{
		Count:    cnt,
		Duration: time.Since(t0),
		Errors:   errs,
	}, nil

}

// sweepOrphanedRecommendations archives Open recommendations whose target
// cloud_resourses row is no longer Active for the given account. Uses
// NOT EXISTS so it catches both soft-deleted rows (status != 'active') and
// hard-deleted rows (no cloud_resourses row at all). Recommendations with
// NULL resource_id (account-level / native) are left alone.
func sweepOrphanedRecommendations(ctx *security.RequestContext, accountId string) (int64, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return 0, err
	}
	query := `update recommendation
			   set status = 'Archive', updated_at = now()
			 where cloud_account_id = $1
			   and status = 'Open'
			   and is_dismissed = false
			   and resource_id is not null
			   and not exists (
			       select 1 from cloud_resourses
			        where id = recommendation.resource_id
			          and account = $1
			          and lower(status) = 'active'
			   )`
	r, err := dbms.Exec(query, accountId)
	if err != nil {
		return 0, err
	}
	count, _ := r.RowsAffected()
	return count, nil
}

func StoreRecommendations(ctx *security.RequestContext, accountId string, filter providers.ListRecommendationsRequest) (StoreRecommendationResponse, error) {
	t0 := time.Now()

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("unable to get dbms", "error", err)
		return StoreRecommendationResponse{
			Count:    0,
			Duration: time.Since(t0),
		}, err
	}

	recommendations, account, err := getRecommendationsInternal(ctx, accountId, filter)
	if err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			ctx.GetLogger().Debug("service does not support recommendations", "serviceName", filter.ServiceName)
		} else {
			ctx.GetLogger().Error("unable to fetch recommendations", "error", err)
		}
		return StoreRecommendationResponse{
			Duration: time.Since(t0),
		}, err
	}

	defer func() {
		ctx.GetLogger().Info("stored recommendation sync completed", "time", time.Since(t0).String(), "data", slog.AnyValue(filter))
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		err := updateOrCreateAgentStatus(ctx, accountId, AgentStatusConnected, msg, true, map[string]any{
			"account_number": account.AccountNumber,
			"recommendations": map[string]any{
				"updated_at": time.Now().UTC().Format(time.RFC3339),
				"last_job":   filter,
				"err":        msg,
			},
		})
		if err != nil {
			ctx.GetLogger().Error("Failed to update agent status", "error", err.Error())
		}
	}()

	if len(recommendations.Items) == 0 {
		query := `update recommendation set status = 'Archive' where cloud_account_id = $1 and (resource_id in (select id from cloud_resourses where lower(service_name) = $2 and account = $1) or lower((recommendation ->> 'service_name')::varchar) = $2)`
		r, err := dbms.Exec(query, accountId, strings.ToLower(filter.ServiceName))
		if err != nil {
			ctx.GetLogger().Error("unable to archive recommendations", "error", err)
		}
		if c, err := r.RowsAffected(); err == nil {
			ctx.GetLogger().Info("archieved recommendations", "count", c)
		}

		return StoreRecommendationResponse{
			Duration: time.Since(t0),
		}, nil
	}

	// validate and get objects
	externalResourceIds := []string{}
	for _, r := range recommendations.Items {
		err := common.ValidateStruct(r)
		if err != nil {
			ctx.GetLogger().Error("recommendation: validation error", "error", err, "data", slog.AnyValue(r))
			return StoreRecommendationResponse{
				Duration: time.Since(t0),
			}, err
		}
		accountObjectId := buildExternalResourceId(account.CloudProvider, account.AccountNumber, r.ResourceRegion, r.ResourceServiceName, r.ResourceType, r.ResourceId, "")
		externalResourceIds = append(externalResourceIds, accountObjectId)
		if r.ExternalResourceId != "" {
			externalResourceIds = append(externalResourceIds, r.ExternalResourceId)
		}
	}

	externalResourceIdMap, err := getExternalIdAndResourceIdMap(ctx, dbms, accountId, externalResourceIds)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch resources", "error", err)
		return StoreRecommendationResponse{
			Duration: time.Since(t0),
		}, err
	}

	// build data to store
	recommendationsToStore := []map[string]any{}
	currentTime := time.Now().UTC().Format(time.RFC3339)
	onConflictKeys := map[string]bool{}
	for _, r := range recommendations.Items {
		dbData := map[string]any{}
		dbData["id"] = uuid.New().String()
		dbData["created_at"] = currentTime
		dbData["updated_at"] = currentTime
		dbData["tenant_id"] = ctx.GetSecurityContext().GetTenantId()
		dbData["cloud_account_id"] = accountId

		if r.Data == nil {
			r.Data = map[string]any{}
		}
		r.Data["cloud_provider"] = strings.ToLower(account.CloudProvider)

		accountObjectId := buildExternalResourceId(account.CloudProvider, account.AccountNumber, r.ResourceRegion, r.ResourceServiceName, r.ResourceType, r.ResourceId, "")

		var resourceId *string
		// Prefer ExternalResourceId (actual resource reference) over accountObjectId for resource linking
		if r.ExternalResourceId != "" {
			if rId, ok := externalResourceIdMap[r.ExternalResourceId]; ok && rId != "" {
				resourceId = &rId
			}
		}
		if resourceId == nil {
			if rId, ok := externalResourceIdMap[accountObjectId]; ok && rId != "" {
				resourceId = &rId
			}
		}
		dbData["resource_id"] = resourceId

		dbData["recommendation"] = "{}"
		r.Data["service_name"] = r.ResourceServiceName
		if len(r.Data) > 0 {
			byetData, err := common.MarshalJson(r.Data)
			if err != nil {
				ctx.GetLogger().Error("unable to marshal recommendation data", "error", err)
				return StoreRecommendationResponse{}, err
			}
			dbData["recommendation"] = string(byetData)
		}

		dbData["recommendation_action"] = providers.RecommendationActionModify
		if r.Action != "" {
			dbData["recommendation_action"] = r.Action
		}

		dbData["severity"] = providers.RecommendationSeverityMedium
		if r.Severity != "" {
			dbData["severity"] = r.Severity
		}

		dbData["estimated_savings"] = r.Savings
		dbData["status"] = "Open"
		dbData["category"] = r.CategoryName
		dbData["rule_name"] = r.RuleName
		dbData["account_object_id"] = accountObjectId

		var dedupeGroup *string
		if r.DedupeGroup != "" {
			dg := r.DedupeGroup
			dedupeGroup = &dg
		}
		dbData["dedupe_group"] = dedupeGroup

		resourceIdStr := ""
		if resourceId != nil {
			resourceIdStr = *resourceId
		}

		key := fmt.Sprintf("%v::%v::%v::%v::%v", accountId, r.RuleName, resourceIdStr, r.CategoryName, accountObjectId)
		if _, ok := onConflictKeys[key]; ok {
			ctx.GetLogger().Warn("recommendation: skipping duplicate recommendation", "key", key, "data", slog.AnyValue(r))
			continue
		}
		recommendationsToStore = append(recommendationsToStore, dbData)
		onConflictKeys[key] = true
	}
	// archive existing recommendations
	_, err = dbms.DoInTransaction(func(dmt common.DatabaseManagerTx) (any, error) {
		query := `update recommendation set status = 'Archive' where cloud_account_id = $1 and (resource_id in (select id from cloud_resourses where lower(service_name) = $2 and account = $1) or lower((recommendation ->> 'service_name')::varchar) = $2 )`

		_, err := dmt.Exec(query, accountId, strings.ToLower(filter.ServiceName))
		if err != nil {
			ctx.GetLogger().Error("unable to archive recommendations", "error", err)
			return nil, err
		}

		insertQuery := `insert into recommendation(id, created_at, updated_at, tenant_id, cloud_account_id, resource_id, recommendation, recommendation_action, severity, estimated_savings, status, category, rule_name, account_object_id, dedupe_group)
						values(:id, :created_at, :updated_at, :tenant_id, :cloud_account_id, :resource_id, :recommendation, :recommendation_action, :severity, :estimated_savings, :status, :category, :rule_name, :account_object_id, :dedupe_group)
						on conflict (cloud_account_id, rule_name, resource_id, category, account_object_id)
						do update set
							recommendation = EXCLUDED.recommendation,
							recommendation_action = EXCLUDED.recommendation_action,
							severity = EXCLUDED.severity,
							estimated_savings = EXCLUDED.estimated_savings,
							status = EXCLUDED.status,
							updated_at = EXCLUDED.updated_at,
							dedupe_group = EXCLUDED.dedupe_group
						`
		_, err = dmt.NamedExec(insertQuery, recommendationsToStore)
		return nil, err
	})

	return StoreRecommendationResponse{
		Duration: time.Since(t0),
		Count:    len(recommendations.Items),
	}, err
}

func getRecommendationsInternal(ctx *security.RequestContext, accountId string, filter providers.ListRecommendationsRequest) (providers.ListRecommendationsResponse, providers.Account, error) {
	ctx.GetLogger().Info("fetching recommendations", "accountId", accountId, "filter", slog.AnyValue(filter))
	account, provider, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch account", "error", err, "accountId", accountId)
		return providers.ListRecommendationsResponse{}, providers.Account{}, err
	}
	cloudProvider, ok := providers.GetProvider(provider)
	if !ok {
		return providers.ListRecommendationsResponse{}, providers.Account{}, fmt.Errorf("provider not found")
	}

	databaseManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("unable to get dbms", "error", err)
		return providers.ListRecommendationsResponse{}, providers.Account{}, err
	}
	query := `select created_at, resourse_id, name, type, status, region, arn, tags::text, meta::text, service_name 
	from cloud_resourses
	where account = $1 and lower(service_name) = $2 and lower(status) = 'active'`
	rows, err := databaseManager.Query(query, accountId, strings.ToLower(filter.ServiceName))
	if err != nil {
		ctx.GetLogger().Error("unable to fetch existing resources", "error", err)
		return providers.ListRecommendationsResponse{}, providers.Account{}, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("unable to close rows", "error", err)
		}
	}()

	existingResources := []providers.Resource{}
	for rows.Next() {
		var createdAt *time.Time
		var resourseId, name, resourceType, status, region, arn, tagsStr, serviceName, metaStr *string

		err := rows.Scan(&createdAt, &resourseId, &name, &resourceType, &status, &region, &arn, &tagsStr, &metaStr, &serviceName)
		if err != nil {
			ctx.GetLogger().Error("unable to scan resources", "error", err)
			return providers.ListRecommendationsResponse{}, providers.Account{}, err
		}
		tags := map[string][]string{}
		if tagsStr != nil {
			err := common.UnmarshalJson([]byte(*tagsStr), &tags)
			if err != nil {
				ctx.GetLogger().Error("unable to parse tags", "error", err, "tags", *tagsStr)
			}
		}

		meta := map[string]any{}
		if metaStr != nil {
			err := common.UnmarshalJson([]byte(*metaStr), &meta)
			if err != nil {
				ctx.GetLogger().Error("unable to parse meta", "error", err, "meta", *metaStr)
			}
		}

		resource := providers.Resource{
			Id:          *resourseId,
			Name:        *name,
			Type:        *resourceType,
			Arn:         *arn,
			ServiceName: *serviceName,
			Status:      providers.ResourceStatus(*status),
			Region:      *region,
			CreatedAt:   *createdAt,
			Tags:        tags,
			Meta:        meta,
		}
		existingResources = append(existingResources, resource)
	}

	err = rows.Close()
	if err != nil {
		ctx.GetLogger().Error("unable to close rows", "error", err)
		return providers.ListRecommendationsResponse{}, providers.Account{}, err
	}

	recommendations, err := cloudProvider.ListRecommendations(ctx, account, filter, existingResources)
	return recommendations, account, err

}

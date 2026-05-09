package recommendation

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/services/account"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/ml"
	"nudgebee/services/observability"
	"nudgebee/services/security"
	"strconv"
	"time"
)

const abandonedResourceNetworkThresholdDefault = 5000
const abandonedResourceObservationPeriodDaysDefault = 7

var runningAccountJobs = map[string]string{}

func clearRecommendationData(ctx *security.RequestContext, dbms *database.DatabaseManager, accountId string, category string, ruleName string) error {
	_, err := dbms.Db.Exec(` update recommendation
	set
		status = $1
	where
		cloud_account_id = $2
		and category = $3
		and rule_name = $4
		and status = $5`, models.RecommendationStatusArchive, accountId, category, ruleName, models.RecommendationStatusOpen)
	if err != nil {
		ctx.GetLogger().Error("error clearing recommendation data", "error", err)
		return err
	}

	return nil
}

func upsertRecommendationData(ctx *security.RequestContext, dbms *database.DatabaseManager, accountId string, data []map[string]any) error {
	if len(data) == 0 {
		return nil
	}
	// Compute finops score for each recommendation before upserting
	for _, d := range data {
		ComputeAndSetFinOpsScoreFields(d)
	}
	_, err := dbms.Db.NamedExec(` INSERT INTO recommendation
		(status, tenant_id, cloud_account_id, recommendation, severity, category, rule_name, estimated_savings, recommendation_action, resource_id, account_object_id, updated_at, finops_score, finops_band, finops_score_breakdown)
		values (:status, :tenant_id, :cloud_account_id, :recommendation, :severity, :category, :rule_name, :estimated_savings, :recommendation_action, :resource_id, :account_object_id, :updated_at, :finops_score, :finops_band, :finops_score_breakdown)
		ON CONFLICT (rule_name, cloud_account_id, resource_id, category, account_object_id)
		DO UPDATE SET recommendation = (EXCLUDED.recommendation), status = (EXCLUDED.status), updated_at = (EXCLUDED.updated_at), estimated_savings = (EXCLUDED.estimated_savings), finops_score = (EXCLUDED.finops_score), finops_band = (EXCLUDED.finops_band), finops_score_breakdown = (EXCLUDED.finops_score_breakdown) `, data)
	if err != nil {
		ctx.GetLogger().Error("error upserting recommendation data", "error", err)
		return err
	}
	return nil
}

func getRecommendationSettings(ctx *security.RequestContext, dbms *database.DatabaseManager, accountId string, recommendationName string) (map[string]string, error) {
	rows, err := dbms.Db.Queryx(`select name, value from cloud_account_attrs where cloud_account_id = $1 and name like $2`, accountId, "recommendations:"+recommendationName+":%")
	if err != nil {
		ctx.GetLogger().Error("error getting recommendation settings", "error", err)
		return nil, err
	}

	defer func() {
		err := rows.Close()
		if err != nil {
			ctx.GetLogger().Error("error closing rows", "error", err)
		}
	}()

	settings := make(map[string]string)
	for rows.Next() {
		var name string
		var value string
		err = rows.Scan(&name, &value)
		if err != nil {
			ctx.GetLogger().Error("error scanning recommendation settings", "error", err)
			return nil, err
		}
		settings[name] = value
	}
	return settings, nil
}

func processAbandonedRecommendations(ctx *security.RequestContext, accountId string, dbms *database.DatabaseManager, metricsProvider string) error {
	runningAccountJobsKey := fmt.Sprintf("abanadoned_resource:%s", accountId)
	if _, ok := runningAccountJobs[runningAccountJobsKey]; ok {
		ctx.GetLogger().Info("job already running for account", "account_id", accountId)
		return nil
	}
	runningAccountJobs[runningAccountJobsKey] = "abanadoned_resource"
	t0 := time.Now()
	defer func() {
		delete(runningAccountJobs, accountId)
		ctx.GetLogger().Info("processAbandonedRecommendations", "time", time.Since(t0))
	}()
	recommendationName := "abandoned_resource"

	settings, err := getRecommendationSettings(ctx, dbms, accountId, recommendationName)
	if err != nil {
		ctx.GetLogger().Error("error getting recommendation settings", "error", err)
		return err
	}
	networkThreshold := abandonedResourceNetworkThresholdDefault
	observationPeriodDays := abandonedResourceObservationPeriodDaysDefault

	if val, ok := settings["network_threshold"]; ok {
		networkThreshold, err = strconv.Atoi(val)
		if err != nil {
			ctx.GetLogger().Error("error converting network threshold to int", "error", err, "value", val)
			networkThreshold = abandonedResourceNetworkThresholdDefault
		}
	}

	if val, ok := settings["observation_days"]; ok {
		observationPeriodDays, err = strconv.Atoi(val)
		if err != nil {
			ctx.GetLogger().Error("error converting observation days to int", "error", err, "value", val)
			observationPeriodDays = abandonedResourceObservationPeriodDaysDefault
		}
	}

	// If the account uses Datadog as metrics provider, sync network metrics first
	if metricsProvider == "datadog" {
		ctx.GetLogger().Info("Syncing Datadog network metrics for abandoned resource detection", "account_id", accountId)
		if syncErr := syncDatadogNetworkMetrics(ctx, dbms, accountId, observationPeriodDays); syncErr != nil {
			ctx.GetLogger().Error("error syncing datadog network metrics", "error", syncErr, "account_id", accountId)
			// Continue anyway — there may be partial data from previous syncs
		}
	}

	ctx.GetLogger().Info("Processing abandoned recommendations", "account_id", accountId)
	err = clearRecommendationData(ctx, dbms, accountId, "RightSizing", recommendationName)
	if err != nil {
		ctx.GetLogger().Error("error clearing abandoned resource recommendations", "error", err)
		return err
	}
	rows, err := dbms.Db.Queryx(fmt.Sprintf(`with TrafficRates as ( select crm.tags ->> 'namespace' as namespace, crm.tags ->> 'controller' as controller, crm.tags ->> 'controllerKind' as kind, AVG(value) as avg_rate, DATE_PART('day', max(timestamp) - min(timestamp)) as date_diff, crm.tenant_id as tenant, crm.cloud_account_id as account, avg(amount) as amount from cloud_resource_metrics crm inner join spends s on s.cloud_account = crm.cloud_account_id and crm.tags ->> 'controllerKind' = s.tags->>'controllerKind' and crm.tags ->> 'controller' = s.tags->>'controller' and crm.tags ->> 'namespace'=s.tags->>'namespace' where metric in ('networkTransferBytes', 'networkReceiveBytes') and timestamp >= NOW() - interval '%d day' and timestamp <= now() group by crm.tags ->> 'namespace', crm.tags ->> 'controller', crm.tags ->> 'controllerKind', crm.tenant_id, crm.cloud_account_id ) select ksw.tenant_id::varchar, ksw.cloud_resource_id::varchar, t.avg_rate, ksw.cloud_account_id::varchar, ksw.name, ksw.namespace, ksw.kind, date_diff, t.amount from TrafficRates t inner join k8s_workloads ksw on t.account = ksw.cloud_account_id and t.tenant = ksw.tenant_id and t.namespace = ksw.namespace and t.controller = ksw.name and t.kind = ksw.kind where ksw.is_active is not false and ksw.cloud_account_id = $2 and ksw.kind not in ('Job', 'Pod', 'DaemonSet') and ksw.namespace not in ('kube-system', 'nudgebee-agent') and t.date_diff > 5 and t.avg_rate < $1 order by t.avg_rate`, observationPeriodDays), networkThreshold, accountId)

	if err != nil {
		ctx.GetLogger().Error("error getting abandoned resources", "error", err)
		return err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			ctx.GetLogger().Error("error closing rows", "error", err)
		}
	}()

	abandonedResources := make([]map[string]any, 0)
	for rows.Next() {
		d := make(map[string]any)
		err = rows.MapScan(d)
		if err != nil {
			ctx.GetLogger().Error("error scanning abandoned resources", "error", err)
			return err
		}
		abandonedResources = append(abandonedResources, d)
	}

	if len(abandonedResources) == 0 {
		ctx.GetLogger().Info("No abandoned resources found")
		return nil
	}

	recommendations := make([]map[string]any, 0)
	for _, row := range abandonedResources {
		cloudResourceId := row["cloud_resource_id"].(string)
		avgRate := row["avg_rate"].(float64)
		amount := row["amount"].(float64)
		r := map[string]any{
			"message":   fmt.Sprintf("network traffic %f in last %d days is less than threshold %d", avgRate, observationPeriodDays, networkThreshold),
			"traffic":   avgRate,
			"threshold": networkThreshold,
			"window":    fmt.Sprintf("%d DAY", observationPeriodDays),
		}
		jsonStr, err := common.MarshalJson(r)
		if err != nil {
			ctx.GetLogger().Error("error marshalling recommendation", "error", r)
			return err
		}

		recommendation := map[string]any{
			"tenant_id":             row["tenant_id"].(string),
			"recommendation":        string(jsonStr),
			"cloud_account_id":      accountId,
			"resource_id":           cloudResourceId,
			"category":              "RightSizing",
			"rule_name":             recommendationName,
			"severity":              "Medium",
			"estimated_savings":     amount * 30,
			"account_object_id":     cloudResourceId,
			"status":                models.RecommendationStatusOpen,
			"recommendation_action": "Modify",
			"updated_at":            time.Now(),
		}
		recommendations = append(recommendations, recommendation)
	}

	err = upsertRecommendationData(ctx, dbms, accountId, recommendations)
	if err != nil {
		ctx.GetLogger().Error("error upserting abandoned resource recommendations", "error", err)
		return err
	}

	return nil
}

func processSpotInstanceRecommendations(ctx *security.RequestContext, accountId string, dbms *database.DatabaseManager) error {
	runningAccountJobsKey := fmt.Sprintf("spot_instance_recommendation:%s", accountId)
	if _, ok := runningAccountJobs[runningAccountJobsKey]; ok {
		ctx.GetLogger().Info("job already running for account", "account_id", accountId)
		return nil
	}
	runningAccountJobs[runningAccountJobsKey] = "spot_instance_recommendation"

	t0 := time.Now()
	defer func() {
		delete(runningAccountJobs, runningAccountJobsKey)
		ctx.GetLogger().Info("processSpotInstanceRecommendations", "time", time.Since(t0))
	}()

	ctx.GetLogger().Info("Processing spot recommendations", "account_id", accountId)

	err := clearRecommendationData(ctx, dbms, accountId, "K8sSpotRecommendation", "Spot instance recommendation")
	if err != nil {
		ctx.GetLogger().Error("error clearing spot instance recommendations", "error", err)
		return err
	}

	rows, err := dbms.Db.Queryx(`select cr2.cloud_account_id::varchar , cr2.cloud_resource_id::varchar, cr2."name" as controller_name, cr2.namespace as namespace, cr2.kind as type, ( avg(s.amount) * 30 ) - ( avg(s.amount) * 30 * .10 ) as "estimated_saving", max(cr2.meta ->> 'total_pods'::text) as "total_pods", string_agg(distinct case when (intnc.meta -> 'node_info' -> 'labels' ->> 'karpenter.sh/capacity-type'::text) is not null then intnc.meta -> 'node_info' -> 'labels' ->> 'karpenter.sh/capacity-type'::text when (intnc.meta -> 'node_info' -> 'labels' ->> 'eks.amazonaws.com/capacityType'::text) is not null then intnc.meta -> 'node_info' -> 'labels' ->> 'eks.amazonaws.com/capacityType'::text else 'on-demand'::text end, ',') as node_type, string_agg(distinct (intnc.meta -> 'node_info' -> 'labels'::text) ->>'node.kubernetes.io/instance-type'::text, ',') as node_flavor, avg(crd.resource_cost) as instance_cost_on_demand from k8s_pods cr left join k8s_workloads cr2 on cr.meta ->>'controller'::text = cr2."name" and cr.meta ->> 'namespace' = cr2.namespace and cr2.cloud_account_id = cr.cloud_account_id left join k8s_nodes intnc on intnc.tenant_id = cr.tenant_id and intnc.cloud_account_id = cr.cloud_account_id and (cr.meta ->> 'node'::text) = intnc.name left join spends s on s.cloud_account = cr.cloud_account_id and s.cloud_resource_id = cr.cloud_resource_id left join cloud_resource_details crd on crd.resource_type = (((intnc.meta ->'node_info'::text) -> 'labels'::text) ->> 'node.kubernetes.io/instance-type'::text) and crd.resource_region = (((intnc.meta -> 'node_info'::text) -> 'labels'::text) ->> 'topology.kubernetes.io/region'::text) where cr.meta ->> 'controllerKind' in ('ReplicaSet', 'Deployment', 'Rollout') and cr2.is_active is not false and cr.is_active is not false and (cr2.meta ->> 'total_pods'::text):: int > 1 and cr.meta ->> 'namespace' not in ('kube-system', 'nudgebee-agent') and (cr.meta ->> 'node'::text) is not null and lower(case when (intnc.meta -> 'node_info' -> 'labels' ->> 'karpenter.sh/capacity-type'::text) is not null then intnc.meta -> 'node_info' -> 'labels' ->> 'karpenter.sh/capacity-type'::text when (intnc.meta -> 'node_info' -> 'labels' ->> 'eks.amazonaws.com/capacityType'::text) is not null then intnc.meta -> 'node_info' -> 'labels' ->>'eks.amazonaws.com/capacityType'::text else 'on-demand'::text end) != 'spot' and cr2.cloud_account_id = $1 group by cr2.cloud_resource_id, cr2.cloud_account_id, cr2.namespace, cr2.kind, cr.meta ->> 'namespace', cr2.name`, accountId)

	if err != nil {
		ctx.GetLogger().Error("error getting spot instance recommendations for pods", "error", err)
		return err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			ctx.GetLogger().Error("error closing rows", "error", err)
		}
	}()

	eligiblePods := make([]map[string]any, 0)
	for rows.Next() {
		d := make(map[string]any)
		err = rows.MapScan(d)
		if err != nil {
			ctx.GetLogger().Error("error scanning spot instance recommendations", "error", err)
			return err
		}
		eligiblePods = append(eligiblePods, d)
	}

	rows, err = dbms.Db.Queryx(`WITH pod_info AS ( SELECT cr.tenant_id, cr.cloud_account_id, cr.meta -> 'config' -> 'labels' ->> 'job-name' AS job_name, cr.meta ->> 'namespace'::text AS namespace, cr.meta ->>'node'::text AS node, cr.meta -> 'config' -> 'labels' -> 'job-name' AS label_job_name, cr.last_seen, COALESCE( cr2.meta -> 'job_data' -> 'parents' ->> 0, cr.meta -> 'config' -> 'labels' ->> 'job-name' ) AS controller_name, COALESCE(cr2.cloud_resource_id, cr.cloud_resource_id) AS resource_id FROM k8s_pods cr LEFT JOIN k8s_workloads cr2 ON cr2."name" = cr.meta -> 'config' -> 'labels' ->> 'job-name' AND cr2.meta ->> 'namespace' = cr.meta ->> 'namespace' AND cr2.tenant_id = cr.tenant_id AND cr2.cloud_account_id = cr.cloud_account_id where cr.meta -> 'config' -> 'labels' ->> 'job-name' is not null and cr.cloud_account_id = $1 ) SELECT pod_info.controller_name, pod_info.namespace, 0 AS estimated_saving, pod_info.cloud_account_id, pod_info.tenant_id, max(pod_info.resource_id::text) AS resource_id, 'Job' AS type FROM pod_info INNER JOIN k8s_nodes intnc ON intnc.tenant_id = pod_info.tenant_id AND intnc.cloud_account_id = pod_info.cloud_account_id AND pod_info.node = intnc.name WHERE pod_info.job_name IS NOT NULL AND pod_info.last_seen > NOW() - INTERVAL '7 day' GROUP BY pod_info.controller_name, pod_info.namespace, pod_info.cloud_account_id, pod_info.tenant_id`, accountId)

	if err != nil {
		ctx.GetLogger().Error("error getting spot instance recommendations for jobs", "error", err)
		return err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			ctx.GetLogger().Error("error closing rows", "error", err)
		}
	}()

	eligibleJobs := make([]map[string]any, 0)
	for rows.Next() {
		d := make(map[string]any)
		err = rows.MapScan(d)
		if err != nil {
			ctx.GetLogger().Error("error scanning spot instance recommendations for jobs", "error", err)
			return err
		}
		eligibleJobs = append(eligibleJobs, d)
	}

	recommendations := make([]map[string]any, 0)

	acc, err := account.GetAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("error getting account details", "error", err)
		return err
	}

	for _, resource := range eligiblePods {
		resourceJson, err := common.MarshalJson(resource)
		if err != nil {
			ctx.GetLogger().Error("error marshalling spot instance recommendations", "error", err)
			return err
		}
		recommendation := map[string]any{
			"tenant_id":             acc.Tenant,
			"recommendation":        string(resourceJson),
			"cloud_account_id":      accountId,
			"resource_id":           resource["cloud_resource_id"],
			"category":              "K8sSpotRecommendation",
			"rule_name":             "Spot instance recommendation",
			"severity":              "Medium",
			"estimated_savings":     resource["estimated_saving"],
			"account_object_id":     resource["cloud_resource_id"],
			"status":                models.RecommendationStatusOpen,
			"recommendation_action": "Modify",
			"updated_at":            time.Now(),
		}
		recommendations = append(recommendations, recommendation)
	}

	for _, job := range eligibleJobs {
		jobJson, err := common.MarshalJson(job)
		if err != nil {
			ctx.GetLogger().Error("error marshalling spot instance recommendations for jobs", "error", err)
			return err
		}
		recommendation := map[string]any{
			"tenant_id":             acc.Tenant,
			"recommendation":        string(jobJson),
			"cloud_account_id":      accountId,
			"category":              "K8sSpotRecommendation",
			"rule_name":             "Spot instance recommendation",
			"severity":              "Medium",
			"estimated_savings":     job["estimated_saving"],
			"account_object_id":     job["resource_id"],
			"resource_id":           job["resource_id"],
			"status":                models.RecommendationStatusOpen,
			"recommendation_action": "Modify",
			"updated_at":            time.Now(),
		}
		recommendations = append(recommendations, recommendation)
	}

	err = upsertRecommendationData(ctx, dbms, accountId, recommendations)
	if err != nil {
		ctx.GetLogger().Error("error upserting spot instance recommendations", "error", err)
		return err
	}

	return nil
}

func processHpaRecommendations(ctx *security.RequestContext, accountId string, dbms *database.DatabaseManager) error {
	return nil
}

func processImageScanner(ctx *security.RequestContext, accountId string, dbms *database.DatabaseManager) error {
	rows, err := dbms.Db.Queryx(`
	SELECT name, value, cloud_account_id 
	FROM public.cloud_account_attrs 
	where cloud_account_id::varchar = $1 and name in ('enable_image_scan')`, accountId)

	if err != nil {
		ctx.GetLogger().Error("error getting image scanner recommendations", "error", err)
		return err
	}

	defer func() {
		err := rows.Close()
		if err != nil {
			ctx.GetLogger().Error("error closing rows", "error", err)
		}
	}()

	enableImageScan := true
	for rows.Next() {
		var name string
		var value string
		var cloudAccountId string
		err = rows.Scan(&name, &value, &cloudAccountId)
		if err != nil {
			ctx.GetLogger().Error("error scanning image scanner recommendations", "error", err)
			return err
		}
		if value == "false" {
			enableImageScan = true
			break
		}
	}

	if !enableImageScan {
		ctx.GetLogger().Info("Image scanner is not enabled for account", "account_id", accountId)
		return nil
	}

	rows, err = dbms.Db.Queryx(`
		WITH excluded_images AS (
			SELECT r.recommendation->>'image_name'::text AS image_name
			FROM recommendation r
			WHERE r.cloud_account_id = $1
			  AND r.category = 'Security' AND r.rule_name = 'image_scan'
			  AND r.account_object_id IS NOT NULL
			UNION
			SELECT at2.payload->'action_params'->>'image_name'
			FROM agent_task at2
			WHERE at2.cloud_account_id = $1
			  AND at2.payload->>'action_name' = 'image_scanner'
		)
		SELECT DISTINCT ON (container->>'image')
			container->>'image' as image,
			cr.cloud_account_id,
			cr.tenant_id,
			cr.name,
			cr.meta->>'namespace' as namespace,
			cr.workload_type as kind
		FROM k8s_pods cr
		CROSS JOIN LATERAL jsonb_array_elements(cr.meta->'config'->'containers') as container
		WHERE cr.is_active IS NOT FALSE
			AND cr.cloud_account_id = $1
			AND cr.status = 'Running'
			AND cr.meta->>'namespace' NOT IN ('kube-system', 'nudgebee-agent')
			AND cr.workload_type != 'Job'
			AND container->>'image' NOT IN (SELECT image_name FROM excluded_images WHERE image_name IS NOT NULL)
		LIMIT 5
	`, accountId)

	if err != nil {
		ctx.GetLogger().Error("error getting image scanner recommendations", "error", err)
		return err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()

	pendingImages := make([]map[string]any, 0)
	for rows.Next() {
		d := make(map[string]any)
		err = rows.MapScan(d)
		if err != nil {
			ctx.GetLogger().Error("error scanning image scanner recommendations", "error", err)
			return err
		}
		pendingImages = append(pendingImages, d)
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
			return err
		}
		task := map[string]any{
			"cloud_account_id": accountId,
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
		return nil
	}

	_, err = dbms.Db.NamedExec(`INSERT INTO agent_task (cloud_account_id, tenant, action, payload, status, source)
		VALUES (:cloud_account_id, :tenant, :action, :payload, :status, :source)
		ON CONFLICT (cloud_account_id, (payload->'action_params'->>'image_name')) WHERE action = 'image_scanner'
		DO NOTHING`, tasks)
	if err != nil {
		ctx.GetLogger().Error("error inserting image scanner tasks", "error", err)
		return err
	}

	return nil
}

func processHealthCheckRecommendations(ctx *security.RequestContext, accountId string, dbms *database.DatabaseManager) error {
	runningAccountJobsKey := fmt.Sprintf("healthcheck:%s", accountId)
	if _, ok := runningAccountJobs[runningAccountJobsKey]; ok {
		ctx.GetLogger().Info("job already running for account", "account_id", accountId)
		return nil
	}
	runningAccountJobs[runningAccountJobsKey] = "healthcheck"
	t0 := time.Now()
	defer func() {
		delete(runningAccountJobs, runningAccountJobsKey)
		ctx.GetLogger().Info("processHealthCheckRecommendations", "time", time.Since(t0))
	}()

	recommendationName := "health_check"

	ctx.GetLogger().Info("Processing health check recommendations", "account_id", accountId)

	err := clearRecommendationData(ctx, dbms, accountId, "Configuration", recommendationName)
	if err != nil {
		ctx.GetLogger().Error("error clearing health check recommendations", "error", err)
		return err
	}

	query := `
WITH workload_events AS (
    SELECT
        e.subject_name,
        e.subject_namespace,
        e.cluster,
        COUNT(*) FILTER (WHERE e.finding_type IN ('Unhealthy', 'CrashLoopBackOff', 'LivenessProbeFailure', 'ReadinessProbeFailure')) AS unhealthy_events
    FROM events e
    WHERE e.cloud_account_id = $1
      AND e.starts_at >= NOW() - INTERVAL '7 days'
    GROUP BY e.subject_name, e.subject_namespace, e.cluster
)
SELECT
    ksw.tenant_id::varchar,
    ksw.cloud_resource_id::varchar,
    ksw.cloud_account_id::varchar,
    ksw.name,
    ksw.namespace,
    ksw.kind,
    ksw.meta,
    ksw.labels,
    we.unhealthy_events
FROM k8s_workloads ksw
LEFT JOIN workload_events we
ON ksw.name = we.subject_name AND ksw.namespace = we.subject_namespace
WHERE ksw.is_active IS TRUE
  AND ksw.cloud_account_id = $1
  AND ksw.namespace NOT IN ('kube-system', 'nudgebee-agent')
`
	rows, err := dbms.Db.Queryx(query, accountId)
	if err != nil {
		ctx.GetLogger().Error("error querying workloads for health check recommendations", "error", err)
		return err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()

	workloads := make([]map[string]any, 0)
	for rows.Next() {
		d := make(map[string]any)
		err = rows.MapScan(d)
		if err != nil {
			ctx.GetLogger().Error("error scanning workload data", "error", err)
			return err
		}
		workloads = append(workloads, d)
	}

	if len(workloads) == 0 {
		ctx.GetLogger().Info("No workloads found for health check recommendations")
		return nil
	}

	recommendations := make([]map[string]any, 0)
	for _, row := range workloads {
		cloudResourceId := row["cloud_resource_id"].(string)
		workloadName := row["name"].(string)
		namespace := row["namespace"].(string)
		kind := row["kind"].(string)
		meta := row["meta"].([]byte)
		unhealthyEvents := int64(0)
		if row["unhealthy_events"] != nil {
			unhealthyEvents = row["unhealthy_events"].(int64)
		}

		var metaMap map[string]interface{}
		if err := common.UnmarshalJson(meta, &metaMap); err != nil {
			ctx.GetLogger().Error("error unmarshalling meta", "error", err)
			continue
		}

		livenessProbeConfigured := false
		readinessProbeConfigured := false

		if config, ok := metaMap["config"].(map[string]interface{}); ok {
			if containers, ok := config["containers"].([]interface{}); ok {
				for _, c := range containers {
					if containerMap, ok := c.(map[string]interface{}); ok {
						if _, ok := containerMap["liveness_probe"]; ok {
							livenessProbeConfigured = true
						}
						if _, ok := containerMap["readiness_probe"]; ok {
							readinessProbeConfigured = true
						}
					}
				}
			}
		}

		messages := []string{}
		if !livenessProbeConfigured {
			messages = append(messages, "Liveness probe not configured")
		}
		if !readinessProbeConfigured {
			messages = append(messages, "Readiness probe not configured")
		}

		if (livenessProbeConfigured || readinessProbeConfigured) && unhealthyEvents > 3 {
			messages = append(messages, "High number of health check failures detected, consider adjusting probe timings (initialDelaySeconds, timeoutSeconds, periodSeconds)")
		}

		if len(messages) == 0 {
			continue
		}

		recommendationDetail := map[string]any{
			"messages":        messages,
			"unhealthyEvents": unhealthyEvents,
			"workload": map[string]string{
				"name":      workloadName,
				"namespace": namespace,
				"kind":      kind,
			},
		}

		jsonStr, err := common.MarshalJson(recommendationDetail)
		if err != nil {
			ctx.GetLogger().Error("error marshalling health check recommendation", "error", err)
			continue
		}

		recommendation := map[string]any{
			"tenant_id":             row["tenant_id"].(string),
			"recommendation":        string(jsonStr),
			"cloud_account_id":      accountId,
			"resource_id":           cloudResourceId,
			"category":              "Configuration",
			"rule_name":             recommendationName,
			"severity":              "Medium",
			"estimated_savings":     0,
			"account_object_id":     cloudResourceId,
			"status":                models.RecommendationStatusOpen,
			"recommendation_action": "Modify",
			"updated_at":            time.Now(),
		}
		recommendations = append(recommendations, recommendation)
	}

	if len(recommendations) == 0 {
		ctx.GetLogger().Info("No health check recommendations to insert")
		return nil
	}

	err = upsertRecommendationData(ctx, dbms, accountId, recommendations)
	if err != nil {
		ctx.GetLogger().Error("error upserting health check recommendations", "error", err)
		return err
	}

	return nil
}

func GenerateRecommendation(ctx *security.RequestContext, request GenerateRecommendationRequest) (GenerateRecommendationResponse, error) {
	t0 := time.Now()
	defer func() {
		ctx.GetLogger().Info("GenerateRecommendation", "time", time.Since(t0))
	}()
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return GenerateRecommendationResponse{}, err
	}

	type accountInfo struct {
		Id       string
		TenantId string
	}
	var accountList []accountInfo

	if len(request.AccountId) == 0 {
		rows, err := dbms.Db.Queryx(`select ca.id::varchar, ca.tenant::varchar
			from cloud_accounts ca
			where ca.status = 'active' and account_type = 'kubernetes'
		`)
		if err != nil {
			ctx.GetLogger().Error("error getting account ids", "error", err)
			return GenerateRecommendationResponse{}, err
		}
		defer func() {
			err := rows.Close()
			if err != nil {
				slog.Error("Failed to close rows", "error", err)
			}
		}()

		for rows.Next() {
			var acc accountInfo
			err = rows.Scan(&acc.Id, &acc.TenantId)
			if err != nil {
				ctx.GetLogger().Error("error scanning account ids", "error", err)
				return GenerateRecommendationResponse{}, err
			}
			accountList = append(accountList, acc)
		}
	} else {
		for _, id := range request.AccountId {
			var tenantId string
			_ = dbms.Db.Get(&tenantId, `SELECT tenant::varchar FROM cloud_accounts WHERE id::varchar = $1`, id)
			accountList = append(accountList, accountInfo{Id: id, TenantId: tenantId})
		}
	}

	for _, acc := range accountList {
		accountId := acc.Id

		// Create a tenant-scoped context for each account so that downstream
		// queries (e.g. GetIntegrationByConfigNameValues) have a valid tenant ID.
		accountCtx := ctx
		if acc.TenantId != "" && ctx.GetSecurityContext().GetTenantId() == "" {
			accountCtx = security.NewRequestContext(
				ctx.GetContext(),
				security.NewSecurityContextForSuperAdminAndTenant(acc.TenantId),
				ctx.GetLogger(),
				ctx.GetTracer(),
				ctx.GetMeter(),
			)
		}

		response, err := dbms.Db.Queryx("select id from agent where last_connected_at > now() - interval '1 DAY' and cloud_account_id= $1", accountId)

		if err != nil {
			if err == sql.ErrNoRows {
				ctx.GetLogger().Info("No active agent found for account", "account_id", accountId)
			}
			return GenerateRecommendationResponse{}, err
		}
		defer func() {
			err := response.Close()
			if err != nil {
				ctx.GetLogger().Error("error closing response", "error", err)
			}
		}()

		count := 0
		for response.Next() {
			count++
		}
		if count == 0 {
			ctx.GetLogger().Info("No active agent found for account", "account_id", accountId)
			continue
		}

		ctx.GetLogger().Info("Processing recommendations for account", "account_id", accountId)

		// Determine metrics provider for this account
		metricsProvider, _, _ := observability.GetLogsMetricsTracesProvider(accountCtx, accountId, "", "metrics", "")

		err = processAbandonedRecommendations(accountCtx, accountId, dbms, metricsProvider)
		if err != nil {
			accountCtx.GetLogger().Error("error processing abandoned recommendations", "error", err)
		}
		err = processSpotInstanceRecommendations(accountCtx, accountId, dbms)
		if err != nil {
			accountCtx.GetLogger().Error("error processing spot instance recommendations", "error", err)
		}
		err = processHpaRecommendations(accountCtx, accountId, dbms)
		if err != nil {
			accountCtx.GetLogger().Error("error processing hpa recommendations", "error", err)
		}
		err = processImageScanner(accountCtx, accountId, dbms)
		if err != nil {
			accountCtx.GetLogger().Error("error processing image scanner recommendations", "error", err)
		}
		err = processHealthCheckRecommendations(accountCtx, accountId, dbms)
		if err != nil {
			accountCtx.GetLogger().Error("error processing health check recommendations", "error", err)
		}

		// Trigger PV rightsizing via ml-k8s-server for Datadog accounts
		// Non-Datadog accounts are handled by the K8s agent pipeline
		if metricsProvider == "datadog" {
			apiKey, appKey, site, ddErr := integrations.GetDatadogConfigs(accountCtx, accountId)
			if ddErr != nil {
				accountCtx.GetLogger().Error("error getting datadog configs for volume rightsizing", "error", ddErr, "account_id", accountId)
			} else {
				_, ddErr = ml.TriggerVolumeRightsizing(accountCtx, ml.VolumeRightsizingRequest{
					AccountId:             accountId,
					TenantId:              acc.TenantId,
					PersistRecommendation: true,
					MetricsProvider:       metricsProvider,
					DatadogApiKey:         apiKey,
					DatadogAppKey:         appKey,
					DatadogSite:           site,
				})
				if ddErr != nil {
					accountCtx.GetLogger().Error("error triggering volume rightsizing for datadog account", "error", ddErr, "account_id", accountId)
				}
			}
		}
	}
	return GenerateRecommendationResponse{}, err
}

func GenerateSecurityRecommendation(ctx *security.RequestContext, request GenerateRecommendationRequest) (GenerateRecommendationResponse, error) {
	t0 := time.Now()
	defer func() {
		ctx.GetLogger().Info("GenerateRecommendation", "time", time.Since(t0))
	}()
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return GenerateRecommendationResponse{}, err
	}
	if len(request.AccountId) == 0 {
		accounts := make([]string, 0)
		rows, err := dbms.Db.Queryx(`select ca.id::varchar, ca.account_name
			from cloud_accounts ca 
			where ca.status = 'active' and account_type = 'kubernetes'
		`)
		if err != nil {
			ctx.GetLogger().Error("error getting account ids", "error", err)
			return GenerateRecommendationResponse{}, err
		}
		defer func() {
			err := rows.Close()
			if err != nil {
				slog.Error("Failed to close rows", "error", err)
			}
		}()

		for rows.Next() {
			var id string
			var name string
			err = rows.Scan(&id, &name)
			if err != nil {
				ctx.GetLogger().Error("error scanning account ids", "error", err)
				return GenerateRecommendationResponse{}, err
			}
			accounts = append(accounts, id)
		}
		request.AccountId = accounts
	}

	for _, accountId := range request.AccountId {
		response, err := dbms.Db.Queryx("select id from agent where last_connected_at > now() - interval '1 DAY' and cloud_account_id= $1", accountId)

		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				ctx.GetLogger().Debug("No active agent found for account", "account_id", accountId)
			}
			return GenerateRecommendationResponse{}, err
		}
		defer func() {
			err := response.Close()
			if err != nil {
				ctx.GetLogger().Error("error closing response", "error", err)
			}
		}()

		count := 0
		for response.Next() {
			count++
		}
		if count == 0 {
			ctx.GetLogger().Info("No active agent found for account", "account_id", accountId)
			continue
		}

		ctx.GetLogger().Info("Processing Security recommendations for account", "account_id", accountId)

		err = processImageScanner(ctx, accountId, dbms)
		if err != nil {
			ctx.GetLogger().Error("error processing image scanner recommendations", "error", err)
		}
	}
	return GenerateRecommendationResponse{}, err
}

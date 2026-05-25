package cloud

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/samber/lo"
)

func init() {
	playbooks.RegisterAction("cloud_resource", &cloudResourceAction{})
	playbooks.RegisterAction("cloud_metrics", &cloudMetricsAction{})
	playbooks.RegisterAction("cloud_logs", &cloudLogAction{})
	playbooks.RegisterAction("cloud_service_map", &cloudServiceMapAction{})
	playbooks.RegisterAction("cloud_performance_insights", &cloudPerformanceInsightsAction{})
	playbooks.RegisterAction("cloud_azure_activity_log", &cloudAzureActivityLogAction{})
	playbooks.RegisterAction("cloud_azure_alert_rule", &cloudAzureAlertRuleAction{})
	playbooks.RegisterAction("cloud_azure_service_health", &cloudAzureServiceHealthAction{})
	playbooks.RegisterAction("cloud_azure_related_alerts", &cloudAzureRelatedAlertsAction{})
	playbooks.RegisterAction("cloud_azure_kql_query_results", &cloudAzureKQLQueryResultsAction{})
	playbooks.RegisterAction("cloud_azure_dcr_info", &cloudAzureDCRInfoAction{})
	playbooks.RegisterAction("cloud_vpc_flowlogs", &cloudVpcFlowLogsAction{})
	playbooks.RegisterAction("cloud_cli", &cloudCliAction{})
}

// getCloudAccountIdByNumber fetches the cloud account ID from the database using account number and tenant ID
// This helper eliminates duplicate code across multiple action handlers (cloudCliAction, cloudResourceAction, etc.)
func getCloudAccountIdByNumber(accountNumber, tenantId string) (string, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", err
	}
	var accountId string
	err = dbms.Db.QueryRowx(
		"SELECT id FROM cloud_accounts WHERE account_number = $1 AND tenant = $2 AND status = 'active'",
		accountNumber, tenantId,
	).Scan(&accountId)
	return accountId, err
}

// lookupResourcesFromDB fetches resources directly from the cloud_resourses table.
// This is the fast path for cloud_resource auto-discovery: avoids the HTTP→cloud-collector→AWS API
// round-trip that was costing ~12 sec per call and starving the SQS consumer goroutine.
// Returns an empty slice when no rows match — callers should treat that as "not in cache".
func lookupResourcesFromDB(tenantId, accountId, serviceName, region string, resourceIds []string) ([]Resource, error) {
	if tenantId == "" || accountId == "" || serviceName == "" {
		return nil, nil
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	type row struct {
		ResourseId  sql.NullString `db:"resourse_id"`
		Name        sql.NullString `db:"name"`
		Type        sql.NullString `db:"type"`
		Arn         sql.NullString `db:"arn"`
		ServiceName sql.NullString `db:"service_name"`
		Status      sql.NullString `db:"status"`
		Region      sql.NullString `db:"region"`
		Tags        []byte         `db:"tags"`
		Meta        []byte         `db:"meta"`
		CreatedAt   sql.NullTime   `db:"created_at"`
	}

	args := []any{tenantId, accountId, serviceName}
	whereClauses := []string{
		"tenant = $1",
		"account = $2",
		"lower(service_name) = lower($3)",
	}
	if region != "" {
		args = append(args, region)
		whereClauses = append(whereClauses, fmt.Sprintf("region = $%d", len(args)))
	}
	if len(resourceIds) > 0 {
		args = append(args, pq.Array(resourceIds))
		whereClauses = append(whereClauses, fmt.Sprintf("(resourse_id = ANY($%d) OR external_resource_id = ANY($%d) OR arn = ANY($%d))", len(args), len(args), len(args)))
	}

	query := fmt.Sprintf(`SELECT resourse_id, name, type, arn, service_name, status, region, tags, meta, created_at
		FROM cloud_resourses
		WHERE %s
		ORDER BY is_active DESC, last_seen DESC
		LIMIT 100`, strings.Join(whereClauses, " AND "))

	var rows []row
	if err := dbms.Db.Select(&rows, query, args...); err != nil {
		return nil, err
	}

	out := make([]Resource, 0, len(rows))
	for _, r := range rows {
		res := Resource{
			Id:          r.ResourseId.String,
			Name:        r.Name.String,
			Type:        r.Type.String,
			Arn:         r.Arn.String,
			ServiceName: r.ServiceName.String,
			Status:      ResourceStatus(r.Status.String),
			Region:      r.Region.String,
		}
		if r.CreatedAt.Valid {
			res.CreatedAt = r.CreatedAt.Time
		}
		if len(r.Tags) > 0 {
			_ = common.UnmarshalJson(r.Tags, &res.Tags)
		}
		if len(r.Meta) > 0 {
			_ = common.UnmarshalJson(r.Meta, &res.Meta)
		}
		out = append(out, res)
	}
	return out, nil
}

// buildCloudResourceResponse renders the playbook action response for a list of cloud resources.
// Shared between AutoExecute (DB fast-path) and Execute (HTTP path) so both produce the same
// label set and metadata shape — keeps downstream consumers unchanged.
func buildCloudResourceResponse(items []Resource, rawParams map[string]any) playbooks.PlaybookActionResponse {
	if len(items) == 0 {
		return nil
	}
	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}
	labels := map[string]any{}
	first := items[0]
	labels["resource_arn"] = first.Arn
	labels["resource_name"] = first.Name
	labels["resource_region"] = first.Region
	labels["resource_service_name"] = first.ServiceName
	labels["resource_type"] = first.Type
	labels["resource_id"] = first.Id
	for k, v := range first.Meta {
		if v == nil {
			continue
		}
		switch k {
		case "PrivateDnsName":
			labels["resource_private_dns_name"] = v
		case "PrivateIpAddress":
			labels["resource_private_ip_address"] = v
		case "PublicDnsName":
			labels["resource_public_dns"] = v
		case "PublicIpAddress":
			labels["resource_public_ip_address"] = v
		case "SubnetId":
			labels["resource_subnet_id"] = v
		case "VpcId":
			labels["resource_vpc_id"] = v
		}
	}
	additionalInfo := map[string]any{
		"action_name":        "cloud_resources",
		"actual_action_name": "cloud_resources",
	}
	if title, ok := rawParams["title"]; ok {
		additionalInfo["title"] = title
	} else {
		additionalInfo["title"] = "Resource Details"
	}
	return playbooks.NewPlaybookActionResponseJsonWithLabels(items, additionalInfo, []playbooks.PlaybookActionResponseInsight{}, metadata, labels)
}

// getVpcIdForResource looks up the VpcId from cloud_resourses meta for a given AWS resource ID.
// Uses regex to find "VpcId" at any depth in the meta JSON, so it works for any AWS resource type
// (EC2, RDS, ALB, Lambda, etc.) without hardcoding JSON paths per resource type.
// Returns empty string if not found.
func getVpcIdForResource(resourceId, tenantId string) string {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return ""
	}
	var vpcId string
	err = dbms.Db.QueryRowx(
		`SELECT (regexp_match(meta::text, '"VpcId":\s*"(vpc-[a-f0-9]+)"'))[1]
		 FROM cloud_resourses
		 WHERE resourse_id = $1 AND tenant = $2 AND cloud_provider = 'AWS'
		 AND meta::text ~ '"VpcId":\s*"vpc-'
		 LIMIT 1`,
		resourceId, tenantId,
	).Scan(&vpcId)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("getVpcIdForResource: failed to query cloud_resourses", "resourceId", resourceId, "error", err)
		}
		return ""
	}
	return vpcId
}

type cloudResourceAction struct {
}

type cloudResourceActionParams struct {
	AccountId    string   `json:"account_id,omitempty"`
	ResourceId   string   `json:"resource_id,omitempty"`
	ResourceIds  []string `json:"resource_ids,omitempty"`
	InstanceId   string   `json:"instance_id,omitempty"`
	InstanceIds  []string `json:"instance_ids,omitempty"`
	ResourceType string   `json:"resource_type" validate:"required"`
	ServiceName  string   `json:"service_name" validate:"required"`
	Region       string   `json:"region" validate:"required"`
}

func (a *cloudResourceAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	labels := ctx.GetEvent().Labels

	// AWS
	if labels["aws_region"] != "" && labels["aws_event_instance"] != "" && labels["aws_service_name"] != "" {
		return true
	}

	// Azure Monitor Alert (polling-based or webhook)
	// Allow when service_name is known or target resource is a real resource (has /providers/)
	if isAzureAlertSource(ctx.GetEvent().Source) && ctx.GetEvent().Labels["azure_alert_target_resource"] != "" {
		if ctx.GetEvent().Labels["azure_service_name"] != "" || isAzureResourceID(ctx.GetEvent().Labels["azure_alert_target_resource"]) {
			return true
		}
	}

	// GCP
	if labels["gcp_region"] != "" && labels["gcp_event_instance"] != "" && labels["gcp_service_name"] != "" {
		return true
	}

	return false
}

func (a *cloudResourceAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	// Auto-discovery used to call /v1/cloud/get_resources on cloud-collector for every event,
	// which forwarded to live AWS/GCP/Azure APIs. That single endpoint took 2-12 sec per call
	// (some services fall back to full account enumeration) and starved the SQS consumer
	// goroutine on the same pod. Auto-discovery now serves resources directly from the
	// cloud_resourses table — populated by the periodic resource sync + EventBridge updates.
	// On a cache miss we return nil (skip the auto-action) rather than fall through to a live
	// call: explicit `cloud_resource` playbook steps still hit the live path via Execute.

	// Azure Monitor Alert (polling-based or webhook)
	if isAzureAlertSource(ctx.GetEvent().Source) && ctx.GetEvent().Labels["azure_alert_target_resource"] != "" {
		targetResource := ctx.GetEvent().Labels["azure_alert_target_resource"]
		serviceName := ctx.GetEvent().Labels["azure_service_name"]

		if serviceName == "" {
			serviceName = getAzureResourceType(targetResource)
		}

		region := ctx.GetEvent().Labels["azure_region"]
		if region == "" {
			region = extractResourceGroup(targetResource)
		}

		rawParams := map[string]any{
			"resource_ids": []string{targetResource},
			"region":       region,
			"service_name": serviceName,
			"title":        "Alert Rule Details For - " + ctx.GetEvent().Labels["azure_alert_name"],
		}
		accountId := ctx.GetAccountId()
		if accountId != "" {
			rawParams["account_id"] = accountId
			items, err := lookupResourcesFromDB(ctx.GetTenantId(), accountId, serviceName, region, []string{targetResource})
			if err != nil {
				ctx.GetLogger().Warn("cloudResourceAction: db lookup failed, skipping auto-discovery", "error", err)
				return nil, nil
			}
			return buildCloudResourceResponse(items, rawParams), nil
		}
		return nil, nil
	}

	labels := ctx.GetEvent().Labels

	// Handle GCP
	if labels["gcp_region"] != "" && labels["gcp_event_instance"] != "" {
		rawParams := map[string]any{
			"resource_ids": []string{labels["gcp_event_instance"]},
			"region":       labels["gcp_region"],
			"service_name": labels["gcp_service_name"],
			"title":        "Resource Details For Service - " + labels["gcp_service_name"],
		}

		if labels["gcp_account"] == "" {
			return nil, nil
		}
		accountId, err := getCloudAccountIdByNumber(labels["gcp_account"], ctx.GetTenantId())
		if err != nil {
			return nil, err
		}
		rawParams["account_id"] = accountId
		items, err := lookupResourcesFromDB(ctx.GetTenantId(), accountId, labels["gcp_service_name"], labels["gcp_region"], []string{labels["gcp_event_instance"]})
		if err != nil {
			ctx.GetLogger().Warn("cloudResourceAction: db lookup failed, skipping auto-discovery", "error", err)
			return nil, nil
		}
		return buildCloudResourceResponse(items, rawParams), nil
	}

	// Handle AWS
	resource_ids := []string{labels["aws_event_instance"]}
	if labels["aws_event_alarm_dimensions"] != "" {
		dimensionsArr := []map[string]any{}
		err := common.UnmarshalJson([]byte(ctx.GetEvent().Labels["aws_event_alarm_dimensions"]), &dimensionsArr)
		if err != nil {
			ctx.GetLogger().Error("unable to parse dimensions data from alarms")
		}
		for _, e := range dimensionsArr {
			if e["Value"] != nil {
				resource_ids = append(resource_ids, e["Value"].(string))
			}
		}
	}
	rawParams := map[string]any{
		"resource_ids": resource_ids,
		"region":       ctx.GetEvent().Labels["aws_region"],
		"service_name": ctx.GetEvent().Labels["aws_service_name"],
		"title":        "Resource Details For Service - " + ctx.GetEvent().Labels["aws_service_name"],
	}
	if labels["aws_account"] == "" {
		return nil, nil
	}
	accountId, err := getCloudAccountIdByNumber(labels["aws_account"], ctx.GetTenantId())
	if err != nil {
		return nil, err
	}
	rawParams["account_id"] = accountId
	items, err := lookupResourcesFromDB(ctx.GetTenantId(), accountId, labels["aws_service_name"], labels["aws_region"], lo.Uniq(resource_ids))
	if err != nil {
		ctx.GetLogger().Warn("cloudResourceAction: db lookup failed, skipping auto-discovery", "error", err)
		return nil, nil
	}
	return buildCloudResourceResponse(items, rawParams), nil
}

func (a *cloudResourceAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params cloudResourceActionParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	if params.AccountId == "" {
		params.AccountId = ctx.GetAccountId()
	}

	if params.AccountId == "" {
		return nil, errors.New("account_id is required")
	}

	if params.Region == "" {
		return nil, errors.New("region is required")
	}

	if params.ServiceName == "" {
		return nil, errors.New("service is required")
	}

	resourceIds := params.ResourceIds
	if params.ResourceId != "" {
		resourceIds = append(resourceIds, params.ResourceId)
	}
	if params.InstanceId != "" {
		resourceIds = append(resourceIds, params.InstanceId)
	}
	if len(params.InstanceIds) > 0 {
		resourceIds = append(resourceIds, params.InstanceIds...)
	}

	resourceRequest := QueryResourceRequest{
		AccountId:   params.AccountId,
		ServiceName: params.ServiceName,
		ResourceIds: lo.Uniq(resourceIds),
		Regions:     []string{params.Region},
	}

	resourceResp, err := QueryResources(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), resourceRequest)

	if err != nil {
		return nil, err
	}

	if len(resourceResp.Items) == 0 {
		return nil, nil
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}

	labels := map[string]any{}
	if len(resourceResp.Items) > 0 {
		firstElement := resourceResp.Items[0]
		labels["resource_arn"] = firstElement.Arn
		labels["resource_name"] = firstElement.Name
		labels["resource_region"] = firstElement.Region
		labels["resource_service_name"] = firstElement.ServiceName
		labels["resource_type"] = firstElement.Type
		labels["resource_id"] = firstElement.Id
		for k, v := range firstElement.Meta {
			if v == nil {
				continue
			}
			switch k {
			case "PrivateDnsName":
				labels["resource_private_dns_name"] = v
			case "PrivateIpAddress":
				labels["resource_private_ip_address"] = v
			case "PublicDnsName":
				labels["resource_public_dns"] = v
			case "PublicIpAddress":
				labels["resource_public_ip_address"] = v
			case "SubnetId":
				labels["resource_subnet_id"] = v
			case "VpcId":
				labels["resource_vpc_id"] = v

			}
		}
	}

	additionalInfo := map[string]any{
		"action_name":        "cloud_resources",
		"actual_action_name": "cloud_resources",
	}

	if title, ok := rawParams["title"]; ok {
		additionalInfo["title"] = title
	} else {
		additionalInfo["title"] = "Resource Details"
	}

	return playbooks.NewPlaybookActionResponseJsonWithLabels(resourceResp.Items, additionalInfo, []playbooks.PlaybookActionResponseInsight{}, metadata, labels), err
}

type cloudMetricsAction struct {
}

type cloudMetricsActionParams struct {
	AccountId       string              `json:"account_id,omitempty"`
	StartTime       *time.Time          `json:"start_time"`
	EndTime         *time.Time          `json:"end_time"`
	ResourceIds     []string            `json:"resource_ids"`
	ResourceId      string              `json:"resource_id"`
	ServiceName     string              `json:"service_name" validate:"required"`
	Region          string              `json:"region" validate:"required"`
	MetricNames     []string            `json:"metric_names"`
	MetricName      string              `json:"metric_name"`
	MetricNamespace string              `json:"metric_namespace"`
	Step            time.Duration       `json:"step"`
	Query           string              `json:"query"`
	Dimensions      []map[string]string `json:"dimensions,omitempty"`
	Dimension       map[string]string   `json:"dimension,omitempty"`
	Statistics      []string            `json:"statistics"`
	Statistic       string              `json:"statistic"`
}

// mapAlarmDimensionsToMetricDimensions maps CloudWatch alarm dimension names to metric API dimension names
// Some AWS services use different dimension names in alarms vs the metrics API
func mapAlarmDimensionsToMetricDimensions(alarmDimensions []map[string]any, namespace string) []map[string]any {
	// Define mappings for specific namespaces
	dimensionNameMapping := map[string]map[string]string{
		"AWS/VPN": {
			"VpnConnectionId": "VpnId",
			// TunnelIpAddress stays the same
		},
		// Add more namespace-specific mappings as needed
	}

	// Get the mapping for this namespace
	namespaceMapping, hasMappings := dimensionNameMapping[namespace]
	if !hasMappings {
		// No mappings needed for this namespace, return as-is
		return alarmDimensions
	}

	// Apply the mappings
	mappedDimensions := make([]map[string]any, 0, len(alarmDimensions))
	for _, dim := range alarmDimensions {
		name, _ := dim["Name"].(string)
		value := dim["Value"]

		// Map the dimension name if a mapping exists
		if mappedName, exists := namespaceMapping[name]; exists {
			mappedDimensions = append(mappedDimensions, map[string]any{
				"Name":  mappedName,
				"Value": value,
			})
		} else {
			// Keep the original dimension
			mappedDimensions = append(mappedDimensions, dim)
		}
	}

	return mappedDimensions
}

func (a *cloudMetricsAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	labels := ctx.GetEvent().Labels

	// AWS standard namespace alarms with instance
	if labels["aws_region"] != "" && labels["aws_event_metric_name"] != "" && labels["aws_event_metric_namespace"] != "" && labels["aws_event_metric_statistic"] != "" && labels["aws_event_instance"] != "" {
		return true
	}

	// AWS custom namespace alarms (log-based metrics)
	if labels["aws_region"] != "" && labels["aws_event_metric_name"] != "" && labels["aws_event_metric_namespace"] != "" && labels["aws_event_metric_statistic"] != "" && !strings.HasPrefix(labels["aws_event_metric_namespace"], "AWS/") {
		return true
	}

	// Azure metrics (webhook-based)
	if labels["signal_type"] == "Metric" {
		return true
	}

	// Azure Monitor Alert (polling-based or webhook) — metrics evidence for fired alerts
	if isAzureAlertSource(ctx.GetEvent().Source) && labels["azure_alert_target_resource"] != "" {
		if isAzureResourceID(labels["azure_alert_target_resource"]) {
			return true
		}
	}

	// GCP metrics — skip log-based alerts (no metric to chart)
	if labels["gcp_alert_type"] != "log" && labels["gcp_region"] != "" && labels["gcp_event_metric_type"] != "" && labels["gcp_event_instance"] != "" {
		return true
	}

	// AWS EventBridge events (EC2 state changes, RDS events, etc.) — no metric labels,
	// but we can auto-discover metrics based on service_name and instance
	if labels["aws_region"] != "" && labels["aws_service_name"] != "" && labels["aws_event_instance"] != "" {
		return true
	}

	return false
}

func (a *cloudMetricsAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	// Azure Monitor Alert (polling-based or webhook) — fetch metrics for the target resource
	if isAzureAlertSource(ctx.GetEvent().Source) && ctx.GetEvent().Labels["azure_alert_target_resource"] != "" {
		targetResource := ctx.GetEvent().Labels["azure_alert_target_resource"]
		serviceName := ctx.GetEvent().Labels["azure_service_name"]
		if serviceName == "" {
			serviceName = getAzureResourceType(targetResource)
		}

		rawParams := map[string]any{
			"resource_ids": []string{targetResource},
			"region":       ctx.GetEvent().Labels["azure_region"],
			"service_name": serviceName,
			"title":        "Metrics For - " + ctx.GetEvent().Labels["azure_alert_name"],
			"step":         5 * time.Minute,
		}

		// If alert context is available, extract metric namespace
		if alertCtxStr := ctx.GetEvent().Labels["azure_alert_context"]; alertCtxStr != "" {
			var alertContext map[string]any
			err := common.UnmarshalJson([]byte(alertCtxStr), &alertContext)
			if err != nil {
				ctx.GetLogger().Warn("Failed to unmarshal azure_alert_context", "error", err)
			} else {
				if condition, ok := alertContext["condition"].(map[string]any); ok {
					if allOfRaw, ok := condition["allOf"].([]any); ok && len(allOfRaw) > 0 {
						if crit, ok := allOfRaw[0].(map[string]any); ok {
							if ns, ok := crit["metricNamespace"].(string); ok {
								rawParams["metric_namespace"] = ns
							}
							// Extract alarming metric for fallback
							if metricName, ok := crit["metricName"].(string); ok {
								rawParams["_fallback_metric"] = metricName
							}
						}
					}
				}
			}
		}

		// Include the alert's actual metric first, then add correlated defaults.
		// Without this, a disk space alert would only show CPU/Network defaults.
		defaultMetrics := getDefaultAzureMetrics(serviceName)
		if fallback, ok := rawParams["_fallback_metric"].(string); ok {
			seen := map[string]bool{fallback: true}
			metrics := []string{fallback}
			for _, m := range defaultMetrics {
				if !seen[m] {
					metrics = append(metrics, m)
					seen[m] = true
				}
			}
			rawParams["metric_names"] = metrics
		} else if len(defaultMetrics) > 0 {
			rawParams["metric_names"] = defaultMetrics
		}
		delete(rawParams, "_fallback_metric")

		return a.Execute(ctx, rawParams)
	}

	// Azure metrics (webhook-based)
	labels := ctx.GetEvent().Labels

	// Handle GCP
	if labels["gcp_region"] != "" && labels["gcp_event_metric_type"] != "" {
		// Don't pass metric_names or statistics — let cloud-collector auto-discover
		// all predefined metrics for the service (via gcloudServiceMetricsMap)
		// with their per-metric statistics config (gcloudMetricsStatsMap).
		rawParams := map[string]any{
			"resource_ids":     []string{labels["gcp_event_instance"]},
			"metric_namespace": labels["gcp_event_metric_type"],
			"region":           labels["gcp_region"],
			"service_name":     labels["gcp_service_name"],
			"title":            "Metrics For - " + labels["gcp_event_instance"],
		}

		// Get cloud account ID
		if labels["gcp_account"] != "" {
			dbms, err := database.GetDatabaseManager(database.Metastore)
			if err != nil {
				return nil, err
			}
			var accountId string
			err = dbms.Db.QueryRowx("select id from cloud_accounts ca where account_number = $1 AND tenant = $2 AND status = 'active'",
				labels["gcp_account"], ctx.GetTenantId()).Scan(&accountId)
			if err != nil {
				ctx.GetLogger().Warn("cloud_metrics: could not find cloud account",
					"account_number", labels["gcp_account"], "error", err)
			} else {
				rawParams["account_id"] = accountId
			}
		}

		// Add time range if available
		if labels["gcp_event_start_time"] != "" {
			startTime, err := time.Parse(time.RFC3339, labels["gcp_event_start_time"])
			if err == nil {
				startTime = startTime.Add(-1 * time.Hour)
				rawParams["start_time"] = &startTime
			}
		}

		if labels["gcp_event_end_time"] != "" {
			endTime, err := time.Parse(time.RFC3339, labels["gcp_event_end_time"])
			if err == nil {
				rawParams["end_time"] = &endTime
			}
		}

		return a.Execute(ctx, rawParams)
	}

	// Handle Azure
	if labels["signal_type"] == "Metric" {
		rawParams := map[string]any{
			"metric_namespace": ctx.GetEvent().Labels["metric_namespace"],
			"title":            "Metrics For - " + ctx.GetEvent().Labels["alertname"],
			"region":           ctx.GetEvent().Labels["region"],
			"service_name":     ctx.GetEvent().Labels["service_name"],
		}
		var essentials map[string]any
		err := common.UnmarshalJson([]byte(ctx.GetEvent().Labels["azure_essentials"]), &essentials)
		if err != nil {
			return nil, fmt.Errorf("azure: failed to unmarshal azure_essentials: %w", err)
		}
		if targetIDsRaw, ok := essentials["alertTargetIDs"].([]any); ok {
			var targetIDs []string
			for _, idRaw := range targetIDsRaw {
				if idStr, ok := idRaw.(string); ok {
					targetIDs = append(targetIDs, idStr)
				}
			}
			rawParams["resource_ids"] = targetIDs
		} else {
			return nil, errors.New("azure: could not find 'alertTargetIDs' in essentials")
		}
		if rawParams["service_name"] == "" {
			if val, ok := essentials["targetResourceType"].(string); ok {
				rawParams["service_name"] = val
			}
		}
		var alertCtx map[string]any
		err = common.UnmarshalJson([]byte(ctx.GetEvent().Labels["azure_alert_context"]), &alertCtx)
		if err != nil {
			return nil, fmt.Errorf("azure: failed to unmarshal azure_alert_context: %w", err)
		}
		conditionRaw, ok := alertCtx["condition"]
		if !ok {
			return nil, errors.New("azure: context missing 'condition'")
		}
		conditionMap, ok := conditionRaw.(map[string]any)
		if !ok {
			return nil, errors.New("azure: 'condition' is not an object")
		}

		allOfRaw, ok := conditionMap["allOf"]
		if !ok {
			return nil, errors.New("azure: condition missing 'allOf'")
		}
		allOfList, ok := allOfRaw.([]any)
		if !ok || len(allOfList) == 0 {
			return nil, errors.New("azure: 'allOf' is not a list or is empty")
		}

		metricCondMap, ok := allOfList[0].(map[string]any)
		if !ok {
			return nil, errors.New("azure: 'allOf[0]' is not an object")
		}
		// Use default metrics for the service type instead of just the alarming metric
		serviceName, _ := rawParams["service_name"].(string)
		defaultMetrics := getDefaultAzureMetrics(serviceName)
		if len(defaultMetrics) > 0 {
			rawParams["metric_names"] = defaultMetrics
		} else if metricName, ok := metricCondMap["metricName"].(string); ok {
			// Fall back to alarming metric if no defaults for this service type
			rawParams["metric_names"] = []string{metricName}
		}
		if startTimeStr, ok := conditionMap["windowStartTime"].(string); ok {
			// starttime is 1 hour before actual start time of the alert
			if startTime, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
				rawParams["start_time"] = &startTime
			}
		}
		if endTimeStr, ok := conditionMap["windowEndTime"].(string); ok {
			if endTime, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
				rawParams["end_time"] = &endTime
			}
		}
		if dimsRaw, ok := metricCondMap["dimensions"].([]any); ok {
			dimList := []map[string]string{}
			dimMap := make(map[string]string)
			for _, dimItemRaw := range dimsRaw {
				if dimItem, ok := dimItemRaw.(map[string]any); ok {
					name, _ := dimItem["name"].(string)
					value, _ := dimItem["value"].(string)
					if name != "" {
						dimMap[name] = value
					}
				}
			}
			if len(dimMap) > 0 {
				dimList = append(dimList, dimMap)
			}
			rawParams["dimensions"] = dimList
		}
		stepStrRaw := ctx.GetEvent().Labels["window_size"]
		if stepStrRaw != "" {
			stepStr := strings.ToLower(strings.TrimPrefix(stepStrRaw, "PT"))
			step, err := time.ParseDuration(stepStr)
			if err == nil {
				rawParams["step"] = step
			}
		}
		return a.Execute(ctx, rawParams)
	}

	// Handle custom namespace alarms (log-based metrics)
	namespace := ctx.GetEvent().Labels["aws_event_metric_namespace"]
	if namespace != "" && !strings.HasPrefix(namespace, "AWS/") {
		rawParams := map[string]any{
			"metric_names":     []string{ctx.GetEvent().Labels["aws_event_metric_name"]},
			"metric_namespace": namespace,
			"statistics":       []string{ctx.GetEvent().Labels["aws_event_metric_statistic"]},
			"region":           ctx.GetEvent().Labels["aws_region"],
			"service_name":     ctx.GetEvent().Labels["aws_service_name"],
			"title":            "Metrics For - " + ctx.GetEvent().Labels["aws_event_metric_name"],
		}

		// Parse and add dimensions if present
		if ctx.GetEvent().Labels["aws_event_alarm_dimensions"] != "" {
			dimensionsArr := []map[string]any{}
			err := common.UnmarshalJson([]byte(ctx.GetEvent().Labels["aws_event_alarm_dimensions"]), &dimensionsArr)
			if err != nil {
				ctx.GetLogger().Error("cloud_metrics: unable to parse aws_event_alarm_dimensions", "error", err)
			} else if len(dimensionsArr) > 0 {
				// Convert to []map[string]string format expected by Execute
				// Filter out @-prefixed dimensions (e.g. @aws.account, @aws.region)
				// which are auto-injected by CloudWatch Logs and are not real metric dimensions
				dimensions := []map[string]string{}
				for _, dim := range dimensionsArr {
					dimMap := make(map[string]string)
					if name, ok := dim["Name"].(string); ok {
						if strings.HasPrefix(name, "@") {
							continue
						}
						if value, ok := dim["Value"].(string); ok {
							dimMap[name] = value
						}
					}
					if len(dimMap) > 0 {
						dimensions = append(dimensions, dimMap)
					}
				}
				if len(dimensions) > 0 {
					rawParams["dimensions"] = dimensions
				}
			}
		}

		// Get cloud account ID
		if ctx.GetEvent().Labels["aws_account"] != "" {
			dbms, err := database.GetDatabaseManager(database.Metastore)
			if err != nil {
				return nil, err
			}
			var accountId string
			err = dbms.Db.QueryRowx("select id from cloud_accounts ca where account_number = $1 AND tenant = $2 AND status = 'active'", ctx.GetEvent().Labels["aws_account"], ctx.GetTenantId()).Scan(&accountId)
			if err != nil {
				ctx.GetLogger().Warn("cloud_metrics: could not find cloud account", "account_number", ctx.GetEvent().Labels["aws_account"], "error", err)
			} else {
				rawParams["account_id"] = accountId
			}
		}

		// Add time range if available
		if ctx.GetEvent().Labels["aws_event_start_time"] != "" {
			startTime, err := time.Parse(time.RFC3339, ctx.GetEvent().Labels["aws_event_start_time"])
			if err == nil {
				startTime = startTime.Add(-1 * time.Hour)
				rawParams["start_time"] = &startTime
			}
		}

		if ctx.GetEvent().Labels["aws_event_end_time"] != "" {
			endTime, err := time.Parse(time.RFC3339, ctx.GetEvent().Labels["aws_event_end_time"])
			if err == nil {
				rawParams["end_time"] = &endTime
			}
		}

		return a.Execute(ctx, rawParams)
	}

	// Standard AWS namespace alarms
	// Don't pass metric_names, statistics, or metric_namespace — let cloud-collector
	// auto-discover all predefined metrics for the service (via serviceCloudwatchNamespaceMap)
	// with their per-metric statistics config. Passing metric_namespace causes cloud-collector
	// to take the explicit-namespace path which skips resource type auto-detection.
	rawParams := map[string]any{
		"resource_ids": []string{ctx.GetEvent().Labels["aws_event_instance"]},
		"region":       ctx.GetEvent().Labels["aws_region"],
		"service_name": ctx.GetEvent().Labels["aws_service_name"],
		"title":        "Metrics For - " + ctx.GetEvent().Labels["aws_event_instance"],
	}
	if ctx.GetEvent().Labels["aws_event_alarm_dimensions"] != "" {
		dimensionsArr := []map[string]any{}
		err := common.UnmarshalJson([]byte(ctx.GetEvent().Labels["aws_event_alarm_dimensions"]), &dimensionsArr)
		if err != nil {
			ctx.GetLogger().Error("cloud: unable to parse aws_event_alarm_dimensions", "error", err, "aws_event_alarm_dimensions", ctx.GetEvent().Labels["aws_event_alarm_dimensions"])
		} else {
			// Map alarm dimension names to CloudWatch metric dimension names
			// Some services use different dimension names in alarms vs metrics API
			mappedDimensions := mapAlarmDimensionsToMetricDimensions(dimensionsArr, ctx.GetEvent().Labels["aws_event_metric_namespace"])
			rawParams["dimensions"] = mappedDimensions
		}

	}
	if ctx.GetEvent().Labels["aws_account"] != "" {
		dbms, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			return nil, err
		}
		var accountId string
		err = dbms.Db.QueryRowx("select id from cloud_accounts ca where account_number = $1 AND tenant = $2 AND status = 'active'", ctx.GetEvent().Labels["aws_account"], ctx.GetTenantId()).Scan(&accountId)
		if err != nil {
			return nil, err
		}
		rawParams["account_id"] = accountId
	}

	if ctx.GetEvent().Labels["aws_event_start_time"] != "" {
		startTime, err := time.Parse(time.RFC3339, ctx.GetEvent().Labels["aws_event_start_time"])
		if err == nil {
			// set start time 1 hour before the event start time to capture metrics leading up to the event
			startTime = startTime.Add(-1 * time.Hour)
			rawParams["start_time"] = &startTime
		}
	}

	if ctx.GetEvent().Labels["aws_event_end_time"] != "" {
		endTime, err := time.Parse(time.RFC3339, ctx.GetEvent().Labels["aws_event_end_time"])
		if err == nil {
			rawParams["end_time"] = &endTime
		}
	}
	return a.Execute(ctx, rawParams)
}

func (a *cloudMetricsAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params cloudMetricsActionParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	if params.AccountId == "" {
		params.AccountId = ctx.GetAccountId()
	}

	if params.AccountId == "" {
		return nil, errors.New("account_id is required")
	}

	if params.StartTime == nil && ctx.GetEvent().StartedAt != nil {
		adjustedTime := ctx.GetEvent().StartedAt.Add(-1 * time.Hour)
		params.StartTime = &adjustedTime
	}

	if params.EndTime == nil && ctx.GetEvent().EndedAt != nil {
		params.EndTime = ctx.GetEvent().EndedAt
	}

	if params.ServiceName == "" {
		return nil, errors.New("service_name is required")
	}

	// handle singular forms
	if len(params.ResourceIds) == 0 && params.ResourceId != "" {
		params.ResourceIds = []string{params.ResourceId}
	}

	if len(params.MetricNames) == 0 && params.MetricName != "" {
		params.MetricNames = []string{params.MetricName}
	}

	if len(params.Dimensions) == 0 && len(params.Dimension) > 0 {
		params.Dimensions = []map[string]string{params.Dimension}
	}

	resourceResp, err := QueryMetrics(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), QueryMetricsRequest{
		AccountId: params.AccountId,
		Query: MetricsQuery{
			StartDate:       params.StartTime,
			EndDate:         params.EndTime,
			ResourceIds:     lo.Uniq(params.ResourceIds),
			ServiceName:     params.ServiceName,
			Region:          params.Region,
			MetricNames:     lo.Uniq(params.MetricNames),
			Step:            params.Step,
			Dimensions:      params.Dimensions,
			Statistics:      lo.Uniq(params.Statistics),
			MetricNamespace: params.MetricNamespace,
			Query:           params.Query,
		},
	})

	if err != nil {
		return nil, err
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}

	additionalInfo := map[string]any{
		"action_name":        "prometheus_enricher",
		"actual_action_name": "prometheus_queries_enricher",
	}

	if title, ok := rawParams["title"]; ok {
		additionalInfo["title"] = title
	} else {
		additionalInfo["title"] = "Cloudwatch Metrics"
	}

	seriesList := []any{}
	// Group series by metric name for separate chart rendering
	metricGroups := map[string][]any{}

	for _, item := range resourceResp.Items {
		data := map[string]any{
			"metric": map[string]any{
				"service_name": item.ServiceName,
				"region":       item.Region,
				"resource_id":  item.ResourceId,
				"name":         item.Name,
				"statistics":   item.Statistics,
			},
			"timestamps": lo.Map(item.Timestamps, func(t time.Time, _ int) int64 {
				return t.UnixMilli() / 1000
			}),
			"values": item.Values,
		}
		seriesList = append(seriesList, data)

		metricKey := item.Name
		if item.Statistics != "" {
			metricKey = item.Name + " (" + item.Statistics + ")"
		}
		metricGroups[metricKey] = append(metricGroups[metricKey], data)
	}

	// Skip storing evidence when no metric series were returned — avoids empty
	// "prometheus_enricher" cards that mislead the LLM into reporting empty Prometheus data.
	if len(seriesList) == 0 {
		ctx.GetLogger().Warn("cloud_metrics: query returned zero time series",
			"service_name", params.ServiceName,
			"region", params.Region,
			"resource_ids", params.ResourceIds,
			"metric_namespace", params.MetricNamespace,
			"account_id", params.AccountId)
		return nil, nil
	}

	responseData := map[string]any{
		"result_type":        "matrix",
		"series_list_result": seriesList,
	}

	// When multiple metrics exist, add metric_groups so frontend can render separate charts
	if len(metricGroups) > 1 {
		groups := map[string]any{}
		for name, series := range metricGroups {
			groups[name] = map[string]any{
				"series_list_result": series,
			}
		}
		responseData["metric_groups"] = groups
	}

	return playbooks.PrometheusActionResponse{
		Metadata:       metadata,
		Data:           responseData,
		AdditionalInfo: additionalInfo,
		Insight:        []playbooks.PlaybookActionResponseInsight{},
	}, err
}

type cloudServiceMapAction struct {
}

type cloudServiceMapActionParams struct {
	AccountId   string `json:"account_id,omitempty"`
	Region      string `json:"region" validate:"required"`
	ServiceName string `json:"service_name" validate:"required"`
	ResourceId  string `json:"resource_id" validate:"required"`
}

func (a *cloudServiceMapAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	labels := ctx.GetEvent().Labels

	// AWS
	if labels["aws_region"] != "" && labels["aws_event_instance"] != "" && labels["aws_service_name"] != "" {
		return true
	}

	// GCP — service map is not implemented in cloud-collector (returns ErrUnsupported),
	// so skip auto-execution to avoid producing an empty card.

	// Azure Monitor Alert (polling-based or webhook)
	if isAzureAlertSource(ctx.GetEvent().Source) && labels["azure_alert_target_resource"] != "" {
		if isAzureResourceID(labels["azure_alert_target_resource"]) {
			return true
		}
	}

	return false
}

func (a *cloudServiceMapAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels

	// Handle Azure Monitor Alert (polling-based or webhook)
	if isAzureAlertSource(ctx.GetEvent().Source) && labels["azure_alert_target_resource"] != "" {
		targetResource := labels["azure_alert_target_resource"]
		serviceName := labels["azure_service_name"]
		if serviceName == "" {
			serviceName = getAzureResourceType(targetResource)
		}

		rawParams := map[string]any{
			"resource_id":  targetResource,
			"region":       labels["azure_region"],
			"service_name": serviceName,
			"title":        "Service Map For - " + labels["azure_alert_name"],
		}
		return a.Execute(ctx, rawParams)
	}

	// GCP service map not implemented in cloud-collector — skipped in CanAutoExecute

	// Handle AWS
	rawParams := map[string]any{
		"resource_id":  labels["aws_event_instance"],
		"region":       labels["aws_region"],
		"service_name": labels["aws_service_name"],
		"title":        "Service Map For - " + labels["aws_event_instance"],
	}
	if labels["aws_account"] != "" {
		dbms, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			return nil, err
		}
		var accountId string
		err = dbms.Db.QueryRowx("select id from cloud_accounts ca where account_number = $1 AND tenant = $2 AND status = 'active'", ctx.GetEvent().Labels["aws_account"], ctx.GetTenantId()).Scan(&accountId)
		if err != nil {
			return nil, err
		}
		rawParams["account_id"] = accountId
	}

	return a.Execute(ctx, rawParams)
}

func (a *cloudServiceMapAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params cloudServiceMapActionParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	if params.AccountId == "" {
		params.AccountId = ctx.GetAccountId()
	}

	if params.ServiceName == "" {
		return nil, errors.New("service_name is required")
	}

	if params.ResourceId == "" {
		return nil, errors.New("resource_id is required")
	}

	resourceResp, err := QueryServiceMap(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), QueryServiceMapRequest{
		AccountId: params.AccountId,
		Query: QueryServiceMapQuery{
			Region: params.Region,
			Resources: []QueryServiceMapResourceRequest{
				{
					ServiceName: params.ServiceName,
					Resource:    params.ResourceId,
				},
			},
		},
	})

	if err != nil {
		return nil, err
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}

	additionalInfo := map[string]any{
		"action_name":  "cloud_service_map",
		"title":        "Cloud Service Map",
		"service_name": params.ServiceName,
		"region":       params.Region,
	}
	if len(resourceResp.Applications) == 0 {
		return nil, nil
	}

	resp := playbooks.NewPlaybookActionResponseJson(map[string]any{"data": resourceResp.Applications}, additionalInfo, []playbooks.PlaybookActionResponseInsight{}, metadata)
	resp.Format = "service_map"
	return resp, err
}

type cloudLogAction struct {
}

type cloudLogsActionParams struct {
	AccountId     string     `json:"account_id,omitempty"`
	Region        string     `json:"region" validate:"required"`
	LogGroupName  string     `json:"log_group_name"`
	ServiceName   string     `json:"service_name"`
	ResourceId    string     `json:"resource_id"`
	QueryString   string     `json:"query_string"`
	Query         string     `json:"query"`
	StartTime     *time.Time `json:"start_time" validate:"required"`
	EndTime       *time.Time `json:"end_time" validate:"required"`
	Limit         *int64     `json:"limit"`
	LogMetricName string     `json:"log_metric_name" mapstructure:"log_metric_name"`
	FilterPattern string     `json:"filter_pattern" mapstructure:"filter_pattern"`
}

func (a *cloudLogAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	event := ctx.GetEvent()
	labels := event.Labels

	// AWS
	if labels["aws_region"] != "" && labels["aws_event_instance"] != "" && labels["aws_service_name"] != "" {
		return true
	}

	// AWS - metric filter log group
	if labels["aws_region"] != "" && labels["metric_filter_log_group_name"] != "" {
		return true
	}

	// Azure
	if event.Source == "azure_monitor_webhook" && labels["alert_target_ids"] != "" && labels["target_resource_type"] != "" {
		parts := strings.Split(labels["alert_target_ids"], ",")
		return len(parts) == 1
	}

	// Azure Monitor Alert (polling-based or webhook)
	// Skip when KQL query + workspace are available — cloud_azure_kql_query_results renders
	// tabular results as a markdown table (KQL results don't fit the log viewer format).
	if isAzureAlertSource(event.Source) && labels["azure_alert_target_resource"] != "" {
		hasKQLWorkspace := isAzureKQLQueryableResourceID(labels["azure_alert_kql_workspace_scope"]) ||
			isAzureKQLQueryableResourceID(labels["azure_dcr_workspace_id"])
		if labels["azure_alert_kql_query"] != "" && hasKQLWorkspace {
			return false
		}
		if labels["azure_service_name"] != "" || isAzureResourceID(labels["azure_alert_target_resource"]) {
			return true
		}
	}

	// GCP metric alerts
	if labels["gcp_region"] != "" && labels["gcp_event_instance"] != "" && labels["gcp_service_name"] != "" {
		return true
	}

	// GCP log-based alerts — logs are the most valuable evidence for these
	if labels["gcp_alert_type"] == "log" && labels["gcp_region"] != "" {
		return true
	}

	return false
}

func (a *cloudLogAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels

	// Handle GCP (metric alerts with instance, or log-based alerts)
	if labels["gcp_region"] != "" && (labels["gcp_event_instance"] != "" || labels["gcp_alert_type"] == "log") {
		rawParams := map[string]any{
			"resource_id":  labels["gcp_event_instance"],
			"region":       labels["gcp_region"],
			"service_name": labels["gcp_service_name"],
		}

		if labels["gcp_event_instance"] != "" {
			rawParams["title"] = "Logs For - " + labels["gcp_event_instance"]
		} else {
			rawParams["title"] = "Logs For - " + labels["gcp_service_name"]
		}

		// For log-based metric alerts, narrow the log query to the specific log
		// that the metric monitors (e.g., postgres.log for slow-query metrics).
		// The metric_log label is populated from alert.Metric.Labels by the
		// incident processor (gcloud_monitoring_incidents_v3.go).
		if metricLog := labels["metric_log"]; metricLog != "" && labels["gcp_project_id"] != "" {
			rawParams["log_group_name"] = fmt.Sprintf("projects/%s/logs/%s",
				labels["gcp_project_id"], url.PathEscape(metricLog))
		}

		if labels["gcp_account"] != "" {
			accountId, err := getCloudAccountIdByNumber(labels["gcp_account"], ctx.GetTenantId())
			if err != nil {
				ctx.GetLogger().Warn("cloud_logs: could not find cloud account",
					"account_number", labels["gcp_account"], "error", err)
			} else {
				rawParams["account_id"] = accountId
			}
		}

		// For GCP log-based metric alerts, pass the metric name so the
		// cloud-collector can fetch its filter and apply it to the log query.
		if metricType := labels["gcp_metric_type"]; strings.HasPrefix(metricType, "logging.googleapis.com/user/") {
			metricName := strings.TrimPrefix(metricType, "logging.googleapis.com/user/")
			rawParams["log_metric_name"] = metricName
		}

		return a.Execute(ctx, rawParams)
	}

	// Handle Azure
	if ctx.GetEvent().Source == "azure_monitor_webhook" && labels["alert_target_ids"] != "" {
		parts := strings.Split(ctx.GetEvent().Labels["alert_target_ids"], ",")
		rawParams := map[string]any{
			"resource_id":  parts[0],
			"region":       "NA",
			"service_name": ctx.GetEvent().Labels["target_resource_type"],
			"title":        "Logs For - " + parts[0],
		}
		return a.Execute(ctx, rawParams)
	}

	// Azure Monitor Alert (polling-based or webhook)
	if targetResource := ctx.GetEvent().Labels["azure_alert_target_resource"]; targetResource != "" && isAzureAlertSource(ctx.GetEvent().Source) {
		serviceName := labels["azure_service_name"]
		if serviceName == "" {
			serviceName = getAzureResourceType(targetResource)
		}

		rawParams := map[string]any{
			"resource_id":  targetResource,
			"region":       labels["azure_region"],
			"service_name": serviceName,
			"title":        "Logs For - " + labels["azure_alert_name"],
		}
		return a.Execute(ctx, rawParams)
	}

	// Check if we have metric filter log group name from event labels
	if ctx.GetEvent().Labels["metric_filter_log_group_name"] != "" {
		rawParams := map[string]any{
			"log_group_name": ctx.GetEvent().Labels["metric_filter_log_group_name"],
			"region":         ctx.GetEvent().Labels["aws_region"],
			"title":          "Logs For - " + ctx.GetEvent().Labels["aws_event_metric_name"],
		}

		// Pass metric filter pattern to cloud-collector which uses FilterLogEvents API
		// (natively supports the same pattern syntax as CloudWatch metric filters)
		if filterPattern := ctx.GetEvent().Labels["metric_filter_pattern"]; filterPattern != "" {
			rawParams["filter_pattern"] = filterPattern
		}

		if ctx.GetEvent().Labels["aws_account"] != "" {
			dbms, err := database.GetDatabaseManager(database.Metastore)
			if err != nil {
				return nil, err
			}
			var accountId string
			err = dbms.Db.QueryRowx("select id from cloud_accounts ca where account_number = $1 AND tenant = $2 AND status = 'active'", ctx.GetEvent().Labels["aws_account"], ctx.GetTenantId()).Scan(&accountId)
			if err != nil {
				ctx.GetLogger().Warn("cloud_logs: could not find cloud account", "account_number", ctx.GetEvent().Labels["aws_account"], "error", err)
			} else {
				rawParams["account_id"] = accountId
			}
		}
		return a.Execute(ctx, rawParams)
	}

	rawParams := map[string]any{
		"resource_id":  ctx.GetEvent().Labels["aws_event_instance"],
		"region":       ctx.GetEvent().Labels["aws_region"],
		"service_name": ctx.GetEvent().Labels["aws_service_name"],
		"title":        "Logs For - " + ctx.GetEvent().Labels["aws_event_instance"],
	}
	if ctx.GetEvent().Labels["aws_account"] != "" {
		dbms, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			return nil, err
		}
		var accountId string
		err = dbms.Db.QueryRowx("select id from cloud_accounts ca where account_number = $1 AND tenant = $2 AND status = 'active'", ctx.GetEvent().Labels["aws_account"], ctx.GetTenantId()).Scan(&accountId)
		if err != nil {
			return nil, err
		}
		rawParams["account_id"] = accountId
	}
	return a.Execute(ctx, rawParams)
}

func (a *cloudLogAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params cloudLogsActionParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	if params.AccountId == "" {
		params.AccountId = ctx.GetAccountId()
	}

	if params.AccountId == "" {
		return nil, errors.New("account_id is required")
	}

	if params.LogGroupName == "" && params.ServiceName == "" && params.ResourceId == "" {
		return nil, errors.New("log_group_name or (service_name and resource_id) is required")
	}

	if params.StartTime == nil && ctx.GetEvent().StartedAt != nil {
		params.StartTime = ctx.GetEvent().StartedAt
	}

	if params.EndTime == nil && ctx.GetEvent().EndedAt != nil {
		params.EndTime = ctx.GetEvent().EndedAt
	}

	// For log-based metric alerts, widen the time window to 1 hour.
	// The default 10-minute window from getPlaybookStartEndTime is too narrow
	// because alerts accumulate over time before firing. With the metric filter
	// applied, the result set will be small enough for a wider window.
	if params.LogMetricName != "" && params.EndTime != nil {
		widerStart := params.EndTime.Add(-1 * time.Hour)
		params.StartTime = &widerStart
	}

	query := params.Query
	if query == "" {
		query = params.QueryString
	}
	if params.Limit == nil {
		var limit int64 = 1000
		params.Limit = &limit
	}
	resourceResp, err := QueryLogs(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), QueryLogsRequest{
		AccountId: params.AccountId,
		Query: LogQuery{
			Region:        params.Region,
			LogGroupName:  params.LogGroupName,
			ServiceName:   params.ServiceName,
			ResourceId:    params.ResourceId,
			QueryString:   query,
			StartTime:     params.StartTime,
			EndTime:       params.EndTime,
			Limit:         params.Limit,
			LogMetricName: params.LogMetricName,
			FilterPattern: params.FilterPattern,
		},
	})

	if err != nil {
		return nil, err
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}

	const maxMessageLen = 4096 // Truncate individual log messages to prevent oversized evidences
	logoutput := []map[string]any{}

	for _, item := range resourceResp.Results {
		log := map[string]any{}
		log["timestamp"] = item.Timestamp
		msg := item.Message
		if len(msg) > maxMessageLen {
			msg = msg[:maxMessageLen] + "... [truncated]"
		}
		log["message"] = msg
		labels := map[string]string{}
		for _, v := range item.Labels {
			labels[v.Label] = v.Value
		}
		log["labels"] = labels
		logoutput = append(logoutput, log)
	}

	if len(logoutput) == 0 {
		return nil, nil
	}

	insights := actionLogExtractErrorPatterns(logoutput, 2)
	labels := map[string]any{}
	if len(logoutput) > 0 {
		firstElement := logoutput[0]
		if labels1, ok := firstElement["labels"].(map[string]any); ok {
			labels = labels1
		}
	}

	response := playbooks.NewPlaybookActionResponseJson(map[string]any{"data": logoutput}, map[string]any{}, insights, metadata)
	response.Labels = labels
	return response, err
}

func actionLogExtractErrorPatterns(logs []map[string]any, maxErrors int) []playbooks.PlaybookActionResponseInsight {
	var insights []playbooks.PlaybookActionResponseInsight
	seenMessages := make(map[string]bool) // Track distinct log messages

	// Common error patterns to search for (case-insensitive)
	errorPatterns := []string{
		"exception",
		"error",
		"fatal",
	}

	for _, logData := range logs {
		if len(insights) >= maxErrors {
			break
		}

		body, _ := logData["message"].(string)
		if body == "" {
			continue
		}

		// Skip if we've already seen this exact log message
		if seenMessages[body] {
			continue
		}

		bodyLower := strings.ToLower(body)

		// Check if log body contains any error patterns
		for _, pattern := range errorPatterns {
			if strings.Contains(bodyLower, pattern) {
				severityText, _ := logData["severity_text"].(string)
				if severityText == "" {
					severityText = "ERROR"
				}

				insights = append(insights, playbooks.PlaybookActionResponseInsight{
					Message:  fmt.Sprintf("%s:%s", severityText, body),
					Severity: "High",
				})
				seenMessages[body] = true
				break
			}
		}
	}

	return insights
}

type cloudPerformanceInsightsAction struct{}

type cloudPerformanceInsightsParams struct {
	AccountId            string     `json:"account_id,omitempty"`
	DBInstanceIdentifier string     `json:"db_instance_identifier"`
	Region               string     `json:"region"`
	StartTime            *time.Time `json:"start_time"`
	EndTime              *time.Time `json:"end_time"`
}

func (a *cloudPerformanceInsightsAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	labels := ctx.GetEvent().Labels

	// Check for standard RDS alarms with DBInstanceIdentifier
	if labels["aws_event_metric_namespace"] == "AWS/RDS" && labels["aws_event_alarm_dimensions"] != "" {
		dimensionsArr := []map[string]any{}
		err := common.UnmarshalJson([]byte(labels["aws_event_alarm_dimensions"]), &dimensionsArr)
		if err == nil {
			for _, dim := range dimensionsArr {
				if dimName, ok := dim["Name"].(string); ok && dimName == "DBInstanceIdentifier" {
					return true
				}
			}
		}
	}

	// Check for log-based RDS metrics with extracted log group
	if labels["metric_filter_log_group_name"] != "" && strings.Contains(labels["metric_filter_log_group_name"], "/aws/rds/instance/") {
		return true
	}

	return false
}

func (a *cloudPerformanceInsightsAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels
	var dbInstanceIdentifier string
	region := labels["aws_region"]

	// Extract DB instance identifier from alarm dimensions
	if labels["aws_event_alarm_dimensions"] != "" {
		dimensionsArr := []map[string]any{}
		err := common.UnmarshalJson([]byte(labels["aws_event_alarm_dimensions"]), &dimensionsArr)
		if err == nil {
			for _, dim := range dimensionsArr {
				if dimName, ok := dim["Name"].(string); ok && dimName == "DBInstanceIdentifier" {
					if dimValue, ok := dim["Value"].(string); ok {
						dbInstanceIdentifier = dimValue
						break
					}
				}
			}
		}
	}

	// Extract DB instance identifier from log group name if not found in dimensions
	if dbInstanceIdentifier == "" && labels["metric_filter_log_group_name"] != "" {
		logGroupName := labels["metric_filter_log_group_name"]
		// Parse: "/aws/rds/instance/main/postgresql" -> extract "main"
		if strings.HasPrefix(logGroupName, "/aws/rds/instance/") {
			parts := strings.Split(logGroupName, "/")
			if len(parts) >= 5 {
				dbInstanceIdentifier = parts[4]
			}
		}
	}

	if dbInstanceIdentifier == "" {
		ctx.GetLogger().Warn("cloud_performance_insights: could not extract DB instance identifier")
		return nil, fmt.Errorf("could not extract DB instance identifier from event labels")
	}

	if region == "" {
		ctx.GetLogger().Warn("cloud_performance_insights: aws_region not found in labels")
		return nil, fmt.Errorf("aws_region not found in event labels")
	}

	rawParams := map[string]any{
		"db_instance_identifier": dbInstanceIdentifier,
		"region":                 region,
		"title":                  "Performance Insights For - " + dbInstanceIdentifier,
	}

	// Get cloud account ID
	if labels["aws_account"] != "" {
		dbms, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			return nil, err
		}
		var accountId string
		err = dbms.Db.QueryRowx("select id from cloud_accounts ca where account_number = $1 AND tenant = $2 AND status = 'active'", labels["aws_account"], ctx.GetTenantId()).Scan(&accountId)
		if err != nil {
			ctx.GetLogger().Warn("cloud_performance_insights: could not find cloud account", "account_number", labels["aws_account"], "error", err)
		} else {
			rawParams["account_id"] = accountId
		}
	}

	// Add time range if available
	if ctx.GetEvent().StartedAt != nil {
		startTime := ctx.GetEvent().StartedAt.Add(-1 * time.Hour)
		rawParams["start_time"] = &startTime
	}

	if ctx.GetEvent().EndedAt != nil {
		rawParams["end_time"] = ctx.GetEvent().EndedAt
	}

	return a.Execute(ctx, rawParams)
}

func (a *cloudPerformanceInsightsAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params cloudPerformanceInsightsParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	if params.AccountId == "" {
		params.AccountId = ctx.GetAccountId()
	}

	if params.AccountId == "" {
		return nil, errors.New("account_id is required")
	}

	if params.DBInstanceIdentifier == "" {
		return nil, errors.New("db_instance_identifier is required")
	}

	if params.Region == "" {
		return nil, errors.New("region is required")
	}

	// Set default time range if not provided
	if params.StartTime == nil && ctx.GetEvent().StartedAt != nil {
		startTime := ctx.GetEvent().StartedAt.Add(-1 * time.Hour)
		params.StartTime = &startTime
	}

	if params.EndTime == nil && ctx.GetEvent().EndedAt != nil {
		params.EndTime = ctx.GetEvent().EndedAt
	}

	// Build request body
	requestBody := map[string]any{
		"account_id":             params.AccountId,
		"db_instance_identifier": params.DBInstanceIdentifier,
		"region":                 params.Region,
	}

	if params.StartTime != nil {
		requestBody["start_time"] = params.StartTime.Format(time.RFC3339)
	}

	if params.EndTime != nil {
		requestBody["end_time"] = params.EndTime.Format(time.RFC3339)
	}

	// Call cloud-collector Performance Insights endpoint
	resp, err := common.HttpPost(config.Config.CloudCollectorServerUrl+"/v1/cloud/performance_insights",
		common.HttpWithTimeout(60*time.Second),
		common.HttpWithJsonBody(requestBody),
		common.HttpWithHeaders(map[string]string{
			config.Config.CloudCollectorServerTokenHeader: config.Config.CloudCollectorServerToken,
			"X-Hasura-User-Tenant-Id":                     ctx.GetTenantId(),
		}))

	if err != nil {
		return nil, fmt.Errorf("failed to call performance insights endpoint: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()
	bodyData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read performance insights response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("performance insights request failed with status %d: %s", resp.StatusCode, string(bodyData))
	}

	var piResponse map[string]any
	if err := common.UnmarshalJson(bodyData, &piResponse); err != nil {
		return nil, fmt.Errorf("failed to parse performance insights response: %w", err)
	}

	// Extract data from the response
	var piData map[string]any
	if data, ok := piResponse["data"].(map[string]any); ok {
		piData = data
	} else {
		piData = map[string]any{}
	}

	// Check if Performance Insights is enabled
	piEnabled := false
	if enabled, ok := piData["performance_insights_enabled"].(bool); ok {
		piEnabled = enabled
	}

	metadata := map[string]any{
		"query-result-version":         "1.0",
		"query":                        rawParams,
		"performance_insights_enabled": piEnabled,
	}

	additionalInfo := map[string]any{
		"action_name":        "cloud_performance_insights",
		"actual_action_name": "cloud_performance_insights",
	}

	if title, ok := rawParams["title"]; ok {
		additionalInfo["title"] = title
	} else {
		additionalInfo["title"] = "Performance Insights For - " + params.DBInstanceIdentifier
	}

	if len(piData) == 0 {
		return nil, nil
	}

	insights := []playbooks.PlaybookActionResponseInsight{}
	if !piEnabled {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Performance Insights is not enabled for RDS instance '%s'", params.DBInstanceIdentifier),
			Severity: "info",
		})
	}

	return playbooks.NewPlaybookActionResponseJson(piData, additionalInfo, insights, metadata), nil
}

// --- Azure alert actions ---

// isAzureAlertSource returns true for both polling-based and webhook-based Azure Monitor alert sources.
func isAzureAlertSource(source string) bool {
	return source == "Azure_Monitor_Alert" || source == "azure_monitor_webhook"
}

// isAzureAlert checks if this is any Azure Monitor alert (polling-based or webhook).
func isAzureAlert(ctx playbooks.PlaybookActionContext) bool {
	if !isAzureAlertSource(ctx.GetEvent().Source) {
		return false
	}
	labels := ctx.GetEvent().Labels
	return labels["azure_subscription_id"] != "" && labels["azure_alert_target_resource"] != ""
}

// isAzureSubscriptionLevelAlert checks if this is an Azure subscription-level alert
// (ServiceHealth, Activity Log) where the target resource is just a subscription, not a specific resource.
func isAzureSubscriptionLevelAlert(ctx playbooks.PlaybookActionContext) bool {
	if !isAzureAlert(ctx) {
		return false
	}
	return !isAzureResourceID(ctx.GetEvent().Labels["azure_alert_target_resource"])
}

// getAzureSubscriptionAccountID resolves the cloud account ID for the current context.
func getAzureSubscriptionAccountID(ctx playbooks.PlaybookActionContext) (string, error) {
	accountId := ctx.GetAccountId()
	if accountId == "" {
		return "", errors.New("account_id is required")
	}
	return accountId, nil
}

// azureTextEnricherResponse is a custom response type whose JSON field names match
// what TextEnricherDynamicCard expects: top-level "data" and "title" fields.
// PlaybookActionResponseMarkdown uses "text" (not "data") which the card can't read.
type azureTextEnricherResponse struct {
	Data           string                                    `json:"data"`
	Title          string                                    `json:"title"`
	AdditionalInfo map[string]any                            `json:"additional_info"`
	Insight        []playbooks.PlaybookActionResponseInsight `json:"insight"`
	Labels         map[string]any                            `json:"-"` // extracted labels for downstream actions
}

func (m azureTextEnricherResponse) GetFormatName() string             { return "markdown" }
func (m azureTextEnricherResponse) GetData() any                      { return m.Data }
func (m azureTextEnricherResponse) GetAdditionalInfo() map[string]any { return m.AdditionalInfo }
func (m azureTextEnricherResponse) GetInsights() []playbooks.PlaybookActionResponseInsight {
	return m.Insight
}
func (m azureTextEnricherResponse) ExtractLabels() map[string]any { return m.Labels }

// formatAzureMarkdownResponse builds an evidence response rendered via TextEnricherDynamicCard.
func formatAzureMarkdownResponse(title, markdown string, insights []playbooks.PlaybookActionResponseInsight) azureTextEnricherResponse {
	return azureTextEnricherResponse{
		Data:  markdown,
		Title: title,
		AdditionalInfo: map[string]any{
			"actual_action_name": "text_enricher",
		},
		Insight: insights,
	}
}

// safeStr extracts a nested string from a map, returning "" if not found.
func safeStr(m map[string]any, keys ...string) string {
	current := any(m)
	for _, key := range keys {
		cm, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = cm[key]
	}
	s, _ := current.(string)
	return s
}

// safeAny extracts a nested value from a map.
func safeAny(m map[string]any, keys ...string) any {
	current := any(m)
	for _, key := range keys {
		cm, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = cm[key]
	}
	return current
}

// truncateStr truncates a string to maxLen, adding "..." if truncated.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// --- cloudAzureActivityLogAction ---

type cloudAzureActivityLogAction struct{}

func (a *cloudAzureActivityLogAction) Execute(ctx playbooks.PlaybookActionContext, _ map[string]any) (playbooks.PlaybookActionResponse, error) {
	return a.AutoExecute(ctx)
}

func (a *cloudAzureActivityLogAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	if !isAzureAlertSource(ctx.GetEvent().Source) {
		return false
	}
	// Activity log only needs subscription ID — it queries subscription-level operations,
	// not a specific resource. This allows Activity Log alerts (Resource Deleted, VM State
	// Change, etc.) to get enrichment even when azure_alert_target_resource is absent.
	return ctx.GetEvent().Labels["azure_subscription_id"] != ""
}

func (a *cloudAzureActivityLogAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels
	subscriptionID := labels["azure_subscription_id"]

	accountId, err := getAzureSubscriptionAccountID(ctx)
	if err != nil {
		return nil, err
	}

	eventTime := time.Now().UTC()
	if ctx.GetEvent().StartedAt != nil {
		eventTime = *ctx.GetEvent().StartedAt
	}

	cmd := fmt.Sprintf(
		"az monitor activity-log list --subscription %s --start-time %s --end-time %s --max-events 50 --output json",
		subscriptionID, eventTime.Add(-30*time.Minute).Format(time.RFC3339), eventTime.Format(time.RFC3339),
	)

	resp, err := ExecuteCli(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), CloudExecuteCliCommandRequest{
		AccountID: accountId,
		Command:   cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch activity log: %w", err)
	}

	dataStr, _ := resp["data"].(string)
	trimmed := strings.TrimSpace(dataStr)

	var md strings.Builder
	md.WriteString("Azure operations that occurred in the 30 minutes before the alert fired. Look for changes that may have triggered the alert.\n\n")

	if trimmed == "" || trimmed == "[]" || trimmed == "null" {
		md.WriteString("No activity log events found in this time window.\n")
		return formatAzureMarkdownResponse("Activity Log — Events Before Alert", md.String(), nil), nil
	}

	var entries []map[string]any
	if err := common.UnmarshalJson([]byte(trimmed), &entries); err != nil {
		md.WriteString("```\n" + truncateStr(trimmed, 5000) + "\n```\n")
		return formatAzureMarkdownResponse("Activity Log — Events Before Alert", md.String(), nil), nil
	}

	if len(entries) == 0 {
		md.WriteString("No activity log events found in this time window.\n")
		return formatAzureMarkdownResponse("Activity Log — Events Before Alert", md.String(), nil), nil
	}

	md.WriteString("| Time | Operation | Status | Caller | Resource |\n")
	md.WriteString("|------|-----------|--------|--------|----------|\n")

	var insights []playbooks.PlaybookActionResponseInsight
	for _, entry := range entries {
		ts := safeStr(entry, "eventTimestamp")
		op := safeStr(entry, "operationName", "localizedValue")
		if op == "" {
			op = safeStr(entry, "operationName", "value")
		}
		status := safeStr(entry, "status", "localizedValue")
		if status == "" {
			status = safeStr(entry, "status", "value")
		}
		caller := safeStr(entry, "caller")
		resource := safeStr(entry, "resourceId")

		// Truncate long resource IDs for table readability
		displayResource := resource
		if len(displayResource) > 60 {
			parts := strings.Split(displayResource, "/")
			if len(parts) > 2 {
				displayResource = ".../" + parts[len(parts)-2] + "/" + parts[len(parts)-1]
			}
		}

		fmt.Fprintf(&md, "| %s | %s | %s | %s | %s |\n",
			ts, op, status, caller, displayResource)

		// Flag failed operations as insights
		statusLower := strings.ToLower(status)
		if strings.Contains(statusLower, "failed") || strings.Contains(statusLower, "error") {
			insights = append(insights, playbooks.PlaybookActionResponseInsight{
				Message:  fmt.Sprintf("Failed operation: %s — %s", op, status),
				Severity: "High",
			})
		}
	}

	fmt.Fprintf(&md, "\n*%d events shown*\n", len(entries))

	return formatAzureMarkdownResponse("Activity Log — Events Before Alert", md.String(), insights), nil
}

// --- cloudAzureAlertRuleAction ---

type cloudAzureAlertRuleAction struct{}

func (a *cloudAzureAlertRuleAction) Execute(ctx playbooks.PlaybookActionContext, _ map[string]any) (playbooks.PlaybookActionResponse, error) {
	return a.AutoExecute(ctx)
}

func (a *cloudAzureAlertRuleAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	if !isAzureAlert(ctx) {
		return false
	}
	return isAzureResourceID(ctx.GetEvent().Labels["azure_alert_rule"])
}

func (a *cloudAzureAlertRuleAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels
	alertRule := labels["azure_alert_rule"]

	accountId, err := getAzureSubscriptionAccountID(ctx)
	if err != nil {
		return nil, err
	}

	cmd := fmt.Sprintf("az resource show --ids '%s' --output json", alertRule)

	resp, err := ExecuteCli(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), CloudExecuteCliCommandRequest{
		AccountID: accountId,
		Command:   cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch alert rule config: %w", err)
	}

	dataStr, _ := resp["data"].(string)
	trimmed := strings.TrimSpace(dataStr)

	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return formatAzureMarkdownResponse("Alert Rule Configuration",
			"No alert rule configuration found.\n", nil), nil
	}

	var rule map[string]any
	if err := common.UnmarshalJson([]byte(trimmed), &rule); err != nil {
		return formatAzureMarkdownResponse("Alert Rule Configuration",
			"```\n"+truncateStr(trimmed, 5000)+"\n```\n", nil), nil
	}

	ruleType := strings.ToLower(safeStr(rule, "type"))
	isKQL := strings.Contains(ruleType, "scheduledqueryrules")

	var md strings.Builder
	extractedLabels := map[string]any{}

	md.WriteString("| Property | Value |\n")
	md.WriteString("|----------|-------|\n")

	addRow := func(prop, val string) {
		if val != "" {
			fmt.Fprintf(&md, "| %s | %s |\n", prop, val)
		}
	}

	addRow("Name", safeStr(rule, "name"))
	if isKQL {
		addRow("Type", "Scheduled Query Rule (KQL / Log Alert)")
	} else {
		addRow("Type", safeStr(rule, "type"))
	}
	addRow("Location", safeStr(rule, "location"))

	props, _ := rule["properties"].(map[string]any)
	if props != nil {
		if enabled, ok := props["enabled"].(bool); ok {
			addRow("Enabled", fmt.Sprintf("%v", enabled))
		}
		if sev, ok := props["severity"].(float64); ok {
			addRow("Severity", fmt.Sprintf("%.0f", sev))
		}
		addRow("Description", safeStr(props, "description"))
		addRow("Evaluation Frequency", safeStr(props, "evaluationFrequency"))
		addRow("Window Size", safeStr(props, "windowSize"))

		// Extract scopes (workspace for KQL rules)
		var scopeStrs []string
		if scopes, ok := props["scopes"].([]any); ok {
			for _, s := range scopes {
				if ss, ok := s.(string); ok {
					scopeStrs = append(scopeStrs, ss)
				}
			}
			addRow("Scopes", strings.Join(scopeStrs, ", "))
		}

		// Extract condition/criteria
		if criteria, ok := props["criteria"].(map[string]any); ok {
			if allOf, ok := criteria["allOf"].([]any); ok {
				for i, cRaw := range allOf {
					c, ok := cRaw.(map[string]any)
					if !ok {
						continue
					}

					operator := safeStr(c, "operator")
					threshold := ""
					if t, ok := c["threshold"].(float64); ok {
						threshold = fmt.Sprintf("%.2f", t)
					}
					timeAgg := safeStr(c, "timeAggregation")

					query := safeStr(c, "query")
					metricName := safeStr(c, "metricName")

					if isKQL && query != "" {
						// KQL alert — show full query, dimensions, threshold
						addRow(fmt.Sprintf("Condition[%d]", i), fmt.Sprintf("%s %s (aggregation: %s)", operator, threshold, timeAgg))

						// Dimensions
						if dims, ok := c["dimensions"].([]any); ok && len(dims) > 0 {
							for _, dRaw := range dims {
								d, ok := dRaw.(map[string]any)
								if !ok {
									continue
								}
								dimName := safeStr(d, "name")
								dimOp := safeStr(d, "operator")
								dimVals := ""
								if vs, ok := d["values"].([]any); ok {
									valStrs := []string{}
									for _, v := range vs {
										if s, ok := v.(string); ok {
											valStrs = append(valStrs, s)
										}
									}
									dimVals = strings.Join(valStrs, ", ")
								}
								addRow(fmt.Sprintf("Dimension[%s]", dimName), fmt.Sprintf("%s [%s]", dimOp, dimVals))
							}
						}

						// Failing periods
						if fp, ok := c["failingPeriods"].(map[string]any); ok {
							evalPeriods := ""
							minFailing := ""
							if v, ok := fp["numberOfEvaluationPeriods"].(float64); ok {
								evalPeriods = fmt.Sprintf("%.0f", v)
							}
							if v, ok := fp["minFailingPeriodsToAlert"].(float64); ok {
								minFailing = fmt.Sprintf("%.0f", v)
							}
							if evalPeriods != "" {
								addRow("Failing Periods", fmt.Sprintf("%s of %s evaluations must fail", minFailing, evalPeriods))
							}
						}

						md.WriteString("\n**KQL Query:**\n```kql\n" + query + "\n```\n")

						// Extract labels for downstream actions
						extractedLabels["azure_alert_kql_query"] = query
						// Prefer a Log Analytics workspace scope; fall back to the first scope.
						// Scheduled query rules can target any KQL-queryable resource
						// (workspaces, App Insights, VMs, AKS, etc.), so the first scope
						// is always valid for QueryLogs.
						for _, s := range scopeStrs {
							if isAzureKQLQueryableResourceID(s) {
								extractedLabels["azure_alert_kql_workspace_scope"] = s
								break
							}
						}
						if extractedLabels["azure_alert_kql_workspace_scope"] == nil && len(scopeStrs) > 0 {
							extractedLabels["azure_alert_kql_workspace_scope"] = scopeStrs[0]
						}
						extractedLabels["azure_alert_kql_operator"] = operator
						extractedLabels["azure_alert_kql_threshold"] = threshold
						extractedLabels["azure_alert_kql_aggregation"] = timeAgg
					} else if metricName != "" {
						// Metric alert
						ns := safeStr(c, "metricNamespace")
						condition := fmt.Sprintf("%s %s %s", metricName, operator, threshold)
						if ns != "" {
							condition = fmt.Sprintf("%s/%s %s %s", ns, metricName, operator, threshold)
						}
						if timeAgg != "" {
							condition += fmt.Sprintf(" (aggregation: %s)", timeAgg)
						}
						addRow(fmt.Sprintf("Condition[%d]", i), condition)

						// Metric dimensions
						if dims, ok := c["dimensions"].([]any); ok {
							for _, dRaw := range dims {
								d, ok := dRaw.(map[string]any)
								if !ok {
									continue
								}
								addRow(fmt.Sprintf("Dimension[%s]", safeStr(d, "name")),
									fmt.Sprintf("%s [%s]", safeStr(d, "operator"), safeStr(d, "values")))
							}
						}
					} else if query != "" {
						// Unknown type with query
						addRow(fmt.Sprintf("Query[%d]", i), truncateStr(query, 200))
					}
				}
			}
		}
	}

	result := formatAzureMarkdownResponse("Alert Rule Configuration", md.String(), nil)
	result.Labels = extractedLabels
	return result, nil
}

// --- cloudAzureServiceHealthAction ---

type cloudAzureServiceHealthAction struct{}

func (a *cloudAzureServiceHealthAction) Execute(ctx playbooks.PlaybookActionContext, _ map[string]any) (playbooks.PlaybookActionResponse, error) {
	return a.AutoExecute(ctx)
}

func (a *cloudAzureServiceHealthAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	return isAzureSubscriptionLevelAlert(ctx)
}

func (a *cloudAzureServiceHealthAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels
	subscriptionID := labels["azure_subscription_id"]

	accountId, err := getAzureSubscriptionAccountID(ctx)
	if err != nil {
		return nil, err
	}

	cmd := fmt.Sprintf(
		"az rest --method get --url https://management.azure.com/subscriptions/%s/providers/Microsoft.ResourceHealth/events?api-version=2024-02-01 --output json",
		subscriptionID,
	)

	resp, err := ExecuteCli(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), CloudExecuteCliCommandRequest{
		AccountID: accountId,
		Command:   cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch service health events: %w", err)
	}

	dataStr, _ := resp["data"].(string)
	trimmed := strings.TrimSpace(dataStr)

	var md strings.Builder
	md.WriteString("Active or recent Azure service health incidents affecting this subscription. Service outages or degradation here may explain the alert.\n\n")

	if trimmed == "" || trimmed == "[]" || trimmed == "{}" || trimmed == "null" {
		md.WriteString("No active service health events found.\n")
		return formatAzureMarkdownResponse("Azure Service Health Events", md.String(), nil), nil
	}

	// Response is either {value: [...]} or directly an array
	var events []map[string]any
	var wrapper map[string]any
	if err := common.UnmarshalJson([]byte(trimmed), &wrapper); err == nil {
		if valueArr, ok := wrapper["value"].([]any); ok {
			for _, v := range valueArr {
				if vm, ok := v.(map[string]any); ok {
					events = append(events, vm)
				}
			}
		}
	}
	if events == nil {
		// Try parsing as direct array
		_ = common.UnmarshalJson([]byte(trimmed), &events)
	}

	if len(events) == 0 {
		md.WriteString("No active service health events found.\n")
		return formatAzureMarkdownResponse("Azure Service Health Events", md.String(), nil), nil
	}

	md.WriteString("| Event | Type | Status | Impacted Services | Last Updated |\n")
	md.WriteString("|-------|------|--------|-------------------|--------------|\n")

	var insights []playbooks.PlaybookActionResponseInsight
	for _, event := range events {
		props, _ := event["properties"].(map[string]any)
		if props == nil {
			continue
		}
		title := safeStr(props, "title")
		eventType := safeStr(props, "eventType")
		status := safeStr(props, "status")
		lastUpdate := safeStr(props, "lastUpdateTime")

		if title == "" {
			title = safeStr(event, "name")
		}

		// Extract impacted services and regions from the impact array
		impactedServices := []string{}
		if impactArr, ok := props["impact"].([]any); ok {
			for _, imp := range impactArr {
				if impMap, ok := imp.(map[string]any); ok {
					svc := safeStr(impMap, "impactedService")
					regions := []string{}
					if regArr, ok := impMap["impactedRegions"].([]any); ok {
						for _, reg := range regArr {
							if regMap, ok := reg.(map[string]any); ok {
								r := safeStr(regMap, "impactedRegion")
								if r != "" {
									regions = append(regions, r)
								}
							}
						}
					}
					if svc != "" {
						entry := svc
						if len(regions) > 0 {
							entry += " (" + strings.Join(regions, ", ") + ")"
						}
						impactedServices = append(impactedServices, entry)
					}
				}
			}
		}

		impactStr := strings.Join(impactedServices, "; ")

		fmt.Fprintf(&md, "| %s | %s | %s | %s | %s |\n",
			title, eventType, status, impactStr, lastUpdate)

		statusLower := strings.ToLower(status)
		if strings.Contains(statusLower, "active") || strings.Contains(statusLower, "investigating") {
			insights = append(insights, playbooks.PlaybookActionResponseInsight{
				Message:  fmt.Sprintf("Active service health event: %s — %s (%s)", title, status, impactStr),
				Severity: "High",
			})
		}
	}

	fmt.Fprintf(&md, "\n*%d events found*\n", len(events))

	return formatAzureMarkdownResponse("Azure Service Health Events", md.String(), insights), nil
}

// --- cloudAzureRelatedAlertsAction ---

type cloudAzureRelatedAlertsAction struct{}

func (a *cloudAzureRelatedAlertsAction) Execute(ctx playbooks.PlaybookActionContext, _ map[string]any) (playbooks.PlaybookActionResponse, error) {
	return a.AutoExecute(ctx)
}

func (a *cloudAzureRelatedAlertsAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	if !isAzureSubscriptionLevelAlert(ctx) {
		return false
	}
	alertRule := ctx.GetEvent().Labels["azure_alert_rule"]
	return isAzureResourceID(alertRule) && extractResourceGroup(alertRule) != ""
}

func (a *cloudAzureRelatedAlertsAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels
	subscriptionID := labels["azure_subscription_id"]
	alertRule := labels["azure_alert_rule"]
	rg := extractResourceGroup(alertRule)

	accountId, err := getAzureSubscriptionAccountID(ctx)
	if err != nil {
		return nil, err
	}

	cmd := fmt.Sprintf(
		"az monitor metrics alert list --resource-group %s --subscription %s --output json",
		rg, subscriptionID,
	)

	resp, err := ExecuteCli(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), CloudExecuteCliCommandRequest{
		AccountID: accountId,
		Command:   cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch related alerts: %w", err)
	}

	dataStr, _ := resp["data"].(string)
	trimmed := strings.TrimSpace(dataStr)

	var md strings.Builder
	fmt.Fprintf(&md, "Other alert rules in resource group **%s** that may indicate correlated issues.\n\n", rg)

	if trimmed == "" || trimmed == "[]" || trimmed == "null" {
		md.WriteString("No other alert rules found in this resource group.\n")
		return formatAzureMarkdownResponse("Related Alert Rules", md.String(), nil), nil
	}

	var alerts []map[string]any
	if err := common.UnmarshalJson([]byte(trimmed), &alerts); err != nil {
		md.WriteString("```\n" + truncateStr(trimmed, 5000) + "\n```\n")
		return formatAzureMarkdownResponse("Related Alert Rules", md.String(), nil), nil
	}

	if len(alerts) == 0 {
		md.WriteString("No other alert rules found in this resource group.\n")
		return formatAzureMarkdownResponse("Related Alert Rules", md.String(), nil), nil
	}

	md.WriteString("| Name | Severity | Enabled | Target Resource | Condition |\n")
	md.WriteString("|------|----------|---------|-----------------|----------|\n")

	for _, alert := range alerts {
		name := safeStr(alert, "name")
		sev := ""
		if s, ok := alert["severity"].(float64); ok {
			sev = fmt.Sprintf("Sev%.0f", s)
		}
		enabled := ""
		if e, ok := alert["enabled"].(bool); ok {
			enabled = fmt.Sprintf("%v", e)
		}

		// Target resource from scopes
		target := ""
		if scopes, ok := alert["scopes"].([]any); ok && len(scopes) > 0 {
			if s, ok := scopes[0].(string); ok {
				target = s
				if len(target) > 50 {
					parts := strings.Split(target, "/")
					if len(parts) > 2 {
						target = ".../" + parts[len(parts)-2] + "/" + parts[len(parts)-1]
					}
				}
			}
		}

		// Condition summary
		condition := ""
		if criteria := safeAny(alert, "criteria"); criteria != nil {
			if criteriaMap, ok := criteria.(map[string]any); ok {
				if allOf, ok := criteriaMap["allOf"].([]any); ok && len(allOf) > 0 {
					if c, ok := allOf[0].(map[string]any); ok {
						metricName := safeStr(c, "metricName")
						operator := safeStr(c, "operator")
						threshold := ""
						if t, ok := c["threshold"].(float64); ok {
							threshold = fmt.Sprintf("%.2f", t)
						}
						if metricName != "" {
							condition = fmt.Sprintf("%s %s %s", metricName, operator, threshold)
						}
					}
				}
			}
		}

		fmt.Fprintf(&md, "| %s | %s | %s | %s | %s |\n",
			name, sev, enabled, target, condition)
	}

	fmt.Fprintf(&md, "\n*%d alert rules found*\n", len(alerts))

	return formatAzureMarkdownResponse("Related Alert Rules", md.String(), nil), nil
}

// --- cloudAzureKQLQueryResultsAction ---
// Re-executes the KQL query from a Log Alerts V2 rule to show actual triggering data with dimensions.
// Renders as a markdown table via azureTextEnricherResponse (not the log viewer).

type cloudAzureKQLQueryResultsAction struct{}

func (a *cloudAzureKQLQueryResultsAction) Execute(ctx playbooks.PlaybookActionContext, _ map[string]any) (playbooks.PlaybookActionResponse, error) {
	return a.AutoExecute(ctx)
}

func (a *cloudAzureKQLQueryResultsAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	if !isAzureAlertSource(ctx.GetEvent().Source) {
		return false
	}
	labels := ctx.GetEvent().Labels
	if labels["azure_alert_kql_query"] == "" {
		return false
	}
	return isAzureKQLQueryableResourceID(labels["azure_alert_kql_workspace_scope"]) ||
		isAzureKQLQueryableResourceID(labels["azure_dcr_workspace_id"])
}

func (a *cloudAzureKQLQueryResultsAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels
	kqlQuery := labels["azure_alert_kql_query"]
	workspaceScope := labels["azure_alert_kql_workspace_scope"]
	// The workspace scope label may contain a non-workspace resource ID (e.g. a VM)
	// from resource-centric alert rules. Fall through to the DCR workspace in that case.
	if !isAzureKQLQueryableResourceID(workspaceScope) {
		workspaceScope = labels["azure_dcr_workspace_id"]
	}

	accountId, err := getAzureSubscriptionAccountID(ctx)
	if err != nil {
		return nil, err
	}

	var limit int64 = 50
	resourceResp, err := QueryLogs(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), QueryLogsRequest{
		AccountId: accountId,
		Query: LogQuery{
			ResourceId:  workspaceScope,
			QueryString: kqlQuery,
			Limit:       &limit,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute KQL query: %w", err)
	}

	var md strings.Builder
	md.WriteString("Results from re-executing the alert rule's KQL query against the Log Analytics workspace.\n\n")

	if len(resourceResp.Results) == 0 {
		md.WriteString("No results returned. The condition may have cleared since the alert fired.\n")
		return formatAzureMarkdownResponse("Alert Query Results", md.String(), nil), nil
	}

	// Build table from first result's labels to discover columns
	var columns []string
	first := resourceResp.Results[0]
	for _, l := range first.Labels {
		columns = append(columns, l.Label)
	}

	// Write header
	md.WriteString("| ")
	if first.Message != "" {
		md.WriteString("Message | ")
	}
	for _, col := range columns {
		md.WriteString(col + " | ")
	}
	md.WriteString("\n|")
	if first.Message != "" {
		md.WriteString("---|")
	}
	for range columns {
		md.WriteString("---|")
	}
	md.WriteString("\n")

	// Write rows
	for _, row := range resourceResp.Results {
		md.WriteString("| ")
		if first.Message != "" {
			md.WriteString(truncateStr(row.Message, 80) + " | ")
		}
		labelMap := map[string]string{}
		for _, l := range row.Labels {
			labelMap[l.Label] = l.Value
		}
		for _, col := range columns {
			md.WriteString(labelMap[col] + " | ")
		}
		md.WriteString("\n")
	}

	fmt.Fprintf(&md, "\n*%d rows returned*\n", len(resourceResp.Results))

	var insights []playbooks.PlaybookActionResponseInsight
	threshold := labels["azure_alert_kql_threshold"]
	operator := labels["azure_alert_kql_operator"]
	if threshold != "" && operator != "" {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Alert condition: %s %s (results above are current values)", operator, threshold),
			Severity: "Medium",
		})
	}

	return formatAzureMarkdownResponse("Alert Query Results", md.String(), insights), nil
}

// --- cloudAzureDCRInfoAction ---
// Shows Data Collection Rules configured for the target resource (VMs/VMSS).

type cloudAzureDCRInfoAction struct{}

func (a *cloudAzureDCRInfoAction) Execute(ctx playbooks.PlaybookActionContext, _ map[string]any) (playbooks.PlaybookActionResponse, error) {
	return a.AutoExecute(ctx)
}

func (a *cloudAzureDCRInfoAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	if !isAzureAlertSource(ctx.GetEvent().Source) {
		return false
	}
	labels := ctx.GetEvent().Labels
	target := labels["azure_alert_target_resource"]
	if target == "" || !isAzureResourceID(target) {
		return false
	}
	svc := strings.ToLower(labels["azure_service_name"])
	return strings.Contains(svc, "virtualmachines") || strings.Contains(svc, "virtualmachinescalesets")
}

func (a *cloudAzureDCRInfoAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels
	targetResource := labels["azure_alert_target_resource"]

	accountId, err := getAzureSubscriptionAccountID(ctx)
	if err != nil {
		return nil, err
	}

	reqCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)

	// Fetch DCR associations for the resource
	assocCmd := fmt.Sprintf(
		"az rest --method get --url 'https://management.azure.com%s/providers/Microsoft.Insights/dataCollectionRuleAssociations?api-version=2022-06-01' --output json",
		targetResource,
	)

	assocResp, err := ExecuteCli(reqCtx, CloudExecuteCliCommandRequest{
		AccountID: accountId,
		Command:   assocCmd,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch DCR associations: %w", err)
	}

	dataStr, _ := assocResp["data"].(string)
	trimmed := strings.TrimSpace(dataStr)

	var md strings.Builder
	md.WriteString("Data Collection Rules configured for this resource. These determine what performance counters, logs, and metrics are collected and which Log Analytics tables they flow into.\n\n")
	extractedLabels := map[string]any{}

	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		md.WriteString("No Data Collection Rule associations found for this resource.\n")
		return formatAzureMarkdownResponse("Data Collection Rules", md.String(), nil), nil
	}

	var wrapper map[string]any
	if err := common.UnmarshalJson([]byte(trimmed), &wrapper); err != nil {
		md.WriteString("```\n" + truncateStr(trimmed, 3000) + "\n```\n")
		return formatAzureMarkdownResponse("Data Collection Rules", md.String(), nil), nil
	}

	associations, _ := wrapper["value"].([]any)
	if len(associations) == 0 {
		md.WriteString("No Data Collection Rule associations found.\n")
		return formatAzureMarkdownResponse("Data Collection Rules", md.String(), nil), nil
	}

	// For each association, fetch the DCR details
	for _, assocRaw := range associations {
		assoc, ok := assocRaw.(map[string]any)
		if !ok {
			continue
		}

		props, _ := assoc["properties"].(map[string]any)
		if props == nil {
			continue
		}

		dcrID, _ := props["dataCollectionRuleId"].(string)
		if dcrID == "" {
			continue
		}

		// Fetch DCR details
		dcrCmd := fmt.Sprintf("az rest --method get --url 'https://management.azure.com%s?api-version=2022-06-01' --output json", dcrID)
		dcrResp, err := ExecuteCli(reqCtx, CloudExecuteCliCommandRequest{
			AccountID: accountId,
			Command:   dcrCmd,
		})
		if err != nil {
			ctx.GetLogger().Warn("cloud_azure_dcr_info: failed to fetch DCR details", "dcrId", dcrID, "error", err)
			continue
		}

		dcrDataStr, _ := dcrResp["data"].(string)
		dcrTrimmed := strings.TrimSpace(dcrDataStr)

		var dcr map[string]any
		if err := common.UnmarshalJson([]byte(dcrTrimmed), &dcr); err != nil {
			continue
		}

		dcrName := safeStr(dcr, "name")
		fmt.Fprintf(&md, "### DCR: %s\n\n", dcrName)

		dcrProps, _ := dcr["properties"].(map[string]any)
		if dcrProps == nil {
			continue
		}

		// Data Sources
		if dataSources, ok := dcrProps["dataSources"].(map[string]any); ok {
			// Performance counters
			if perfCounters, ok := dataSources["performanceCounters"].([]any); ok && len(perfCounters) > 0 {
				md.WriteString("**Performance Counters:**\n\n")
				md.WriteString("| Name | Counters | Sampling (sec) | Streams |\n")
				md.WriteString("|------|----------|---------------|---------|\n")
				for _, pcRaw := range perfCounters {
					pc, ok := pcRaw.(map[string]any)
					if !ok {
						continue
					}
					name := safeStr(pc, "name")
					sampling := ""
					if s, ok := pc["samplingFrequencyInSeconds"].(float64); ok {
						sampling = fmt.Sprintf("%.0f", s)
					}
					var counters []string
					if cList, ok := pc["counterSpecifiers"].([]any); ok {
						for _, c := range cList {
							if cs, ok := c.(string); ok {
								counters = append(counters, cs)
							}
						}
					}
					var streams []string
					if sList, ok := pc["streams"].([]any); ok {
						for _, s := range sList {
							if ss, ok := s.(string); ok {
								streams = append(streams, ss)
							}
						}
					}
					fmt.Fprintf(&md, "| %s | %s | %s | %s |\n",
						name, truncateStr(strings.Join(counters, ", "), 100), sampling, strings.Join(streams, ", "))
				}
				md.WriteString("\n")
			}

			// Syslog / Windows Event Log
			if syslog, ok := dataSources["syslog"].([]any); ok && len(syslog) > 0 {
				md.WriteString("**Syslog Collection:** Configured\n\n")
			}
			if winEvents, ok := dataSources["windowsEventLogs"].([]any); ok && len(winEvents) > 0 {
				md.WriteString("**Windows Event Logs:** Configured\n\n")
			}
		}

		// Data Flows — shows destination tables
		if dataFlows, ok := dcrProps["dataFlows"].([]any); ok && len(dataFlows) > 0 {
			md.WriteString("**Data Flows (Destination Tables):**\n\n")
			md.WriteString("| Streams | Destinations | Output Stream |\n")
			md.WriteString("|---------|-------------|---------------|\n")
			for _, dfRaw := range dataFlows {
				df, ok := dfRaw.(map[string]any)
				if !ok {
					continue
				}
				var streams, dests []string
				if sList, ok := df["streams"].([]any); ok {
					for _, s := range sList {
						if ss, ok := s.(string); ok {
							streams = append(streams, ss)
						}
					}
				}
				if dList, ok := df["destinations"].([]any); ok {
					for _, d := range dList {
						if ds, ok := d.(string); ok {
							dests = append(dests, ds)
						}
					}
				}
				outputStream := safeStr(df, "outputStream")
				fmt.Fprintf(&md, "| %s | %s | %s |\n",
					strings.Join(streams, ", "), strings.Join(dests, ", "), outputStream)
			}
			md.WriteString("\n")
		}

		// Destinations — extract workspace info
		if destinations, ok := dcrProps["destinations"].(map[string]any); ok {
			if laList, ok := destinations["logAnalytics"].([]any); ok {
				for _, laRaw := range laList {
					la, ok := laRaw.(map[string]any)
					if !ok {
						continue
					}
					wsID := safeStr(la, "workspaceResourceId")
					wsName := safeStr(la, "name")
					if wsID != "" {
						fmt.Fprintf(&md, "**Log Analytics Workspace:** %s (`%s`)\n\n", wsName, wsID)
						extractedLabels["azure_dcr_workspace_id"] = wsID
					}
				}
			}
		}
	}

	result := formatAzureMarkdownResponse("Data Collection Rules", md.String(), nil)
	result.Labels = extractedLabels
	return result, nil
}

// isAzureResourceID checks if a resource ID points to an actual Azure resource (not just a subscription)
func isAzureResourceID(resourceID string) bool {
	return strings.Contains(strings.ToLower(resourceID), "/providers/")
}

// isAzureKQLQueryableResourceID checks if a resource ID points to a resource that supports
// KQL queries — Log Analytics workspaces or Application Insights components.
func isAzureKQLQueryableResourceID(resourceID string) bool {
	lower := strings.ToLower(resourceID)
	return resourceID != "" && (strings.Contains(lower, "microsoft.operationalinsights/workspaces") ||
		strings.Contains(lower, "microsoft.insights/components"))
}

// getAzureResourceType extracts the full resource type from an Azure resource ID
// e.g. "/subscriptions/.../providers/Microsoft.Compute/virtualMachines/myvm" -> "microsoft.compute/virtualmachines"
// getDefaultAzureMetrics returns default metric names for common Azure resource types.
// This ensures cloud_metrics works even when azure_alert_context is unavailable.
func getDefaultAzureMetrics(serviceName string) []string {
	defaults := map[string][]string{
		"microsoft.compute/virtualmachines": {
			"Percentage CPU",
			"Available Memory Bytes",
			"Network In Total",
			"Network Out Total",
			"Disk Read Bytes",
			"Disk Write Bytes",
			"Disk Read Operations/Sec",
			"Disk Write Operations/Sec",
		},
		"microsoft.compute/virtualmachinescalesets": {
			"Percentage CPU",
			"Available Memory Bytes",
			"Network In Total",
			"Network Out Total",
		},
		"microsoft.sql/servers/databases": {
			"cpu_percent",
			"physical_data_read_percent",
			"log_write_percent",
			"dtu_consumption_percent",
			"storage_percent",
		},
		"microsoft.dbforpostgresql/flexibleservers": {
			"cpu_percent",
			"memory_percent",
			"storage_percent",
			"active_connections",
		},
		"microsoft.dbformysql/flexibleservers": {
			"cpu_percent",
			"memory_percent",
			"storage_percent",
			"active_connections",
		},
		"microsoft.containerservice/managedclusters": {
			"node_cpu_usage_percentage",
			"node_memory_rss_percentage",
			"node_disk_usage_percentage",
		},
		"microsoft.web/sites": {
			"CpuTime",
			"MemoryWorkingSet",
			"Http5xx",
			"HttpResponseTime",
			"Requests",
		},
		"microsoft.cache/redis": {
			"usedmemorypercentage",
			"serverLoad",
			"connectedclients",
			"cacheRead",
			"cacheWrite",
		},
	}
	return defaults[strings.ToLower(serviceName)]
}

func getAzureResourceType(resourceID string) string {
	parts := strings.Split(strings.Trim(resourceID, "/"), "/")
	for i, part := range parts {
		if strings.EqualFold(part, "providers") && i+2 < len(parts) {
			// Build full nested resource type: provider/type1/type2/...
			// e.g. "/providers/Microsoft.Sql/servers/myserver/databases/mydb"
			// → "microsoft.sql/servers/databases"
			remaining := parts[i+1:] // [Microsoft.Sql, servers, myserver, databases, mydb]
			typeParts := []string{remaining[0]}
			for j := 1; j < len(remaining); j += 2 {
				typeParts = append(typeParts, remaining[j])
			}
			return strings.ToLower(strings.Join(typeParts, "/"))
		}
	}
	return ""
}

// extractResourceGroup extracts the resource group name from an Azure resource ID
// e.g. "/subscriptions/.../resourceGroups/myRG/providers/..." -> "myRG"
func extractResourceGroup(resourceID string) string {
	parts := strings.Split(strings.Trim(resourceID, "/"), "/")
	for i, part := range parts {
		if strings.EqualFold(part, "resourceGroups") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// --- VPC Flow Logs action ---

// Validation regexes for AWS identifiers interpolated into shell commands.
// These reject any input outside the documented AWS character sets, eliminating
// shell metacharacters (`;`, `$`, backticks, quotes, newlines, etc.) before
// the value reaches command construction.
var (
	awsRegionRegex       = regexp.MustCompile(`^[a-z]{2}-(?:gov-)?[a-z]+-\d+$`)
	awsVpcIdRegex        = regexp.MustCompile(`^vpc-[0-9a-f]{8,17}$`)
	awsLogGroupNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-/.#]{1,512}$`)
)

// shellSingleQuote wraps s in single quotes and safely escapes embedded single
// quotes using the standard `'\”` POSIX-shell pattern. Use for any
// user-controlled value interpolated into a shell command string.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type cloudVpcFlowLogsAction struct{}

type cloudVpcFlowLogsParams struct {
	AccountId        string     `json:"account_id,omitempty"`
	VpcId            string     `json:"vpc_id"`
	Region           string     `json:"region" validate:"required"`
	LogGroupName     string     `json:"log_group_name"` // Optional: override auto-discovery
	StartTime        *time.Time `json:"start_time"`
	EndTime          *time.Time `json:"end_time"`
	OnlyFailed       bool       `json:"only_failed"`        // If true, only show rejected/failed connections
	IncludePublicIPs bool       `json:"include_public_ips"` // If true, include public IP traffic (default: false, private only)
	IPWhitelist      []string   `json:"ip_whitelist"`       // Optional: specific CIDR ranges to include (overrides private IP filter)
}

func (a *cloudVpcFlowLogsAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	labels := ctx.GetEvent().Labels

	if labels["aws_region"] == "" {
		return false
	}

	if labels["resource_vpc_id"] != "" {
		return true
	}

	// aws_event_instance alone is not enough — resources like ECS tasks, DynamoDB
	// tables, and SQS queues carry an ARN but no VpcId in cloud_resourses meta.
	// Confirm the lookup will succeed before claiming we can run.
	if labels["aws_event_instance"] != "" {
		return getVpcIdForResource(labels["aws_event_instance"], ctx.GetTenantId()) != ""
	}

	return false
}

func (a *cloudVpcFlowLogsAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels
	vpcId := labels["resource_vpc_id"]
	region := labels["aws_region"]

	// Resolve VpcId from cloud_resourses if not in labels
	if vpcId == "" && labels["aws_event_instance"] != "" {
		vpcId = getVpcIdForResource(labels["aws_event_instance"], ctx.GetTenantId())
	}

	if vpcId == "" || region == "" {
		ctx.GetLogger().Warn("cloud_vpc_flowlogs: missing required labels", "vpc_id", vpcId, "region", region)
		return nil, fmt.Errorf("vpc_id and region are required")
	}

	rawParams := map[string]any{
		"vpc_id":      vpcId,
		"region":      region,
		"only_failed": true, // By default, only show failed/rejected connections
		"title":       "VPC Flow Logs Failed Connections - " + vpcId,
	}

	// Get cloud account ID
	if labels["aws_account"] != "" {
		accountId, err := getCloudAccountIdByNumber(labels["aws_account"], ctx.GetTenantId())
		if err != nil {
			ctx.GetLogger().Warn("cloud_vpc_flowlogs: could not find cloud account", "account_number", labels["aws_account"], "error", err)
		} else {
			rawParams["account_id"] = accountId
		}
	}

	return a.Execute(ctx, rawParams)
}

func (a *cloudVpcFlowLogsAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params cloudVpcFlowLogsParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	if params.AccountId == "" {
		params.AccountId = ctx.GetAccountId()
	}

	if params.AccountId == "" {
		return nil, errors.New("account_id is required")
	}

	if params.VpcId == "" {
		return nil, errors.New("vpc_id is required")
	}

	if params.Region == "" {
		return nil, errors.New("region is required")
	}

	// Reject any input that doesn't match the documented AWS format. This
	// eliminates shell metacharacters before they reach command construction
	// (defense-in-depth alongside single-quote wrapping below).
	if !awsRegionRegex.MatchString(params.Region) {
		return nil, fmt.Errorf("invalid region format: %q", params.Region)
	}
	if !awsVpcIdRegex.MatchString(params.VpcId) {
		return nil, fmt.Errorf("invalid vpc_id format: %q", params.VpcId)
	}
	if params.LogGroupName != "" && !awsLogGroupNameRegex.MatchString(params.LogGroupName) {
		return nil, fmt.Errorf("invalid log_group_name format: %q", params.LogGroupName)
	}

	// Prioritize time sources: explicit params > event times > default (last 1 hour)
	var startTime, endTime time.Time

	if params.StartTime != nil {
		startTime = *params.StartTime
	} else if ctx.GetEvent().StartedAt != nil {
		startTime = *ctx.GetEvent().StartedAt
	} else {
		startTime = time.Now().Add(-1 * time.Hour)
	}

	if params.EndTime != nil {
		endTime = *params.EndTime
	} else if ctx.GetEvent().EndedAt != nil {
		endTime = *ctx.GetEvent().EndedAt
	} else {
		endTime = time.Now()
	}

	// Discover VPC Flow Logs log group name and format
	var logGroupName string
	var logFormat string
	if params.LogGroupName != "" {
		// Use provided log group name
		logGroupName = params.LogGroupName
		logFormat = "" // Will use default format
		ctx.GetLogger().Info("cloud_vpc_flowlogs: using provided log group name", "log_group", logGroupName)
	} else {
		// Auto-discover log group and format by querying EC2 describe-flow-logs
		ctx.GetLogger().Info("cloud_vpc_flowlogs: discovering log group name and format", "vpc_id", params.VpcId)
		discoveredLogGroup, discoveredFormat, err := a.discoverVpcFlowLogsConfig(ctx, params.AccountId, params.VpcId, params.Region)
		if err != nil {
			ctx.GetLogger().Warn("cloud_vpc_flowlogs: failed to discover log group", "error", err)
			// Return helpful error message
			return nil, fmt.Errorf("failed to discover VPC Flow Logs log group for VPC '%s'. VPC Flow Logs may not be enabled or published to CloudWatch Logs. Error: %w", params.VpcId, err)
		}
		logGroupName = discoveredLogGroup
		logFormat = discoveredFormat
		ctx.GetLogger().Info("cloud_vpc_flowlogs: discovered log group", "log_group", logGroupName, "log_format", logFormat)
	}

	// Build CloudWatch Insights query for VPC Flow Logs
	// Query structure: filter by VPC, aggregate failed connections
	// Build parse pattern from log format
	parsePattern := buildVpcFlowLogsParsePattern(logFormat)

	queryString := fmt.Sprintf(`fields @timestamp, @message
| parse @message %s
| filter log_status = "OK"`, parsePattern)

	if params.OnlyFailed {
		queryString += `
| filter action = "REJECT"`
	}

	// IP filtering logic: configurable based on parameters
	if !params.IncludePublicIPs {
		// Determine which IP ranges to filter
		var ipRanges []string
		if len(params.IPWhitelist) > 0 {
			// Use custom whitelist provided by user
			ipRanges = params.IPWhitelist
		} else {
			// Use default RFC 1918 private IP ranges
			ipRanges = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
		}

		// Build regex filter conditions for source and destination
		// CloudWatch Logs Insights doesn't support isIpv4InSubnet, so we use regex patterns
		var srcConditions []string
		var dstConditions []string
		for _, cidr := range ipRanges {
			regex := cidrToRegex(cidr)
			if regex != "" {
				srcConditions = append(srcConditions, fmt.Sprintf(`srcaddr like /%s/`, regex))
				dstConditions = append(dstConditions, fmt.Sprintf(`dstaddr like /%s/`, regex))
			}
		}

		if len(srcConditions) > 0 {
			queryString += fmt.Sprintf(`
| filter (%s)
| filter (%s)`,
				strings.Join(srcConditions, " or "),
				strings.Join(dstConditions, " or "))
		}
	}

	queryString += `
| stats
    sum(bytes) as total_bytes,
    sum(packets) as total_packets,
    count(*) as connection_count
  by srcaddr, dstaddr, dstport, protocol, action
| sort total_bytes desc
| limit 100`

	// Execute CloudWatch Logs Insights query.
	// All user-controlled values are single-quoted with shellSingleQuote so
	// embedded shell metacharacters cannot break out. Region and logGroupName
	// are also regex-validated above.
	command := fmt.Sprintf(`aws logs start-query --log-group-name %s --start-time %d --end-time %d --query-string %s --region %s --output json`,
		shellSingleQuote(logGroupName),
		startTime.Unix(),
		endTime.Unix(),
		shellSingleQuote(queryString),
		shellSingleQuote(params.Region),
	)

	ctx.GetLogger().Info("cloud_vpc_flowlogs: executing CloudWatch query", "vpc_id", params.VpcId, "command", command)

	// Execute via cloud CLI
	cliResp, err := ExecuteCli(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), CloudExecuteCliCommandRequest{
		AccountID: params.AccountId,
		Command:   command,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to start VPC Flow Logs query: %w", err)
	}

	// Parse the query ID from response
	var queryIdResponse map[string]any
	if dataStr, ok := cliResp["data"].(string); ok {
		if err := common.UnmarshalJson([]byte(dataStr), &queryIdResponse); err != nil {
			return nil, fmt.Errorf("failed to parse query ID response: %w", err)
		}
	} else {
		return nil, errors.New("invalid CLI response format")
	}

	queryId, ok := queryIdResponse["queryId"].(string)
	if !ok || queryId == "" {
		return nil, errors.New("failed to extract query ID from response")
	}

	ctx.GetLogger().Info("cloud_vpc_flowlogs: query started", "query_id", queryId)

	// Wait for query to complete (poll for results)
	// CloudWatch Insights queries can take several minutes for large log groups
	// Poll for up to 5 minutes with exponential backoff
	maxAttempts := 60 // 60 attempts with varying sleep times
	baseDelay := 2 * time.Second
	maxDelay := 10 * time.Second
	pollStartTime := time.Now()

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// Exponential backoff with cap: 2s, 4s, 8s, 10s, 10s...
		delay := baseDelay * time.Duration(1<<uint(min(attempt, 3)))
		if delay > maxDelay {
			delay = maxDelay
		}
		time.Sleep(delay)

		getResultsCommand := fmt.Sprintf(`aws logs get-query-results --query-id %s --region %s --output json`,
			shellSingleQuote(queryId),
			shellSingleQuote(params.Region),
		)

		resultsResp, err := ExecuteCli(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), CloudExecuteCliCommandRequest{
			AccountID: params.AccountId,
			Command:   getResultsCommand,
		})

		if err != nil {
			ctx.GetLogger().Warn("cloud_vpc_flowlogs: error getting query results", "error", err, "attempt", attempt)
			continue
		}

		var queryResults map[string]any
		if dataStr, ok := resultsResp["data"].(string); ok {
			if err := common.UnmarshalJson([]byte(dataStr), &queryResults); err != nil {
				ctx.GetLogger().Warn("cloud_vpc_flowlogs: error parsing query results", "error", err)
				continue
			}
		}

		status, _ := queryResults["status"].(string)
		elapsedTime := time.Since(pollStartTime).Round(time.Second)
		ctx.GetLogger().Info("cloud_vpc_flowlogs: query status",
			"status", status,
			"attempt", attempt,
			"elapsed", elapsedTime.String())

		if status == "Complete" {
			// Format flows as logs for cloud_logs card rendering
			logoutput := []map[string]any{}
			totalBytes := int64(0)
			totalPackets := int64(0)
			totalConnections := int64(0)
			failedFlowCount := 0

			// Protocol number to name mapping
			protocolNames := map[string]string{
				"1":  "ICMP",
				"6":  "TCP",
				"17": "UDP",
				"47": "GRE",
				"50": "ESP",
				"51": "AH",
			}

			if resultsList, ok := queryResults["results"].([]any); ok {
				for _, resultRow := range resultsList {
					if rowFields, ok := resultRow.([]any); ok {
						flow := make(map[string]string)
						for _, field := range rowFields {
							if fieldMap, ok := field.(map[string]any); ok {
								fieldName, _ := fieldMap["field"].(string)
								fieldValue, _ := fieldMap["value"].(string)
								flow[fieldName] = fieldValue
							}
						}

						// Parse numeric fields for summary statistics
						if bytesStr, ok := flow["total_bytes"]; ok {
							if bytes, err := strconv.ParseInt(bytesStr, 10, 64); err == nil {
								totalBytes += bytes
							}
						}
						if packetsStr, ok := flow["total_packets"]; ok {
							if packets, err := strconv.ParseInt(packetsStr, 10, 64); err == nil {
								totalPackets += packets
							}
						}
						if countStr, ok := flow["connection_count"]; ok {
							if count, err := strconv.ParseInt(countStr, 10, 64); err == nil {
								totalConnections += count
							}
						}

						// Get protocol name
						protocolName := "Unknown"
						if protocolNum, ok := flow["protocol"]; ok {
							if name, exists := protocolNames[protocolNum]; exists {
								protocolName = name
							} else {
								protocolName = fmt.Sprintf("Protocol-%s", protocolNum)
							}
						}

						// Track failed flows
						action := flow["action"]
						if action == "REJECT" {
							failedFlowCount++
						}

						// Build human-readable log message
						message := fmt.Sprintf("%s: %s → %s:%s (%s) - %s packets, %s bytes, %s connections",
							action,
							flow["srcaddr"],
							flow["dstaddr"],
							flow["dstport"],
							protocolName,
							flow["total_packets"],
							flow["total_bytes"],
							flow["connection_count"],
						)

						// Format as log entry (same format as cloud_logs)
						logEntry := map[string]any{
							"timestamp": startTime.Format(time.RFC3339),
							"message":   message,
							"severity":  lo.Ternary(action == "REJECT", "ERROR", "INFO"),
							"labels":    flow, // All flow fields as labels
						}

						logoutput = append(logoutput, logEntry)
					}
				}
			}

			metadata := map[string]any{
				"query-result-version": "1.0",
				"query":                rawParams,
				"query_id":             queryId,
				"log_group_name":       logGroupName,
				"only_failed":          params.OnlyFailed,
				"vpc_id":               params.VpcId,
				"region":               params.Region,
			}

			additionalInfo := map[string]any{
				"action_name": "cloud_logs", // Use "cloud_logs" to trigger CloudLog card rendering
			}

			if title, ok := rawParams["title"]; ok {
				additionalInfo["title"] = title
			} else {
				additionalInfo["title"] = fmt.Sprintf("VPC Flow Logs - %s (Private IPs Only)", params.VpcId)
			}

			insights := []playbooks.PlaybookActionResponseInsight{}
			if len(logoutput) == 0 {
				insights = append(insights, playbooks.PlaybookActionResponseInsight{
					Message:  fmt.Sprintf("No %s private IP connections found in VPC Flow Logs for VPC '%s' during the specified time range (Internet traffic excluded)", lo.Ternary(params.OnlyFailed, "rejected", ""), params.VpcId),
					Severity: "info",
				})
			} else {
				if params.OnlyFailed && failedFlowCount > 0 {
					insights = append(insights, playbooks.PlaybookActionResponseInsight{
						Message:  fmt.Sprintf("Found %d rejected internal connection patterns between private IPs in VPC '%s'. These are likely security group misconfigurations preventing internal communication.", failedFlowCount, params.VpcId),
						Severity: "high",
					})
				}

				// Add summary insight
				insights = append(insights, playbooks.PlaybookActionResponseInsight{
					Message: fmt.Sprintf("Analyzed %d connection patterns (%d total connections, %s data transferred) between private IPs in VPC '%s'",
						len(logoutput),
						totalConnections,
						formatBytes(totalBytes),
						params.VpcId),
					Severity: "info",
				})
			}

			// Return in cloud_logs format: {"data": [...]}
			return playbooks.NewPlaybookActionResponseJson(map[string]any{"data": logoutput}, additionalInfo, insights, metadata), nil
		}

		if status == "Failed" || status == "Cancelled" {
			return nil, fmt.Errorf("VPC Flow Logs query failed with status: %s", status)
		}

		// Status is still "Running" or "Scheduled", continue polling
	}

	return nil, fmt.Errorf("VPC Flow Logs query timed out after %v (query still running)", time.Since(pollStartTime).Round(time.Second))
}

// discoverVpcFlowLogsConfig discovers the CloudWatch Logs log group name and format for VPC Flow Logs
// by querying the EC2 describe-flow-logs API
func (a *cloudVpcFlowLogsAction) discoverVpcFlowLogsConfig(ctx playbooks.PlaybookActionContext, accountId string, vpcId string, region string) (string, string, error) {
	// Defense-in-depth: validate again at the function boundary. Callers are
	// expected to validate, but constructing a shell command without checking
	// would silently rely on that contract.
	if !awsRegionRegex.MatchString(region) {
		return "", "", fmt.Errorf("invalid region format: %q", region)
	}
	if !awsVpcIdRegex.MatchString(vpcId) {
		return "", "", fmt.Errorf("invalid vpc_id format: %q", vpcId)
	}

	// Query EC2 to find flow logs for this VPC. The filter argument is a
	// single shell token (`Name=...,Values=...`); single-quote wrapping
	// prevents any embedded metacharacter from breaking out of the token.
	command := fmt.Sprintf(`aws ec2 describe-flow-logs --filter %s --region %s --output json`,
		shellSingleQuote(fmt.Sprintf("Name=resource-id,Values=%s", vpcId)),
		shellSingleQuote(region),
	)

	ctx.GetLogger().Debug("cloud_vpc_flowlogs: querying flow logs configuration", "command", command)

	cliResp, err := ExecuteCli(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), CloudExecuteCliCommandRequest{
		AccountID: accountId,
		Command:   command,
	})

	if err != nil {
		return "", "", fmt.Errorf("failed to query flow logs: %w", err)
	}

	// Parse the response
	var flowLogsResponse map[string]any
	if dataStr, ok := cliResp["data"].(string); ok {
		if err := common.UnmarshalJson([]byte(dataStr), &flowLogsResponse); err != nil {
			return "", "", fmt.Errorf("failed to parse flow logs response: %w", err)
		}
	} else {
		return "", "", errors.New("invalid CLI response format")
	}

	// Extract flow logs array
	flowLogs, ok := flowLogsResponse["FlowLogs"].([]any)
	if !ok || len(flowLogs) == 0 {
		return "", "", fmt.Errorf("no VPC Flow Logs configured for VPC '%s'. Enable VPC Flow Logs with CloudWatch Logs as destination", vpcId)
	}

	// Find the first flow log that publishes to CloudWatch Logs
	for _, flowLogRaw := range flowLogs {
		flowLog, ok := flowLogRaw.(map[string]any)
		if !ok {
			continue
		}

		// Check if this flow log publishes to CloudWatch Logs
		logDestinationType, _ := flowLog["LogDestinationType"].(string)
		if logDestinationType != "cloud-watch-logs" {
			ctx.GetLogger().Debug("cloud_vpc_flowlogs: skipping flow log with non-CloudWatch destination",
				"destination_type", logDestinationType)
			continue
		}

		// Extract log group name
		logGroupName, ok := flowLog["LogGroupName"].(string)
		if !ok || logGroupName == "" {
			ctx.GetLogger().Warn("cloud_vpc_flowlogs: flow log missing LogGroupName field")
			continue
		}

		// Extract log format (may not be present for default format)
		logFormat, _ := flowLog["LogFormat"].(string)
		if logFormat == "" {
			// Use default VPC Flow Logs format
			logFormat = "${version} ${account-id} ${interface-id} ${srcaddr} ${dstaddr} ${srcport} ${dstport} ${protocol} ${packets} ${bytes} ${start} ${end} ${action} ${log-status}"
		}

		// Found a valid log group
		ctx.GetLogger().Info("cloud_vpc_flowlogs: found flow log configuration",
			"log_group", logGroupName,
			"flow_log_id", flowLog["FlowLogId"],
			"log_format", logFormat)
		return logGroupName, logFormat, nil
	}

	return "", "", fmt.Errorf("VPC '%s' has flow logs configured but none publish to CloudWatch Logs. Found %d flow log(s) with other destinations (S3, Kinesis Firehose, etc.)", vpcId, len(flowLogs))
}

// formatBytes converts bytes to human-readable format (KB, MB, GB, TB)
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGT"[exp])
}

// buildVpcFlowLogsParsePattern builds a CloudWatch Logs Insights parse pattern from VPC Flow Logs format
// Converts format like "${version} ${account-id} ${srcaddr}" to regex pattern
func buildVpcFlowLogsParsePattern(logFormat string) string {
	if logFormat == "" {
		// Default format
		return `/(?<version>\d+) (?<account_id>\d+) (?<interface_id>\S+) (?<srcaddr>\S+) (?<dstaddr>\S+) (?<srcport>\d+) (?<dstport>\d+) (?<protocol>\d+) (?<packets>\d+) (?<bytes>\d+) (?<start>\d+) (?<end>\d+) (?<action>\S+) (?<log_status>\S+)/`
	}

	// Numeric fields that should use \d+ instead of \S+
	numericFields := map[string]bool{
		"version":    true,
		"account-id": true,
		"srcport":    true,
		"dstport":    true,
		"protocol":   true,
		"packets":    true,
		"bytes":      true,
		"start":      true,
		"end":        true,
	}

	// Use strings.Builder to avoid O(n) allocations from repeated string concatenation
	var pattern strings.Builder
	pattern.Grow(len(logFormat) * 2)
	pattern.WriteByte('/')
	inField := false
	fieldStart := 0

	for i := 0; i < len(logFormat); i++ {
		ch := logFormat[i]

		if ch == '$' && i+1 < len(logFormat) && logFormat[i+1] == '{' {
			inField = true
			fieldStart = i + 2
			i++
			continue
		}

		if inField {
			if ch == '}' {
				fieldName := logFormat[fieldStart:i]
				captureGroupName := strings.ReplaceAll(fieldName, "-", "_")

				pattern.WriteString("(?<")
				pattern.WriteString(captureGroupName)
				if numericFields[fieldName] {
					pattern.WriteString(`>\d+)`)
				} else {
					pattern.WriteString(`>\S+)`)
				}

				inField = false
			}
		} else {
			switch ch {
			case ' ':
				pattern.WriteByte(' ')
			case '.', '*', '+', '?', '[', ']', '(', ')', '{', '}', '^', '$', '|', '\\':
				pattern.WriteByte('\\')
				pattern.WriteByte(ch)
			default:
				pattern.WriteByte(ch)
			}
		}
	}

	pattern.WriteByte('/')
	return pattern.String()
}

// cidrToRegex converts CIDR notation to CloudWatch Logs Insights regex pattern
// Handles common RFC 1918 ranges and provides best-effort conversion for custom ranges
func cidrToRegex(cidr string) string {
	// Parse CIDR notation
	parts := strings.Split(cidr, "/")
	if len(parts) != 2 {
		return "" // Invalid CIDR
	}

	ip := parts[0]
	prefixLen := 0
	if _, err := fmt.Sscanf(parts[1], "%d", &prefixLen); err != nil || prefixLen < 0 || prefixLen > 32 {
		return "" // Invalid prefix length
	}

	// Parse IP address
	ipParts := strings.Split(ip, ".")
	if len(ipParts) != 4 {
		return "" // Invalid IP
	}

	// Convert IP parts to integers
	octets := make([]int, 4)
	for i, part := range ipParts {
		if val, err := strconv.Atoi(part); err != nil || val < 0 || val > 255 {
			return "" // Invalid octet
		} else {
			octets[i] = val
		}
	}

	// Convert to 32-bit integer for calculation
	ipInt := uint32(octets[0])<<24 | uint32(octets[1])<<16 | uint32(octets[2])<<8 | uint32(octets[3])

	// Calculate network mask
	mask := uint32(0xFFFFFFFF << (32 - prefixLen))

	// Calculate network address (apply mask)
	networkInt := ipInt & mask

	// Calculate broadcast address (last address in range)
	broadcastInt := networkInt | ^mask

	// Build regex based on the address range
	return buildRegexForIPRange(networkInt, broadcastInt, prefixLen)
}

// buildRegexForIPRange creates a regex pattern that matches IPs in the given range
func buildRegexForIPRange(networkInt, broadcastInt uint32, prefixLen int) string {
	// Extract octets from network and broadcast addresses
	netOctets := []int{
		int((networkInt >> 24) & 0xFF),
		int((networkInt >> 16) & 0xFF),
		int((networkInt >> 8) & 0xFF),
		int(networkInt & 0xFF),
	}
	bcastOctets := []int{
		int((broadcastInt >> 24) & 0xFF),
		int((broadcastInt >> 16) & 0xFF),
		int((broadcastInt >> 8) & 0xFF),
		int(broadcastInt & 0xFF),
	}

	// Special case: octet-aligned prefixes
	if prefixLen%8 == 0 {
		numOctets := prefixLen / 8
		if numOctets == 0 {
			return ".*" // Match all IPs
		}

		parts := make([]string, numOctets)
		for i := 0; i < numOctets; i++ {
			parts[i] = strconv.Itoa(netOctets[i])
		}
		return fmt.Sprintf("^%s\\.", strings.Join(parts, "\\."))
	}

	// For non-octet-aligned prefixes, build a more complex regex
	// This handles the octet where the split occurs
	fullOctets := prefixLen / 8

	// Build the fixed part (full octets)
	fixedPart := ""
	if fullOctets > 0 {
		parts := make([]string, fullOctets)
		for i := 0; i < fullOctets; i++ {
			parts[i] = strconv.Itoa(netOctets[i])
		}
		fixedPart = strings.Join(parts, "\\.")
	}

	// Handle the variable octet (where the network/host boundary is)
	if fullOctets < 4 {
		minVal := netOctets[fullOctets]
		maxVal := bcastOctets[fullOctets]

		variablePart := ""
		if minVal == maxVal {
			variablePart = strconv.Itoa(minVal)
		} else {
			// Generate regex for range
			variablePart = generateOctetRangeRegex(minVal, maxVal)
		}

		if fixedPart != "" {
			return fmt.Sprintf("^%s\\.%s\\.", fixedPart, variablePart)
		}
		return fmt.Sprintf("^%s\\.", variablePart)
	}

	// Full /32 - exact match
	return fmt.Sprintf("^%s$", strings.Join([]string{
		strconv.Itoa(netOctets[0]),
		strconv.Itoa(netOctets[1]),
		strconv.Itoa(netOctets[2]),
		strconv.Itoa(netOctets[3]),
	}, "\\."))
}

// generateOctetRangeRegex creates a regex pattern for matching a range of octet values
func generateOctetRangeRegex(min, max int) string {
	if min == max {
		return strconv.Itoa(min)
	}

	// For simplicity in regex, handle common cases
	// Note: A fully optimal regex for arbitrary ranges is complex

	// Common RFC 1918 ranges
	if min == 16 && max == 31 {
		return "(1[6-9]|2[0-9]|3[0-1])" // For 172.16.0.0/12
	}

	// Handle simple decade ranges
	if max-min < 10 && min/10 == max/10 {
		if max-min == 9 && min%10 == 0 {
			return fmt.Sprintf("%d[0-9]", min/10)
		}
		return fmt.Sprintf("%d[%d-%d]", min/10, min%10, max%10)
	}

	// For complex ranges, use alternation (not optimal but correct)
	// Group consecutive ranges when possible
	var patterns []string

	// Start with the minimum value's decade
	if min%10 != 0 {
		endOfDecade := ((min/10)+1)*10 - 1
		if endOfDecade > max {
			endOfDecade = max
		}
		if min == endOfDecade {
			patterns = append(patterns, strconv.Itoa(min))
		} else {
			patterns = append(patterns, fmt.Sprintf("%d[%d-%d]", min/10, min%10, endOfDecade%10))
		}
		min = endOfDecade + 1
	}

	// Handle full decades
	for min <= max && min%10 == 0 && max-min >= 9 {
		patterns = append(patterns, fmt.Sprintf("%d[0-9]", min/10))
		min += 10
	}

	// Handle remainder
	if min <= max {
		if min == max {
			patterns = append(patterns, strconv.Itoa(min))
		} else if min/10 == max/10 {
			patterns = append(patterns, fmt.Sprintf("%d[%d-%d]", min/10, min%10, max%10))
		} else {
			// Fall back to listing remaining values
			for i := min; i <= max; i++ {
				patterns = append(patterns, strconv.Itoa(i))
			}
		}
	}

	if len(patterns) == 1 {
		return patterns[0]
	}
	return "(" + strings.Join(patterns, "|") + ")"
}

// cloudCliAction executes an AWS, GCP, or Azure CLI command on a cloud account.
type cloudCliAction struct{}

type cloudCliActionParams struct {
	AccountId string `json:"account_id"`
	Command   string `json:"command" validate:"required"`
	Title     string `json:"title,omitempty"`
}

func (a *cloudCliAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params cloudCliActionParams
	if err := common.UnmarshalMapToStruct(rawParams, &params); err != nil {
		return nil, fmt.Errorf("cloud_cli: invalid params: %w", err)
	}
	if err := common.ValidateStruct(params); err != nil {
		return nil, fmt.Errorf("cloud_cli: validation failed: %w", err)
	}

	accountId := params.AccountId
	if accountId == "" {
		accountId = ctx.GetAccountId()
	}
	if accountId == "" {
		return nil, fmt.Errorf("cloud_cli: account_id is required")
	}

	// If account_id looks like an account number rather than a UUID, resolve it.
	if !strings.Contains(accountId, "-") {
		resolvedId, err := getCloudAccountIdByNumber(accountId, ctx.GetTenantId())
		if err != nil {
			return nil, fmt.Errorf("cloud_cli: failed to resolve account %s: %w", accountId, err)
		}
		accountId = resolvedId
	}

	resp, err := ExecuteCli(
		security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil),
		CloudExecuteCliCommandRequest{
			AccountID: accountId,
			Command:   params.Command,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("cloud_cli: execution failed: %w", err)
	}

	stdout, _ := resp["data"].(string)

	title := params.Title
	if title == "" {
		title = "Cloud CLI"
	}

	data := map[string]any{
		"command": params.Command,
		"stdout":  stdout,
	}

	additionalInfo := map[string]any{
		"action_name": "cloud_cli",
		"title":       title,
	}

	return playbooks.NewPlaybookActionResponseJson(data, additionalInfo, nil, nil), nil
}

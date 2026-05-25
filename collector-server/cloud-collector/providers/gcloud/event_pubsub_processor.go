package gcloud

import (
	"bytes"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"reflect"
	"strings"
	"text/template"
	"time"
)

// gcpProviderAPI defines GCP provider methods for actions
type gcpProviderAPI interface {
	QueryLogs(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error)
	QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error)
	ListResources(ctx providers.CloudProviderContext, account providers.Account, query providers.ListResourceRequest) (providers.ListResourcesResponse, error)
}

// TemplatedPubSubProcessor processes Pub/Sub events based on rules
type TemplatedPubSubProcessor struct {
	ruleSet EventRuleSet
	gcpAPI  gcpProviderAPI
}

// NewTemplatedPubSubProcessor creates new processor with rules
func NewTemplatedPubSubProcessor(rules EventRuleSet, api gcpProviderAPI) *TemplatedPubSubProcessor {
	return &TemplatedPubSubProcessor{ruleSet: rules, gcpAPI: api}
}

// Process processes a Pub/Sub event against all rules
func (p *TemplatedPubSubProcessor) Process(ctx providers.CloudProviderContext, event PubSubEvent, account providers.Account) (providers.Event, error) {
	logger := ctx.GetLogger()

	var eventData map[string]interface{}
	if err := common.UnmarshalJson(event.Data, &eventData); err != nil {
		logger.Error("Failed to unmarshal event data", "error", err)
		eventData = make(map[string]interface{})
	}

	templateContext := map[string]interface{}{
		"ID":        event.ID,
		"Source":    event.Source,
		"Type":      event.Type,
		"Time":      event.Time,
		"Subject":   event.Subject,
		"Data":      eventData,
		"ProjectID": event.ProjectID,
		"LogName":   event.LogName,
		"Resource":  event.Resource,
		"Account":   account,
	}

	for _, rule := range p.ruleSet.Rules {
		for _, trigger := range rule.Triggers {
			if !p.matches(ctx, event, trigger, templateContext) {
				continue
			}

			logger.Debug("Rule matched", "rule", rule.Name, "eventType", event.Type)

			providerEvent, err := p.buildEventFromTemplate(ctx, rule, templateContext, account)
			if err != nil {
				logger.Error("Failed to build event from template", "error", err, "rule", rule.Name)
				continue
			}

			if err := p.executeActions(ctx, rule, templateContext, account, &providerEvent); err != nil {
				logger.Error("Failed to execute actions", "error", err, "rule", rule.Name)
			}

			rawEventBytes, _ := common.MarshalJson(event)
			var rawMap map[string]any
			_ = common.UnmarshalJson(rawEventBytes, &rawMap)
			providerEvent.Raw = rawMap

			return providerEvent, nil
		}
	}

	logger.Warn("No matching rule found for event, skipping", "eventType", event.Type, "source", event.Source)
	return providers.Event{}, nil
}

// matches checks if event matches rule triggers
func (p *TemplatedPubSubProcessor) matches(ctx providers.CloudProviderContext, event PubSubEvent, trigger EventRuleTrigger, templateData any) bool {
	if !strings.EqualFold(trigger.SourceSystem, "GCP_PubSub") {
		return false
	}

	if trigger.Identifier != "" && !strings.Contains(event.Source, trigger.Identifier) {
		return false
	}

	for _, filter := range trigger.EventFilters {
		if !p.evaluateFilterCondition(ctx, filter, templateData) {
			return false
		}
	}

	return true
}

// evaluateFilterCondition evaluates filter template
func (p *TemplatedPubSubProcessor) evaluateFilterCondition(ctx providers.CloudProviderContext, filter EventFilter, data any) bool {
	rendered, err := p.renderTemplateValue(ctx, "filter:"+filter.Description, filter.Template, data)
	if err != nil {
		ctx.GetLogger().Error("Failed to render filter template", "error", err)
		return false
	}

	switch strings.ToLower(strings.TrimSpace(rendered)) {
	case "true", "1":
		return true
	default:
		return false
	}
}

// renderField renders a template field with fallback to its static value
func (p *TemplatedPubSubProcessor) renderField(ctx providers.CloudProviderContext, name string, field EventFieldTemplate, data map[string]interface{}) string {
	rendered, _ := p.renderTemplateValue(ctx, name, field.Template, data)
	if rendered == "" {
		return field.Value
	}
	return rendered
}

// buildEventFromTemplate builds provider event from template
func (p *TemplatedPubSubProcessor) buildEventFromTemplate(ctx providers.CloudProviderContext, rule EventRule, templateData map[string]interface{}, account providers.Account) (providers.Event, error) {
	tmpl := rule.EventTemplate
	eventTime, _ := templateData["Time"].(time.Time)

	event := providers.Event{
		Title:               p.renderField(ctx, "Title", tmpl.Title, templateData),
		Description:         p.renderField(ctx, "Description", tmpl.Description, templateData),
		EventSeverity:       providers.EventSeverity(p.renderField(ctx, "Severity", tmpl.Severity, templateData)),
		EventStatus:         providers.EventStatus(p.renderField(ctx, "Status", tmpl.EventStatus, templateData)),
		ResourceId:          p.renderField(ctx, "ResourceID", tmpl.ResourceId, templateData),
		ResourceType:        p.renderField(ctx, "ResourceType", tmpl.ResourceType, templateData),
		ResourceServiceName: p.renderField(ctx, "ServiceName", tmpl.ResourceServiceName, templateData),
		ResourceRegion:      p.renderField(ctx, "Region", tmpl.ResourceRegion, templateData),
		Date:                eventTime,
		Labels:              p.buildGCPEventLabels(ctx, rule, templateData, account),
	}

	return event, nil
}

// executeActions executes rule actions
func (p *TemplatedPubSubProcessor) executeActions(ctx providers.CloudProviderContext, rule EventRule, templateData map[string]interface{}, account providers.Account, event *providers.Event) error {
	actionData := make(map[string]interface{})
	for k, v := range templateData {
		actionData[k] = v
	}

	for _, action := range rule.Actions {
		switch action.Type {
		case "gcp_get_resource":
			if err := p.executeGetResourceAction(ctx, action, actionData, account, event); err != nil {
				ctx.GetLogger().Error("Failed to execute get_resource action", "error", err)
			}
		case "gcp_get_metric":
			if err := p.executeGetMetricAction(ctx, action, actionData, account, event); err != nil {
				ctx.GetLogger().Error("Failed to execute get_metric action", "error", err)
			}
		case "gcp_get_logs":
			if err := p.executeGetLogsAction(ctx, action, actionData, account, event); err != nil {
				ctx.GetLogger().Error("Failed to execute get_logs action", "error", err)
			}
		}
	}
	return nil
}

// executeGetResourceAction fetches resource details
func (p *TemplatedPubSubProcessor) executeGetResourceAction(ctx providers.CloudProviderContext, action EventAction, templateData map[string]interface{}, account providers.Account, event *providers.Event) error {
	serviceName, _ := p.renderTemplateValue(ctx, "service_name", action.Params["service_name"], templateData)
	resourceID, _ := p.renderTemplateValue(ctx, "resource_id", action.Params["resource_id"], templateData)

	resp, err := p.gcpAPI.ListResources(ctx, account, providers.ListResourceRequest{
		ServiceName: serviceName,
		ResourceIds: []string{resourceID},
	})
	if err != nil {
		return err
	}

	// Find resource by ID
	for _, res := range resp.Items {
		if res.Id == resourceID {
			templateData[action.Name] = res
			break
		}
	}
	return nil
}

// executeGetMetricAction queries metrics
func (p *TemplatedPubSubProcessor) executeGetMetricAction(ctx providers.CloudProviderContext, action EventAction, templateData map[string]interface{}, account providers.Account, event *providers.Event) error {
	serviceName, _ := p.renderTemplateValue(ctx, "service_name", action.Params["service_name"], templateData)
	resourceID, _ := p.renderTemplateValue(ctx, "resource_id", action.Params["resource_id"], templateData)

	resp, err := p.gcpAPI.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
		ServiceName: serviceName,
		ResourceIds: []string{resourceID},
	})
	if err != nil {
		return err
	}

	templateData[action.Name] = resp
	return nil
}

// executeGetLogsAction queries logs
func (p *TemplatedPubSubProcessor) executeGetLogsAction(ctx providers.CloudProviderContext, action EventAction, templateData map[string]interface{}, account providers.Account, event *providers.Event) error {
	filter, _ := p.renderTemplateValue(ctx, "filter", action.Params["filter"], templateData)

	resp, err := p.gcpAPI.QueryLogs(ctx, account, providers.QueryLogsRequest{
		QueryString: filter,
	})
	if err != nil {
		return err
	}

	templateData[action.Name] = resp
	return nil
}

// renderTemplateValue renders template with data
func (p *TemplatedPubSubProcessor) renderTemplateValue(ctx providers.CloudProviderContext, fieldName string, templateStr interface{}, data any) (string, error) {
	str, ok := templateStr.(string)
	if !ok {
		return fmt.Sprintf("%v", templateStr), nil
	}

	if str == "" {
		return "", nil
	}

	funcMap := template.FuncMap{
		"replace":   strings.ReplaceAll,
		"toLower":   strings.ToLower,
		"toUpper":   strings.ToUpper,
		"contains":  strings.Contains,
		"hasPrefix": strings.HasPrefix,
		"hasSuffix": strings.HasSuffix,
		"split":     strings.Split,
		"join":      strings.Join,
		"trim":      strings.TrimSpace,
		"toJson": func(v any) (string, error) {
			b, err := common.MarshalJson(v)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
		"eq": func(a, b any) bool {
			return reflect.DeepEqual(a, b)
		},
		"ne": func(a, b any) bool {
			return !reflect.DeepEqual(a, b)
		},
		"and": func(a, b bool) bool {
			return a && b
		},
		"or": func(a, b bool) bool {
			return a || b
		},
		"not": func(a bool) bool {
			return !a
		},
		"default": func(defaultValue any, givenValue any) any {
			if givenValue == nil {
				return defaultValue
			}
			val := reflect.ValueOf(givenValue)
			switch val.Kind() {
			case reflect.String:
				if val.String() == "" {
					return defaultValue
				}
			case reflect.Slice, reflect.Array, reflect.Map:
				if val.Len() == 0 {
					return defaultValue
				}
			case reflect.Pointer, reflect.Interface:
				if val.IsNil() {
					return defaultValue
				}
			}
			return givenValue
		},
		"extractProjectId": func(resourcePath string) string {
			parts := strings.Split(resourcePath, "/")
			for i, part := range parts {
				if part == "projects" && i+1 < len(parts) {
					return parts[i+1]
				}
			}
			return ""
		},
		"extractRegion": func(resourcePath string) string {
			parts := strings.Split(resourcePath, "/")
			for i, part := range parts {
				if part == "regions" && i+1 < len(parts) {
					return parts[i+1]
				}
				if part == "zones" && i+1 < len(parts) {
					zone := parts[i+1]
					if idx := strings.LastIndex(zone, "-"); idx > 0 {
						return zone[:idx]
					}
					return zone
				}
			}
			return ""
		},
		"extractResourceName": func(resourcePath string) string {
			parts := strings.Split(resourcePath, "/")
			if len(parts) > 0 {
				return parts[len(parts)-1]
			}
			return ""
		},
	}

	tmpl, err := template.New(fieldName).Funcs(funcMap).Parse(str)
	if err != nil {
		return "", fmt.Errorf("template parse error: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template execute error: %w", err)
	}

	return buf.String(), nil
}

// buildGCPEventLabels creates GCP-specific labels for event evidence enrichment
// These labels enable automatic evidence collection (metrics, logs, resources, CLI output)
func (p *TemplatedPubSubProcessor) buildGCPEventLabels(ctx providers.CloudProviderContext, rule EventRule, templateData map[string]interface{}, account providers.Account) map[string]string {
	labels := make(map[string]string)

	// Extract project ID
	if projectID, ok := templateData["ProjectID"].(string); ok && projectID != "" {
		labels["gcp_project_id"] = projectID
		labels["gcp_account"] = projectID // For cloud_resource/cloud_metrics/cloud_logs auto-execute
	}

	// Extract resource information from event data
	if eventData, ok := templateData["Data"].(map[string]interface{}); ok {
		// GCP Cloud Monitoring Alert structure
		if incident, ok := eventData["incident"].(map[string]interface{}); ok {
			// Extract metric information
			if metric, ok := incident["metric"].(map[string]interface{}); ok {
				if metricType, ok := metric["type"].(string); ok {
					labels["gcp_metric_type"] = metricType
					labels["gcp_event_metric_type"] = metricType
					labels["gcp_event_metric_name"] = metricType
				}
				if displayName, ok := metric["displayName"].(string); ok {
					labels["gcp_event_metric_display_name"] = displayName
				}
			}

			// Extract resource information
			if resource, ok := incident["resource"].(map[string]interface{}); ok {
				if resourceType, ok := resource["type"].(string); ok {
					labels["gcp_event_resource_type"] = resourceType
				}
				if resourceLabels, ok := resource["labels"].(map[string]interface{}); ok {
					// Serialize resource labels as JSON for dimension-based queries
					if labelsJSON, err := common.MarshalJson(resourceLabels); err == nil {
						labels["gcp_event_resource_labels"] = string(labelsJSON)
					}

					// Extract zone and region first
					if zone, ok := resourceLabels["zone"].(string); ok {
						labels["gcp_zone"] = zone
						// Extract region from zone (e.g., "us-central1-a" -> "us-central1")
						if region := extractRegionFromZone(zone); region != "" {
							labels["gcp_region"] = region
						}
					}

					// Extract gcp_event_instance with prioritization to avoid silent overwrites
					// Priority: database_id > cluster_name > instance_id
					if databaseID, ok := resourceLabels["database_id"].(string); ok {
						labels["gcp_event_instance"] = databaseID
					} else if clusterName, ok := resourceLabels["cluster_name"].(string); ok {
						labels["gcp_event_instance"] = clusterName
					} else if instanceID, ok := resourceLabels["instance_id"].(string); ok {
						labels["gcp_event_instance"] = instanceID
					}
				}
			}

			// Extract condition information (for metrics aggregation)
			if condition, ok := incident["condition"].(map[string]interface{}); ok {
				if conditionThreshold, ok := condition["conditionThreshold"].(map[string]interface{}); ok {
					if aggregations, ok := conditionThreshold["aggregations"].([]interface{}); ok && len(aggregations) > 0 {
						if agg, ok := aggregations[0].(map[string]interface{}); ok {
							if alignmentPeriod, ok := agg["alignmentPeriod"].(string); ok {
								labels["gcp_event_metric_aggregation_period"] = alignmentPeriod
							}
							if perSeriesAligner, ok := agg["perSeriesAligner"].(string); ok {
								labels["gcp_event_metric_aggregation"] = perSeriesAligner
							}
						}
					}
				}
			}

			// Extract time information
			if startedAt, ok := incident["started_at"].(string); ok {
				labels["gcp_event_start_time"] = startedAt
			}
			if endedAt, ok := incident["ended_at"].(string); ok {
				labels["gcp_event_end_time"] = endedAt
			}
		}
	}

	// Extract from Resource field (for audit logs)
	if resource, ok := templateData["Resource"].(*LogResource); ok && resource != nil {
		if resource.Type != "" {
			labels["gcp_event_resource_type"] = resource.Type
		}
		if resource.Labels != nil {
			if labelsJSON, err := common.MarshalJson(resource.Labels); err == nil {
				labels["gcp_event_resource_labels"] = string(labelsJSON)
			}
			// Extract common labels
			if instanceID, ok := resource.Labels["instance_id"]; ok {
				labels["gcp_event_instance"] = instanceID
			}
			if zone, ok := resource.Labels["zone"]; ok {
				labels["gcp_zone"] = zone
				if region := extractRegionFromZone(zone); region != "" {
					labels["gcp_region"] = region
				}
			}
		}
	}

	// Extract LogName for log queries
	if logName, ok := templateData["LogName"].(string); ok && logName != "" {
		labels["gcp_log_name"] = logName
	}

	// Determine alert type for downstream evidence actions
	if labels["gcp_event_metric_type"] != "" {
		labels["gcp_alert_type"] = "metric"
	} else if labels["gcp_log_name"] != "" {
		labels["gcp_alert_type"] = "log"
	} else {
		labels["gcp_alert_type"] = "unknown"
	}

	// Map resource type to service name (uses the canonical mapping from gcloud_monitoring_alerts_common.go)
	if labels["gcp_event_resource_type"] != "" {
		labels["gcp_service_name"] = mapResourceTypeToServiceName(labels["gcp_event_resource_type"])
	}

	// Add alert rule name
	labels["gcp_alert_rule_name"] = rule.Name

	return labels
}

// extractRegionFromZone extracts region from GCP zone
// e.g., "us-central1-a" -> "us-central1"
func extractRegionFromZone(zone string) string {
	parts := strings.Split(zone, "-")
	if len(parts) >= 2 {
		return strings.Join(parts[:len(parts)-1], "-")
	}
	return zone
}

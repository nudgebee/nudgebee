package azure

import (
	"bytes"
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"text/template"
	"time"
)

// azureProviderAPI defines the subset of Azure provider methods needed by actions.
type azureProviderAPI interface {
	QueryLogs(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error)
	QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error)
	ListResources(ctx providers.CloudProviderContext, account providers.Account, query providers.ListResourceRequest) (providers.ListResourcesResponse, error)
}

// TemplatedEventGridProcessor processes Event Grid events using a template-based rules engine.
// This is analogous to AWS EventBridge's TemplatedEventBridgeProcessor.
type TemplatedEventGridProcessor struct {
	rules           []AzureEventRule
	azureProvider   azureProviderAPI
	templateFuncMap template.FuncMap
}

// NewEventGridProcessor creates a fully-initialized Event Grid processor by loading event rules
// and creating the Azure provider internally. This is the preferred constructor for use outside
// the azure package (e.g., from HTTP endpoint handlers).
func NewEventGridProcessor(rulesPath string) (*TemplatedEventGridProcessor, error) {
	rules, err := GetAzureEventRules(rulesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load azure event rules: %w", err)
	}
	return NewTemplatedEventGridProcessor(rules, defaultAzureProvider), nil
}

// NewTemplatedEventGridProcessor creates a new processor with the given rules and Azure provider.
func NewTemplatedEventGridProcessor(rules []AzureEventRule, azureProvider azureProviderAPI) *TemplatedEventGridProcessor {
	processor := &TemplatedEventGridProcessor{
		rules:         rules,
		azureProvider: azureProvider,
	}

	// Initialize template function map with helper functions
	processor.templateFuncMap = template.FuncMap{
		// String functions
		"contains":  stringContains,
		"hasPrefix": stringHasPrefix,
		"hasSuffix": stringHasSuffix,
		"toLower":   stringToLower,
		"toUpper":   stringToUpper,
		"trim":      stringTrim,
		"split":     stringSplit,
		"join":      stringJoin,
		"replace":   stringReplace,
		"basename":  extractResourceName,
		"eq":        eq,
		"ne":        ne,
		"and":       and,
		"or":        or,
		"not":       not,

		// Azure-specific functions
		"extractSubscriptionId": extractSubscriptionIdFromResourceId,
		"extractResourceGroup":  extractResourceGroupFromResourceId,
		"extractProvider":       extractProviderFromResourceId,
		"extractResourceType":   extractResourceTypeFromResourceId,
		"extractResourceName":   extractResourceName,
		"extractRegion":         extractRegionFromEventData,
		"normalizeAzureRegion":  normalizeAzureRegion,

		// Operation status mapping
		"getOperationStatus": getOperationStatus,
	}

	return processor
}

// Process processes an Event Grid event according to the defined rules.
// It returns a providers.Event if a matching rule is found and successfully processed.
func (p *TemplatedEventGridProcessor) Process(ctx providers.CloudProviderContext, event EventGridEvent, account providers.Account) (providers.Event, error) {
	// Parse event data into a map for template processing
	var eventData map[string]interface{}
	if err := json.Unmarshal(event.Data, &eventData); err != nil {
		ctx.GetLogger().Error("failed to parse event data", "error", err, "eventId", event.ID)
		return providers.Event{}, err
	}

	// Create template context with event and account info
	templateContext := map[string]interface{}{
		"ID":              event.ID,
		"EventType":       event.EventType,
		"Subject":         event.Subject,
		"EventTime":       event.EventTime,
		"Data":            eventData,
		"DataVersion":     event.DataVersion,
		"MetadataVersion": event.MetadataVersion,
		"Topic":           event.Topic,
		"Account":         account,
	}

	// Find matching rule
	for _, rule := range p.rules {
		matched, err := p.ruleMatches(ctx, rule, templateContext)
		if err != nil {
			ctx.GetLogger().Warn("error evaluating rule", "ruleName", rule.Name, "error", err)
			continue
		}

		if !matched {
			continue
		}

		// Extract resource name and operation for enhanced logging
		resourceName := extractResourceName(event.Subject)
		operationName := ""
		if op, ok := eventData["operationName"].(string); ok {
			operationName = op
		}

		// Log with custom message based on resource type and operation
		if resourceName != "" && operationName != "" {
			status := getOperationStatus(operationName)
			ctx.GetLogger().Info("Azure Event Matched",
				"ruleName", rule.Name,
				"resourceName", resourceName,
				"operation", operationName,
				"status", status,
				"eventType", event.EventType,
				"eventId", event.ID)
		} else {
			ctx.GetLogger().Info("matched Event Grid rule", "ruleName", rule.Name, "eventId", event.ID, "eventType", event.EventType)
		}

		// Execute rule and create providers.Event
		providerEvent, err := p.executeRule(ctx, rule, templateContext, account)
		if err != nil {
			ctx.GetLogger().Error("failed to execute rule", "ruleName", rule.Name, "error", err)
			return providers.Event{}, err
		}

		// Log successful event processing with detailed custom message
		if resourceName != "" && operationName != "" {
			status := getOperationStatus(operationName)
			ctx.GetLogger().Info("Azure Resource Event Processed Successfully",
				"resourceName", resourceName,
				"operation", operationName,
				"newStatus", status,
				"resourceId", providerEvent.ResourceId,
				"serviceName", providerEvent.ResourceServiceName)
		}

		return providerEvent, nil
	}

	// No matching rule found - return empty event (will be skipped by caller)
	operationName := ""
	if op, ok := eventData["operationName"].(string); ok {
		operationName = op
	}

	resourceName := extractResourceName(event.Subject)
	if operationName != "" && resourceName != "" {
		ctx.GetLogger().Warn("  No matching Event Grid rule found",
			"eventId", event.ID,
			"eventType", event.EventType,
			"resourceName", resourceName,
			"operationName", operationName,
			"subject", event.Subject,
			"suggestion", "Add a rule in azure_resource_events.yaml to process this event type")
	} else {
		ctx.GetLogger().Debug("no matching Event Grid rule found", "eventId", event.ID, "eventType", event.EventType, "subject", event.Subject)
	}
	return providers.Event{}, nil
}

// ruleMatches checks if a rule's triggers match the event.
func (p *TemplatedEventGridProcessor) ruleMatches(ctx providers.CloudProviderContext, rule AzureEventRule, templateContext map[string]interface{}) (bool, error) {
	trigger := rule.Triggers

	// Check source system
	// Support both Azure_EventGrid (standard) and Azure_Monitor_Alert (custom event source)
	if trigger.SourceSystem != "Azure_EventGrid" &&
		trigger.SourceSystem != "Azure_CloudEvent" &&
		trigger.SourceSystem != "Azure_Monitor_Alert" {
		return false, nil
	}

	// Check identifier (if specified)
	if trigger.Identifier != "" {
		eventType, _ := templateContext["EventType"].(string)
		subject, _ := templateContext["Subject"].(string)

		// Identifier can match eventType or subject
		if eventType != trigger.Identifier && subject != trigger.Identifier {
			// Try to match provider from subject
			provider := getServiceNameFromEventGridSource(subject)
			if provider != trigger.Identifier {
				return false, nil
			}
		}
	}

	// Evaluate event filters (using Go templates)
	for _, filter := range trigger.EventFilters {
		if filter.Template == "" {
			continue
		}

		tmpl, err := template.New("filter").Funcs(p.templateFuncMap).Parse(filter.Template)
		if err != nil {
			ctx.GetLogger().Error("failed to parse filter template", "template", filter.Template, "error", err)
			return false, err
		}

		var result bytes.Buffer
		if err := tmpl.Execute(&result, templateContext); err != nil {
			ctx.GetLogger().Error("failed to execute filter template", "template", filter.Template, "error", err)
			return false, err
		}

		// Template should evaluate to "true" or "false"
		if result.String() != "true" {
			return false, nil
		}
	}

	return true, nil
}

// executeRule executes a matched rule and creates a providers.Event.
func (p *TemplatedEventGridProcessor) executeRule(ctx providers.CloudProviderContext, rule AzureEventRule, templateContext map[string]interface{}, account providers.Account) (providers.Event, error) {
	// Extract event time
	var eventTime time.Time
	if et, ok := templateContext["EventTime"].(time.Time); ok {
		eventTime = et
	}

	// Build the raw event map from the EventGrid event data so it is preserved
	// as the "Raw Event" evidence in the DB. Without this, the evidence is "null"
	// and the caller/principal/correlation info from the Activity Log entry is lost.
	rawEvent := map[string]any{
		"id":        templateContext["ID"],
		"eventType": templateContext["EventType"],
		"subject":   templateContext["Subject"],
		"eventTime": templateContext["EventTime"],
		"topic":     templateContext["Topic"],
	}
	if data, ok := templateContext["Data"].(map[string]interface{}); ok {
		for k, v := range data {
			rawEvent[k] = v
		}
	}

	providerEvent := providers.Event{
		EventId:     templateContext["ID"].(string),
		Date:        eventTime,
		EventSource: "Azure_Monitor_Alert",
		Raw:         rawEvent,
	}

	// Populate event fields using templates
	eventTemplate := rule.EventTemplate

	// EventName (aggregation key) - if not specified, use title
	if eventTemplate.EventName.Value != "" || eventTemplate.EventName.Template != "" {
		eventName, err := p.evaluateFieldTemplate(ctx, eventTemplate.EventName, templateContext)
		if err != nil {
			return providerEvent, fmt.Errorf("failed to evaluate event_name template: %w", err)
		}
		providerEvent.EventName = eventName
	}

	// Title (specific event title)
	title, err := p.evaluateFieldTemplate(ctx, eventTemplate.Title, templateContext)
	if err != nil {
		return providerEvent, fmt.Errorf("failed to evaluate title template: %w", err)
	}
	providerEvent.Title = title

	// If EventName was not set, default to title for backwards compatibility
	if providerEvent.EventName == "" {
		providerEvent.EventName = title
	}

	// Description
	description, err := p.evaluateFieldTemplate(ctx, eventTemplate.Description, templateContext)
	if err != nil {
		return providerEvent, fmt.Errorf("failed to evaluate description template: %w", err)
	}
	providerEvent.Description = description

	// Severity
	severity, err := p.evaluateFieldTemplate(ctx, eventTemplate.Severity, templateContext)
	if err != nil {
		return providerEvent, fmt.Errorf("failed to evaluate severity template: %w", err)
	}
	providerEvent.EventSeverity = providers.EventSeverity(severity)

	// Event Status
	eventStatus, err := p.evaluateFieldTemplate(ctx, eventTemplate.EventStatus, templateContext)
	if err != nil {
		return providerEvent, fmt.Errorf("failed to evaluate event status template: %w", err)
	}
	providerEvent.EventStatus = providers.EventStatusFromString(eventStatus)

	// Resource ID
	resourceId, err := p.evaluateFieldTemplate(ctx, eventTemplate.ResourceId, templateContext)
	if err != nil {
		return providerEvent, fmt.Errorf("failed to evaluate resource ID template: %w", err)
	}
	providerEvent.ResourceId = resourceId

	// Resource Type
	resourceType, err := p.evaluateFieldTemplate(ctx, eventTemplate.ResourceType, templateContext)
	if err != nil {
		return providerEvent, fmt.Errorf("failed to evaluate resource type template: %w", err)
	}
	providerEvent.ResourceType = resourceType

	// Resource Service Name
	resourceServiceName, err := p.evaluateFieldTemplate(ctx, eventTemplate.ResourceServiceName, templateContext)
	if err != nil {
		return providerEvent, fmt.Errorf("failed to evaluate resource service name template: %w", err)
	}
	providerEvent.ResourceServiceName = resourceServiceName

	// Resource Region
	resourceRegion, err := p.evaluateFieldTemplate(ctx, eventTemplate.ResourceRegion, templateContext)
	if err != nil {
		return providerEvent, fmt.Errorf("failed to evaluate resource region template: %w", err)
	}
	providerEvent.ResourceRegion = resourceRegion

	// Execute actions (if any)
	var evidences []providers.EventEvidence
	for _, action := range rule.Actions {
		// Enhanced logging for update_cloud_resource actions
		if action.Type == "update_cloud_resource" {
			ctx.GetLogger().Info("Updating Cloud Resource",
				"actionName", action.Name,
				"actionType", action.Type,
				"resourceId", resourceId,
				"serviceName", resourceServiceName)
		} else {
			ctx.GetLogger().Debug("executing action", "actionName", action.Name, "actionType", action.Type)
		}

		actionResult, err := p.executeAction(ctx, action, templateContext, account)
		if err != nil {
			ctx.GetLogger().Error("Failed to execute action", "actionName", action.Name, "actionType", action.Type, "error", err)
			// Continue with other actions even if one fails
			continue
		}

		// Log success for cloud resource updates
		if action.Type == "update_cloud_resource" {
			ctx.GetLogger().Info("Cloud Resource Updated Successfully",
				"actionName", action.Name,
				"resourceId", resourceId,
				"serviceName", resourceServiceName)
		}

		// Convert action result to EventEvidence
		resultJSON, _ := json.Marshal(actionResult)
		evidence := providers.EventEvidence{
			Type:    providers.EventEvidenceTypeJson,
			Data:    string(resultJSON),
			Insight: []string{action.Description},
			AdditionalInfo: map[string]string{
				"action_name": action.Name,
				"action_type": action.Type,
			},
		}
		evidences = append(evidences, evidence)
	}

	providerEvent.AdditionalContext = evidences

	// Populate labels from template
	if len(eventTemplate.Labels) > 0 {
		labels := make(map[string]string)
		for key, labelTemplate := range eventTemplate.Labels {
			labelValue, err := p.evaluateFieldTemplate(ctx, labelTemplate, templateContext)
			if err != nil {
				ctx.GetLogger().Warn("failed to evaluate label template", "key", key, "error", err)
				continue
			}
			labels[key] = labelValue
		}

		// Auto-populate labels that the api-server's auto-execute enrichment checks,
		// using fields already available on the event. Without these, the auto-execute
		// actions (cloud_resource, cloud_service_map, cloud_logs, cloud_metrics) skip
		// Azure resource events entirely.
		if labels["azure_alert_target_resource"] == "" && providerEvent.ResourceId != "" {
			labels["azure_alert_target_resource"] = providerEvent.ResourceId
		}
		if labels["azure_service_name"] == "" && providerEvent.ResourceServiceName != "" {
			labels["azure_service_name"] = providerEvent.ResourceServiceName
		}

		providerEvent.Labels = labels
	}

	return providerEvent, nil
}

// evaluateFieldTemplate evaluates a single field template and returns the result as a string.
func (p *TemplatedEventGridProcessor) evaluateFieldTemplate(ctx providers.CloudProviderContext, fieldTemplate AzureEventFieldTemplate, templateContext map[string]interface{}) (string, error) {
	// If value is set, use it directly
	if fieldTemplate.Value != "" {
		return fieldTemplate.Value, nil
	}

	// If template is set, evaluate it
	if fieldTemplate.Template != "" {
		tmpl, err := template.New("field").Funcs(p.templateFuncMap).Parse(fieldTemplate.Template)
		if err != nil {
			return "", fmt.Errorf("failed to parse template: %w", err)
		}

		var result bytes.Buffer
		if err := tmpl.Execute(&result, templateContext); err != nil {
			return "", fmt.Errorf("failed to execute template: %w", err)
		}

		return result.String(), nil
	}

	// Neither value nor template is set
	return "", nil
}

// executeAction executes a rule action and returns the result.
func (p *TemplatedEventGridProcessor) executeAction(ctx providers.CloudProviderContext, action AzureActionDefinition, templateContext map[string]interface{}, account providers.Account) (interface{}, error) {
	// Evaluate action parameters using templates. Walk recursively so templates
	// inside nested maps and slices (e.g. meta_updates: { last_operation: '{{ .Data.operationName }}' })
	// are rendered too — without this, nested template strings were stored verbatim
	// in cloud_resourses.meta as literal "{{ .Data.operationName }}".
	evaluated, ok := p.evaluateTemplateValue(ctx, action.Params, templateContext).(map[string]interface{})
	if !ok {
		evaluated = action.Params
	}
	evaluatedParams := evaluated

	// Execute action based on type
	switch action.Type {
	case "azure_get_resource":
		return p.executeGetResourceAction(ctx, account, evaluatedParams)
	case "azure_get_metric":
		return p.executeGetMetricAction(ctx, account, evaluatedParams)
	case "update_cloud_resource":
		return p.executeUpdateCloudResourceAction(ctx, account, evaluatedParams)
	default:
		return nil, fmt.Errorf("unknown action type: %s", action.Type)
	}
}

// evaluateTemplateValue walks a value (string, map, or slice) and renders any
// Go templates found inside, leaving non-template scalars unchanged. Maps and
// slices are recursed into so nested templates (e.g. inside meta_updates) are
// resolved instead of being stored verbatim.
func (p *TemplatedEventGridProcessor) evaluateTemplateValue(ctx providers.CloudProviderContext, value interface{}, templateContext map[string]interface{}) interface{} {
	switch v := value.(type) {
	case string:
		// Fast path: skip the template machinery for strings that obviously
		// have no template directives. Most YAML param values (resource IDs,
		// service names, status enums) are static strings hit on every
		// realtime event.
		if !contains(v, "{{") {
			return v
		}
		tmpl, err := template.New("param").Funcs(p.templateFuncMap).Parse(v)
		if err != nil {
			ctx.GetLogger().Warn("failed to parse action param template", "value", v, "error", err)
			return v
		}
		var result bytes.Buffer
		if err := tmpl.Execute(&result, templateContext); err != nil {
			ctx.GetLogger().Warn("failed to execute action param template", "value", v, "error", err)
			return v
		}
		return result.String()
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for k, item := range v {
			out[k] = p.evaluateTemplateValue(ctx, item, templateContext)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = p.evaluateTemplateValue(ctx, item, templateContext)
		}
		return out
	default:
		return value
	}
}

// executeGetResourceAction retrieves a resource from Azure.
func (p *TemplatedEventGridProcessor) executeGetResourceAction(ctx providers.CloudProviderContext, account providers.Account, params map[string]interface{}) (interface{}, error) {
	logger := ctx.GetLogger().With("action", "azure_get_resource")

	// Extract required parameters
	serviceName, _ := params["service_name"].(string)
	resourceId, _ := params["resource_id"].(string)
	region, _ := params["region"].(string)

	if serviceName == "" || resourceId == "" {
		return nil, fmt.Errorf("azure_get_resource action requires service_name and resource_id parameters")
	}

	logger.Info("azure_get_resource: fetching resource details",
		"serviceName", serviceName,
		"resourceId", resourceId,
		"region", region)

	// Use the Azure provider's ListResources method to get resource details
	if p.azureProvider == nil {
		return nil, fmt.Errorf("azure_get_resource: Azure provider not initialized")
	}

	request := providers.ListResourceRequest{
		ServiceName: serviceName,
		ResourceIds: []string{resourceId},
	}

	// Add region if specified
	if region != "" {
		request.Regions = []string{region}
	}

	response, err := p.azureProvider.ListResources(ctx, account, request)
	if err != nil {
		logger.Error("azure_get_resource: failed to list resources", "error", err)
		return nil, fmt.Errorf("azure_get_resource: failed to fetch resource: %w", err)
	}

	if len(response.Items) == 0 {
		logger.Warn("azure_get_resource: no resource found", "resourceId", resourceId)
		return nil, fmt.Errorf("azure_get_resource: resource not found: %s", resourceId)
	}

	resource := response.Items[0]
	logger.Info("azure_get_resource: successfully fetched resource",
		"resourceId", resource.Id,
		"resourceName", resource.Name,
		"resourceType", resource.Type)

	return resource, nil
}

// executeGetMetricAction retrieves metrics from Azure Monitor.
func (p *TemplatedEventGridProcessor) executeGetMetricAction(ctx providers.CloudProviderContext, account providers.Account, params map[string]interface{}) (interface{}, error) {
	logger := ctx.GetLogger().With("action", "azure_get_metric")

	// Extract required parameters
	serviceName, _ := params["service_name"].(string)
	metricName, _ := params["metric_name"].(string)
	resourceId, _ := params["resource_id"].(string)
	region, _ := params["region"].(string)

	if serviceName == "" || metricName == "" || resourceId == "" {
		return nil, fmt.Errorf("azure_get_metric action requires service_name, metric_name, and resource_id parameters")
	}

	// Parse time parameters
	startTimeOffset, _ := params["start_time_offset"].(string)
	endTimeOffset, _ := params["end_time_offset"].(string)
	stepSeconds, _ := params["step_seconds"].(float64)
	aggregation, _ := params["aggregation"].(string)

	// Default values
	if startTimeOffset == "" {
		startTimeOffset = "-1h" // Default to 1 hour ago
	}
	if endTimeOffset == "" {
		endTimeOffset = "0" // Default to now
	}
	if stepSeconds == 0 {
		stepSeconds = 300 // Default to 5 minutes
	}
	if aggregation == "" {
		aggregation = "Average" // Default aggregation
	}

	logger.Info("azure_get_metric: fetching metrics",
		"serviceName", serviceName,
		"metricName", metricName,
		"resourceId", resourceId,
		"region", region,
		"startTimeOffset", startTimeOffset,
		"endTimeOffset", endTimeOffset,
		"aggregation", aggregation)

	// Parse time offsets (simple implementation)
	now := time.Now()
	startTime, err := parseTimeOffset(now, startTimeOffset)
	if err != nil {
		return nil, fmt.Errorf("azure_get_metric: invalid start_time_offset: %w", err)
	}
	endTime, err := parseTimeOffset(now, endTimeOffset)
	if err != nil {
		return nil, fmt.Errorf("azure_get_metric: invalid end_time_offset: %w", err)
	}

	// Use the Azure provider's QueryMetrices method
	if p.azureProvider == nil {
		return nil, fmt.Errorf("azure_get_metric: Azure provider not initialized")
	}

	request := providers.QueryMetricsRequest{
		ServiceName: serviceName,
		MetricNames: []string{metricName},
		ResourceIds: []string{resourceId},
		StartDate:   &startTime,
		EndDate:     &endTime,
		Step:        time.Duration(stepSeconds) * time.Second,
		Statistics:  []string{aggregation},
		Region:      region,
	}

	response, err := p.azureProvider.QueryMetrices(ctx, account, request)
	if err != nil {
		logger.Error("azure_get_metric: failed to query metrics", "error", err)
		return nil, fmt.Errorf("azure_get_metric: failed to fetch metrics: %w", err)
	}

	logger.Info("azure_get_metric: successfully fetched metrics",
		"metricName", metricName,
		"dataPointCount", len(response.Items))

	return response, nil
}

// parseTimeOffset parses a time offset string like "-1h", "-30m", "0" relative to a reference time
func parseTimeOffset(referenceTime time.Time, offset string) (time.Time, error) {
	if offset == "0" || offset == "" {
		return referenceTime, nil
	}

	duration, err := time.ParseDuration(offset)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time offset format: %s (expected format: -1h, -30m, etc.)", offset)
	}

	return referenceTime.Add(duration), nil
}

// executeUpdateCloudResourceAction is implemented in event_eventgrid_processor_update_resource.go

// Template helper functions

func stringContains(s, substr string) bool {
	return contains(s, substr)
}

func stringHasPrefix(s, prefix string) bool {
	return hasPrefix(s, prefix)
}

func stringHasSuffix(s, suffix string) bool {
	return hasSuffix(s, suffix)
}

func stringToLower(s string) string {
	return toLower(s)
}

func stringToUpper(s string) string {
	return toUpper(s)
}

func stringTrim(s string) string {
	return trim(s)
}

func stringSplit(s, sep string) []string {
	return split(s, sep)
}

func stringJoin(elems []string, sep string) string {
	return join(elems, sep)
}

func stringReplace(s, old, new string) string {
	return replace(s, old, new)
}

func extractSubscriptionIdFromResourceId(resourceId string) string {
	subscription, _, _, _, _ := parseAzureResourceID(resourceId)
	return subscription
}

func extractResourceGroupFromResourceId(resourceId string) string {
	_, resourceGroup, _, _, _ := parseAzureResourceID(resourceId)
	return resourceGroup
}

func extractProviderFromResourceId(resourceId string) string {
	_, _, provider, _, _ := parseAzureResourceID(resourceId)
	return provider
}

func extractResourceTypeFromResourceId(resourceId string) string {
	_, _, _, resourceType, _ := parseAzureResourceID(resourceId)
	return resourceType
}

func extractRegionFromEventData(eventData interface{}) string {
	if dataMap, ok := eventData.(map[string]interface{}); ok {
		if location, ok := dataMap["location"].(string); ok {
			return normalizeAzureRegion(location)
		}
	}
	return ""
}

// getOperationStatus maps Azure operation names to status values.
func getOperationStatus(operationName string) string {
	operationStatusMap := map[string]string{
		// VM Operations
		"Microsoft.Compute/virtualMachines/write":             "Active",
		"Microsoft.Compute/virtualMachines/start/action":      "Active",
		"Microsoft.Compute/virtualMachines/powerOff/action":   "Inactive",
		"Microsoft.Compute/virtualMachines/deallocate/action": "Inactive",
		"Microsoft.Compute/virtualMachines/restart/action":    "Active",
		"Microsoft.Compute/virtualMachines/delete":            "Deleted",

		// SQL Operations
		"Microsoft.Sql/servers/databases/write":         "Active",
		"Microsoft.Sql/servers/databases/delete":        "Deleted",
		"Microsoft.Sql/servers/databases/pause/action":  "Inactive",
		"Microsoft.Sql/servers/databases/resume/action": "Active",

		// Storage Operations
		"Microsoft.Storage/storageAccounts/write":  "Active",
		"Microsoft.Storage/storageAccounts/delete": "Deleted",

		// Web App Operations
		"Microsoft.Web/sites/write":        "Active",
		"Microsoft.Web/sites/start/action": "Active",
		"Microsoft.Web/sites/stop/action":  "Inactive",
		"Microsoft.Web/sites/delete":       "Deleted",

		// AKS Operations
		"Microsoft.ContainerService/managedClusters/write":        "Active",
		"Microsoft.ContainerService/managedClusters/start/action": "Active",
		"Microsoft.ContainerService/managedClusters/stop/action":  "Inactive",
		"Microsoft.ContainerService/managedClusters/delete":       "Deleted",
	}

	if status, ok := operationStatusMap[operationName]; ok {
		return status
	}

	// Default mappings based on operation suffix
	if hasSuffix(operationName, "/write") {
		return "Active"
	}
	if hasSuffix(operationName, "/delete") {
		return "Deleted"
	}
	if hasSuffix(operationName, "/start/action") {
		return "Active"
	}
	if hasSuffix(operationName, "/stop/action") {
		return "Inactive"
	}

	// Read-only operations (listKeys, regenerateKey, etc.) - don't change resource state
	if stringContains(operationName, "/listKeys/action") ||
		stringContains(operationName, "/regenerateKey/action") ||
		stringContains(operationName, "/listConnectionStrings/action") ||
		stringContains(operationName, "/listSecrets/action") {
		return "Active"
	}

	// Default for unknown operations: assume Active (resource exists and is operational)
	// This prevents showing "Unknown" status for valid but unrecognized operations
	return "Active"
}

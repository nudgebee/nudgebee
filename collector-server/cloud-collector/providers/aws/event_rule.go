package aws

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// EventRuleTrigger defines the conditions for an event rule to match.
type EventRuleTrigger struct {
	// SourceSystem specifies the system that generated the event (e.g., "AWS_EventBridge", "AWS_CloudTrail").
	// This maps to the `source` field in your YAML.
	SourceSystem string `yaml:"source" json:"source"`

	// Identifier is a primary matching field whose meaning depends on SourceSystem.
	// For "AWS_EventBridge", this matches the EventBridge event's 'source' field (e.g., "aws.ec2").
	// This maps to the `alert_name` field in your YAML.
	Identifier string `yaml:"alert_name,omitempty" json:"alert_name,omitempty"`
	// EventBridgeDetailType is removed. Use EventFilters with field "detail-type" instead.
	// Example: { field: "detail-type", value: "ECS Task State Change", condition: "equals" }

	// DetailFilters allow for matching on specific fields within the event's detail payload.
	EventFilters []EventFilter `yaml:"event_filters,omitempty" json:"event_filters,omitempty"` // Renamed from DetailFilters

	// Add other origin-specific fields here as needed, e.g.:
	// CloudTrailEventSource string `yaml:"cloudtrail_event_source,omitempty" json:"cloudtrail_event_source,omitempty"`
}

// EventFilter defines a condition to match against a field in the event JSON.
type EventFilter struct {
	// Template is a Go template string that must evaluate to a boolean ("true" or "false")
	// for the filter to be considered a match.
	Template    string `yaml:"template" json:"template"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"` // Optional description for clarity
}

// EventFieldTemplate defines how a specific field in providers.Event should be populated.
type EventFieldTemplate struct {
	Value    string `yaml:"value,omitempty" json:"value,omitempty"`       // A static value
	Template string `yaml:"template,omitempty" json:"template,omitempty"` // A Go template string
}

// UnmarshalYAML implements custom unmarshaling for EventFieldTemplate.
// It supports both plain string values and structured {value: "...", template: "..."} format.
func (e *EventFieldTemplate) UnmarshalYAML(node *yaml.Node) error {
	// Try to unmarshal as a plain string first
	var str string
	if err := node.Decode(&str); err == nil {
		e.Value = str
		e.Template = ""
		return nil
	}

	// If that fails, try to unmarshal as a struct
	type eventFieldTemplateAlias EventFieldTemplate
	var alias eventFieldTemplateAlias
	if err := node.Decode(&alias); err != nil {
		return err
	}
	*e = EventFieldTemplate(alias)
	return nil
}

// EventOutputTemplate defines how to construct a providers.Event.
type EventOutputTemplate struct {
	Title               EventFieldTemplate            `yaml:"title,omitempty" json:"title,omitempty"`
	Severity            EventFieldTemplate            `yaml:"severity,omitempty" json:"severity,omitempty"` // Can be static value or template: "Info", "Warning", "Error", "Critical"
	Description         EventFieldTemplate            `yaml:"description,omitempty" json:"description,omitempty"`
	EventStatus         EventFieldTemplate            `yaml:"status,omitempty" json:"status,omitempty"`           // Can be static value or template: "Open", "Closed", "InProgress", "FIRING", "RESOLVED"
	EventName           EventFieldTemplate            `yaml:"event_name,omitempty" json:"event_name,omitempty"`   // Override for EventName (default: ebEvent.DetailType)
	Fingerprint         EventFieldTemplate            `yaml:"fingerprint,omitempty" json:"fingerprint,omitempty"` // Stable identifier for dedup (e.g., alarm name + region + account)
	ResourceId          EventFieldTemplate            `yaml:"resource_id,omitempty" json:"resource_id,omitempty"`
	ResourceType        EventFieldTemplate            `yaml:"resource_type,omitempty" json:"resource_type,omitempty"`
	ResourceServiceName EventFieldTemplate            `yaml:"resource_service_name,omitempty" json:"resource_service_name,omitempty"`
	ResourceRegion      EventFieldTemplate            `yaml:"resource_region,omitempty" json:"resource_region,omitempty"`
	Labels              map[string]EventFieldTemplate `yaml:"labels,omitempty" json:"labels,omitempty"`
}

// ActionDefinition specifies an action to be taken when a rule matches.
type ActionDefinition struct {
	Name        string         `yaml:"name" json:"name"`                                   // Name of the action, used as a key in AdditionalContext
	Type        string         `yaml:"type" json:"type"`                                   // e.g., "aws_get_resource", "aws_get_metric", "aws_get_log"
	Params      map[string]any `yaml:"params,omitempty" json:"params,omitempty"`           // Parameters for the action, values can be templates
	Description string         `yaml:"description,omitempty" json:"description,omitempty"` // Optional description of the action
}

// Helper structs for typed action parameters (used internally after parsing map[string]any)
type GetResourceActionParams struct {
	ServiceName        string `json:"service_name"`              // e.g., "ecr", "ec2"
	ResourceIdentifier string `json:"resource_identifier"`       // The actual ID, Name, or ARN value
	Region             string `json:"region"`                    // AWS Region
	IdentifierType     string `json:"identifier_type,omitempty"` // "id", "name", or "arn". Default: checks id and arn.
	ResourceType       string `json:"resource_type,omitempty"`   // e.g., "repository", "instance". Filters on providers.Resource.Type
}

type GetMetricActionParams struct {
	Namespace       string              `json:"namespace"`
	MetricName      string              `json:"metric_name"`
	Dimensions      []map[string]string `json:"dimensions"` // List of {name: "dimName", value: "dimValue"}
	PeriodSeconds   int64               `json:"period_seconds"`
	Statistic       string              `json:"statistic"`
	StartTimeOffset string              `json:"start_time_offset"` // e.g., "-15m", "-1h"
	EndTimeOffset   string              `json:"end_time_offset"`   // e.g., "+5m", "0m"
}

type GetLogActionParams struct {
	LogGroupName       string `json:"log_group_name,omitempty"`        // Direct log group name (optional if AutoDetectLogGroup is true)
	AutoDetectLogGroup bool   `json:"auto_detect_log_group,omitempty"` // Set to true to auto-discover ECS service logs
	Query              string `json:"query"`                           // CloudWatch Logs Insights query string
	StartTimeOffset    string `json:"start_time_offset"`               // e.g., "-15m", "-1h"
	EndTimeOffset      string `json:"end_time_offset"`                 // e.g., "+5m", "0m"
	Limit              int64  `json:"limit"`                           // Max number of log events to return
	// Fields required for auto-discovery if AutoDetectLogGroup is true
	ClusterName string `json:"cluster_name,omitempty"` // Required if AutoDetectLogGroup is true
	ServiceName string `json:"service_name,omitempty"` // Required if AutoDetectLogGroup is true
}

// EventProcessingRule defines a single rule for processing an event.
type EventProcessingRule struct {
	Name     string             `yaml:"name" json:"name"` // A unique name for the rule
	Triggers EventRuleTrigger   `yaml:"triggers" json:"triggers"`
	Actions  []ActionDefinition `yaml:"actions,omitempty" json:"actions,omitempty"`

	// ActionsOnly = true means: run the actions, but do NOT emit a downstream
	// event (don't update events table, don't fire playbook). Use for sync /
	// bookkeeping rules that exist purely to mutate state in cloud_resourses
	// (e.g. Resource_Sync_*). Without this flag every state change creates an
	// audit-trail event row and triggers playbook auto-actions for it, which
	// is the bulk of cloud-collector load. event_template fields are ignored
	// for ActionsOnly rules.
	ActionsOnly bool                `yaml:"actions_only,omitempty" json:"actions_only,omitempty"`
	EventOutput EventOutputTemplate `yaml:"event_template" json:"event_template"`
}

// EventRuleSet holds a collection of event processing rules.
type EventRuleSet struct {
	Rules []EventProcessingRule `yaml:"rules" json:"rules"`
}

//go:embed aws_runbook.yaml
var embeddedRulesYAML []byte
var embeddedRules EventRuleSet

// DefaultEventRulesPath is the default path for the event rules YAML file.
// This can be made configurable, e.g., via an environment variable or a config setting.
const DefaultEventRulesPath = "aws_runbook.yaml" // Updated to use aws_runbook.yaml

// GetEventRules loads the event processing rules from a YAML file.
// It first tries to load from the embedded aws_runbook.yaml.
// If rulesFilePath is provided, it attempts to load from that path as an override.
func GetEventRules(rulesFilePath string) (EventRuleSet, error) {
	var ruleSet EventRuleSet
	var yamlContent []byte
	var loadedFrom string

	if rulesFilePath != "" {
		// Attempt to load from the specified file path (override)
		absPath, err := filepath.Abs(rulesFilePath)
		if err != nil {
			return ruleSet, fmt.Errorf("failed to get absolute path for override rules file '%s': %w", rulesFilePath, err)
		}
		yamlContent, err = os.ReadFile(absPath)
		if err != nil {
			return ruleSet, fmt.Errorf("failed to read override event rules file '%s': %w", absPath, err)
		}
		loadedFrom = fmt.Sprintf("file: %s", absPath)
	} else if len(embeddedRulesYAML) > 0 {
		if len(embeddedRules.Rules) > 0 {
			return embeddedRules, nil
		}
		// Use embedded content if no override path is given and embedded content exists
		yamlContent = embeddedRulesYAML
		loadedFrom = "embedded aws_runbook.yaml"
	} else {
		// Fallback if embedded content is empty (should not happen if go:embed is correct and file exists at compile time)
		// and no override path is given. This case is unlikely but handled for robustness.
		return ruleSet, fmt.Errorf("no rules file path provided and embedded rules are empty (ensure aws_runbook.yaml is in the same directory as event_rule.go at compile time)")
	}

	err := yaml.Unmarshal(yamlContent, &ruleSet)
	if err != nil {
		return ruleSet, fmt.Errorf("failed to unmarshal event rules from '%s': %w", loadedFrom, err)
	}

	return ruleSet, nil
}

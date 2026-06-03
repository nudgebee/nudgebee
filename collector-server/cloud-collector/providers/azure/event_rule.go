package azure

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed azure_resource_events.yaml
var defaultAzureEventRulesYAML []byte

// AzureEventRuleTrigger defines the conditions for an Azure event rule to match.
type AzureEventRuleTrigger struct {
	// SourceSystem specifies the system that generated the event (e.g., "Azure_EventGrid", "Azure_CloudEvent").
	SourceSystem string `yaml:"source" json:"source"`

	// Identifier is a primary matching field whose meaning depends on SourceSystem.
	// For "Azure_EventGrid", this matches the Event Grid event's 'subject' field (resource URI) or eventType.
	Identifier string `yaml:"alert_name,omitempty" json:"alert_name,omitempty"`

	// EventFilters allow for matching on specific fields within the event's data payload.
	EventFilters []AzureEventFilter `yaml:"event_filters,omitempty" json:"event_filters,omitempty"`
}

// AzureEventFilter defines a condition to match against a field in the event JSON.
type AzureEventFilter struct {
	// Template is a Go template string that must evaluate to a boolean ("true" or "false")
	// for the filter to be considered a match.
	Template    string `yaml:"template" json:"template"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"` // Optional description for clarity
}

// AzureEventFieldTemplate defines how a specific field in providers.Event should be populated.
type AzureEventFieldTemplate struct {
	Value    string `yaml:"value,omitempty" json:"value,omitempty"`       // A static value
	Template string `yaml:"template,omitempty" json:"template,omitempty"` // A Go template string
}

// UnmarshalYAML implements custom unmarshaling for AzureEventFieldTemplate.
// It supports both plain string values and structured {value: "...", template: "..."} format.
func (e *AzureEventFieldTemplate) UnmarshalYAML(node *yaml.Node) error {
	// Try to unmarshal as a plain string first
	var str string
	if err := node.Decode(&str); err == nil {
		e.Value = str
		e.Template = ""
		return nil
	}

	// If that fails, try to unmarshal as a struct
	type azureEventFieldTemplateAlias AzureEventFieldTemplate
	var alias azureEventFieldTemplateAlias
	if err := node.Decode(&alias); err != nil {
		return err
	}
	*e = AzureEventFieldTemplate(alias)
	return nil
}

// AzureEventOutputTemplate defines how to construct a providers.Event.
type AzureEventOutputTemplate struct {
	EventName           AzureEventFieldTemplate            `yaml:"event_name,omitempty" json:"event_name,omitempty"` // Aggregation key - generic event type for grouping
	Title               AzureEventFieldTemplate            `yaml:"title,omitempty" json:"title,omitempty"`           // Specific event title with resource details
	Severity            AzureEventFieldTemplate            `yaml:"severity,omitempty" json:"severity,omitempty"`     // Can be static value or template: "Info", "Warning", "Error", "Critical"
	Description         AzureEventFieldTemplate            `yaml:"description,omitempty" json:"description,omitempty"`
	EventStatus         AzureEventFieldTemplate            `yaml:"status,omitempty" json:"status,omitempty"` // Can be static value or template: "Open", "Closed", "InProgress", "FIRING", "RESOLVED"
	ResourceId          AzureEventFieldTemplate            `yaml:"resource_id,omitempty" json:"resource_id,omitempty"`
	ResourceType        AzureEventFieldTemplate            `yaml:"resource_type,omitempty" json:"resource_type,omitempty"`
	ResourceServiceName AzureEventFieldTemplate            `yaml:"resource_service_name,omitempty" json:"resource_service_name,omitempty"`
	ResourceRegion      AzureEventFieldTemplate            `yaml:"resource_region,omitempty" json:"resource_region,omitempty"`
	Labels              map[string]AzureEventFieldTemplate `yaml:"labels,omitempty" json:"labels,omitempty"` // Map of label key to template/value
}

// AzureActionDefinition specifies an action to be taken when a rule matches.
type AzureActionDefinition struct {
	Name        string         `yaml:"name" json:"name"`                                   // Name of the action, used as a key in AdditionalContext
	Type        string         `yaml:"type" json:"type"`                                   // e.g., "azure_get_resource", "azure_get_metric", "update_cloud_resource"
	Params      map[string]any `yaml:"params,omitempty" json:"params,omitempty"`           // Parameters for the action, values can be templates
	Description string         `yaml:"description,omitempty" json:"description,omitempty"` // Optional description of the action
}

// AzureEventRule defines a complete rule for processing Azure Event Grid events.
type AzureEventRule struct {
	Name          string                   `yaml:"name" json:"name"`
	Description   string                   `yaml:"description,omitempty" json:"description,omitempty"`
	Triggers      AzureEventRuleTrigger    `yaml:"triggers" json:"triggers"`
	EventTemplate AzureEventOutputTemplate `yaml:"event_template" json:"event_template"`
	Actions       []AzureActionDefinition  `yaml:"actions,omitempty" json:"actions,omitempty"`
}

// AzureEventRules is the root structure for the YAML file containing multiple rules.
type AzureEventRules struct {
	Rules []AzureEventRule `yaml:"rules" json:"rules"`
}

// GetAzureEventRules loads and parses Azure event rules from a YAML file.
// It follows this priority order:
// 1. If rulesFilePath parameter is provided, use that path
// 2. If CLOUD_COLLECTOR_AZURE_EVENT_RULES_PATH environment variable is set, use that path
// 3. Fallback to default locations (azure_resource_events.yaml in various directories)
func GetAzureEventRules(rulesFilePath string) ([]AzureEventRule, error) {
	var effectivePath string
	var loadedFrom string

	// Priority 1: Use provided parameter if not empty
	if rulesFilePath != "" {
		effectivePath = rulesFilePath
		loadedFrom = "parameter"
	} else {
		// Priority 2: Check environment variable
		envPath := os.Getenv("CLOUD_COLLECTOR_AZURE_EVENT_RULES_PATH")
		if envPath != "" {
			effectivePath = envPath
			loadedFrom = "environment variable CLOUD_COLLECTOR_AZURE_EVENT_RULES_PATH"
		}
	}

	// Priority 3: If still empty, try default locations
	var data []byte
	var err error

	if effectivePath == "" {
		currentDir, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current directory: %w", err)
		}

		// Try multiple possible locations
		possiblePaths := []string{
			filepath.Join(currentDir, "providers", "azure", "azure_resource_events.yaml"),
			filepath.Join(currentDir, "azure_resource_events.yaml"),
			"./providers/azure/azure_resource_events.yaml",
			"./azure_resource_events.yaml",
		}

		for _, path := range possiblePaths {
			if _, err := os.Stat(path); err == nil {
				effectivePath = path
				loadedFrom = "default location"
				break
			}
		}

		// If no file found, use embedded default
		if effectivePath == "" {
			data = defaultAzureEventRulesYAML
			loadedFrom = "embedded default"
		}
	}

	// Load and parse the rules file
	if data == nil {
		data, err = os.ReadFile(effectivePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read Azure event rules file from %s (%s): %w", effectivePath, loadedFrom, err)
		}
	}

	var rulesConfig AzureEventRules
	if err := yaml.Unmarshal(data, &rulesConfig); err != nil {
		return nil, fmt.Errorf("failed to parse Azure event rules YAML from %s (%s): %w", effectivePath, loadedFrom, err)
	}

	return rulesConfig.Rules, nil
}

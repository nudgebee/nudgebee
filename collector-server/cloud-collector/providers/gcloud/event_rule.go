package gcloud

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed gcp_runbook.yaml
var gcpRunbookYAML []byte

// embeddedRules caches parsed embedded rules to avoid re-parsing on every call
var embeddedRules EventRuleSet

// EventRuleTrigger defines conditions for event rule matching
type EventRuleTrigger struct {
	SourceSystem string        `yaml:"source" json:"source"`
	Identifier   string        `yaml:"alert_name,omitempty" json:"alert_name,omitempty"`
	EventFilters []EventFilter `yaml:"event_filters,omitempty" json:"event_filters,omitempty"`
}

// EventFilter defines condition to match event fields
type EventFilter struct {
	Template    string `yaml:"template" json:"template"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// EventFieldTemplate defines field population
type EventFieldTemplate struct {
	Value    string `yaml:"value,omitempty" json:"value,omitempty"`
	Template string `yaml:"template,omitempty" json:"template,omitempty"`
}

// UnmarshalYAML custom unmarshaling for EventFieldTemplate
func (e *EventFieldTemplate) UnmarshalYAML(node *yaml.Node) error {
	var str string
	if err := node.Decode(&str); err == nil {
		e.Value = str
		e.Template = ""
		return nil
	}

	type eventFieldTemplateAlias EventFieldTemplate
	var alias eventFieldTemplateAlias
	if err := node.Decode(&alias); err != nil {
		return err
	}
	*e = EventFieldTemplate(alias)
	return nil
}

// EventOutputTemplate defines providers.Event construction
type EventOutputTemplate struct {
	Title               EventFieldTemplate `yaml:"title,omitempty" json:"title,omitempty"`
	Severity            EventFieldTemplate `yaml:"severity,omitempty" json:"severity,omitempty"`
	Description         EventFieldTemplate `yaml:"description,omitempty" json:"description,omitempty"`
	EventStatus         EventFieldTemplate `yaml:"status,omitempty" json:"status,omitempty"`
	ResourceId          EventFieldTemplate `yaml:"resource_id,omitempty" json:"resource_id,omitempty"`
	ResourceType        EventFieldTemplate `yaml:"resource_type,omitempty" json:"resource_type,omitempty"`
	ResourceServiceName EventFieldTemplate `yaml:"resource_service_name,omitempty" json:"resource_service_name,omitempty"`
	ResourceRegion      EventFieldTemplate `yaml:"resource_region,omitempty" json:"resource_region,omitempty"`
}

// EventAction defines action to take on rule match
type EventAction struct {
	Name        string         `yaml:"name" json:"name"`
	Type        string         `yaml:"type" json:"type"`
	Params      map[string]any `yaml:"params,omitempty" json:"params,omitempty"`
	Description string         `yaml:"description,omitempty" json:"description,omitempty"`
}

// EventRule defines complete event rule
type EventRule struct {
	Name          string              `yaml:"name" json:"name"`
	Triggers      []EventRuleTrigger  `yaml:"triggers" json:"triggers"`
	EventTemplate EventOutputTemplate `yaml:"event_template" json:"event_template"`
	Actions       []EventAction       `yaml:"actions,omitempty" json:"actions,omitempty"`
}

// EventRuleSet contains all rules
type EventRuleSet struct {
	Rules []EventRule `yaml:"rules" json:"rules"`
}

// LoadGCPEventRules loads GCP event rules from embedded YAML or file
func LoadGCPEventRules(customFilePath string) (EventRuleSet, error) {
	var data []byte

	if customFilePath != "" {
		absPath, err := filepath.Abs(customFilePath)
		if err != nil {
			return EventRuleSet{}, fmt.Errorf("failed to get absolute path: %w", err)
		}
		data, err = os.ReadFile(absPath)
		if err != nil {
			return EventRuleSet{}, fmt.Errorf("failed to read custom rules file %s: %w", absPath, err)
		}
	} else {
		if len(embeddedRules.Rules) > 0 {
			return embeddedRules, nil
		}
		data = gcpRunbookYAML
	}

	var ruleSet EventRuleSet
	if err := yaml.Unmarshal(data, &ruleSet); err != nil {
		return EventRuleSet{}, fmt.Errorf("failed to unmarshal rules YAML: %w", err)
	}

	if customFilePath == "" {
		embeddedRules = ruleSet
	}

	return ruleSet, nil
}

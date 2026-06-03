package system

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

	"nudgebee/runbook/config"

	"gopkg.in/yaml.v3"
)

//go:embed cron_triggers.yaml
var cronTriggersYAML []byte

type CronTrigger struct {
	Name      string         `yaml:"name"`
	Webhook   string         `yaml:"webhook"`
	Schedule  string         `yaml:"schedule"`
	Payload   map[string]any `yaml:"payload"`
	Headers   []CronHeader   `yaml:"headers"`
	RetryConf *CronRetryConf `yaml:"retry_conf"`
	Comment   string         `yaml:"comment"`
}

type CronHeader struct {
	Name         string `yaml:"name"`
	Value        string `yaml:"value"`
	ValueFromEnv string `yaml:"value_from_env"`
}

type CronRetryConf struct {
	NumRetries           int `yaml:"num_retries"`
	RetryIntervalSeconds int `yaml:"retry_interval_seconds"`
	TimeoutSeconds       int `yaml:"timeout_seconds"`
	ToleranceSeconds     int `yaml:"tolerance_seconds"`
}

// LoadCronTriggers parses the embedded cron_triggers.yaml file.
func LoadCronTriggers() ([]CronTrigger, error) {
	var triggers []CronTrigger
	if err := yaml.Unmarshal(cronTriggersYAML, &triggers); err != nil {
		return nil, fmt.Errorf("failed to parse embedded cron triggers: %w", err)
	}

	for i := range triggers {
		triggers[i].Webhook = resolveTemplateVars(triggers[i].Webhook)
	}

	return triggers, nil
}

// resolveTemplateVars replaces {{VAR}} placeholders with known config values.
func resolveTemplateVars(s string) string {
	replacements := map[string]string{
		"{{SERVICE_API_SERVER_URL}}":     config.Config.ServiceEndpoint,
		"{{CLOUD_COLLECTOR_SERVER_URL}}": config.Config.CloudCollectorServerUrl,
	}
	for placeholder, value := range replacements {
		s = strings.ReplaceAll(s, placeholder, value)
	}
	return s
}

// resolveEnvVar resolves known env var names to config values.
func resolveEnvVar(envName string) string {
	switch envName {
	case "ACTION_API_SERVER_TOKEN":
		return config.Config.ServiceApiServerToken
	case "CLOUD_COLLECTOR_SERVER_TOKEN":
		return config.Config.CloudCollectorServerToken
	default:
		return os.Getenv(envName)
	}
}

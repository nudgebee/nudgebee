package aws

import (
	"embed"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"path"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed alarm_templates/*.yaml
var alarmTemplatesFS embed.FS

// TemplateService represents the service name and its templates
type TemplateService struct {
	ServiceName string                    `yaml:"service_name"`
	Templates   []providers.AlarmTemplate `yaml:"templates"`
}

var (
	templateCache     = make(map[string][]providers.AlarmTemplate)
	templateCacheLock sync.RWMutex
	templateLoadOnce  sync.Once
	templateLoadError error
)

// LoadAlarmTemplates loads alarm templates for a specific service
// Returns cached templates if already loaded, otherwise loads from embedded YAML
func LoadAlarmTemplates(serviceName string) ([]providers.AlarmTemplate, error) {
	// Initialize templates on first call
	templateLoadOnce.Do(func() {
		templateLoadError = initializeTemplates()
	})

	if templateLoadError != nil {
		return nil, templateLoadError
	}

	templateCacheLock.RLock()
	defer templateCacheLock.RUnlock()

	templates, ok := templateCache[serviceName]
	if !ok {
		return nil, fmt.Errorf("no alarm templates found for service: %s", serviceName)
	}

	return templates, nil
}

// GetAllTemplates returns all loaded alarm templates across all services
func GetAllTemplates() map[string][]providers.AlarmTemplate {
	templateLoadOnce.Do(func() {
		templateLoadError = initializeTemplates()
	})

	templateCacheLock.RLock()
	defer templateCacheLock.RUnlock()

	// Return a copy to prevent external modification
	result := make(map[string][]providers.AlarmTemplate)
	for k, v := range templateCache {
		result[k] = v
	}
	return result
}

// initializeTemplates loads all embedded YAML templates into the cache
func initializeTemplates() error {
	templateCacheLock.Lock()
	defer templateCacheLock.Unlock()

	// Read all files from the alarm_templates directory
	entries, err := alarmTemplatesFS.ReadDir("alarm_templates")
	if err != nil {
		return fmt.Errorf("failed to read alarm_templates directory: %w", err)
	}

	// Iterate through all YAML files and load them
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()

		// Only process .yaml files
		if !strings.HasSuffix(filename, ".yaml") && !strings.HasSuffix(filename, ".yml") {
			continue
		}

		// Extract service name from filename (e.g., "ec2.yaml" -> "ec2")
		serviceName := strings.TrimSuffix(strings.TrimSuffix(filename, ".yaml"), ".yml")

		// Read file content
		filePath := path.Join("alarm_templates", filename)
		yamlContent, err := alarmTemplatesFS.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", filename, err)
		}

		// Load the service templates
		if err := loadServiceTemplates(serviceName, string(yamlContent)); err != nil {
			return fmt.Errorf("failed to load %s templates: %w", serviceName, err)
		}
	}

	return nil
}

// loadServiceTemplates unmarshals YAML content and stores in cache
func loadServiceTemplates(serviceName, yamlContent string) error {
	var service TemplateService
	if err := yaml.Unmarshal([]byte(yamlContent), &service); err != nil {
		return fmt.Errorf("failed to unmarshal YAML for %s: %w", serviceName, err)
	}

	// Validate service name matches
	if service.ServiceName != serviceName {
		return fmt.Errorf("service name mismatch: expected %s, got %s", serviceName, service.ServiceName)
	}

	// Validate templates
	if len(service.Templates) == 0 {
		return fmt.Errorf("no templates found in %s.yaml", serviceName)
	}

	templateCache[serviceName] = service.Templates
	return nil
}

// GetTemplateByName retrieves a specific alarm template by name across all services
func GetTemplateByName(templateName string) (*providers.AlarmTemplate, error) {
	templateLoadOnce.Do(func() {
		templateLoadError = initializeTemplates()
	})

	if templateLoadError != nil {
		return nil, templateLoadError
	}

	templateCacheLock.RLock()
	defer templateCacheLock.RUnlock()

	for _, templates := range templateCache {
		for i := range templates {
			if templates[i].Name == templateName {
				return &templates[i], nil
			}
		}
	}

	return nil, fmt.Errorf("template not found: %s", templateName)
}

// ReloadTemplates clears the cache and forces reloading of all templates
// Primarily for testing purposes
func ReloadTemplates() error {
	templateCacheLock.Lock()
	defer templateCacheLock.Unlock()

	templateCache = make(map[string][]providers.AlarmTemplate)
	templateLoadOnce = sync.Once{}
	templateLoadError = nil

	return initializeTemplates()
}

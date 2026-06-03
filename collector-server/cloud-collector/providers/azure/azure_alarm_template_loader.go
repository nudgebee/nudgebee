package azure

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
var azureAlarmTemplatesFS embed.FS

// AzureTemplateService represents the service name and its templates
type AzureTemplateService struct {
	ServiceName string                    `yaml:"service_name"`
	Templates   []providers.AlarmTemplate `yaml:"templates"`
}

var (
	azureTemplateCache     = make(map[string][]providers.AlarmTemplate)
	azureTemplateCacheLock sync.RWMutex
	azureTemplateLoadOnce  sync.Once
	azureTemplateLoadError error
)

// LoadAzureAlarmTemplates loads alarm templates for a specific Azure service
// Returns cached templates if already loaded, otherwise loads from embedded YAML
func LoadAzureAlarmTemplates(serviceName string) ([]providers.AlarmTemplate, error) {
	// Initialize templates on first call
	azureTemplateLoadOnce.Do(func() {
		azureTemplateLoadError = initializeAzureTemplates()
	})

	if azureTemplateLoadError != nil {
		return nil, azureTemplateLoadError
	}

	azureTemplateCacheLock.RLock()
	defer azureTemplateCacheLock.RUnlock()

	templates, ok := azureTemplateCache[serviceName]
	if !ok {
		return nil, fmt.Errorf("no Azure alarm templates found for service: %s", serviceName)
	}

	return templates, nil
}

// GetAllAzureTemplates returns all loaded Azure alarm templates across all services
func GetAllAzureTemplates() map[string][]providers.AlarmTemplate {
	azureTemplateLoadOnce.Do(func() {
		azureTemplateLoadError = initializeAzureTemplates()
	})

	azureTemplateCacheLock.RLock()
	defer azureTemplateCacheLock.RUnlock()

	// Return a copy to prevent external modification
	result := make(map[string][]providers.AlarmTemplate)
	for k, v := range azureTemplateCache {
		result[k] = v
	}
	return result
}

// initializeAzureTemplates loads all embedded YAML templates into the cache
func initializeAzureTemplates() error {
	azureTemplateCacheLock.Lock()
	defer azureTemplateCacheLock.Unlock()

	// Read all files from the alarm_templates directory
	entries, err := azureAlarmTemplatesFS.ReadDir("alarm_templates")
	if err != nil {
		return fmt.Errorf("failed to read Azure alarm_templates directory: %w", err)
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

		// Extract service name from filename (e.g., "vm.yaml" -> "vm")
		serviceName := strings.TrimSuffix(strings.TrimSuffix(filename, ".yaml"), ".yml")

		// Read file content
		filePath := path.Join("alarm_templates", filename)
		yamlContent, err := azureAlarmTemplatesFS.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", filename, err)
		}

		// Load the service templates
		if err := loadAzureServiceTemplates(serviceName, string(yamlContent)); err != nil {
			return fmt.Errorf("failed to load %s templates: %w", serviceName, err)
		}
	}

	return nil
}

// loadAzureServiceTemplates unmarshals YAML content and stores in cache
func loadAzureServiceTemplates(serviceName, yamlContent string) error {
	var service AzureTemplateService
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

	azureTemplateCache[serviceName] = service.Templates
	return nil
}

// GetAzureTemplateByName retrieves a specific Azure alarm template by name across all services
func GetAzureTemplateByName(templateName string) (*providers.AlarmTemplate, error) {
	azureTemplateLoadOnce.Do(func() {
		azureTemplateLoadError = initializeAzureTemplates()
	})

	if azureTemplateLoadError != nil {
		return nil, azureTemplateLoadError
	}

	azureTemplateCacheLock.RLock()
	defer azureTemplateCacheLock.RUnlock()

	for _, templates := range azureTemplateCache {
		for i := range templates {
			if templates[i].Name == templateName {
				return &templates[i], nil
			}
		}
	}

	return nil, fmt.Errorf("azure template not found: %s", templateName)
}

// ReloadAzureTemplates clears the cache and forces reloading of all templates
// Primarily for testing purposes
func ReloadAzureTemplates() error {
	azureTemplateCacheLock.Lock()
	defer azureTemplateCacheLock.Unlock()

	azureTemplateCache = make(map[string][]providers.AlarmTemplate)
	azureTemplateLoadOnce = sync.Once{}
	azureTemplateLoadError = nil

	return initializeAzureTemplates()
}

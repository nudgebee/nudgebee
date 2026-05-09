package gcloud

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
var gcpAlarmTemplatesFS embed.FS

// GCPTemplateService represents the service name and its templates
type GCPTemplateService struct {
	ServiceName string                    `yaml:"service_name"`
	Templates   []providers.AlarmTemplate `yaml:"templates"`
}

var (
	gcpTemplateCache     = make(map[string][]providers.AlarmTemplate)
	gcpTemplateCacheLock sync.RWMutex
	gcpTemplateLoadOnce  sync.Once
	gcpTemplateLoadError error
)

// LoadGCPAlarmTemplates loads alarm templates for a specific GCP service
// Returns cached templates if already loaded, otherwise loads from embedded YAML
func LoadGCPAlarmTemplates(serviceName string) ([]providers.AlarmTemplate, error) {
	// Initialize templates on first call
	gcpTemplateLoadOnce.Do(func() {
		gcpTemplateLoadError = initializeGCPTemplates()
	})

	if gcpTemplateLoadError != nil {
		return nil, gcpTemplateLoadError
	}

	gcpTemplateCacheLock.RLock()
	defer gcpTemplateCacheLock.RUnlock()

	templates, ok := gcpTemplateCache[serviceName]
	if !ok {
		return nil, fmt.Errorf("no GCP alarm templates found for service: %s", serviceName)
	}

	return templates, nil
}

// GetAllGCPTemplates returns all loaded alarm templates across all GCP services
func GetAllGCPTemplates() map[string][]providers.AlarmTemplate {
	gcpTemplateLoadOnce.Do(func() {
		gcpTemplateLoadError = initializeGCPTemplates()
	})

	gcpTemplateCacheLock.RLock()
	defer gcpTemplateCacheLock.RUnlock()

	// Return a copy to prevent external modification
	result := make(map[string][]providers.AlarmTemplate)
	for k, v := range gcpTemplateCache {
		result[k] = v
	}
	return result
}

// initializeGCPTemplates loads all embedded YAML templates into the cache
func initializeGCPTemplates() error {
	gcpTemplateCacheLock.Lock()
	defer gcpTemplateCacheLock.Unlock()

	// Read all files from the alarm_templates directory
	entries, err := gcpAlarmTemplatesFS.ReadDir("alarm_templates")
	if err != nil {
		return fmt.Errorf("failed to read GCP alarm_templates directory: %w", err)
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

		// Extract service name from filename (e.g., "compute.yaml" -> "compute")
		fileServiceName := strings.TrimSuffix(strings.TrimSuffix(filename, ".yaml"), ".yml")

		// Read file content
		filePath := path.Join("alarm_templates", filename)
		yamlContent, err := gcpAlarmTemplatesFS.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", filename, err)
		}

		// Load the service templates
		if err := loadGCPServiceTemplates(fileServiceName, string(yamlContent)); err != nil {
			return fmt.Errorf("failed to load %s templates: %w", fileServiceName, err)
		}
	}

	return nil
}

// loadGCPServiceTemplates unmarshals YAML content and stores in cache
func loadGCPServiceTemplates(fileServiceName, yamlContent string) error {
	var service GCPTemplateService
	if err := yaml.Unmarshal([]byte(yamlContent), &service); err != nil {
		return fmt.Errorf("failed to unmarshal GCP YAML for %s: %w", fileServiceName, err)
	}

	// Validate templates
	if len(service.Templates) == 0 {
		return fmt.Errorf("no templates found in %s.yaml", fileServiceName)
	}

	// Store by the service_name specified in the YAML (e.g., "Compute Engine", "Cloud SQL")
	// But also create an alias with the filename (e.g., "compute", "cloudsql") for easier lookup
	gcpTemplateCache[service.ServiceName] = service.Templates
	gcpTemplateCache[fileServiceName] = service.Templates // Alias

	return nil
}

// GetGCPTemplateByName retrieves a specific alarm template by name across all GCP services
func GetGCPTemplateByName(templateName string) (*providers.AlarmTemplate, error) {
	gcpTemplateLoadOnce.Do(func() {
		gcpTemplateLoadError = initializeGCPTemplates()
	})

	if gcpTemplateLoadError != nil {
		return nil, gcpTemplateLoadError
	}

	gcpTemplateCacheLock.RLock()
	defer gcpTemplateCacheLock.RUnlock()

	for _, templates := range gcpTemplateCache {
		for i := range templates {
			if templates[i].Name == templateName {
				return &templates[i], nil
			}
		}
	}

	return nil, fmt.Errorf("GCP template not found: %s", templateName)
}

// ReloadGCPTemplates clears the cache and forces reloading of all templates
// Primarily for testing purposes
func ReloadGCPTemplates() error {
	gcpTemplateCacheLock.Lock()
	defer gcpTemplateCacheLock.Unlock()

	gcpTemplateCache = make(map[string][]providers.AlarmTemplate)
	gcpTemplateLoadOnce = sync.Once{}
	gcpTemplateLoadError = nil

	return initializeGCPTemplates()
}

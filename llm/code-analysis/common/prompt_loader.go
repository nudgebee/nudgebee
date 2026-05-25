package common

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed prompts/agents/*.tmpl
var promptFiles embed.FS

// PromptLoader handles loading and templating prompts from external files
type PromptLoader struct {
	templates map[string]*template.Template
}

// NewPromptLoader creates a new prompt loader instance
func NewPromptLoader() *PromptLoader {
	loader := &PromptLoader{
		templates: make(map[string]*template.Template),
	}
	return loader
}

// LoadPrompt loads and processes a prompt template with the given data
func (pl *PromptLoader) LoadPrompt(templateName string, data map[string]any) (string, error) {
	// Check if template is already cached
	if tmpl, exists := pl.templates[templateName]; exists {
		return pl.executeTemplate(tmpl, data)
	}

	// Load template from embedded files
	templatePath := fmt.Sprintf("prompts/agents/%s.tmpl", templateName)
	content, err := promptFiles.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("failed to load prompt template %s: %w", templateName, err)
	}

	// Parse template
	tmpl, err := template.New(templateName).Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("failed to parse prompt template %s: %w", templateName, err)
	}

	// Cache template
	pl.templates[templateName] = tmpl

	return pl.executeTemplate(tmpl, data)
}

// executeTemplate executes a template with the given data
func (pl *PromptLoader) executeTemplate(tmpl *template.Template, data map[string]any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	// Clean up extra whitespace
	result := strings.TrimSpace(buf.String())

	return result, nil
}

// GetAvailablePrompts returns a list of available prompt templates
func (pl *PromptLoader) GetAvailablePrompts() []string {
	// This would need to be implemented to scan the embedded filesystem
	// For now, return known prompts
	knownPrompts := []string{
		"code_fixer",
		"error_rca",
		"security_auditor",
		"performance_debugger",
		"code_agent",
		"router_agent",
	}

	return knownPrompts
}

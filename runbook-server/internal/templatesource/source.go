// Package templatesource loads system runbook templates from one of several
// sources (an embedded vendored snapshot, a GitHub repo, or a mirror URL) and
// reconciles them into the workflow_templates table. See the package design in
// the repo plan: bundled is the trusted offline default; GitHub sync is opt-in.
package templatesource

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/storage"
	"nudgebee/runbook/internal/tasks"

	"gopkg.in/yaml.v3"
)

// Source names; also stored verbatim in workflow_templates.source.
const (
	SourceBundled = "bundled"
	SourceGitHub  = "github"
	SourceURL     = "url"
)

// RawTemplate is one decoded template ready to upsert. Definition/TemplateVariables/
// Tags are JSON (the YAML re-marshaled), matching the jsonb columns. Checksum is the
// sha256 of the raw YAML file bytes (NOT the derived JSON), so it is stable.
type RawTemplate struct {
	Slug                  string
	Name                  string
	Description           string
	Category              string
	Icon                  string
	Status                string
	DefinitionJSON        string
	TemplateVariablesJSON string
	TagsJSON              string
	Checksum              string
}

// Bundle is the full set of templates a Source resolved at a point in time.
// OK=false means the fetch failed/was incomplete and MUST NOT be reconciled.
type Bundle struct {
	SourceName string
	Ref        string
	Templates  []RawTemplate
	OK         bool
}

// Source resolves the current set of system templates from somewhere.
type Source interface {
	Name() string
	Fetch(ctx context.Context) (*Bundle, error)
}

// toInput maps a decoded template to the storage upsert input.
func (rt RawTemplate) toInput() storage.SystemTemplateInput {
	return storage.SystemTemplateInput{
		ExternalKey:           rt.Slug,
		Name:                  rt.Name,
		Description:           rt.Description,
		Category:              rt.Category,
		Icon:                  rt.Icon,
		Status:                rt.Status,
		DefinitionJSON:        rt.DefinitionJSON,
		TemplateVariablesJSON: rt.TemplateVariablesJSON,
		TagsJSON:              rt.TagsJSON,
		Checksum:              rt.Checksum,
	}
}

// validTaskTypes returns the set of task types the engine actually supports. Built
// from the same registry used at execution time so the allowlist can never drift.
func validTaskTypes() map[string]bool {
	reg := tasks.NewInitializedTaskRegistry()
	set := make(map[string]bool)
	for _, t := range reg.ListTasks() {
		set[t.GetName()] = true
	}
	return set
}

// decode parses one template YAML file into a RawTemplate, computing the checksum
// over the raw bytes and rejecting the template if any task references a type the
// engine does not know about. validTypes is passed in so the registry is built once
// per Fetch rather than per file.
func decode(raw []byte, validTypes map[string]bool) (RawTemplate, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return RawTemplate{}, fmt.Errorf("invalid yaml: %w", err)
	}

	rt := RawTemplate{
		Slug:        asString(doc["slug"]),
		Name:        asString(doc["name"]),
		Description: asString(doc["description"]),
		Category:    asString(doc["category"]),
		Icon:        asString(doc["icon"]),
		Status:      asString(doc["status"]),
		Checksum:    fmt.Sprintf("%x", sha256.Sum256(raw)),
	}
	if rt.Slug == "" {
		return RawTemplate{}, fmt.Errorf("template is missing slug")
	}
	if rt.Name == "" {
		return RawTemplate{}, fmt.Errorf("template %q is missing name", rt.Slug)
	}
	if rt.Status == "" {
		rt.Status = "ACTIVE"
	}

	if doc["definition"] == nil {
		return RawTemplate{}, fmt.Errorf("template %q is missing definition", rt.Slug)
	}
	defJSON, err := json.Marshal(doc["definition"])
	if err != nil {
		return RawTemplate{}, fmt.Errorf("template %q: failed to encode definition: %w", rt.Slug, err)
	}
	rt.DefinitionJSON = string(defJSON)

	rt.TemplateVariablesJSON = "[]"
	if doc["template_variables"] != nil {
		tvJSON, err := json.Marshal(doc["template_variables"])
		if err != nil {
			return RawTemplate{}, fmt.Errorf("template %q: failed to encode template_variables: %w", rt.Slug, err)
		}
		rt.TemplateVariablesJSON = string(tvJSON)
	}

	rt.TagsJSON = "{}"
	if doc["tags"] != nil {
		tagsJSON, err := json.Marshal(doc["tags"])
		if err != nil {
			return RawTemplate{}, fmt.Errorf("template %q: failed to encode tags: %w", rt.Slug, err)
		}
		rt.TagsJSON = string(tagsJSON)
	}

	// Task-type allowlist: reject definitions referencing unknown task types.
	var def model.WorkflowDefinition
	if err := json.Unmarshal(defJSON, &def); err != nil {
		return RawTemplate{}, fmt.Errorf("template %q: definition is not a valid workflow: %w", rt.Slug, err)
	}
	if err := validateTaskTypes(rt.Slug, def.Tasks, validTypes); err != nil {
		return RawTemplate{}, err
	}

	return rt, nil
}

// validateTaskTypes walks the task tree (including nested group/foreach tasks) and
// rejects any task whose type is not registered.
func validateTaskTypes(slug string, tasksList []model.Task, validTypes map[string]bool) error {
	for _, t := range tasksList {
		if t.Type != "" && !validTypes[t.Type] {
			return fmt.Errorf("template %q references unknown task type %q", slug, t.Type)
		}
		if len(t.Tasks) > 0 {
			if err := validateTaskTypes(slug, t.Tasks, validTypes); err != nil {
				return err
			}
		}
	}
	return nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// NewSourceFromConfig builds the configured Source. Defaults to the bundled source.
func NewSourceFromConfig() (Source, error) {
	switch config.Config.RunbookServerTemplateSource {
	case SourceGitHub:
		return NewGitHubSource(
			config.Config.RunbookServerTemplateGitHubRepo,
			config.Config.RunbookServerTemplateGitHubRef,
			config.Config.RunbookServerTemplateGitHubToken,
		), nil
	case SourceURL:
		return NewURLSource(config.Config.RunbookServerTemplateSourceURL), nil
	case SourceBundled, "":
		return NewBundledSource(), nil
	default:
		return nil, fmt.Errorf("unknown template source %q", config.Config.RunbookServerTemplateSource)
	}
}

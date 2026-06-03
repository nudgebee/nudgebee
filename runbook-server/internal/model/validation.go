package model

import (
	"encoding/json"
	"reflect"
	"regexp" // Add this import
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
)

var validate *validator.Validate

func init() {
	validate = validator.New()
	validate.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})
	_ = validate.RegisterValidation("duration", ValidateDuration)
	_ = validate.RegisterValidation("workflowtrigger", ValidateWorkflowTrigger)
	_ = validate.RegisterValidation("workflowname", ValidateWorkflowName)       // New
	_ = validate.RegisterValidation("taskid", ValidateTaskID)                   // New
	_ = validate.RegisterValidation("workflowversion", ValidateWorkflowVersion) // New
	_ = validate.RegisterValidation("setstate", ValidateSetState)               // New custom validation for SetState field
	_ = validate.RegisterValidation("setvars", ValidateSetVars)                 // New custom validation for SetVars field
	validate.RegisterStructValidation(validateTriggerStructLevel, Trigger{})
	validate.RegisterStructValidation(validateWorkflowDefinitionStructLevel, WorkflowDefinition{})
}

// ValidateSetVars is a custom validator for the polymorphic SetVars field.
func ValidateSetVars(fl validator.FieldLevel) bool {
	setVarsMap, ok := fl.Field().Interface().(map[string]any)
	if !ok {
		return false
	}

	for _, val := range setVarsMap {
		if _, isString := val.(string); isString {
			// Simple string assignment is valid
			continue
		}

		// Check if it's a polymorphic object
		if valMap, isMap := val.(map[string]any); isMap {
			var config SetVarConfig
			jsonBytes, err := json.Marshal(valMap)
			if err != nil {
				return false
			}
			if err := json.Unmarshal(jsonBytes, &config); err != nil {
				return false
			}
			if err := validate.Struct(config); err != nil {
				return false
			}
		} else {
			// Invalid type
			return false
		}
	}
	return true
}

// ValidateSetState is a custom validator for the polymorphic SetState field.
func ValidateSetState(fl validator.FieldLevel) bool {
	setStateMap, ok := fl.Field().Interface().(map[string]any)
	if !ok {
		// Should not happen if the field is map[string]any
		return false
	}

	for _, val := range setStateMap {
		if _, isString := val.(string); isString {
			// Simple string assignment is valid
			continue
		}

		// Check if it's a polymorphic object
		if valMap, isMap := val.(map[string]any); isMap {
			var config SetStateConfig
			// Using json.Marshal/Unmarshal to convert map[string]any to struct for validation
			// This is a common pattern when dealing with dynamic maps needing struct validation
			jsonBytes, err := json.Marshal(valMap)
			if err != nil {
				return false // Invalid map structure that can't be marshaled
			}
			if err := json.Unmarshal(jsonBytes, &config); err != nil {
				return false // Invalid map structure for SetStateConfig
			}

			// Validate the extracted config struct
			if err := validate.Struct(config); err != nil {
				// Report error for the specific key if needed, but for simple bool return,
				// returning false is enough.
				return false
			}
		} else {
			// Neither string nor map -> invalid type for set_state value
			return false
		}
	}
	return true
}

// validateTriggerStructLevel is a wrapper function to call Trigger.Validate as a StructLevelFunc.
func validateTriggerStructLevel(sl validator.StructLevel) {
	trigger := sl.Current().Interface().(Trigger)
	trigger.Validate(sl)
}

// validateWorkflowDefinitionStructLevel checks for missing dependencies and duplicate task IDs in WorkflowDefinition.
func validateWorkflowDefinitionStructLevel(sl validator.StructLevel) {
	def, ok := sl.Current().Interface().(WorkflowDefinition)
	if !ok {
		return
	}
	// Build adjacency list for cycle detection
	adj := make(map[string][]string)
	for _, t := range def.Tasks {
		adj[t.ID] = append([]string{}, t.DependsOn...)
	}

	// Cycle detection using DFS
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	var hasCycle func(string) bool
	hasCycle = func(node string) bool {
		if recStack[node] {
			return true
		}
		if visited[node] {
			return false
		}
		visited[node] = true
		recStack[node] = true
		for _, dep := range adj[node] {
			if hasCycle(dep) {
				return true
			}
		}
		recStack[node] = false
		return false
	}
	for id := range adj {
		if hasCycle(id) {
			sl.ReportError(id, "tasks", "Tasks", "circular_dependency", id)
			break // Only need to report one cycle
		}
	}
	// Validate that the sum of all task timeouts does not exceed the workflow timeout
	if def.Timeout != "" {
		wfTimeout, err := time.ParseDuration(def.Timeout)
		if err == nil && wfTimeout > 0 {
			var totalTaskTimeout time.Duration
			for _, t := range def.Tasks {
				if t.Timeout != "" {
					if taskTimeout, err := time.ParseDuration(t.Timeout); err == nil {
						totalTaskTimeout += taskTimeout
					}
				}
			}
			if totalTaskTimeout > 0 && totalTaskTimeout > wfTimeout {
				sl.ReportError(def.Timeout, "timeout", "Timeout", "task_timeouts_exceed_workflow", "")
			}
		}
	}

	idSet := make(map[string]struct{})
	for i, t := range def.Tasks {
		if _, exists := idSet[t.ID]; exists {
			sl.ReportError(t.ID, "tasks", "Tasks", "duplicate_task_id", t.ID)
		} else {
			idSet[t.ID] = struct{}{}
		}
		// Check dependencies
		for _, dep := range t.DependsOn {
			if dep == t.ID {
				sl.ReportError(dep, "tasks["+strconv.Itoa(i)+"].depends_on", "DependsOn", "self_dependency", dep)
				continue
			}
			if _, ok := idSet[dep]; !ok {
				// Only report if dep is not in any task (not just before in list)
				found := false
				for _, other := range def.Tasks {
					if other.ID == dep {
						found = true
						break
					}
				}
				if !found {
					sl.ReportError(dep, "tasks["+strconv.Itoa(i)+"].depends_on", "DependsOn", "missing_dependency", dep)
				}
			}
		}
	}
}

// ValidateWorkflow validates a Workflow struct using go-playground/validator.
func ValidateWorkflow(wf Workflow) error {
	return validate.Struct(wf)
}

// ValidateConfig validates a Config struct using go-playground/validator.
func ValidateConfig(cfg Config) error {
	return validate.Struct(cfg)
}

// ValidateDuration is a custom validator for duration strings.
func ValidateDuration(fl validator.FieldLevel) bool {
	durationStr := fl.Field().String()
	if durationStr == "" {
		return true // omitempty handled by the tag
	}
	_, err := time.ParseDuration(durationStr)
	return err == nil
}

// ValidateWorkflowTrigger is a custom validator for WorkflowTrigger.
func ValidateWorkflowTrigger(fl validator.FieldLevel) bool {
	triggerType := WorkflowTrigger(fl.Field().String())
	switch triggerType {
	case WorkflowTriggerSchedule, WorkflowTriggerManual, WorkflowTriggerWebhook, WorkflowTriggerEvent, WorkflowTriggerOptimization:
		return true
	default:
		return false
	}
}

// Regex for workflow name: starts and ends with alphanumeric, allows space, hyphen, underscore in between.
var workflowNameRegex = regexp.MustCompile("^[a-zA-Z0-9](?:[a-zA-Z0-9 _-]*[a-zA-Z0-9])?$")

// Regex for task ID: alphanumeric, hyphen, or underscore.
var taskIDRegex = regexp.MustCompile("^[a-zA-Z0-9_-]+$")

// ValidateWorkflowName is a custom validator for workflow names.
func ValidateWorkflowName(fl validator.FieldLevel) bool {
	name := fl.Field().String()
	if len(name) < 3 || len(name) > 50 {
		return false
	}
	return workflowNameRegex.MatchString(name)
}

// ValidateTaskID is a custom validator for task IDs.
func ValidateTaskID(fl validator.FieldLevel) bool {
	id := fl.Field().String()
	if len(id) < 3 || len(id) > 64 {
		return false
	}
	return taskIDRegex.MatchString(id)
}

// ValidateWorkflowVersion checks if the version is strictly "v1".
func ValidateWorkflowVersion(fl validator.FieldLevel) bool {
	return fl.Field().String() == "v1"
}

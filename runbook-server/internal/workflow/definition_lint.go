package workflow

import (
	"fmt"
	"strings"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/scripting/executors"
)

// LintWarning is a problem that does not block saving: the automation may still
// run, just not the way its author expects. Validation returns these alongside
// "valid" so people see them without losing the ability to save, and the
// automation builder treats every code it knows as an error to fix.
type LintWarning struct {
	Code    string `json:"code"`
	TaskID  string `json:"task_id"`
	Message string `json:"message"`
}

const (
	// LintScriptLoginCode: a Run Script step signs itself in.
	LintScriptLoginCode = "script_login_code"
	// LintSwitchTargetDependsOn: a switch branch task depends on a task outside
	// its branch, which fails with "missing dependencies" when the branch runs.
	LintSwitchTargetDependsOn = "switch_target_depends_on"
)

// LintDefinition returns the warnings for a workflow's tasks, nested groups and
// foreach bodies included. Never nil, so callers can serialise it as-is.
func LintDefinition(tasks []model.Task) []LintWarning {
	warnings := []LintWarning{}
	lintTasks(tasks, &warnings)
	return warnings
}

func lintTasks(tasks []model.Task, warnings *[]LintWarning) {
	branchOf := switchBranches(tasks)
	for _, task := range tasks {
		if task.Type == "scripting.run_script" {
			script, _ := task.Params["script"].(string)
			if how := executors.ScriptLogin(script, stringEnv(task.Params["env"])); how != "" {
				*warnings = append(*warnings, LintWarning{
					Code:   LintScriptLoginCode,
					TaskID: task.ID,
					Message: fmt.Sprintf("task '%s': the script signs itself in (%s). The step already runs as the account it names, "+
						"so remove the login — or use a built-in action such as aws.cli or k8s.cli.", task.ID, how),
				})
			}
		}
		if sw, ok := branchOf[task.ID]; ok {
			past := switchPast(sw, tasks)
			var outside []string
			for _, dep := range task.DependsOn {
				if !past[dep] {
					outside = append(outside, dep)
				}
			}
			if len(outside) > 0 {
				*warnings = append(*warnings, LintWarning{
					Code:   LintSwitchTargetDependsOn,
					TaskID: task.ID,
					Message: fmt.Sprintf("task '%s' is a branch of switch '%s' but depends on %s, which may not have run when the branch starts. "+
						"A branch runs inside the switch once it has chosen — depend only on the switch or on steps before it.",
						task.ID, sw, strings.Join(outside, ", ")),
				})
			}
		}
		if len(task.Tasks) > 0 {
			lintTasks(task.Tasks, warnings)
		}
		if task.Type == "core.foreach" {
			if subtasks := extractSubtasksFromParams(task.Params); len(subtasks) > 0 {
				lintTasks(subtasks, warnings)
			}
		}
	}
}

// switchBranches maps each task reached through a switch case to that switch.
func switchBranches(tasks []model.Task) map[string]string {
	branchOf := map[string]string{}
	for _, t := range tasks {
		if cases, ok := t.Params["cases"].([]any); ok {
			for _, c := range cases {
				if cm, ok := c.(map[string]any); ok {
					if next, _ := cm["next"].(string); next != "" {
						branchOf[next] = t.ID
					}
				}
			}
		}
		if next, _ := t.Params["default_next"].(string); next != "" {
			branchOf[next] = t.ID
		}
	}
	return branchOf
}

// switchPast returns the switch and every task it transitively depends on; all
// of them have finished before a branch of that switch runs.
func switchPast(switchID string, tasks []model.Task) map[string]bool {
	deps := map[string][]string{}
	for _, t := range tasks {
		deps[t.ID] = t.DependsOn
	}
	seen := map[string]bool{}
	stack := []string{switchID}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		stack = append(stack, deps[id]...)
	}
	return seen
}

func stringEnv(raw any) map[string]string {
	env := map[string]string{}
	switch m := raw.(type) {
	case map[string]string:
		return m
	case map[string]any:
		for k, v := range m {
			env[k] = fmt.Sprintf("%v", v)
		}
	}
	return env
}

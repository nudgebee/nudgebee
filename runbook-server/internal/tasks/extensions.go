package tasks

import "nudgebee/runbook/internal/tasks/types"

// extensionTasks are task types an extension package registers from its
// init() (see cmd/imports_enterprise_bee.go). The OSS build registers none.
var extensionTasks []types.Task

// RegisterExtension adds a task type to every registry built afterwards, with
// the permission and tier its steps need. Call it from an init(): it is not
// safe for concurrent use.
func RegisterExtension(task types.Task, req StepRequirement) {
	extensionTasks = append(extensionTasks, task)
	stepRequirements[task.GetName()] = req
}

// StepRequirementFor builds a requirement for an extension task: the
// principal needs <module>:<class> on the account, and the step has this tier.
// class is "Read", "Write" or "Execute".
func StepRequirementFor(module, class string, tier int) StepRequirement {
	return StepRequirement{Module: module, Class: class, Tier: tier}
}

package model

// Roles a workflow can have when it is bound to an agent (workflows.bee.role).
const (
	// BindingRoleAutomation is an automation that only its agent may start;
	// an extension decides, on every run, whether it may run.
	BindingRoleAutomation = "automation"
	// BindingRoleTrigger is the hidden automation that starts its agent. It
	// fires like any automation but is left out of automation lists.
	BindingRoleTrigger = "trigger"
)

// WorkflowBinding is workflows.bee: the agent a workflow is bound to and its
// role there. Written only by the install path of the extension that owns the
// agent, never from a create or update request. NULL for every other
// workflow.
type WorkflowBinding struct {
	// Bee is the canonical name of the agent.
	Bee string `json:"bee"`
	// InstallID identifies the install when one agent is installed more than once.
	InstallID string `json:"install_id,omitempty"`
	// Name is the automation's canonical name: the tool name its agent sees.
	Name string `json:"name,omitempty"`
	Role string `json:"role"`
	// Mode is the admin-set mode per environment (prod, non_prod).
	Mode map[string]string `json:"mode,omitempty"`
}

// IsAutomation reports whether b binds an automation only its agent starts.
func (b *WorkflowBinding) IsAutomation() bool {
	return b != nil && b.Role == BindingRoleAutomation
}

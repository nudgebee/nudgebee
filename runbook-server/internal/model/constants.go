package model

const (
	// System Search Attributes
	SearchAttrTenantID         = "nb_tenant_id"
	SearchAttrAccountID        = "nb_account_id"
	SearchAttrWorkflowID       = "nb_workflow_id"
	SearchAttrWorkflowTrigger  = "nb_workflow_trigger"
	SearchAttrTriggeredBy      = "nb_triggered_by"
	SearchAttrParentWorkflowID = "nb_parent_workflow_id"
	// Dynamic Execution Tags
	SearchAttrExecutionTags = "nb_execution_tags"
	// Event Trigger Attributes
	SearchAttrEventType = "nb_event_type"
	SearchAttrEventID   = "nb_event_id"

	// Memo keys for execution-to-version linkage. Stored at ExecuteWorkflow
	// time and read back in GetDetailedWorkflowExecution so the execution
	// detail UI can render the canvas from the version snapshot that actually
	// ran (vs the current draft).
	MemoWorkflowVersionID     = "workflow_version_id"     // UUID string of workflow_versions.id
	MemoWorkflowVersionNumber = "workflow_version_number" // int64
	MemoWorkflowVersionName   = "workflow_version_name"   // string; empty when version is unnamed
	// MemoWorkflowIsDraftRun marks a canvas "Run current" execution. Such runs
	// execute the on-screen draft definition without snapshotting a version row;
	// the exact graph is recovered from Temporal history (the start-event input).
	MemoWorkflowIsDraftRun = "workflow_is_draft_run" // bool; true for draft ("Run current") executions
	// MemoWorkflowAITriggered marks an execution the AI assistant started on a
	// user's behalf, so AI-initiated runs can be told apart from human ones when
	// judging the feature's blast radius.
	//
	// Memo rather than a SearchAttribute on purpose: a new search attribute has
	// to be registered per Temporal namespace before it can be written, which
	// couples the rollout to an admin step in every environment. Memo needs no
	// such registration — the same reason MemoWorkflowIsDraftRun lives here. The
	// trade-off is that Memo is not server-side filterable in visibility
	// queries; it is read back with the execution and counted by the caller.
	MemoWorkflowAITriggered = "workflow_ai_triggered" // bool; true for AI-initiated executions
	// Steps that failed under failure_policy "continue" on a run that still
	// completed, written at the end of the run so list views read it per row.
	MemoFailedStepCount      = "failed_step_count"       // int
	MemoFirstFailedStep      = "first_failed_step"       // string; task id
	MemoFirstFailedStepError = "first_failed_step_error" // string; secret-scrubbed, truncated
	// MemoApprovalGate names the approval a run waits on before its own steps
	// (Workflow.ApprovalGate), set only when the run starts.
	MemoApprovalGate = "nb_approval_gate" // string; the gate's reference
)

// ApprovalGateTaskID is the id of the approval step the engine puts in front of
// a run's own steps when its run gate asks for one (Workflow.ApprovalGate). It
// is never stored: a definition may not use the ReservedTaskIDPrefix.
const ApprovalGateTaskID = "__bee_approval"

// ReservedTaskIDPrefix starts the ids of steps only the engine adds.
const ReservedTaskIDPrefix = "__bee_"

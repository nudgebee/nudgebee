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
)

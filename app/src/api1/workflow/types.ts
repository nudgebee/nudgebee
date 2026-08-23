export interface WorkflowDefinitionInput {
  id: string;
  description?: string;
  type?: string;
  default?: string;
}

export interface WorkflowTaskLayout {
  x: number;
  y: number;
}

export interface WorkflowDefinitionTrigger {
  type: string;
  params?: any;
  layout?: WorkflowTaskLayout;
}

export interface WorkflowDefinitionRetryPolicy {
  initial_interval?: string;
  backoff_coefficient?: number;
  maximum_interval?: string;
  maximum_attempts?: number;
  non_retryable_error_types?: string[];
}

export interface WorkflowDefinitionFailurePolicy {
  action?: string; // e.g., "continue", "fail" (default)
  retry?: WorkflowDefinitionRetryPolicy;
}

export interface WorkflowDefinitionHookAction {
  type: string;
  params?: any;
}

export interface WorkflowDefinitionHook {
  success?: WorkflowDefinitionHookAction[];
  failure?: WorkflowDefinitionHookAction[];
  always?: WorkflowDefinitionHookAction[];
}

export interface WorkflowViewport {
  x: number;
  y: number;
  zoom: number;
}

export interface WorkflowDefinitionLayout {
  viewport?: WorkflowViewport;
}

export interface WorkflowDefinitionTask {
  id: string;
  type: string;
  params?: any;
  tasks?: WorkflowDefinitionTask[];
  set_state?: any;
  set_vars?: any;
  depends_on?: string[];
  if?: string;
  matrix?: any;
  failure_policy?: WorkflowDefinitionFailurePolicy;
  timeout?: string;
  hooks?: WorkflowDefinitionHook;
  disabled?: boolean;
  layout?: WorkflowTaskLayout;
  // Wire shape for the disable stash. New shape: `{ originals, splices? }` so
  // re-enable can undo the splice. Old shape (pre-splice) was a flat array;
  // both are accepted by the reader for backward compat.
  _prev_edges?:
    | Array<{
        source: string;
        target: string;
        sourceHandle?: string;
        targetHandle?: string;
        type?: string;
      }>
    | {
        originals: Array<{
          source: string;
          target: string;
          sourceHandle?: string;
          targetHandle?: string;
          type?: string;
        }>;
        splices?: Array<{
          source: string;
          target: string;
          sourceHandle?: string;
          targetHandle?: string;
          type?: string;
        }>;
      };
}

export interface WorkflowDefinition {
  version?: string;
  inputs?: WorkflowDefinitionInput[];
  triggers?: WorkflowDefinitionTrigger[];
  tasks?: WorkflowDefinitionTask[];
  hooks?: WorkflowDefinitionHook;
  output?: any;
  set_execution_tags?: string[];
  retry_policy?: WorkflowDefinitionRetryPolicy;
  timeout?: string;
  layout?: WorkflowDefinitionLayout;
}

export interface WorkflowRequest {
  definition: WorkflowDefinition;
  tags?: Record<string, any>;
  name: string;
  status?: string;
  created_from_session_id?: string;
}

export interface WorkflowCreateRequest {
  account_id: string;
  workflow: WorkflowRequest;
}

export interface ExecutionListItem {
  id: string;
  status: string;
  close_time?: string;
  start_time?: string;
  parent_workflow_id?: string;
  trigger_type?: string;
  triggered_by?: string;
  workflow_id?: string;
  error?: string;
}

export interface WorkflowExecutionGetRequest {
  account_id: string;
  id: string;
  workflow_id: string;
}

// --- Cross-automation execution dashboard ---

/** Filters shared by the execution list and its aggregate. */
export interface ExecutionDashboardFilterRequest {
  start_date?: string;
  end_date?: string;
  workflow_ids?: string[];
  triggered_by?: string[];
  statuses?: string[];
  trigger_types?: string[];
}

export interface AccountExecutionListRequest extends ExecutionDashboardFilterRequest {
  limit?: number;
  /** 1-based. Ignored when next_page_token is set. */
  page?: number;
  next_page_token?: string;
  include_failure_reason?: boolean;
}

export interface ExecutionAggregateRequest extends ExecutionDashboardFilterRequest {
  top_failed_limit?: number;
}

export interface AccountExecutionItem {
  id: string;
  workflow_id: string;
  workflow_name?: string;
  status: string;
  start_time?: string;
  close_time?: string;
  duration_ms?: number;
  trigger_type?: string;
  /** User id of whoever triggered the run. Empty for scheduled runs. */
  triggered_by?: string;
  user_name?: string;
  failure_reason?: string;
  version_number?: number;
}

export interface AccountExecutionListResponse {
  executions: AccountExecutionItem[];
  next_page_token?: string;
  total_count: number;
  total_is_approximate: boolean;
}

export interface FailedAutomationCount {
  workflow_id: string;
  workflow_name?: string;
  failure_count: number;
}

export interface ExecutionAggregateResponse {
  total: number;
  succeeded: number;
  failed: number;
  running: number;
  counts_are_approximate: boolean;
  top_failed: FailedAutomationCount[];
  top_failed_is_approximate: boolean;
  /** Temporal namespace retention. 0 means unknown — do not clamp the picker. */
  retention_days: number;
}

export interface WorkflowExecutionTaskResponse {
  id: string;
  type: string;
  status: string;
  start_time: string;
  end_time: string;
  input?: Record<string, unknown> | null;
  rendered_params?: Record<string, unknown> | null;
  output: Record<string, unknown> | string | null;
  error: string;
  attempt: number;
  children: WorkflowExecutionTaskResponse[] | null;
}

export interface WorkflowExecutionGetResponse {
  workflow_id: string;
  id: string;
  parent_workflow_id?: string;
  triggered_by: string;
  status: string;
  start_time: string;
  close_time: string;
  inputs: Record<string, unknown> | null;
  workflow_result: Record<string, unknown> | string | null;
  tasks: WorkflowExecutionTaskResponse[];
  error?: string;
}

export interface WorkflowTriggerRequest {
  account_id: string;
  id: string;
  inputs?: any;
  event_id?: string;
  use_draft_definition?: boolean;
}

export interface WorkflowTriggerResponse {
  id: string;
  execution_id: string;
}

export interface WorkflowUpdateRequest {
  account_id: string;
  workflow: WorkflowRequest;
  id: string;
}

export interface WorkflowUpdateResponse {
  status: string;
}

export interface WorkflowOperationResponse {
  data?: any;
  errors?: Array<{
    message: string;
    path?: string[];
  }>;
}

// Dry-run types
export interface WorkflowDryRunRequest {
  account_id: string;
  definition: WorkflowDefinition;
  inputs?: Record<string, any>;
  name?: string;
}

export interface WorkflowDryRunTaskResult {
  id: string;
  type?: string;
  status: string;
  output?: any;
  error?: string;
}

export interface WorkflowDryRunResponse {
  status: string;
  output?: any;
  error?: string;
  dryrun_id?: string;
  execution_id?: string;
  tasks?: WorkflowDryRunTaskResult[];
}

export interface WorkflowRetriggerRequest {
  account_id: string;
  workflow_id: string;
  execution_id: string;
  inputs?: Record<string, any>;
}

export interface WorkflowRetriggerResponse {
  id: string;
  execution_id: string;
}

export interface WorkflowCancelRequest {
  account_id: string;
  id: string;
  execution_id: string;
}

export interface WorkflowCancelResponse {
  message?: string;
}

export interface WorkflowCompleteApprovalRequest {
  account_id: string;
  workflow_id: string;
  execution_id: string;
  task_id: string;
  status: string;
  comments?: string;
}

export interface WorkflowCompleteApprovalResponse {
  message?: string;
}

// Template types
export interface TemplateVariable {
  id: string;
  input_ref?: string;
  display_name?: string;
  help_text?: string;
  placeholder?: string;
  validation?: string;
  required?: boolean;
  group?: string;
  type?: string; // 'string' | 'number' | 'boolean' | 'select'
  options?: string[];
}

export interface WorkflowTemplate {
  id: string;
  tenant_id?: string;
  account_id?: string;
  name: string;
  description?: string;
  category?: string;
  icon?: string;
  definition: WorkflowDefinition;
  template_variables?: TemplateVariable[];
  tags?: Record<string, any>;
  is_system?: boolean;
  status?: string;
  created_by?: string;
  created_by_user?: { id: string; display_name: string };
  updated_by?: string;
  updated_by_user?: { id: string; display_name: string };
  created_at?: string;
  updated_at?: string;
}

export interface WorkflowTemplateListResponse {
  total_count: number;
  templates: WorkflowTemplate[];
  next_page_token?: string;
}

export interface WorkflowValidateRequest {
  account_id: string;
  workflow: WorkflowRequest;
}

import { queryGraphQL } from '@lib/HttpService';
import type {
  WorkflowCreateRequest,
  WorkflowTriggerRequest,
  WorkflowUpdateRequest,
  WorkflowDryRunRequest,
  WorkflowRetriggerRequest,
  WorkflowCancelRequest,
  WorkflowCompleteApprovalRequest,
} from './types';

export const GET_WORKFLOW_BY_ID = `
query GetWorkflowById($accountId:String!, $workflowId:String!) {
  workflow_get(request: {account_id: $accountId, id: $workflowId}) {
    definition {
      output
      timeout
      version
      inputs {
        default
        description
        id
        type
      }
      tasks {
        depends_on
        id
        if
        matrix
        outputs
        params
        timeout
        type
        hooks {
          always {
            params
            type
          }
          failure {
            params
            type
          }
          success {
            params
            type
          }
        }
        failure_policy {
          action
          retry {
            backoff_coefficient
            initial_interval
            maximum_attempts
            maximum_interval
            non_retryable_error_types
          }
        }
        set_state
        set_vars
        disabled
        _prev_edges
        layout {
          x
          y
        }
      }
      retry_policy {
        backoff_coefficient
        initial_interval
        maximum_attempts
        maximum_interval
        non_retryable_error_types
      }
      triggers {
        params
        type
        layout {
          x
          y
        }
      }
      layout {
        viewport {
          x
          y
          zoom
        }
      }
      hooks {
        always {
          params
          type
        }
        failure {
          params
          type
        }
        success {
          params
          type
        }
      }
    }
    account_id
    created_at
    created_by
    id
    last_execution_status
    name
    status
    tags
    tenant_id
    updated_at
    updated_by
    created_from_session_id
  }
}
`;

export const LIST_WORKFLOWS = `
query ListWorkflows($accountId:String!, $status:String, $last_execution_status:String, $type:String, $limit:Int, $next_page_token:String, $name:String, $tags:String, $created_by:String) {
  workflow_list(request: {account_id: $accountId, status: $status, last_execution_status: $last_execution_status, type: $type, limit: $limit, next_page_token: $next_page_token, name: $name, tags: $tags, created_by: $created_by}) {
    next_page_token
    total_count
    workflows {
      account_id
      created_at
      created_by
      created_by_user {
        id
        display_name
      }
      id
      last_execution_status
      name
      status
      tags
      tenant_id
      updated_at
      updated_by
      updated_by_user {
        id
        display_name
      }
      last_execution_time
      definition {
        output
        timeout
        version
        triggers {
          params
          type
        }
        inputs {
          default
          description
          id
          type
        }
        tasks {
          id
          type
        }
      }
    }
  }
}
`;

export const LIST_TASK_DEFINITIONS = `
query ListTaskDefinitions {
  workflow_list_taskdefinitions(params: {}) {
    tasks {
      description
      input_schema
      name
      output_schema
      aliases
    }
  }
}
`;
export const LIST_MCP_TOOLS = `
mutation ListMCPTools($accountId: String!, $params: jsonb!) {
  workflow_list_mcp_tools(account_id: $accountId, params: $params) {
    tools {
      name
      description
    }
  }
}
`;

export const CREATE_WORKFLOW = `
mutation CreateWorkflow($request: WorkflowCreateRequest!) {
  workflow_create(request: $request) {
    id
  }
}
`;

export const UPDATE_WORKFLOW = `
mutation UpdateWorkflow($request: WorkflowUpdateRequest!) {
  workflow_update(request: $request) {
    account_id
    created_at
    created_by
    definition
    id
    status
    name
    tags
    updated_at
    updated_by
  }
}
`;

export const LIST_WORKFLOW_VERSIONS = `
query ListWorkflowVersions($accountId: String!, $workflowId: String!) {
  workflow_list_versions(request: {account_id: $accountId, id: $workflowId}) {
    versions {
      id
      workflow_id
      version_number
      source
      restored_from_version
      name
      description
      is_live
      created_by
      created_by_user {
        id
        display_name
      }
      created_at
    }
  }
}
`;

export const GET_WORKFLOW_VERSION = `
query GetWorkflowVersion($accountId: String!, $workflowId: String!, $versionNumber: Int!) {
  workflow_get_version(request: {account_id: $accountId, id: $workflowId, version_number: $versionNumber}) {
    id
    workflow_id
    version_number
    definition
    source
    restored_from_version
    name
    description
    is_live
    created_by
    created_by_user {
      id
      display_name
    }
    created_at
  }
}
`;

export const RESTORE_WORKFLOW_VERSION = `
mutation RestoreWorkflowVersion($accountId: String!, $workflowId: String!, $versionNumber: Int!) {
  workflow_restore_version(request: {account_id: $accountId, id: $workflowId, version_number: $versionNumber}) {
    account_id
    created_at
    created_by
    definition
    id
    status
    name
    tags
    updated_at
    updated_by
    live_version_id
    live_version_number
    live_version_name
  }
}
`;

export const PUBLISH_WORKFLOW_VERSION = `
mutation PublishWorkflowVersion($accountId: String!, $workflowId: String!, $name: String, $description: String, $setLive: Boolean) {
  workflows_publish_version(request: {account_id: $accountId, id: $workflowId, name: $name, description: $description, set_live: $setLive}) {
    id
    workflow_id
    version_number
    source
    name
    description
    is_live
    created_by
    created_by_user {
      id
      display_name
    }
    created_at
  }
}
`;

export const MAKE_WORKFLOW_VERSION_LIVE = `
mutation MakeWorkflowVersionLive($accountId: String!, $workflowId: String!, $versionNumber: Int!) {
  workflows_make_version_live(request: {account_id: $accountId, id: $workflowId, version_number: $versionNumber}) {
    id
    name
    status
    definition
    live_version_id
    live_version_number
    live_version_name
    updated_at
  }
}
`;

export const UPDATE_WORKFLOW_VERSION_METADATA = `
mutation UpdateWorkflowVersionMetadata($accountId: String!, $workflowId: String!, $versionNumber: Int!, $name: String, $description: String) {
  workflows_update_version_metadata(request: {account_id: $accountId, id: $workflowId, version_number: $versionNumber, name: $name, description: $description}) {
    id
    workflow_id
    version_number
    name
    description
    is_live
  }
}
`;

export const LIST_WORKFLOW_EXECUTIONS = `
query listWorkflowExecutions($accountId:String!, $id:String!, $limit:Int, $next_page_token:String, $status:String, $trigger_type:String) {
  workflow_list_executions(request: {account_id: $accountId, id: $id, limit: $limit, next_page_token: $next_page_token, status: $status, trigger_type: $trigger_type}) {
    next_page_token
    executions {
      close_time
      id
      parent_workflow_id
      start_time
      status
      trigger_type
      triggered_by
      workflow_id
    }
  }
}
`;

export const GET_WORKFLOW_EXECUTION = `
query getWorkflowExecution($request: WorkflowExecutionGetRequest!) {
  workflow_get_execution(request: $request) {
    workflow_id
    id
    parent_workflow_id
    triggered_by
    status
    start_time
    close_time
    inputs
    workflow_result
    tasks {
      id
      type
      status
      start_time
      end_time
      input
      output
      error
      attempt
      children
    }
    error
    definition
    definition_source
    version_id
    version_number
    version_name
  }
}
`;

export const TRIGGER_WORKFLOW = `
mutation triggerWorkflow($request: WorkflowTriggerRequest!) {
  workflow_trigger(request: $request) {
    id
    execution_id
  }
}
`;

export const TRIGGER_TASK = `
mutation triggerTask($account_id: String!, $task_type: String!, $params: jsonb) {
  workflow_trigger_task(account_id: $account_id, task_type: $task_type, params: $params)
}
`;

export const TRIGGER_WORKFLOW_DRYRUN = `
mutation triggerWorkflowDryrun($request: WorkflowDryrunRequest!) {
  workflow_trigger_dryrun(request: $request) {
    status
    output
    error
    dryrun_id
    execution_id
    tasks {
      attempt
      children
      end_time
      error
      id
      output
      start_time
      status
      type
    }
  }
}
`;

export const DELETE_WORKFLOW = `
mutation deleteWorkflow($accountId:String!, $id: String!) {
  workflow_delete(request: {account_id: $accountId, id: $id}){
  message
  }
}
`;

export const RETRIGGER_WORKFLOW_EXECUTION = `
mutation retriggerWorkflowExecution($request: WorkflowRetriggerRequest!) {
  workflow_retrigger_execution(request: $request) {
    id
    execution_id
  }
}
`;

export const CANCEL_WORKFLOW_EXECUTION = `
mutation cancelWorkflowExecution($request: WorkflowCancelRequest!) {
  workflow_cancel_execution(request: $request) {
    message
  }
}
`;

export const COMPLETE_APPROVAL = `
mutation completeWorkflowApproval($request: WorkflowCompleteApprovalRequest!) {
  workflow_complete_approval(request: $request) {
    message
  }
}
`;

export const PAUSE_WORKFLOW = `
mutation pauseWorkflow($accountId:String!, $id: String!) {
  workflow_pause(request: {account_id: $accountId, id: $id}) {
    data
  }
}
`;

export const RESUME_WORKFLOW = `
mutation resumeWorkflow($accountId:String!, $id: String!) {
  workflow_resume(request: {account_id: $accountId, id: $id}) {
    data
  }
}
`;

export const CONFIG_LIST = `
mutation configList($request: ConfigListRequest!) {
  config_list(request: $request) {
    id
    key
    value
    type
    labels
    metadata
    tenant_id
    account_id
    created_at
    updated_at
    created_by
    updated_by
  }
}
`;

export const CONFIG_SAVE = `
mutation configSave($request: ConfigSaveInput!) {
  config_save(request: $request) {
    id
  }
}`;

export const CONFIG_DELETE = `
mutation configDelete($request: ConfigDeleteInput!) {
  config_delete(request: $request) {
    message
  }
}`;

export const AI_GENERATE_WORKFLOW = `
query AiGenerateWorkflow($request: AIGenerateWorkflowRequest!) {
  ai_generate_workflow(request: $request) {
    data {
      response
      query
      chain_name
      conversation_id
      session_id
      message_id
      agent_id
      status
      followup {
        data {
          response
          chain_name
          conversation_id
          message_id
        }
      }
    }
  }
}
`;

export const workflow_get_count = `
query WorkflowCount($request: WorkflowCountRequest!) {
  workflow_get_count(request: $request) {
    count
  }
}
`;

export const workflow_get_execution_count = `
query WorkflowExecutionCount($request: WorkflowExecutionCountRequest!) {
  workflow_get_execution_count(request: $request) {
    count
  }
}
`;

export const LIST_WORKFLOW_TEMPLATES = `
query ListWorkflowTemplates($request: WorkflowListTemplateRequest!) {
  workflow_list_template(request: $request) {
    total_count
    next_page_token
    templates {
      id
      name
      description
      category
      icon
      definition {
        version
        inputs {
          id
          description
          type
          default
        }
        triggers {
          type
          params
        }
        tasks {
          id
          type
          params
          depends_on
          if
          timeout
          hooks {
            success { type params }
            failure { type params }
            always { type params }
          }
          failure_policy {
            action
            retry {
              backoff_coefficient
              initial_interval
              maximum_attempts
              maximum_interval
            }
          }
          set_state
          set_vars
          disabled
          _prev_edges
        }
        hooks {
          success { type params }
          failure { type params }
          always { type params }
        }
        retry_policy {
          backoff_coefficient
          initial_interval
          maximum_attempts
          maximum_interval
        }
        timeout
      }
      template_variables {
        id
        input_ref
        display_name
        help_text
        placeholder
        validation
        required
        group
        type
        options
      }
      tags
      is_system
      status
      created_at
      updated_at
    }
  }
}
`;

const apiWorkflow = {
  async getWorkflowById(accountId: string, workflowId: string) {
    if (accountId === 'demo') return { data: null, errors: null };
    try {
      const query = GET_WORKFLOW_BY_ID;
      const variables = { accountId, workflowId };

      const response = await queryGraphQL(query, 'GetWorkflowById', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      return error;
    }
  },
  async listWorkflows(
    accountId: string,
    status?: string,
    lastExecutionStatus?: string,
    type?: string,
    limit?: number,
    nextPageToken?: string,
    name?: string,
    tags?: string,
    createdBy?: string
  ) {
    if (accountId === 'demo') return { data: null, errors: null };
    try {
      const query = LIST_WORKFLOWS;
      const variables: any = { accountId, status, last_execution_status: lastExecutionStatus, type };

      // Only add limit if provided
      if (limit) {
        variables.limit = limit;
      }

      // Only add next_page_token if provided and valid (non-empty string)
      if (nextPageToken && nextPageToken.trim() !== '') {
        variables.next_page_token = nextPageToken;
      }

      // Only add name if provided and valid (non-empty string)
      if (name && name.trim() !== '') {
        variables.name = name;
      }

      // Only add tags if provided and valid (non-empty string)
      if (tags && tags.trim() !== '') {
        variables.tags = tags;
      }

      // Only add created_by if provided and valid (non-empty string)
      if (createdBy && createdBy.trim() !== '') {
        variables.created_by = createdBy;
      }

      const response = await queryGraphQL(query, 'ListWorkflows', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      return error;
    }
  },
  async listTaskDefinitions() {
    try {
      const query = LIST_TASK_DEFINITIONS;

      const response = await queryGraphQL(query, 'ListTaskDefinitions', {});
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      return error;
    }
  },
  async createWorkflow(request: WorkflowCreateRequest) {
    try {
      const query = CREATE_WORKFLOW;
      const variables = { request };

      const response = await queryGraphQL(query, 'CreateWorkflow', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      return error;
    }
  },
  async updateWorkflow(request: WorkflowUpdateRequest) {
    try {
      const query = UPDATE_WORKFLOW;
      const variables = { request };

      const response = await queryGraphQL(query, 'UpdateWorkflow', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      return error;
    }
  },
  async ListWorkflowExecutions(accountId: string, workflowId: string, limit?: number, nextPageToken?: string, status?: string, triggerType?: string) {
    if (accountId === 'demo') return { data: null, errors: null };
    try {
      const query = LIST_WORKFLOW_EXECUTIONS;
      const variables: any = {
        accountId,
        id: workflowId,
      };

      // Only add limit if provided
      if (limit) {
        variables.limit = limit;
      }

      // Only add next_page_token if provided and valid (non-empty string)
      if (nextPageToken && nextPageToken.trim() !== '') {
        variables.next_page_token = nextPageToken;
      }

      // Only add status if provided and valid (non-empty string)
      if (status && status.trim() !== '') {
        variables.status = status;
      }

      // Only add trigger_type if provided and valid (non-empty string)
      if (triggerType && triggerType.trim() !== '') {
        variables.trigger_type = triggerType;
      }

      const response = await queryGraphQL(query, 'listWorkflowExecutions', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      return error;
    }
  },
  async getWorkflowExecution(accountId: string, workflowId: string, executionId: string) {
    if (accountId === 'demo') return { data: null, errors: null };
    try {
      const query = GET_WORKFLOW_EXECUTION;
      const request = {
        account_id: accountId,
        id: executionId,
        workflow_id: workflowId,
      };

      const response = await queryGraphQL(query, 'getWorkflowExecution', { request });
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      return error;
    }
  },
  async triggerWorkflow(request: WorkflowTriggerRequest) {
    try {
      const query = TRIGGER_WORKFLOW;
      const variables = { request };

      const response = await queryGraphQL(query, 'triggerWorkflow', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to trigger workflow:', error);
      return error;
    }
  },
  async triggerTask(accountId: string, taskType: string, params: any) {
    if (accountId === 'demo') return { data: null, errors: null };
    try {
      const query = TRIGGER_TASK;
      const variables = {
        account_id: accountId,
        task_type: taskType,
        params,
      };

      const response = await queryGraphQL(query, 'triggerTask', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to trigger task:', error);
      return error;
    }
  },
  async triggerWorkflowDryRun(request: WorkflowDryRunRequest) {
    try {
      const query = TRIGGER_WORKFLOW_DRYRUN;
      const variables = { request };

      const response = await queryGraphQL(query, 'triggerWorkflowDryrun', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to dry-run workflow:', error);
      return error;
    }
  },
  async deleteWorkflow(accountId: string, id: string) {
    if (accountId === 'demo') return { data: null, errors: null };
    try {
      const query = DELETE_WORKFLOW;
      const variables = { accountId, id };

      const response = await queryGraphQL(query, 'deleteWorkflow', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to delete workflow:', error);
    }
  },
  async pauseWorkflow(accountId: string, id: string) {
    if (accountId === 'demo') return { data: null, errors: null };
    try {
      const query = PAUSE_WORKFLOW;
      const variables = { accountId, id };

      const response = await queryGraphQL(query, 'pauseWorkflow', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to pause workflow:', error);
    }
  },
  async resumeWorkflow(accountId: string, id: string) {
    if (accountId === 'demo') return { data: null, errors: null };
    try {
      const query = RESUME_WORKFLOW;
      const variables = { accountId, id };

      const response = await queryGraphQL(query, 'resumeWorkflow', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to resume workflow:', error);
      return {
        data: null,
        errors: [error],
      };
    }
  },
  async retriggerExecution(request: WorkflowRetriggerRequest) {
    try {
      const query = RETRIGGER_WORKFLOW_EXECUTION;
      const variables = { request };

      const response = await queryGraphQL(query, 'retriggerWorkflowExecution', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to retrigger workflow execution:', error);
      return {
        data: null,
        errors: [error],
      };
    }
  },
  async cancelExecution(request: WorkflowCancelRequest) {
    try {
      const query = CANCEL_WORKFLOW_EXECUTION;
      const variables = { request };

      const response = await queryGraphQL(query, 'cancelWorkflowExecution', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to cancel workflow execution:', error);
      return {
        data: null,
        errors: [error],
      };
    }
  },
  async completeApproval(request: WorkflowCompleteApprovalRequest) {
    try {
      const query = COMPLETE_APPROVAL;
      const variables = { request };

      const response = await queryGraphQL(query, 'completeWorkflowApproval', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to complete workflow approval:', error);
      return {
        data: null,
        errors: [error],
      };
    }
  },
  // accountId is optional. Pass an empty string (or undefined) to operate on
  // tenant-level configs (shared across all accounts in the tenant). Pass a
  // real account id to operate on the merged tenant+account effective view
  // (account rows override tenant rows on key collision).
  async listConfigs(accountId: string, labels?: any, decrypt?: boolean) {
    if (accountId === 'demo') return { data: null, errors: null };
    try {
      const query = CONFIG_LIST;
      const variables: any = {
        request: {
          labels,
          decrypt,
        },
      };
      if (accountId) {
        variables.request.account_id = accountId;
      }

      const response = await queryGraphQL(query, 'configList', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to list configs:', error);
      return error;
    }
  },
  async saveConfig(accountId: string, config: any) {
    if (accountId === 'demo') return { data: null, errors: null };
    try {
      const query = CONFIG_SAVE;
      const variables: any = {
        request: {
          config,
        },
      };
      if (accountId) {
        variables.request.account_id = accountId;
      }

      const response = await queryGraphQL(query, 'configSave', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to save config:', error);
      return error;
    }
  },
  async deleteConfig(accountId: string, key: string) {
    if (accountId === 'demo') return { data: null, errors: null };
    try {
      const query = CONFIG_DELETE;
      const variables: any = {
        request: {
          key,
        },
      };
      if (accountId) {
        variables.request.account_id = accountId;
      }

      const response = await queryGraphQL(query, 'configDelete', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to delete config:', error);
      return error;
    }
  },
  async aiGenerateWorkflow(
    accountId: string,
    query: string,
    conversationId?: string,
    sessionId?: string,
    config?: { llm_provider?: string; llm_model_name?: string; workflow_id?: string },
    async?: boolean,
    messageId?: string,
    agentId?: string
  ) {
    if (accountId === 'demo') return { data: null, errors: null };
    try {
      const gqlQuery = AI_GENERATE_WORKFLOW;
      const variables: any = {
        request: {
          account_id: accountId,
          query: query,
          conversation_id: conversationId,
          session_id: sessionId,
          async: async ?? false,
          message_id: messageId,
          agent_id: agentId,
        },
      };

      // Add config if provided
      if (config) {
        variables.request.config = config;
      }

      const response = await queryGraphQL(gqlQuery, 'AiGenerateWorkflow', variables);
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to generate workflow with AI:', error);
      return { data: null, errors: [error] };
    }
  },
  async getWorkflowCount(accountId: string, filters?: { status?: string; triggerType?: string }) {
    if (accountId === 'demo') return { data: null, errors: null };
    try {
      const query = workflow_get_count;
      const request: any = {
        account_id: accountId,
      };

      if (filters?.status) {
        request.status = filters.status;
      }

      if (filters?.triggerType) {
        request.trigger_type = filters.triggerType;
      }

      const response = await queryGraphQL(query, 'WorkflowCount', { request });
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to get workflow count:', error);
      return { data: null, errors: [error] };
    }
  },
  async getWorkflowExecutionCount(
    accountId: string,
    filters?: {
      startDate?: Date;
      endDate?: Date;
      status?: string;
      triggerType?: string;
      workflowId?: string;
    }
  ) {
    try {
      if (accountId === 'demo') return { data: null, errors: null };
      const query = workflow_get_execution_count;
      const request: any = {
        account_id: accountId,
      };

      if (filters?.startDate) {
        request.start_date = filters.startDate.toISOString();
      }

      if (filters?.endDate) {
        request.end_date = filters.endDate.toISOString();
      }

      if (filters?.status) {
        request.status = filters.status;
      }

      if (filters?.triggerType) {
        request.trigger_type = filters.triggerType;
      }

      if (filters?.workflowId) {
        request.workflow_id = filters.workflowId;
      }

      const response = await queryGraphQL(query, 'WorkflowExecutionCount', { request });
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to get workflow execution count:', error);
      return { data: null, errors: [error] };
    }
  },
  async listTemplates(
    category?: string,
    name?: string,
    limit?: number,
    nextPageToken?: string,
    eventSources?: string[],
    alertNames?: string[],
    subjectTypes?: string[]
  ) {
    try {
      const request: any = { type: 'system' };
      if (category) request.category = category;
      if (name) request.name = name;
      if (limit) request.limit = limit;
      if (nextPageToken) request.next_page_token = nextPageToken;
      if (eventSources?.length) request.event_sources = eventSources;
      if (alertNames?.length) request.alert_names = alertNames;
      if (subjectTypes?.length) request.subject_types = subjectTypes;

      const response = await queryGraphQL(LIST_WORKFLOW_TEMPLATES, 'ListWorkflowTemplates', { request });
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to list workflow templates:', error);
      return { data: null, errors: [error] };
    }
  },
  async listMCPTools(accountId: string, params: Record<string, any>) {
    try {
      if (accountId === 'demo') return { data: null, errors: null };
      const response = await queryGraphQL(LIST_MCP_TOOLS, 'ListMCPTools', { accountId, params });
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      return { data: null, errors: [error] };
    }
  },
  async listWorkflowVersions(accountId: string, workflowId: string) {
    try {
      const response = await queryGraphQL(LIST_WORKFLOW_VERSIONS, 'ListWorkflowVersions', {
        accountId,
        workflowId,
      });
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to list workflow versions:', error);
      return { data: null, errors: [error] };
    }
  },
  async getWorkflowVersion(accountId: string, workflowId: string, versionNumber: number) {
    try {
      const response = await queryGraphQL(GET_WORKFLOW_VERSION, 'GetWorkflowVersion', {
        accountId,
        workflowId,
        versionNumber,
      });
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to get workflow version:', error);
      return { data: null, errors: [error] };
    }
  },
  async restoreWorkflowVersion(accountId: string, workflowId: string, versionNumber: number) {
    try {
      const response = await queryGraphQL(RESTORE_WORKFLOW_VERSION, 'RestoreWorkflowVersion', {
        accountId,
        workflowId,
        versionNumber,
      });
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to restore workflow version:', error);
      return { data: null, errors: [error] };
    }
  },
  async publishWorkflowVersion(
    accountId: string,
    workflowId: string,
    options?: { name?: string | null; description?: string | null; setLive?: boolean }
  ) {
    try {
      const response = await queryGraphQL(PUBLISH_WORKFLOW_VERSION, 'PublishWorkflowVersion', {
        accountId,
        workflowId,
        name: options?.name ?? null,
        description: options?.description ?? null,
        setLive: options?.setLive ?? true,
      });
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to publish workflow version:', error);
      return { data: null, errors: [error] };
    }
  },
  async makeWorkflowVersionLive(accountId: string, workflowId: string, versionNumber: number) {
    try {
      const response = await queryGraphQL(MAKE_WORKFLOW_VERSION_LIVE, 'MakeWorkflowVersionLive', {
        accountId,
        workflowId,
        versionNumber,
      });
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to make workflow version live:', error);
      return { data: null, errors: [error] };
    }
  },
  async updateWorkflowVersionMetadata(
    accountId: string,
    workflowId: string,
    versionNumber: number,
    updates: { name?: string | null; description?: string | null }
  ) {
    try {
      const response = await queryGraphQL(UPDATE_WORKFLOW_VERSION_METADATA, 'UpdateWorkflowVersionMetadata', {
        accountId,
        workflowId,
        versionNumber,
        name: updates.name ?? null,
        description: updates.description ?? null,
      });
      return {
        data: response?.data?.data,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('Failed to update workflow version metadata:', error);
      return { data: null, errors: [error] };
    }
  },
};

export default apiWorkflow;

// Shared TypeScript interfaces for the ask-nudgebee API module

export interface ModelConfig {
  llm_provider: string;
  llm_model_name: string;
}

export interface ConversationAttachment {
  id: string;
  mime_type: string;
  size_bytes: number;
  description: string;
  created_at: string;
  data: string;
}

// V3 flat payload shapes returned by ai_get_conversation_v3

export interface ConversationV3Shell {
  id: string;
  session_id: string;
  account_id: string;
  tenant_id: string;
  user_id: string;
  user_display_name: string | null;
  created_at: string;
  updated_at: string;
  source: string;
  context: string | null;
  status: string;
  title: string | null;
}

export interface ConversationV3Message {
  id: string;
  user_id: string;
  user_display_name: string | null;
  created_at: string;
  updated_at: string;
  message: string;
  message_type: string;
  response: string | null;
  role: string;
  status: string;
  parent_agent_id: string | null;
  message_config: string | null;
  ack_message: string | null;
  attachments: ConversationAttachment[];
}

export interface ConversationV3Agent {
  id: string;
  message_id: string;
  agent_name: string;
  response: string | null;
  response_summary: string | null;
  query: string | null;
  thought: string | null;
  parent_agent_id: string | null;
  status: string;
  references: string | null;
  created_at: string;
  updated_at: string;
}

export interface ConversationV3ToolCall {
  id: string;
  agent_id: string;
  tool_name: string;
  parameters: string | null;
  response: string | null;
  thought: string | null;
  tool_type: string;
  child_agent_id: string | null;
  references: string | null;
  tool_id: string | null;
  status: string;
  created_at: string;
  updated_at: string;
}

// Request interfaces

export interface GenerateQueryRequest {
  account_id: string;
  query: string;
}

export interface InvestigateRequest {
  account_id: string;
  query: string;
  session_id?: string;
  conversation_id?: string;
  agent_id?: string;
  message_id?: string;
  config?: ModelConfig;
  source?: string;
  images?: string[];
}

export interface FollowupRequest {
  account_id: string;
  query: string;
  conversation_id: string;
  agent_id: string;
  message_id: string;
}

export type ConversationActiveFilter = 'All' | 'Mine' | 'Saved' | 'Waiting';

export interface ConversationHistoryRequest {
  account_id: string;
  skipTotalCount?: boolean;
  latestLastRecordedAt?: string;
  source?: string | string[];
  activeFilter?: ConversationActiveFilter;
  status?: string;
  user_username?: string;
  user_username_neq?: string;
  source_not_in?: string[];
  searchText?: string;
  limit?: number;
  offset?: number;
}

export interface ConversationHistoryForInvestigationRequest {
  account_id: string | string[];
  latestLastRecordedAt?: string;
  startUpdatedAt?: string;
  endUpdatedAt?: string;
  status?: string;
  user_id?: string;
  source?: string | string[];
  session_id?: string;
  title?: string;
  extractEventIdsFromTitle?: boolean;
  event_status?: string;
  limit?: number;
  offset?: number;
}

export interface ConversationComparisonRequest {
  startDate: string;
  endDate: string;
  previousStartDate: string;
  previousEndDate: string;
  source?: string | string[];
  extractEventIdsFromTitle?: boolean;
}

export interface FeedbackForSessionRequest {
  account_id: string;
  session_id: string | string[];
}

export interface AiFeedbackCreateRequest {
  session_id?: string;
  conversation_id?: string;
  module?: string;
  question?: string;
  llm_response?: string;
  user_corrected_response?: string;
  useful?: boolean;
  additional_notes?: string;
  cloud_account_id?: string;
}

export interface ListAiFeedbackRequest {
  module?: string;
  useful?: boolean;
  question?: string;
  user_id?: string;
  cloud_account_id?: string;
  start_time?: string | number;
  end_time?: string | number;
}

export interface SaveConversationRequest {
  conversation_id: string;
  account_id?: string;
}

export interface DeleteConversationRequest {
  conversation_id: string;
  account_id?: string;
}

export interface DeleteSavedConversationRequest {
  conversation_id: string;
  account_id?: string;
}

export interface ListAgentsRequest {
  accountId: string | number;
}

export interface ListToolsRequest {
  accountId: string | number;
}

export interface ListFunctionsRequest {
  accountId: string | number;
}

// Agent/tool definitions are open-ended objects; only account_id is required.
// Remaining fields are passed through to the backend verbatim via gqlStringify.

export interface AgentDefinition {
  account_id: string;
  [key: string]: unknown;
}

export interface ToolDefinition {
  account_id: string;
  [key: string]: unknown;
}

export interface ConversationSuggestionsRequest {
  account_id: string;
  [key: string]: unknown;
}

export interface CreateRagDataRequest {
  account_id: string;
  [key: string]: unknown;
}

export interface CreateAgentExtensionRequest {
  account_id: string;
  agent_id?: string;
  additional_instructions?: string;
  config?: Record<string, unknown>;
  tools?: string[];
  [key: string]: unknown;
}

export interface UpdateAgentExtensionRequest {
  account_id: string;
  agent_id?: string;
  additional_instructions?: string;
  config?: Record<string, unknown>;
  tools?: string[];
  [key: string]: unknown;
}

export interface GetWorkspaceFileRequest {
  account_id: string;
  conversation_id?: string;
  path?: string;
  download?: boolean;
}

export interface AiFunctionData {
  name: string;
  description?: string;
  prompt?: string;
  variables?: string[];
  variable_defaults?: Record<string, string>;
  status?: string;
  version?: string;
}

export interface UpdateFunctionData {
  name?: string;
  description?: string;
  prompt?: string;
  variables?: string[];
  variable_defaults?: Record<string, string>;
  status?: string;
  version?: string;
}

export interface StopInvestigateRequest {
  accountId: string;
  conversationId: string;
}

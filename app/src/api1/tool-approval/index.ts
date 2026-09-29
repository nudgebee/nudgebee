import { queryGraphQL } from '@lib/HttpService';

export type ToolApprovalMatchKind = 'tool' | 'exact_command' | 'command_regex' | 'arguments';
export type ToolApprovalOperator = 'equals' | 'regex';

export interface ToolApprovalCondition {
  path: string;
  operator: ToolApprovalOperator;
  value: unknown;
}

export interface ToolApprovalRule {
  id?: string;
  /** null for an all-accounts rule, which covers every account including ones added later. */
  account_id: string | null;
  /** Must be true when account_id is null; the server never reads a missing account as "all". */
  all_accounts?: boolean;
  /** Also waive confirmation in runs with no signed-in person (system jobs, token-only API calls). Off by default. */
  allow_unattended?: boolean;
  name: string;
  enabled: boolean;
  tool_name: string;
  tool_config_id?: string | null;
  match_kind: ToolApprovalMatchKind;
  command_pattern?: string | null;
  working_dir?: string | null;
  conditions: ToolApprovalCondition[];
  revision?: number;
  created_at?: string;
  updated_at?: string;
}

export interface ToolApprovalConfig {
  enabled: boolean;
  rules: ToolApprovalRule[];
}

export interface ToolApprovalAccountOptions {
  account_id: string;
  configuration_required: boolean;
  configuration_pending: boolean;
  configurations: Array<{ id: string; name: string }>;
  error?: string;
}

export type ToolApprovalTargetKind = 'account' | 'integration' | 'command' | 'none';

export interface ToolApprovalToolField {
  name: string;
  type: string;
  description?: string;
  enum?: unknown[];
  /** The field carries a CLI command; conditions on it check each command like a command rule. */
  command?: boolean;
}

export interface ToolApprovalToolOption {
  name: string;
  configuration_mode: 'none' | 'required' | 'command';
  /** What the tool's calls run against, which decides how a rule is scoped. */
  target_kind: ToolApprovalTargetKind;
  /** Input fields an argument condition can test (command-like fields excluded). */
  fields: ToolApprovalToolField[];
}

export interface ToolApprovalIntegration {
  id: string;
  name: string;
}

function unwrap<T>(response: any, action: string): T {
  const gqlErrors = response?.data?.errors;
  if (Array.isArray(gqlErrors) && gqlErrors.length > 0) throw new Error(gqlErrors[0]?.message || 'Tool approval request failed');
  const envelope = response?.data?.data?.[action];
  const backendErrors = envelope?.errors;
  if (Array.isArray(backendErrors) && backendErrors.length > 0) throw new Error(backendErrors[0]?.message || 'Tool approval request failed');
  if (envelope?.data === undefined || envelope?.data === null) throw new Error('Tool approval request failed');
  return envelope.data as T;
}

async function call<T>(action: string, operation: string, request: unknown, signal?: AbortSignal): Promise<T> {
  const query = `mutation ${operation}($request: json!) { ${action}(request: $request) { data } }`;
  const response = await queryGraphQL(query, operation, { request }, undefined, signal);
  return unwrap<T>(response, action);
}

export const listToolApprovalRules = (signal?: AbortSignal) => call<ToolApprovalConfig>('toolapproval_list', 'ListToolApprovalRules', {}, signal);

export const upsertToolApprovalRule = (rule: ToolApprovalRule, signal?: AbortSignal) =>
  call<ToolApprovalRule>('toolapproval_upsert', 'UpsertToolApprovalRule', rule, signal);

/** Accounts for selected-account rules; integrations pin an organisation rule, one rule each. */
export const createToolApprovalRules = (
  rule: ToolApprovalRule,
  accounts: Array<{ account_id: string; tool_config_id: string | null }>,
  integrations: string[] = [],
  signal?: AbortSignal
) => call<{ rules: ToolApprovalRule[] }>('toolapproval_create', 'CreateToolApprovalRules', { rule, accounts, integrations }, signal);

/** The tenant's integrations an organisation rule for this tool can be pinned to. */
export const listToolApprovalIntegrations = (toolName: string, signal?: AbortSignal) =>
  call<{ target_kind: ToolApprovalTargetKind; integrations: ToolApprovalIntegration[] }>(
    'toolapproval_list_options',
    'ListToolApprovalIntegrations',
    { tool_name: toolName, organisation: true },
    signal
  );

export const deleteToolApprovalRule = (id: string, revision: number, signal?: AbortSignal) =>
  call<{ deleted: boolean }>('toolapproval_delete', 'DeleteToolApprovalRule', { id, revision }, signal);

export const checkToolApprovalMatch = (rule: ToolApprovalRule, input: string, signal?: AbortSignal) =>
  call<{ matched: boolean; reason: string }>('toolapproval_check_match', 'CheckToolApprovalMatch', { rule, input }, signal);

export const listToolApprovalOptions = (
  accountIDs: string[],
  toolName: string,
  matchKind: ToolApprovalMatchKind,
  commandPattern: string,
  signal?: AbortSignal
) =>
  call<{ accounts: ToolApprovalAccountOptions[] }>(
    'toolapproval_list_options',
    'ListToolApprovalOptions',
    { account_ids: accountIDs, tool_name: toolName, match_kind: matchKind, command_pattern: commandPattern },
    signal
  );

/** Tools a rule can name, across the organisation; independent of the accounts chosen. */
export const listToolApprovalTools = (signal?: AbortSignal) =>
  call<{ tools: ToolApprovalToolOption[] }>('toolapproval_list_tools', 'ListToolApprovalTools', {}, signal);

export async function listToolApprovalAccounts(signal?: AbortSignal): Promise<Array<{ id: string; account_name: string; cloud_provider: string }>> {
  const query = `query ListToolApprovalAccounts { accounts_list(where: {status: {_eq: "active"}}, order_by: [{column: "account_name", order: asc}]) { rows { id account_name cloud_provider } } }`;
  const response = await queryGraphQL(query, 'ListToolApprovalAccounts', {}, undefined, signal);
  return response?.data?.data?.accounts_list?.rows || [];
}

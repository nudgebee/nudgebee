export const EventListing: string[] = [
  'ACCOUNT_CREATE',
  'ACCOUNT_UPDATE',
  'ACCOUNT_DELETE',
  'ALERT_MANAGER_CREATE',
  'ALERT_MANAGER_DISABLE',
  'AUTOPILOT_CREATE',
  'AUTOPILOT_UPDATE',
  'AUTOPILOT_DELETE',
  'AUTO_PILOT.TASK',
  // AUTOMATION_* — workflow audit types emitted by runbook-server (category
  // AUTOMATION). The AUTORUNBOOK_* entries below remain for historical rows.
  'AUTOMATION_CREATE',
  'AUTOMATION_UPDATE',
  'AUTOMATION_DELETE',
  'AUTOMATION_MANUAL_RUN',
  'AUTOMATION_STATUS_UPDATE',
  'AUTOMATION_PAUSE',
  'AUTOMATION_RESUME',
  'AUTOMATION_EXECUTION_CANCEL',
  'AUTOMATION_TASK_MANUAL_RUN',
  'AUTOMATION_VERSION_RESTORE',
  'AUTOMATION_VERSION_PUBLISH',
  'AUTOMATION_VERSION_SET_LIVE',
  'AUTOMATION_VERSION_UPDATE',
  'AUTOMATION_VERSION_STATUS_UPDATE',
  'AUTOMATION_VERSION_DELETE',
  'AUTORUNBOOK_CREATE',
  'AUTORUNBOOK_UPDATE',
  'AUTORUNBOOK_DELETE',
  'AUTORUNBOOK_ACTION_UPDATE',
  'AUTORUNBOOK_ACTION_CREATE',
  'AUTORUNBOOK_ACTION_PUBLISH',
  'AUTORUNBOOK_MANUAL_RUN',
  'AUTORUNBOOK_SKIP',
  'AUTORUNBOOK_TASK_MANUAL_RUN',
  'GROUP_CREATE',
  'GROUP_UPDATE',
  'GROUP_DELETE',
  'GROUP_USER_CREATE',
  'GROUP_USER_UPDATE',
  'GROUP_USER_DELETE',
  'K8SRELAY_TASK_CREATE',
  'K8SRELAY_AGENT_CONNECTED',
  'K8SRELAY_AGENT_DISCONNECTED',
  'K8SAGENT_TASK_CREATE',
  'MESSAGING_PLATFORM_CREATE',
  'MESSAGING_PLATFORM_UPDATE',
  'MESSAGING_PLATFORM_DELETE',
  'NOTIFICATION_MS_TEAMS_CHANNEL_CREATE',
  'NOTIFICATION_MS_TEAMS_CHANNEL_UPDATE',
  'NOTIFICATION_MS_TEAMS_CHANNEL_DELETE',
  'NOTIFICATION_MS_TEAMS_CONFIGURATION_CREATE',
  'NOTIFICATION_MS_TEAMS_CONFIGURATION_DELETE',
  'NOTIFICATION_MS_TEAMS_CONFIGURATION_UPDATE',
  'NOTIFICATION_SLACK_CHANNEL_CREATE',
  'NOTIFICATION_SLACK_CHANNEL_UPDATE',
  'NOTIFICATION_SLACK_CHANNEL_DELETE',
  'NOTIFICATION_SLACK_CONFIGURATION_CREATE',
  'NOTIFICATION_SLACK_CONFIGURATION_DELETE',
  'NOTIFICATION_SLACK_CONFIGURATION_UPDATE',
  'NOTIFICATION_GOOGLE_CHAT_CONFIGURATION_CREATE',
  'NOTIFICATION_GOOGLE_CHAT_CONFIGURATION_UPDATE',
  'NOTIFICATION_GOOGLE_CHAT_CONFIGURATION_DELETE',
  'NOTIFICATION_GOOGLE_CHAT_CHANNEL_CREATE',
  'NOTIFICATION_GOOGLE_CHAT_CHANNEL_UPDATE',
  'NOTIFICATION_GOOGLE_CHAT_CHANNEL_DELETE',
  'RECOMMENDATION_APPLY',
  'RECOMMENDATION_JOB_CREATE',
  'ROLE_ACCOUNT_CREATE',
  'ROLE_ACCOUNT_DELETE',
  'ROLE_ACCOUNT_UPDATE',
  'ROLE_GROUP_CREATE',
  'ROLE_GROUP_UPDATE',
  'ROLE_GROUP_DELETE',
  'ROLE_USER_CREATE',
  'ROLE_USER_UPDATE',
  'ROLE_USER_DELETE',
  'SLACK_COMMAND',
  'SLACK_EVENT',
  'SLO_CREATE',
  'SLO_UPDATE',
  'SLO_DELETE',
  'TENANT_CREATE',
  'TENANT_UPDATE',
  'TENANT_DELETE',
  'TENANT_USER_CREATE',
  'TENANT_USER_UPDATE',
  'TENANT_USER_DELETE',
  'TICKET_CONFIGURATION_CREATE',
  'TICKET_CONFIGURATION_DELETE',
  'TICKET_CONFIGURATION_UPDATE',
  'TICKET_CREATE',
  'TICKET_UPDATE',
  'TICKET_DELETE',
  'USER_AUTH_CREATE',
  'USER_AUTH_DELETE',
  'USER_AUTH_LOGIN',
  'USER_CREATE',
  'USER_UPDATE',
  'USER_DELETE',
  'EVENT_RESOLVE',
  'EVENT_UPDATE',
  'TRIAGE_CLASSIFY',
  'TRIAGE_RULE_CREATE',
  'TRIAGE_RULE_UPDATE',
  'TRIAGE_RULE_DELETE',
  'TRIAGE_RULE_ENABLE',
  'TRIAGE_RULE_DISABLE',
  'TRIAGE_STATUS_UPDATE',
  'NOTIFICATION_RULE_CREATE',
  'NOTIFICATION_RULE_UPDATE',
  'NOTIFICATION_RULE_DELETE',
  'NOTIFICATIONS_SLACK_COMMAND',
  'NOTIFICATIONS_SLACK_EVENT',
  'NOTIFICATIONS_SLACK_INTERACTION',
  'UPDATE_AGENT_TOKEN',
  'INTEGRATION_CREATE',
  'INTEGRATION_UPDATE',
  'INTEGRATION_DELETE',
  'CUSTOM_AGENT_CREATE',
  'CUSTOM_AGENT_UPDATE',
  'CUSTOM_AGENT_DELETE',
  'CUSTOM_TOOL_CREATE',
  'CUSTOM_TOOL_DELETE',
  'CUSTOM_TOOL_UPDATE',
  'CHAT_CLARIFICATION_RESPOND',
  'CHAT_TOOL_CONFIRMATION_RESPOND',
  'CHAT_CONVERSATION_DELETE',
  'CLI_EXECUTE',
];

export const CategoryListing: string[] = [
  'ACCOUNTS',
  'ALERT_MANAGER',
  'AUTO_PILOT',
  'AUTOMATION',
  // Deprecated: kept so historical rows tagged 'AUTO_RUNBOOK' remain filterable.
  // New automation audits use 'AUTOMATION' (renders as "Automation").
  'AUTO_RUNBOOK',
  'CHAT_ACTIONS',
  'GROUPS',
  'K8S_RELAY',
  'K8S_AGENT',
  'NOTIFICATIONS',
  'RECOMMENDATIONS',
  'ROLES',
  'TENANTS',
  'TICKETS',
  'USERS',
  'EVENTS',
  'TRIAGE',
  'NOTIFICATION_RULES',
  'NOTIFICATIONS_CHAT_ACTIONS',
  'AGENT_TOKEN',
  'INTEGRATIONS',
  // Knowledge Graph coverage filter + manual dependency declarations
  // (api-server audit.EventCategoryKnowledgeGraph).
  'KNOWLEDGE_GRAPH',
  'AGENT',
  'TOOL',
  'CLOUD_COMMAND',
];

// ---- Audit Log deep links ---------------------------------------------------
// A screen that just performed an audited action links to Admin > Audit Log with
// the filter bar pre-filled, so the user lands on that entry instead of the full
// list. buildAuditLogUrl writes the query params; AuditsTable reads them back
// once on mount through parseAuditLogParams. No params = the default view.
// The names are prefixed so no other Admin tab mistakes them for its own filters.

const AUDIT_LOG_PATH = '/user-management';
const AUDIT_LOG_HASH = '#access-users/audit-log';

const LINK_PARAM = {
  account: 'auditAccount',
  category: 'auditCategory',
  target: 'auditTarget',
  start: 'auditStart',
  end: 'auditEnd',
} as const;

// Half-width of the time window placed around `at`: wide enough to absorb the gap
// between the caller's timestamp and the audit row's event_time, tight enough to
// isolate the one action.
const AUDIT_LINK_WINDOW_MS = 5 * 60 * 1000;

export interface AuditLogLinkFilters {
  accountId?: string;
  /** A CategoryListing value, e.g. 'ALERT_MANAGER'. */
  category?: string;
  /** The audit row's event_target, e.g. an alert rule key. */
  target?: string;
  /** When the audited action happened. Omit to keep the page's default range. */
  at?: number | string | Date;
}

export interface AuditLogLinkParams {
  accountId: string;
  category: string;
  target: string;
  startDate?: number;
  endDate?: number;
}

export const buildAuditLogUrl = (filters: AuditLogLinkFilters = {}): string => {
  const params = new URLSearchParams();
  if (filters.accountId) params.set(LINK_PARAM.account, filters.accountId);
  if (filters.category) params.set(LINK_PARAM.category, filters.category);
  if (filters.target) params.set(LINK_PARAM.target, filters.target);
  const at = filters.at == null ? NaN : new Date(filters.at).getTime();
  if (Number.isFinite(at)) {
    params.set(LINK_PARAM.start, String(at - AUDIT_LINK_WINDOW_MS));
    params.set(LINK_PARAM.end, String(at + AUDIT_LINK_WINDOW_MS));
  }
  const query = params.toString();
  return `${AUDIT_LOG_PATH}${query ? `?${query}` : ''}${AUDIT_LOG_HASH}`;
};

export const parseAuditLogParams = (search: string): AuditLogLinkParams => {
  const params = new URLSearchParams(search);
  const category = params.get(LINK_PARAM.category) || '';
  const startDate = Number(params.get(LINK_PARAM.start));
  const endDate = Number(params.get(LINK_PARAM.end));
  // Both ends or neither, and only a range Date can represent — the table calls toISOString() on it.
  const hasRange = startDate > 0 && endDate > startDate && !Number.isNaN(new Date(endDate).getTime());
  return {
    accountId: params.get(LINK_PARAM.account) || '',
    // An unknown category has no option in the filter bar, so it would filter invisibly.
    category: CategoryListing.includes(category) ? category : '',
    target: params.get(LINK_PARAM.target) || '',
    ...(hasRange ? { startDate, endDate } : {}),
  };
};

/**
 * Single source of truth for how each ticketing tool names what we internally
 * call `project_key`. GitHub issues belong to a repo, PagerDuty/ZenDuty to a
 * service, ServiceNow to a table — so the UI mirrors each platform's own term
 * instead of the generic "Project Key".
 *
 * Two surfaces consume this: the create-ticket modal (TicketFormSection) and the
 * workflow Create/Update Ticket action config (ActionDetailsSidebar). Keeping the
 * mapping here stops the two from drifting apart.
 */
type TicketProjectTerm = {
  /** Noun for a field label, e.g. "Repository". */
  noun: string;
  /** Branded "Select X" phrase used by the create-ticket modal. */
  selectLabel: string;
};

const TICKET_PROJECT_TERMS: Record<string, TicketProjectTerm> = {
  jira: { noun: 'Project', selectLabel: 'Select Jira Project' },
  github: { noun: 'Repository', selectLabel: 'Select Github Repository' },
  gitlab: { noun: 'Project', selectLabel: 'Select GitLab Project' },
  servicenow: { noun: 'Table', selectLabel: 'Select Table' },
  pagerduty: { noun: 'Service', selectLabel: 'Select PagerDuty Service' },
  zenduty: { noun: 'Service', selectLabel: 'Select ZenDuty Service' },
};

/**
 * Field-label noun for the `project_key` field (e.g. GitHub → "Repository").
 * Falls back to the generic "Project Key" when the tool is unknown/unresolved
 * (e.g. before an integration has been selected).
 */
export function getTicketProjectFieldLabel(tool?: string | null): string {
  const toolStr = typeof tool === 'string' ? tool : '';
  return TICKET_PROJECT_TERMS[toolStr.toLowerCase()]?.noun ?? 'Project Key';
}

/** Branded "Select X" label for the create-ticket modal's project field. */
export function getTicketProjectSelectLabel(tool?: string | null): string {
  const toolStr = typeof tool === 'string' ? tool : '';
  return TICKET_PROJECT_TERMS[toolStr.toLowerCase()]?.selectLabel ?? 'Select Project';
}

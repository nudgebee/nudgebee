import { getCurrentTenant } from '@lib/auth';
import { clearPersistedFilters } from '@hooks/usePersistedFilters';

// Filters the user picked on the Automations listing, remembered across visits.
// The sidebar links back to a bare /automation, so without this every return
// trip resets the view. Same mechanism the troubleshoot Events table uses.
const WORKFLOW_FILTER_STORAGE_KEY = 'automation:workflows:filters:v1';

export interface PersistedWorkflowFilters {
  // Index signature so this satisfies the helpers' Record<string, unknown>.
  [key: string]: unknown;
  status?: string;
  last_execution_status?: string;
  type?: string;
  name?: string;
  tags?: string;
  created_by?: string;
}

// Scoped by tenant: a created-by filter from one tenant is meaningless after
// switching to another. No tenant yet ⇒ null key ⇒ every read/write is a no-op.
export const workflowFilterKey = (): string | null => {
  const tenantId = getCurrentTenant()?.id;
  return tenantId ? `${WORKFLOW_FILTER_STORAGE_KEY}:${tenantId}` : null;
};

/**
 * Forget the saved listing filters. For links that promise an exact result set
 * (the Home Automations card counts): the listing falls back to a saved filter
 * for every param the URL doesn't carry, and "no trigger type" or "no name
 * search" can't be carried, so a leftover filter would narrow the list below
 * the count that was clicked.
 */
export const clearSavedWorkflowFilters = (): void => clearPersistedFilters(workflowFilterKey());

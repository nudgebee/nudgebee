// Pure helpers for the automation list's three-dot menu, kept separate from the
// (large) WorkflowListing component so the state-aware Activate/Pause decision
// can be unit-tested in isolation.

export type AutomationToggleAction = 'pause' | 'activate' | null;

/**
 * Decides which state toggle the three-dot menu should offer for an automation:
 * an Active automation can be Paused, a Paused one can be Activated, and any
 * other state (e.g. INACTIVE, or unknown) offers neither. This mirrors the
 * Active/Paused vocabulary used on the automation detail view.
 */
export const getAutomationToggleAction = (status?: string): AutomationToggleAction => {
  if (status === 'ACTIVE') return 'pause';
  if (status === 'PAUSED') return 'activate';
  return null;
};

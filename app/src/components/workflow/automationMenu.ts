// Pure helpers for the automation list's three-dot menu, kept separate from the
// (large) WorkflowListing component so the state-aware Activate/Pause decision
// can be unit-tested in isolation.

export type AutomationToggleAction = 'pause' | 'activate' | null;

/**
 * Decides which state toggle the three-dot menu should offer for an automation:
 * an Active automation can be Paused, a Paused one can be Activated, and any
 * other state (e.g. INACTIVE, or unknown) offers neither. This mirrors the
 * Active/Paused vocabulary used on the automation detail view.
 *
 * Status is the only input. The listing used to additionally require a
 * schedule/event/webhook trigger, which hid the toggle for every manual
 * automation even though the builder publishes those as PAUSED by default and
 * the row's Status column says so (#32191). PauseWorkflow/ResumeWorkflow have no
 * trigger precondition either — they no-op over the (empty) Temporal schedule
 * list and just write the status — so don't reintroduce a trigger-type gate.
 */
export const getAutomationToggleAction = (status?: string): AutomationToggleAction => {
  if (status === 'ACTIVE') return 'pause';
  if (status === 'PAUSED') return 'activate';
  return null;
};

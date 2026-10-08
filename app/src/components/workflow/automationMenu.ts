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

/** The saved automation as the list returns it — the parts the checks below read. */
export interface SavedAutomationSummary {
  id?: string;
  name?: string;
  account_id?: string;
  status?: string;
  definition?: { triggers?: { type?: string }[] | null } | null;
}

export interface AiBuildActions {
  runNow: boolean;
  activate: boolean;
}

/**
 * Decides what the Generate-with-AI success screen offers for the automation a
 * build has just saved.
 *
 * Run now: the automation can be run by hand (Active or Paused — an Inactive one
 * is refused by the engine) and a bare manual run means something for it, which
 * it does for a manual or scheduled automation. An event or webhook automation
 * expects a payload a manual run does not carry, so its first run would fail for
 * a reason that says nothing about the automation.
 *
 * Activate: the automation is Paused and has a trigger that fires on its own —
 * a new automation is saved Paused, and such a trigger then never fires until
 * someone activates it. This is narrower than the row menu on purpose (see
 * getAutomationToggleAction): the menu is a general status toggle, this is a
 * prompt for the one case where leaving the automation paused defeats it.
 */
export const getAiBuildActions = (saved?: SavedAutomationSummary | null): AiBuildActions => {
  if (!saved) {
    return { runNow: false, activate: false };
  }
  const triggerTypes = (saved.definition?.triggers ?? []).map((trigger) => trigger?.type).filter(Boolean);
  const runnableByHand = triggerTypes.length === 0 || triggerTypes.some((type) => type === 'manual' || type === 'schedule');
  const firesOnItsOwn = triggerTypes.some((type) => type !== 'manual');
  const canRun = saved.status === 'ACTIVE' || saved.status === 'PAUSED';
  return {
    runNow: canRun && runnableByHand,
    activate: getAutomationToggleAction(saved.status) === 'activate' && firesOnItsOwn,
  };
};

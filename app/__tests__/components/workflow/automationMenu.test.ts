import { getAiBuildActions, getAutomationToggleAction } from '@components/workflow/automationMenu';

describe('getAutomationToggleAction', () => {
  it('offers Pause for an Active automation', () => {
    expect(getAutomationToggleAction('ACTIVE')).toBe('pause');
  });

  it('offers Activate for a Paused automation', () => {
    expect(getAutomationToggleAction('PAUSED')).toBe('activate');
  });

  it('offers neither for INACTIVE, unknown, or missing status', () => {
    expect(getAutomationToggleAction('INACTIVE')).toBeNull();
    expect(getAutomationToggleAction('SOMETHING_ELSE')).toBeNull();
    expect(getAutomationToggleAction(undefined)).toBeNull();
  });

  // Regression guard for #32191: the listing menu decided on status AND trigger
  // type, so a manual-only automation never got a toggle. Status is the only
  // input the decision is allowed to take.
  it('takes status as its only input', () => {
    expect(getAutomationToggleAction.length).toBe(1);
  });
});

// After an AI build the success screen offers Run now and Activate. Most AI-built automations
// were never run and almost none with a schedule was ever switched on, because the screen offered
// neither and did not say the automation was saved paused.
describe('getAiBuildActions', () => {
  const saved = (status: string, ...types: string[]) => ({ status, definition: { triggers: types.map((type) => ({ type })) } });

  it('offers Run now for a manual automation, and no Activate — nothing would start firing', () => {
    expect(getAiBuildActions(saved('PAUSED', 'manual'))).toEqual({ runNow: true, activate: false });
    expect(getAiBuildActions(saved('PAUSED'))).toEqual({ runNow: true, activate: false });
  });

  it('offers both for a paused scheduled automation', () => {
    expect(getAiBuildActions(saved('PAUSED', 'schedule'))).toEqual({ runNow: true, activate: true });
    expect(getAiBuildActions(saved('PAUSED', 'manual', 'schedule'))).toEqual({ runNow: true, activate: true });
  });

  it('offers only Activate for an event or webhook automation — a bare manual run has no payload', () => {
    expect(getAiBuildActions(saved('PAUSED', 'event'))).toEqual({ runNow: false, activate: true });
    expect(getAiBuildActions(saved('PAUSED', 'webhook'))).toEqual({ runNow: false, activate: true });
  });

  it('does not offer Activate for an automation that is already active', () => {
    expect(getAiBuildActions(saved('ACTIVE', 'schedule'))).toEqual({ runNow: true, activate: false });
  });

  it('offers nothing for an inactive automation: it cannot be run, and activating it does not bring its schedule back', () => {
    expect(getAiBuildActions(saved('INACTIVE', 'schedule'))).toEqual({ runNow: false, activate: false });
    expect(getAiBuildActions(saved('INACTIVE', 'manual'))).toEqual({ runNow: false, activate: false });
  });

  it('offers nothing when the saved automation is not known', () => {
    expect(getAiBuildActions(undefined)).toEqual({ runNow: false, activate: false });
    expect(getAiBuildActions(null)).toEqual({ runNow: false, activate: false });
    expect(getAiBuildActions({})).toEqual({ runNow: false, activate: false });
  });
});

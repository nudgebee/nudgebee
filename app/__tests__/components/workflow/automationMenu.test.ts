import { getAutomationToggleAction } from '@components/workflow/automationMenu';

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

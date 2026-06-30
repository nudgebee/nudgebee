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
});

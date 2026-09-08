import { buildAutomationRunHref } from '../CollapsableCard';

// The href is a contract with two other surfaces: the "Triggered for this event"
// menu builds the same destination, and the automation page reads workflow id from
// the path but execution id from the query. Getting either wrong lands the user on
// a run list instead of the run that produced the card, which looks like the card
// is attributed to the wrong automation.
describe('buildAutomationRunHref', () => {
  const source = { workflow_id: 'wf-1', workflow_name: 'EC2 Application Diagnostic', execution_id: 'run-9' };

  it('points at the run that produced the evidence', () => {
    expect(buildAutomationRunHref(source, 'acct-1')).toBe('/automation/wf-1?accountId=acct-1&executionId=run-9#executions');
  });

  it('omits accountId rather than sending the string "undefined"', () => {
    // The page can be opened without accountId in the URL; `?accountId=undefined`
    // is a valid-looking query that resolves to no account.
    expect(buildAutomationRunHref(source, undefined)).toBe('/automation/wf-1?executionId=run-9#executions');
  });

  it('still links to the automation when the run id is missing', () => {
    // Evidence stamped before an execution id was available should degrade to the
    // automation's run list, not to a broken query.
    expect(buildAutomationRunHref({ ...source, execution_id: '' }, 'acct-1')).toBe('/automation/wf-1?accountId=acct-1#executions');
  });

  it('escapes values instead of interpolating them raw', () => {
    expect(buildAutomationRunHref({ workflow_id: 'wf-1', execution_id: 'run 9&x=1' }, 'a b')).toBe(
      '/automation/wf-1?accountId=a+b&executionId=run+9%26x%3D1#executions'
    );
  });
});

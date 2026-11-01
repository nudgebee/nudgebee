import { readableStatusMessage } from '../resolutionStatus';

// A failed agent task was stored as the agent's whole response; the reason is the `msg` inside it.
describe('readableStatusMessage', () => {
  it('reads the reason out of a stored agent response', () => {
    expect(
      readableStatusMessage(
        '{"msg": "rightsizing_resource: StatefulSet rightsizing is not supported", "success": false, "task_processing_duration": 0}'
      )
    ).toBe('rightsizing_resource: StatefulSet rightsizing is not supported');
  });

  it('leaves plain text, JSON without a reason, and empty values unchanged', () => {
    expect(readableStatusMessage('Ran from the remediation panel, exit code 1')).toBe('Ran from the remediation panel, exit code 1');
    expect(readableStatusMessage('{"success": false}')).toBe('{"success": false}');
    expect(readableStatusMessage('{"msg": {"code": 1}}')).toBe('{"msg": {"code": 1}}');
    expect(readableStatusMessage('')).toBe('');
    expect(readableStatusMessage(undefined)).toBeUndefined();
  });
});

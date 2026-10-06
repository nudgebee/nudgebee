import { defaultVersionStatus } from '../versionStatus';

describe('defaultVersionStatus', () => {
  it.each(['ACTIVE', 'PAUSED', 'INACTIVE'] as const)('carries the live version status %s forward', (status) => {
    expect(defaultVersionStatus(status)).toBe(status);
  });

  it.each([null, undefined, '', 'UNKNOWN'])('falls back to PAUSED when there is no usable live status (%p)', (status) => {
    expect(defaultVersionStatus(status)).toBe('PAUSED');
  });
});

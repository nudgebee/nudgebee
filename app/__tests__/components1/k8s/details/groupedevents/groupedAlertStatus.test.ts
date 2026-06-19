import { getGroupedAlertStatus } from '@components1/k8s/details/groupedevents/groupedAlertStatus';

describe('getGroupedAlertStatus', () => {
  it('treats FIRING at index 0 as FIRING', () => {
    expect(getGroupedAlertStatus('FIRING')).toBe('FIRING');
  });

  it('keeps FIRING when combined with other statuses', () => {
    expect(getGroupedAlertStatus('FIRING,CLOSED')).toBe('FIRING');
  });

  it('falls back to CLOSED when FIRING is absent', () => {
    expect(getGroupedAlertStatus('CLOSED')).toBe('CLOSED');
    expect(getGroupedAlertStatus(undefined)).toBe('CLOSED');
  });
});

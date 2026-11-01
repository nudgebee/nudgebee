import { buildAuditLogUrl, parseAuditLogParams } from '@components/audits/common';

const searchOf = (url: string) => new URL(url, 'https://example.com').search;

describe('audit log deep link', () => {
  it('links to the plain Audit Log tab when there is nothing to filter on', () => {
    expect(buildAuditLogUrl()).toBe('/user-management#access-users/audit-log');
  });

  it('round-trips the filters and puts a window around the action time', () => {
    const at = Date.parse('2026-06-25T11:41:04.098Z');
    const url = buildAuditLogUrl({
      accountId: '00000000-0000-0000-0000-000000000001',
      category: 'ALERT_MANAGER',
      target: 'projects/example/alertPolicies/123',
      at: '2026-06-25T11:41:04.098Z',
    });

    expect(url.endsWith('#access-users/audit-log')).toBe(true);
    expect(parseAuditLogParams(searchOf(url))).toEqual({
      accountId: '00000000-0000-0000-0000-000000000001',
      category: 'ALERT_MANAGER',
      target: 'projects/example/alertPolicies/123',
      startDate: at - 5 * 60 * 1000,
      endDate: at + 5 * 60 * 1000,
    });
  });

  it('leaves the date range to the page default when the action time is unknown or unparseable', () => {
    for (const at of [undefined, 'not-a-date']) {
      const parsed = parseAuditLogParams(searchOf(buildAuditLogUrl({ category: 'ALERT_MANAGER', at })));
      expect(parsed).toEqual({ accountId: '', category: 'ALERT_MANAGER', target: '' });
    }
  });

  it('returns the default view for a URL without audit params', () => {
    expect(parseAuditLogParams('')).toEqual({ accountId: '', category: '', target: '' });
    expect(parseAuditLogParams('?groupId=abc')).toEqual({ accountId: '', category: '', target: '' });
  });

  it('drops params the filter bar could not show or the table could not query', () => {
    expect(parseAuditLogParams('?auditCategory=NOT_A_CATEGORY').category).toBe('');
    // Half a range, a reversed range, and a value Date cannot represent.
    for (const search of [
      '?auditStart=1000',
      '?auditStart=2000&auditEnd=1000',
      '?auditStart=1000&auditEnd=Infinity',
      '?auditStart=1000&auditEnd=9e15',
    ]) {
      expect(parseAuditLogParams(search)).toEqual({ accountId: '', category: '', target: '' });
    }
  });
});

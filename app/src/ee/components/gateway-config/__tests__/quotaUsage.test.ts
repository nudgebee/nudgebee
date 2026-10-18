import type { GatewayRateLimit } from '@api1/gateway-config';
import { fmtAmount, resetIn, softExceeded, usedPct } from '../quotaUsage';

const cost: GatewayRateLimit = { id: 'q', user_id: '', metric: 'cost', period: 'day', limit_value: 50, enabled: true };

describe('quota usage helpers', () => {
  it('formats cost in dollars and counts as whole numbers', () => {
    expect(fmtAmount(cost, 3.456)).toBe('$3.46');
    expect(fmtAmount(cost, 32.1)).toBe('$32.10');
    expect(fmtAmount({ ...cost, metric: 'tokens' }, 1234.6)).toBe('1,235');
  });

  it('says how long until the window resets', () => {
    const now = Date.parse('2026-10-16T18:00:00Z');
    expect(resetIn('2026-10-16T18:12:00Z', now)).toBe('in 12m');
    expect(resetIn('2026-10-17T00:00:00Z', now)).toBe('in 6h');
    expect(resetIn('2026-11-01T00:00:00Z', now)).toBe('in 15d');
    expect(resetIn('2026-10-16T17:00:00Z', now)).toBe('');
    expect(resetIn(undefined, now)).toBe('');
  });

  it('marks a quota past its warn level only when both are known', () => {
    expect(usedPct({ ...cost, used: 40 })).toBe(80);
    expect(softExceeded({ ...cost, used: 40, soft_pct: 80 })).toBe(true);
    expect(softExceeded({ ...cost, used: 39.9, soft_pct: 80 })).toBe(false);
    expect(softExceeded({ ...cost, soft_pct: 80 })).toBe(false);
    expect(softExceeded({ ...cost, used: 49 })).toBe(false);
  });
});

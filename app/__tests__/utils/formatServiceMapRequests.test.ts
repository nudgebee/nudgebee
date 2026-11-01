import { formatServiceMapRequests } from '@utils/common';

describe('formatServiceMapRequests', () => {
  it('shows eBPF rates per second with three significant digits', () => {
    expect(formatServiceMapRequests(0.00849555061179088, true)).toBe('0.0085 req/s');
    expect(formatServiceMapRequests(0.4606, true)).toBe('0.461 req/s');
    expect(formatServiceMapRequests(33, true)).toBe('33 req/s');
  });

  it('shows OTel call counts as whole requests', () => {
    expect(formatServiceMapRequests(1234, false)).toBe('1,234 req');
    expect(formatServiceMapRequests(2.6, false)).toBe('3 req');
  });

  it('shows a dash when there is no traffic', () => {
    expect(formatServiceMapRequests(0, true)).toBe('-');
    expect(formatServiceMapRequests(undefined, false)).toBe('-');
    expect(formatServiceMapRequests(NaN, true)).toBe('-');
  });
});

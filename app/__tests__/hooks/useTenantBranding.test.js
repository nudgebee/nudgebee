import { getTenantKey } from '@hooks/useTenantBranding';

jest.mock('@lib/auth', () => ({
  getUserSession: jest.fn(() => null),
}));

jest.mock('src/utils/common', () => ({
  snakeToTitleCase: jest.fn((s) => s.replace(/_/g, ' ')),
}));

// Mock fetch for /api/app/config
global.fetch = jest.fn();

describe('getTenantKey', () => {
  it('converts tenant name to snake_case key', () => {
    expect(getTenantKey('Acme Corp')).toBe('acme_corp');
  });

  it('returns empty string for empty input', () => {
    expect(getTenantKey('')).toBe('');
    expect(getTenantKey(null)).toBe('');
    expect(getTenantKey(undefined)).toBe('');
  });

  it('strips leading and trailing underscores', () => {
    expect(getTenantKey('  Foo Bar  ')).toBe('foo_bar');
  });

  it('handles special characters', () => {
    expect(getTenantKey('Nudgebee!')).toBe('nudgebee');
  });

  it('handles single-word names', () => {
    expect(getTenantKey('Nudgebee')).toBe('nudgebee');
  });
});

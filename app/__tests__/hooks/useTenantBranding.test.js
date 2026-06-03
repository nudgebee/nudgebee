import { getTenantKey, getPartnerKey } from '@hooks/useTenantBranding';

jest.mock('@lib/auth', () => ({
  getUserSession: jest.fn(() => null),
}));

jest.mock('src/utils/common', () => ({
  snakeToTitleCase: jest.fn((s) => s.replace(/_/g, ' ')),
}));

// Mock fetch for /api/public/app_config
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

describe('getPartnerKey', () => {
  it('returns empty string for null/undefined', () => {
    expect(getPartnerKey(null)).toBe('');
    expect(getPartnerKey(undefined)).toBe('');
    expect(getPartnerKey('')).toBe('');
  });

  it('returns empty string for bare domain (no subdomain)', () => {
    expect(getPartnerKey('nudgebee.com')).toBe('');
  });

  it('returns subdomain for partner domain', () => {
    expect(getPartnerKey('rackspace.nudgebee.com')).toBe('rackspace');
  });

  it('skips common infrastructure subdomains', () => {
    expect(getPartnerKey('www.nudgebee.com')).toBe('');
    expect(getPartnerKey('app.nudgebee.com')).toBe('');
    expect(getPartnerKey('api.nudgebee.com')).toBe('');
    expect(getPartnerKey('staging.nudgebee.com')).toBe('');
    expect(getPartnerKey('prod.nudgebee.com')).toBe('');
  });

  it('returns subdomain for custom partner', () => {
    expect(getPartnerKey('mypartner.nudgebee.com')).toBe('mypartner');
  });
});

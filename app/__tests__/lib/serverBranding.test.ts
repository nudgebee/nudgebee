/**
 * Host-to-brand-key helpers in `@lib/serverBranding`: how a request's Host /
 * X-Forwarded-Host headers become the key brand resolution looks up. What the
 * EE provider renders per host is covered in __tests__/ee/hostBranding.test.ts.
 */
import { brandHostFromHeaders, normalizeBrandHost } from '@lib/serverBranding';

describe('normalizeBrandHost', () => {
  it('lower-cases and strips the port', () => {
    expect(normalizeBrandHost('Demo.Partner.Example.COM:8443')).toBe('demo.partner.example.com');
  });

  it('takes the first entry of a comma-joined forwarded host', () => {
    expect(normalizeBrandHost('demo.partner.example.com, proxy.internal')).toBe('demo.partner.example.com');
  });

  it('strips a fully-qualified trailing dot', () => {
    expect(normalizeBrandHost('demo.partner.example.com.')).toBe('demo.partner.example.com');
  });

  it('keeps an IPv6 literal intact while dropping its port', () => {
    expect(normalizeBrandHost('[::1]:3000')).toBe('[::1]');
  });

  it('returns an empty key for missing or blank input', () => {
    expect(normalizeBrandHost(undefined)).toBe('');
    expect(normalizeBrandHost(null)).toBe('');
    expect(normalizeBrandHost('  ')).toBe('');
  });
});

describe('brandHostFromHeaders', () => {
  // The nginx sidecar proxies to 127.0.0.1:3000 and sets only X-Forwarded-Host,
  // so Host arrives as the loopback address in every deployed pod. Reading Host
  // first would collapse every hostname onto one brand.
  it('prefers x-forwarded-host over host', () => {
    expect(brandHostFromHeaders({ host: '127.0.0.1:3000', 'x-forwarded-host': 'demo.partner.example.com' })).toBe('demo.partner.example.com');
  });

  it('falls back to host when nothing forwarded it', () => {
    expect(brandHostFromHeaders({ host: 'localhost:3000' })).toBe('localhost:3000');
  });

  it('takes the first value when a header repeats', () => {
    expect(brandHostFromHeaders({ 'x-forwarded-host': ['demo.partner.example.com', 'spoof.example.com'] })).toBe('demo.partner.example.com');
  });

  it('returns null with no headers at all', () => {
    expect(brandHostFromHeaders(undefined)).toBeNull();
    expect(brandHostFromHeaders({})).toBeNull();
  });
});

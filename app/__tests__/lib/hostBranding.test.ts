/**
 * Per-host brand resolution: which theme file a request's hostname selects.
 *
 * Covers the pure header/host helpers in `@lib/serverBranding` and the EE
 * provider in `@ee/branding/serverInit` that maps a host to a brand kit. The
 * provider reads TENANT_BRANDING_HOST_MAP once at module load, so each case
 * re-imports it under a fresh env.
 */
import fs from 'fs';
import { brandHostFromHeaders, normalizeBrandHost, type ServerBrandingData } from '@lib/serverBranding';

jest.mock('fs');

const THEMES: Record<string, unknown> = {
  'branding/calsoft/theme.json': { title: 'Calsoft', logoUrl: '/branding/calsoft/logo.svg' },
  'branding/acme/theme.json': { title: 'Acme' },
  'branding/default/theme.json': { title: 'Nudgebee' },
};

// Resolve against the tail of the path: the loader joins process.cwd()/public.
// Takes the fs instance explicitly — jest.resetModules() hands the re-imported
// loader a fresh fs mock, and stubbing the stale one leaves every read failing.
function mockThemeFiles(fsModule: typeof fs): void {
  (fsModule.readFileSync as unknown as jest.Mock).mockImplementation((p: string) => {
    const hit = Object.keys(THEMES).find((rel) => String(p).endsWith(rel));
    if (!hit) {
      const err = new Error(`ENOENT: ${p}`) as NodeJS.ErrnoException;
      err.code = 'ENOENT';
      throw err;
    }
    return JSON.stringify(THEMES[hit]);
  });
}

type Resolver = (host?: string | null) => ServerBrandingData | null;

let savedEnv: NodeJS.ProcessEnv;

beforeEach(() => {
  savedEnv = { ...process.env };
});

afterEach(() => {
  // Restored only after the test has run: the provider reads
  // TENANT_BRANDING_FILE at call time, not at import.
  process.env = savedEnv;
});

// Re-import the provider under a given env, and hand back the resolver bound to it.
function resolverWithEnv(env: Record<string, string | undefined>): Resolver {
  jest.resetModules();
  for (const [k, v] of Object.entries(env)) {
    if (v === undefined) delete process.env[k];
    else process.env[k] = v;
  }
  /* eslint-disable @typescript-eslint/no-require-imports */
  mockThemeFiles(require('fs'));
  require('@ee/branding/serverInit');
  const { resolveServerBranding } = require('@lib/serverBranding');
  /* eslint-enable @typescript-eslint/no-require-imports */
  return resolveServerBranding;
}

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

describe('per-host brand resolution', () => {
  const HOST_MAP = JSON.stringify({ 'demo.partner.example.com': 'calsoft', 'acme.example.com': 'acme' });

  it('serves the mapped kit on a mapped host', () => {
    const resolve = resolverWithEnv({ TENANT_BRANDING_HOST_MAP: HOST_MAP, TENANT_BRANDING_FILE: undefined });
    expect(resolve('demo.partner.example.com')?.title).toBe('Calsoft');
    expect(resolve('acme.example.com')?.title).toBe('Acme');
  });

  it('marks a mapped host white-label and an unmapped host not', () => {
    const resolve = resolverWithEnv({ TENANT_BRANDING_HOST_MAP: HOST_MAP, TENANT_BRANDING_FILE: undefined });
    expect(resolve('demo.partner.example.com')?.isWhiteLabel).toBe(true);
    expect(resolve('app.example.com')?.isWhiteLabel).toBe(false);
  });

  it('leaves unmapped hosts on the house brand', () => {
    const resolve = resolverWithEnv({ TENANT_BRANDING_HOST_MAP: HOST_MAP, TENANT_BRANDING_FILE: undefined });
    expect(resolve('app.example.com')?.title).toBe('Nudgebee');
    expect(resolve(undefined)?.title).toBe('Nudgebee');
  });

  it('normalizes both the map key and the request host', () => {
    const resolve = resolverWithEnv({
      TENANT_BRANDING_HOST_MAP: JSON.stringify({ 'Demo.Partner.Example.com:443': 'calsoft' }),
      TENANT_BRANDING_FILE: undefined,
    });
    expect(resolve('demo.partner.example.com:8443')?.title).toBe('Calsoft');
  });

  it('falls back to TENANT_BRANDING_FILE for hosts outside the map', () => {
    const resolve = resolverWithEnv({
      TENANT_BRANDING_HOST_MAP: HOST_MAP,
      TENANT_BRANDING_FILE: 'branding/acme/theme.json',
    });
    expect(resolve('demo.partner.example.com')?.title).toBe('Calsoft');
    expect(resolve('other.example.com')?.title).toBe('Acme');
  });

  // Single-tenant deployments (rackspace) must behave exactly as before.
  it('applies TENANT_BRANDING_FILE to every host when no map is set', () => {
    const resolve = resolverWithEnv({
      TENANT_BRANDING_HOST_MAP: undefined,
      TENANT_BRANDING_FILE: 'branding/calsoft/theme.json',
    });
    expect(resolve('anything.example.com')?.title).toBe('Calsoft');
    expect(resolve(undefined)?.isWhiteLabel).toBe(true);
  });

  it('ignores a malformed map rather than failing the render', () => {
    const spy = jest.spyOn(console, 'error').mockImplementation(() => undefined);
    const resolve = resolverWithEnv({ TENANT_BRANDING_HOST_MAP: '{not json', TENANT_BRANDING_FILE: undefined });
    expect(resolve('demo.partner.example.com')?.title).toBe('Nudgebee');
    spy.mockRestore();
  });

  // A slug becomes a filesystem path, so a traversal attempt must be dropped
  // rather than reaching outside public/branding/.
  it('drops a host whose slug is not a plain identifier', () => {
    const spy = jest.spyOn(console, 'error').mockImplementation(() => undefined);
    const resolve = resolverWithEnv({
      TENANT_BRANDING_HOST_MAP: JSON.stringify({ 'evil.example.com': '../../../etc' }),
      TENANT_BRANDING_FILE: undefined,
    });
    expect(resolve('evil.example.com')?.title).toBe('Nudgebee');
    spy.mockRestore();
  });

  it('stays neutral when a mapped kit is not mounted', () => {
    const spy = jest.spyOn(console, 'error').mockImplementation(() => undefined);
    const resolve = resolverWithEnv({
      TENANT_BRANDING_HOST_MAP: JSON.stringify({ 'demo.partner.example.com': 'missing' }),
      TENANT_BRANDING_FILE: undefined,
    });
    expect(resolve('demo.partner.example.com')).toBeNull();
    spy.mockRestore();
  });

  // TENANT_THEME_CONFIG / TENANT_COLOR_TOKENS are deployment-wide; letting them
  // reach a host-mapped partner would paint one partner's palette on another.
  it('keeps deployment-wide theme env off host-mapped brands', () => {
    const resolve = resolverWithEnv({
      TENANT_BRANDING_HOST_MAP: HOST_MAP,
      TENANT_BRANDING_FILE: undefined,
      TENANT_COLOR_TOKENS: JSON.stringify({ '--nb-color-primary': '#ff0000' }),
    });
    expect(resolve('demo.partner.example.com')?.colorTokens).toBeNull();
    expect(resolve('app.example.com')?.colorTokens).toEqual({ '--nb-color-primary': '#ff0000' });
  });
});

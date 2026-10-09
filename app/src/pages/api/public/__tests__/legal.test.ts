import fs from 'fs';
import type { NextApiRequest, NextApiResponse } from 'next';
import handler from '../legal/[doc]';
import { resolveServerBranding } from '@lib/serverBranding';

// The route decides from the request host alone, so the provider is stood in
// for with one branding record per host.
jest.mock('@lib/serverBranding', () => ({
  awaitServerBrandingReady: jest.fn().mockResolvedValue(undefined),
  brandHostFromHeaders: (headers: Record<string, string | undefined>) => headers['x-forwarded-host'] ?? null,
  resolveServerBranding: jest.fn(),
}));

// Stand-ins for public/: the OSS snapshot ships no house pages, so the tests
// must not depend on the real files being there.
const HOUSE_PAGES: Record<string, string> = {
  'privacy.html': '<html><body>Privacy Notice</body></html>',
  'terms_of_use.html': '<html><body>Terms of Use</body></html>',
};
const realReadFileSync = fs.readFileSync;
function readHousePages(files: Record<string, string>) {
  return jest.spyOn(fs, 'readFileSync').mockImplementation(((file: fs.PathOrFileDescriptor, ...rest: unknown[]) => {
    const name = String(file).split(/[\\/]/).pop() as string;
    if (String(file).includes('public') && name in HOUSE_PAGES) {
      if (name in files) return files[name];
      throw Object.assign(new Error(`ENOENT: no such file, open '${String(file)}'`), { code: 'ENOENT' });
    }
    return (realReadFileSync as (...a: unknown[]) => unknown)(file, ...rest);
  }) as typeof fs.readFileSync);
}

const BRANDS: Record<string, Record<string, unknown> | null> = {
  // No provider answer at all: OSS, or EE with nothing configured.
  'app.example.com': null,
  // A partner that has not set its links yet.
  'new.partner.example.com': { title: 'Contoso Ops', isWhiteLabel: true },
  'ops.partner.example.com': {
    title: 'Contoso Ops',
    isWhiteLabel: true,
    privacyUrl: 'https://www.contoso.example.com/privacy',
    termsUrl: 'https://www.contoso.example.com/terms',
  },
  // Only one of the two set.
  'half.partner.example.com': { title: 'Contoso Ops', isWhiteLabel: true, privacyUrl: 'https://www.contoso.example.com/privacy' },
};

beforeEach(() => {
  (resolveServerBranding as jest.Mock).mockImplementation((host: string) => BRANDS[host] ?? null);
  readHousePages(HOUSE_PAGES);
});

afterEach(() => {
  jest.restoreAllMocks();
});

async function call(host: string, doc: string) {
  const headers: Record<string, string> = {};
  const out: { code?: number; body?: string; location?: string } = {};
  const res = {
    setHeader: (k: string, v: string) => {
      headers[k] = v;
    },
    status: (code: number) => {
      out.code = code;
      return {
        send: (body: string) => {
          out.body = body;
        },
        end: () => undefined,
      };
    },
    redirect: (code: number, location: string) => {
      out.code = code;
      out.location = location;
    },
  } as unknown as NextApiResponse;
  await handler({ method: 'GET', query: { doc }, headers: { 'x-forwarded-host': host } } as unknown as NextApiRequest, res);
  return { ...out, headers };
}

describe('legal pages per host', () => {
  it('serves the house pages where no branding is configured', async () => {
    const privacy = await call('app.example.com', 'privacy');
    expect(privacy.code).toBe(200);
    expect(privacy.headers['Content-Type']).toBe('text/html; charset=utf-8');
    expect(privacy.body).toContain('Privacy Notice');

    const terms = await call('app.example.com', 'terms');
    expect(terms.code).toBe(200);
    expect(terms.body).toContain('Terms of Use');
  });

  it("redirects to the partner's own pages when the host sets them", async () => {
    const privacy = await call('ops.partner.example.com', 'privacy');
    expect(privacy.code).toBe(307);
    expect(privacy.location).toBe('https://www.contoso.example.com/privacy');
    // A changed or cleared link must take effect on the next visit.
    expect(privacy.headers['Cache-Control']).toBe('no-store');
    expect(privacy.body).toBeUndefined();

    const terms = await call('ops.partner.example.com', 'terms');
    expect(terms.code).toBe(307);
    expect(terms.location).toBe('https://www.contoso.example.com/terms');
  });

  it('keeps serving the house page on a partner host that has set no link', async () => {
    const privacy = await call('new.partner.example.com', 'privacy');
    expect(privacy.code).toBe(200);
    expect(privacy.body).toContain('Privacy Notice');
  });

  it('decides each page on its own link', async () => {
    expect((await call('half.partner.example.com', 'privacy')).code).toBe(307);
    const terms = await call('half.partner.example.com', 'terms');
    expect(terms.code).toBe(200);
    expect(terms.body).toContain('Terms of Use');
  });

  it('answers 404 for anything that is not one of the two pages', async () => {
    for (const doc of ['cookies', 'constructor', '__proto__', '../privacy']) {
      expect((await call('app.example.com', doc)).code).toBe(404);
    }
  });

  it('answers 404 when the build ships no house page, as the OSS snapshot does', async () => {
    jest.restoreAllMocks();
    readHousePages({});
    let fresh: typeof handler = handler;
    jest.isolateModules(() => {
      // A fresh module, so the pages read by the tests above are not cached.
      fresh = require('../legal/[doc]').default;
    });
    const res = { status: jest.fn().mockReturnValue({ end: jest.fn(), send: jest.fn() }), setHeader: jest.fn() };
    await fresh(
      { method: 'GET', query: { doc: 'privacy' }, headers: { 'x-forwarded-host': 'app.example.com' } } as unknown as NextApiRequest,
      res as unknown as NextApiResponse
    );
    expect(res.status).toHaveBeenCalledWith(404);
  });
});

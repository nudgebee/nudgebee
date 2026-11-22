import type { NextApiRequest, NextApiResponse } from 'next';
import handler from '@pages/api/manifest';
import { resolveServerBranding } from '@lib/serverBranding';

jest.mock('@lib/serverBranding', () => {
  const actual = jest.requireActual('@lib/serverBranding');
  return {
    ...actual,
    awaitServerBrandingReady: jest.fn().mockResolvedValue(undefined),
    brandHostFromHeaders: jest.fn().mockReturnValue('demo.nudgebee.com'),
    resolveServerBranding: jest.fn(),
  };
});

const mockResolveBranding = resolveServerBranding as jest.Mock;

function makeRes() {
  const res = {
    statusCode: 0,
    body: undefined as any,
    headers: {} as Record<string, string>,
    setHeader(name: string, value: string) {
      res.headers[name] = value;
      return res;
    },
    status(code: number) {
      res.statusCode = code;
      return res;
    },
    send(payload: any) {
      try {
        res.body = typeof payload === 'string' ? JSON.parse(payload) : payload;
      } catch {
        res.body = payload;
      }
      return res;
    },
    json(payload: any) {
      res.body = payload;
      return res;
    },
  };
  return res as unknown as NextApiResponse & typeof res;
}

function makeReq(headers: Record<string, string> = {}) {
  return {
    method: 'GET',
    headers,
  } as unknown as NextApiRequest;
}

describe('/api/manifest', () => {
  const origEnv = process.env;

  beforeEach(() => {
    jest.resetModules();
    process.env = { ...origEnv };
    mockResolveBranding.mockReset();
  });

  afterAll(() => {
    process.env = origEnv;
  });

  it('returns 404 when PWA is not enabled', async () => {
    process.env.NEXT_PUBLIC_ENABLE_PWA = 'false';
    delete process.env.ENABLE_PWA;
    const req = makeReq();
    const res = makeRes();

    await handler(req, res);

    expect(res.statusCode).toBe(404);
    expect(res.body).toEqual({ error: 'PWA is not enabled' });
  });

  it('returns 404 when tenant isWhiteLabel is true', async () => {
    process.env.NEXT_PUBLIC_ENABLE_PWA = 'true';
    (process.env as any).NODE_ENV = 'production';
    mockResolveBranding.mockReturnValue({
      title: 'PartnerCloud',
      assistantName: 'CloudBot',
      isWhiteLabel: true,
      logoUrl: '/branding/partner/logo.svg',
      theme: { palette: { sidebarBg: '#0F172A' } },
    });

    const req = makeReq();
    const res = makeRes();

    await handler(req, res);

    expect(res.statusCode).toBe(404);
    expect(res.body).toEqual({ error: 'PWA is not available for custom branding' });
  });

  it('allows enabling via server ENABLE_PWA flag', async () => {
    process.env.NEXT_PUBLIC_ENABLE_PWA = 'false';
    process.env.ENABLE_PWA = 'true';
    (process.env as any).NODE_ENV = 'production';
    delete process.env.APP_ENV;
    delete process.env.NEXT_PUBLIC_APP_ENV;
    mockResolveBranding.mockReturnValue(null);

    const req = makeReq();
    const res = makeRes();

    await handler(req, res);

    expect(res.statusCode).toBe(200);
    expect(res.body.name).toBe('Nudgebee');
  });

  it('returns default manifest with Local tag in development', async () => {
    process.env.NEXT_PUBLIC_ENABLE_PWA = 'true';
    (process.env as any).NODE_ENV = 'development';
    delete process.env.APP_ENV;
    delete process.env.NEXT_PUBLIC_APP_ENV;
    mockResolveBranding.mockReturnValue(null);

    const req = makeReq();
    const res = makeRes();

    await handler(req, res);

    expect(res.statusCode).toBe(200);
    expect(res.body.name).toBe('Nudgebee (Local)');
    expect(res.body.short_name).toBe('NB Local');
    expect(res.body.description).toBe('Nudgebee — Unified agentic AI platform for day-2 cloud operations');
    expect(res.body.theme_color).toBe('#1B2D4A');
    expect(res.body.display).toBe('minimal-ui');
    expect(res.body.icons[0].src).toBe('/logo192.png');
    expect(res.headers['Content-Type']).toBe('application/manifest+json');
  });

  it('uses runtime APP_ENV and normalizes tag', async () => {
    process.env.NEXT_PUBLIC_ENABLE_PWA = 'true';
    (process.env as any).NODE_ENV = 'production';
    process.env.APP_ENV = 'dev';
    mockResolveBranding.mockReturnValue(null);

    const req = makeReq();
    const res = makeRes();

    await handler(req, res);

    expect(res.statusCode).toBe(200);
    expect(res.body.name).toBe('Nudgebee (Dev)');
    expect(res.body.short_name).toBe('NB Dev');
  });

  it('suppresses tag in production when APP_ENV is prod or production', async () => {
    process.env.NEXT_PUBLIC_ENABLE_PWA = 'true';
    (process.env as any).NODE_ENV = 'production';
    process.env.APP_ENV = 'production';
    mockResolveBranding.mockReturnValue(null);

    const req = makeReq();
    const res = makeRes();

    await handler(req, res);

    expect(res.statusCode).toBe(200);
    expect(res.body.name).toBe('Nudgebee');
    expect(res.body.short_name).toBe('Nudgebee');
  });

  it('falls back to NEXT_PUBLIC_APP_ENV when APP_ENV is unset', async () => {
    process.env.NEXT_PUBLIC_ENABLE_PWA = 'true';
    (process.env as any).NODE_ENV = 'production';
    delete process.env.APP_ENV;
    process.env.NEXT_PUBLIC_APP_ENV = 'QA';
    mockResolveBranding.mockReturnValue(null);

    const req = makeReq();
    const res = makeRes();

    await handler(req, res);

    expect(res.statusCode).toBe(200);
    expect(res.body.name).toBe('Nudgebee (QA)');
    expect(res.body.short_name).toBe('NB QA');
  });
});

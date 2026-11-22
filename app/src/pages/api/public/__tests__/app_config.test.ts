import type { NextApiRequest, NextApiResponse } from 'next';
import handler from '../app_config';
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
    status(code: number) {
      res.statusCode = code;
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

describe('/api/public/app_config', () => {
  const origEnv = process.env;

  beforeEach(() => {
    jest.resetModules();
    process.env = { ...origEnv };
    mockResolveBranding.mockReset();
  });

  afterAll(() => {
    process.env = origEnv;
  });

  it('reports pwaEnabled: false by default when ENABLE_PWA is unset', async () => {
    delete process.env.ENABLE_PWA;
    delete process.env.NEXT_PUBLIC_ENABLE_PWA;
    mockResolveBranding.mockReturnValue({ isWhiteLabel: false });

    const req = makeReq();
    const res = makeRes();

    await handler(req, res);

    expect(res.statusCode).toBe(200);
    expect(res.body.pwaEnabled).toBe(false);
  });

  it('reports pwaEnabled: true when ENABLE_PWA=true and not white-label', async () => {
    process.env.ENABLE_PWA = 'true';
    mockResolveBranding.mockReturnValue({ isWhiteLabel: false, theme: { palette: { sidebarBg: '#1B2D4A' } } });

    const req = makeReq();
    const res = makeRes();

    await handler(req, res);

    expect(res.statusCode).toBe(200);
    expect(res.body.pwaEnabled).toBe(true);
    expect(res.body.pwaThemeColor).toBe('#1B2D4A');
  });

  it('reports pwaEnabled: false for white-label tenants even when ENABLE_PWA=true', async () => {
    process.env.ENABLE_PWA = 'true';
    mockResolveBranding.mockReturnValue({ isWhiteLabel: true });

    const req = makeReq();
    const res = makeRes();

    await handler(req, res);

    expect(res.statusCode).toBe(200);
    expect(res.body.pwaEnabled).toBe(false);
  });
});

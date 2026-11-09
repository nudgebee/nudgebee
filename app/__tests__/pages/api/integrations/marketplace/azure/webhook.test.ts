/**
 * @jest-environment node
 */
import type { NextApiRequest, NextApiResponse } from 'next';
import { createLocalJWKSet, exportJWK, generateKeyPair, SignJWT, type KeyLike } from 'jose';
import handler from '@pages/api/integrations/marketplace/azure/webhook';

// Real signatures, local keys: only the JWKS download is swapped out.
let mockJwks: ReturnType<typeof createLocalJWKSet>;
jest.mock('jose', () => ({
  ...jest.requireActual('jose'),
  createRemoteJWKSet: jest.fn(() => mockJwks),
}));

const APP_ID = '11111111-1111-1111-1111-111111111111';
const TENANT_ID = '22222222-2222-2222-2222-222222222222';
const MARKETPLACE_API_APP_ID = '20e940b3-4c77-4b0b-9a53-9e16a1b010a7'; // gitleaks:allow — Microsoft's public Marketplace app ID, not a secret
const V1_ISSUER = `https://sts.windows.net/${TENANT_ID}/`;
const V2_ISSUER = `https://login.microsoftonline.com/${TENANT_ID}/v2.0`;

let privateKey: KeyLike;
let otherPrivateKey: KeyLike;
// jest.setup.js installs global.fetch as a jest.fn(); reuse it.
const fetchMock = global.fetch as jest.Mock;

beforeAll(async () => {
  const signing = await generateKeyPair('RS256');
  const other = await generateKeyPair('RS256');
  privateKey = signing.privateKey;
  otherPrivateKey = other.privateKey;
  mockJwks = createLocalJWKSet({ keys: [{ ...(await exportJWK(signing.publicKey)), kid: 'k1', alg: 'RS256' }] });
});

beforeEach(() => {
  process.env.AZURE_MARKETPLACE_APP_ID = APP_ID;
  process.env.AZURE_MARKETPLACE_TENANT_ID = TENANT_ID;
  fetchMock.mockReset();
  fetchMock.mockResolvedValue({ ok: true, status: 200 });
  jest.spyOn(console, 'log').mockImplementation(() => {});
  jest.spyOn(console, 'warn').mockImplementation(() => {});
  jest.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  jest.restoreAllMocks();
});

function sign(claims: Record<string, unknown>, opts: { key?: KeyLike; issuer?: string; audience?: string } = {}) {
  return new SignJWT(claims)
    .setProtectedHeader({ alg: 'RS256', kid: 'k1' })
    .setIssuer(opts.issuer ?? V1_ISSUER)
    .setAudience(opts.audience ?? APP_ID)
    .setIssuedAt()
    .setExpirationTime('5m')
    .sign(opts.key ?? privateKey);
}

function makeRes() {
  const res = {
    statusCode: 0,
    body: undefined as unknown,
    ended: false,
    status(code: number) {
      res.statusCode = code;
      return res;
    },
    json(payload: unknown) {
      res.body = payload;
      res.ended = true;
      return res;
    },
    end() {
      res.ended = true;
      return res;
    },
  };
  return res as unknown as NextApiResponse & typeof res;
}

const event = { id: 'op-1', action: 'Unsubscribe', subscriptionId: 'sub-1', subscription: { saasSubscriptionStatus: 'Unsubscribed' } };

async function call(token?: string, method = 'POST') {
  const res = makeRes();
  const req = {
    method,
    headers: token ? { authorization: `Bearer ${token}` } : {},
    body: event,
  } as unknown as NextApiRequest;
  await handler(req, res);
  return res;
}

describe('POST /api/integrations/marketplace/azure/webhook', () => {
  it.each([
    ['v1 token (appid)', V1_ISSUER, { appid: MARKETPLACE_API_APP_ID }],
    ['v2 token (azp)', V2_ISSUER, { azp: MARKETPLACE_API_APP_ID }],
  ])('accepts a Marketplace %s, forwards the JSON body and ends the response', async (_name, issuer, claims) => {
    const res = await call(await sign({ ...claims, tid: TENANT_ID }, { issuer }));

    expect(res.statusCode).toBe(200);
    expect(res.ended).toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toMatch(/\/marketplace\/azure\/webhook$/);
    expect(JSON.parse(init.body)).toEqual(event);
  });

  it.each([
    ['wrong audience', () => sign({ appid: MARKETPLACE_API_APP_ID }, { audience: 'some-other-app' })],
    [
      'issuer from another tenant',
      () => sign({ appid: MARKETPLACE_API_APP_ID }, { issuer: 'https://sts.windows.net/33333333-3333-3333-3333-333333333333/' }),
    ],
    ['multi-tenant "common" issuer', () => sign({ appid: MARKETPLACE_API_APP_ID }, { issuer: 'https://login.microsoftonline.com/common/v2.0' })],
    ['caller is not the Marketplace app', () => sign({ appid: '44444444-4444-4444-4444-444444444444' })],
    ['no caller claim', () => sign({})],
    ['signed by an unknown key', () => sign({ appid: MARKETPLACE_API_APP_ID }, { key: otherPrivateKey })],
    ['not a JWT', async () => 'garbage'],
  ])('rejects %s with 401 and does not forward', async (_name, makeToken) => {
    const res = await call(await makeToken());

    expect(res.statusCode).toBe(401);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('rejects a request without a bearer token', async () => {
    const res = await call();
    expect(res.statusCode).toBe(401);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it.each(['AZURE_MARKETPLACE_APP_ID', 'AZURE_MARKETPLACE_TENANT_ID'])('fails closed with 503 when %s is unset', async (name) => {
    delete process.env[name];
    const res = await call(await sign({ appid: MARKETPLACE_API_APP_ID }));
    expect(res.statusCode).toBe(503);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('rejects non-POST methods', async () => {
    const res = await call(undefined, 'GET');
    expect(res.statusCode).toBe(405);
  });

  it('returns 500 when the api-server rejects the event, so Marketplace retries', async () => {
    fetchMock.mockResolvedValue({ ok: false, status: 400 });
    const res = await call(await sign({ appid: MARKETPLACE_API_APP_ID }));
    expect(res.statusCode).toBe(500);
  });
});

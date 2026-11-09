import fs from 'fs';
import path from 'path';
import type { NextApiRequest, NextApiResponse } from 'next';
import handler from '../microsoft-identity-association';

function makeRes() {
  const res = {
    statusCode: 0,
    headers: {} as Record<string, string>,
    body: undefined as any,
    setHeader(name: string, value: string) {
      res.headers[name] = value;
      return res;
    },
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

describe('/.well-known/microsoft-identity-association.json', () => {
  const origEnv = process.env;

  afterEach(() => {
    process.env = origEnv;
  });

  it('names the configured application', () => {
    process.env = { ...origEnv, MICROSOFT_IDENTITY_APPLICATION_ID: '11111111-2222-4333-8444-555555555555' };
    const res = makeRes();
    handler({ method: 'GET' } as NextApiRequest, res);
    expect(res.statusCode).toBe(200);
    expect(res.headers['Content-Type']).toBe('application/json');
    expect(res.body).toEqual({ associatedApplications: [{ applicationId: '11111111-2222-4333-8444-555555555555' }] });
  });

  it('lists no application when none is configured', () => {
    process.env = { ...origEnv, MICROSOFT_IDENTITY_APPLICATION_ID: '' };
    const res = makeRes();
    handler({ method: 'GET' } as NextApiRequest, res);
    expect(res.body).toEqual({ associatedApplications: [] });
  });

  it('is served by the route the public URL is rewritten to', () => {
    // Microsoft fetches /.well-known/microsoft-identity-association.json; next.config.js
    // rewrites it, and the destination must be a real API route or the URL 404s.
    const appRoot = path.resolve(__dirname, '../../../../..');
    const config = fs.readFileSync(path.join(appRoot, 'next.config.js'), 'utf8');
    const match = config.match(/source:\s*'\/\.well-known\/microsoft-identity-association\.json',\s*destination:\s*'([^']+)'/);
    expect(match).not.toBeNull();
    expect(fs.existsSync(path.join(appRoot, 'src/pages', `${match![1]}.ts`))).toBe(true);
  });
});

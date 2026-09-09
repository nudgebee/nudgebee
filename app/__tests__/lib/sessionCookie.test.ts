/**
 * Session-cookie reads must not depend on NEXTAUTH_URL.
 *
 * getToken() derives the cookie NAME from that env var, so a deployment that unsets
 * it (to let NextAuth resolve its origin per request host) flips the lookup to the
 * non-secure name while NextAuth still writes `__Secure-…`. Every authenticated
 * request then misses a session the user just established — that took dev down on
 * 2026-09-01. These pin the try-both-names behaviour that makes unsetting it safe.
 */
import type { NextApiRequest } from 'next';
import { getToken } from 'next-auth/jwt';
import { readSessionToken } from '@lib/sessionCookie';

jest.mock('next-auth/jwt', () => ({ getToken: jest.fn() }));

const mockGetToken = getToken as unknown as jest.Mock;
const req = {} as NextApiRequest;
const SESSION = { sub: 'user-1', email: 'a@example.com' };

let savedEnv: NodeJS.ProcessEnv;

beforeEach(() => {
  savedEnv = { ...process.env };
  delete process.env.NEXTAUTH_URL;
  mockGetToken.mockReset();
});

afterEach(() => {
  process.env = savedEnv;
});

// The exact production failure: cookie written under the secure name, NEXTAUTH_URL
// absent so a single getToken() call would look under the non-secure one.
it('finds a session stored under the secure cookie name with NEXTAUTH_URL unset', async () => {
  mockGetToken.mockImplementation(({ secureCookie }) => Promise.resolve(secureCookie ? SESSION : null));
  await expect(readSessionToken(req)).resolves.toEqual(SESSION);
});

// The on-prem HTTP-behind-a-TLS-proxy case, which hardcoding secureCookie:true would break.
it('finds a session stored under the non-secure cookie name', async () => {
  mockGetToken.mockImplementation(({ secureCookie }) => Promise.resolve(secureCookie ? null : SESSION));
  await expect(readSessionToken(req)).resolves.toEqual(SESSION);
});

it('tries the secure name first and stops there when it hits', async () => {
  mockGetToken.mockResolvedValue(SESSION);
  await readSessionToken(req);
  expect(mockGetToken).toHaveBeenCalledTimes(1);
  expect(mockGetToken).toHaveBeenCalledWith(expect.objectContaining({ secureCookie: true }));
});

it('falls through to the non-secure name only when the secure one misses', async () => {
  mockGetToken.mockImplementation(({ secureCookie }) => Promise.resolve(secureCookie ? null : SESSION));
  await readSessionToken(req);
  expect(mockGetToken).toHaveBeenCalledTimes(2);
  expect(mockGetToken).toHaveBeenNthCalledWith(2, expect.objectContaining({ secureCookie: false }));
});

it('returns null when neither cookie name has a session', async () => {
  mockGetToken.mockResolvedValue(null);
  await expect(readSessionToken(req)).resolves.toBeNull();
  expect(mockGetToken).toHaveBeenCalledTimes(2);
});

// Behaviour must be identical whether or not the deployment pins NEXTAUTH_URL.
it('behaves the same when NEXTAUTH_URL is set', async () => {
  process.env.NEXTAUTH_URL = 'https://app.example.com';
  mockGetToken.mockImplementation(({ secureCookie }) => Promise.resolve(secureCookie ? SESSION : null));
  await expect(readSessionToken(req)).resolves.toEqual(SESSION);
});

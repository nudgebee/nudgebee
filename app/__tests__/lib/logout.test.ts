const mockGetSession = jest.fn();
const mockSignOut = jest.fn();
const mockGetUserSession = jest.fn();
jest.mock('next-auth/react', () => ({
  getSession: (...a: unknown[]) => mockGetSession(...a),
  signOut: (...a: unknown[]) => mockSignOut(...a),
}));
jest.mock('@lib/auth', () => ({ getUserSession: () => mockGetUserSession() }));

import { logout } from '@lib/logout';

const IDP_CALLBACK = '/api/auth/saml/logout?to=https%3A%2F%2Fidp.example.com%2Fslo%2Fsaml%3FSAMLRequest%3Dabc';

describe('logout', () => {
  beforeEach(() => {
    mockGetSession.mockReset();
    mockGetUserSession.mockReset();
    mockSignOut.mockReset().mockResolvedValue(undefined);
    global.fetch = jest.fn();
  });

  it('signs out exactly as before when the session has no IdP logout path', async () => {
    mockGetUserSession.mockReturnValue({ user: { email: 'ops@example.com' } });
    await logout('/');
    expect(global.fetch).not.toHaveBeenCalled();
    expect(mockGetSession).not.toHaveBeenCalled();
    expect(mockSignOut).toHaveBeenCalledWith({ callbackUrl: '/' });
  });

  it('passes the callback through', async () => {
    mockGetSession.mockResolvedValue(null);
    await logout('/signin');
    expect(mockSignOut).toHaveBeenCalledWith({ callbackUrl: '/signin' });
  });

  // A navigating sign-out leaves the page before anything on it can react to
  // the session ending; one that does not would race the trip to the IdP.
  it('signs out with a navigation to the IdP callback', async () => {
    mockGetUserSession.mockReturnValue({ idpLogoutPath: '/api/auth/saml/logout' });
    (global.fetch as jest.Mock).mockResolvedValue({ ok: true, json: async () => ({ url: IDP_CALLBACK }) });
    await logout('/');
    expect(global.fetch).toHaveBeenCalledWith('/api/auth/saml/logout', { method: 'POST' });
    expect(mockSignOut).toHaveBeenCalledTimes(1);
    expect(mockSignOut).toHaveBeenCalledWith({ callbackUrl: IDP_CALLBACK });
  });

  it('reads the session from the server on a page that does not hold one', async () => {
    mockGetUserSession.mockReturnValue(undefined);
    mockGetSession.mockResolvedValue({ idpLogoutPath: '/api/auth/saml/logout' });
    (global.fetch as jest.Mock).mockResolvedValue({ ok: true, json: async () => ({ url: IDP_CALLBACK }) });
    await logout('/signin');
    expect(mockSignOut).toHaveBeenCalledWith({ callbackUrl: IDP_CALLBACK });
  });

  it('falls back to a plain sign-out when the IdP has nothing to end', async () => {
    mockGetUserSession.mockReturnValue({ idpLogoutPath: '/api/auth/saml/logout' });
    (global.fetch as jest.Mock).mockResolvedValue({ ok: true, json: async () => ({ url: null }) });
    await logout('/');
    expect(mockSignOut).toHaveBeenCalledWith({ callbackUrl: '/' });
  });

  it('falls back to a plain sign-out, and says so, when the logout URL cannot be fetched', async () => {
    const warn = jest.spyOn(console, 'warn').mockImplementation(() => undefined);
    mockGetUserSession.mockReturnValue({ idpLogoutPath: '/api/auth/saml/logout' });
    (global.fetch as jest.Mock).mockRejectedValue(new Error('network down'));
    await logout('/');
    expect(mockSignOut).toHaveBeenCalledWith({ callbackUrl: '/' });
    expect(warn).toHaveBeenCalled();
    warn.mockRestore();
  });

  it('says so when the logout URL request fails', async () => {
    const warn = jest.spyOn(console, 'warn').mockImplementation(() => undefined);
    mockGetUserSession.mockReturnValue({ idpLogoutPath: '/api/auth/saml/logout' });
    (global.fetch as jest.Mock).mockResolvedValue({ ok: false, status: 500 });
    await logout('/');
    expect(mockSignOut).toHaveBeenCalledWith({ callbackUrl: '/' });
    expect(warn.mock.calls[0][0]).toContain('500');
    warn.mockRestore();
  });
});

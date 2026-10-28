import React from 'react';
import { TextEncoder } from 'util';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { signIn, useSession } from 'next-auth/react';
import MsTeamsConnectPage from '@pages/integrations/ms-teams/connect';

jest.mock('next-auth/react', () => ({ useSession: jest.fn(), signIn: jest.fn() }));
jest.mock('@hooks/useTenantBranding', () => ({
  useBrandingConfig: () => ({ title: 'Acme', assistantName: 'nubi', loading: false }),
  toAssistantLabel: (name: string) => name.charAt(0).toUpperCase() + name.slice(1),
}));

// react-dom/server's browser build needs TextEncoder, which jsdom doesn't provide.
Object.assign(global, { TextEncoder });
// eslint-disable-next-line @typescript-eslint/no-var-requires
const { renderToString } = require('react-dom/server');

const mockUseSession = useSession as jest.Mock;
const fetchMock = jest.fn();
const PATH = '/integrations/ms-teams/connect';
const SESSION = { user: { email: 'sam@contoso.com' }, tenant: { id: 'tenant-1', name: 'Acme Prod' } };
const INTEGRATIONS = '/user-management#integrations';

function linkToken(params: Record<string, string> = {}) {
  const body = {
    action_name: 'ms_teams_connect',
    origin: 'ms_teams',
    timestamp: 1761645720,
    action_params: { team_name: 'Platform', team_aad_group_id: 'group-1', installer: 'a1b2c3', ...params },
  };
  return Buffer.from(JSON.stringify({ kid: '1', body, signature: 'v0=abc' }))
    .toString('base64url')
    .replace(/=+$/, '');
}

function openLink(token: string | null, session: unknown = SESSION, status = 'authenticated') {
  window.history.replaceState(null, '', token === null ? PATH : `${PATH}#t=${token}`);
  mockUseSession.mockReturnValue({ data: session, status });
  return render(<MsTeamsConnectPage />);
}

function upstream(status: number, body: unknown) {
  fetchMock.mockResolvedValue({ ok: status >= 200 && status < 300, status, json: async () => body });
}

beforeEach(() => {
  mockUseSession.mockReset();
  (signIn as jest.Mock).mockReset();
  fetchMock.mockReset();
  global.fetch = fetchMock as never;
});

describe('the Microsoft Teams connect page', () => {
  it('renders the same shell for everyone before it reads the link', () => {
    window.history.replaceState(null, '', `${PATH}#t=${linkToken()}`);
    mockUseSession.mockReturnValue({ data: SESSION, status: 'authenticated' });
    // next/head's useLayoutEffect warns under a server render.
    jest.spyOn(console, 'error').mockImplementation(() => {});
    const html = renderToString(<MsTeamsConnectPage />);
    (console.error as jest.Mock).mockRestore();
    expect(html).toContain('Connect Microsoft Teams');
    for (const derived of ['Platform', 'Acme Prod', 'Sign in', 'Open the link']) {
      expect(html).not.toContain(derived);
    }
  });

  it('removes the link from the address bar when it mounts, keeping the router state', () => {
    const replaceState = jest.spyOn(window.history, 'replaceState');
    window.history.replaceState({ __N: true }, '', `${PATH}?x=1#t=${linkToken()}`);
    replaceState.mockClear();
    mockUseSession.mockReturnValue({ data: SESSION, status: 'authenticated' });
    render(<MsTeamsConnectPage />);
    expect(replaceState).toHaveBeenCalledWith({ __N: true }, '', `${PATH}?x=1`);
    expect(window.location.href).not.toContain('#t=');
    replaceState.mockRestore();
  });

  it('asks a signed-out visitor to sign in, landing on Integrations rather than back here', () => {
    openLink(linkToken(), null, 'unauthenticated');
    expect(screen.getByText('Sign in to Acme, then open the link from Teams again.')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('ms-teams-connect-sign-in-btn'));
    expect(signIn).toHaveBeenCalledWith(undefined, { callbackUrl: INTEGRATIONS });
  });

  it('offers nothing to click while the session is loading', () => {
    openLink(linkToken(), null, 'loading');
    expect(screen.queryByTestId('ms-teams-connect-confirm-btn')).toBeNull();
    expect(screen.queryByTestId('ms-teams-connect-sign-in-btn')).toBeNull();
  });

  it('confirms the team into the workspace it shows', async () => {
    upstream(200, { status: 'connected' });
    openLink(linkToken());
    expect(screen.getByTestId('ms-teams-connect-body')).toHaveTextContent('Connect team Platform to workspace Acme Prod?');
    expect(screen.queryByText('Can be completed by')).toBeNull();
    expect(screen.getByTestId('ms-teams-connect-cancel-btn')).toHaveAttribute('href', INTEGRATIONS);
    fireEvent.click(screen.getByTestId('ms-teams-connect-confirm-btn'));
    await screen.findByText(/is connected to/);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe('/api/integrations/ms-teams/link');
    expect(JSON.parse(init.body)).toEqual({ token: linkToken(), tenant_id: 'tenant-1' });
    expect(screen.getByTestId('ms-teams-connect-body')).toHaveTextContent('Microsoft Teams is connected to Acme Prod.');
    expect(screen.getByTestId('ms-teams-connect-integrations-btn')).toHaveAttribute('href', INTEGRATIONS);
  });

  it('names the team by its id when the link has no name', () => {
    openLink(linkToken({ team_name: '' }));
    expect(screen.getByTestId('ms-teams-connect-body')).toHaveTextContent('Connect team group-1 to workspace Acme Prod?');
  });

  it.each([
    [400, { code: 'expired' }, 'This link has expired. Connect from Acme → Integrations → Microsoft Teams.'],
    [
      400,
      { code: 'wrong_user' },
      'This link is for the person who added Nubi to this team. Sign in to Acme with their account, or connect from Integrations.',
    ],
    [400, { code: 'invalid' }, "This link isn't valid. Open it from Teams again."],
    [403, { code: 'role_missing' }, 'Only a Acme admin can connect Microsoft Teams.'],
    [400, { code: 'tenant_linked_elsewhere' }, 'This Microsoft tenant is connected to another Acme workspace. Contact support to move it.'],
    [400, { code: 'already_connected' }, 'Microsoft Teams is already connected to this workspace.'],
    [409, { code: 'workspace_changed' }, 'Your workspace changed in another tab. Open the link from Teams again.'],
    [500, { code: 'error' }, 'Something went wrong. Try again from Teams.'],
    [401, { error: 'not_authenticated' }, 'Something went wrong. Try again from Teams.'],
    [400, { code: 'domain_mismatch', domain: 'contoso.com' }, 'Something went wrong. Try again from Teams.'],
    [400, { code: 'public_domain', domain: 'gmail.com' }, 'Something went wrong. Try again from Teams.'],
  ])('shows the refusal for a %s %j', async (status, body, message) => {
    upstream(status, body);
    openLink(linkToken());
    fireEvent.click(screen.getByTestId('ms-teams-connect-confirm-btn'));
    await waitFor(() => expect(screen.getByTestId('ms-teams-connect-body')).toHaveTextContent(message));
  });

  it('sends the wrong user to Integrations', async () => {
    upstream(400, { code: 'wrong_user' });
    openLink(linkToken());
    fireEvent.click(screen.getByTestId('ms-teams-connect-confirm-btn'));
    await screen.findByText(/This link is for the person who added Nubi/);
    expect(screen.getByTestId('ms-teams-connect-integrations-btn')).toHaveAttribute('href', INTEGRATIONS);
  });

  it('never carries the link in a sign-in callback or a link it offers', async () => {
    const token = linkToken();
    openLink(token, null, 'unauthenticated');
    fireEvent.click(screen.getByTestId('ms-teams-connect-sign-in-btn'));
    const { callbackUrl } = (signIn as jest.Mock).mock.calls[0][1];
    expect(callbackUrl).not.toContain(token);
    expect(callbackUrl).not.toContain('#t=');
    expect(callbackUrl).not.toContain('ms-teams/connect');

    upstream(400, { code: 'invalid' });
    openLink(token);
    const hrefs = () => Array.from(document.querySelectorAll('a')).map((a) => a.getAttribute('href') || '');
    expect(hrefs()).toContain(INTEGRATIONS);
    fireEvent.click(screen.getByTestId('ms-teams-connect-confirm-btn'));
    await screen.findByTestId('ms-teams-connect-integrations-btn');
    for (const href of hrefs()) {
      expect(href).not.toContain(token);
      expect(href).not.toContain('#t=');
    }
  });

  it('asks for the link from Teams when there is none', () => {
    openLink(null);
    expect(screen.getByTestId('ms-teams-connect-body')).toHaveTextContent('Open the link from Teams again.');
    expect(screen.queryByTestId('ms-teams-connect-confirm-btn')).toBeNull();
  });

  it('calls a link it cannot read invalid without posting it', () => {
    openLink('not-a-link');
    expect(screen.getByTestId('ms-teams-connect-body')).toHaveTextContent("This link isn't valid. Open it from Teams again.");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

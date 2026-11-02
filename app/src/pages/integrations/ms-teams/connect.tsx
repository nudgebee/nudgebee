import { useEffect, useRef, useState } from 'react';
import Head from 'next/head';
import { signIn, useSession } from 'next-auth/react';
import { Box, Typography } from '@mui/material';
import { Banner } from '@ui/Banner';
import { Button } from '@ui/Button';
import { Card } from '@ui/Card';
import { Skeleton } from '@ui/Skeleton';
import { toAssistantLabel, useBrandingConfig } from '@hooks/useTenantBranding';
import { ds } from '@utils/colors';

// Opened from a connect link on a Microsoft Teams bot card. Not under withAuth: its
// sign-in redirect would carry this URL, and the link in its fragment, as the callbackUrl.

type Outcome = { code: string };
type LinkDetails = { team: string };
type SessionTenant = { id?: string; name?: string };
const INTEGRATIONS_PATH = '/user-management#integrations';

// For display only; notifications-server verifies the link.
function readLink(token: string): LinkDetails | null {
  try {
    const base64 = token.replace(/-/g, '+').replace(/_/g, '/');
    const params = JSON.parse(atob(base64 + '='.repeat((4 - (base64.length % 4)) % 4)))?.body?.action_params;
    if (!params || typeof params !== 'object') return null;
    return { team: params.team_name || params.team_aad_group_id || '' };
  } catch {
    return null;
  }
}

function outcomeMessage(outcome: Outcome, brand: string, assistant: string): React.ReactNode {
  switch (outcome.code) {
    case 'expired':
      return `This link has expired. @mention ${assistant} in the team for a new one.`;
    case 'invalid':
      return "This link isn't valid. Open it from Teams again.";
    case 'role_missing':
      return `Only a ${brand} admin can connect Microsoft Teams.`;
    case 'wrong_user':
      return `This link is for the person it was posted for. Sign in to ${brand} with their account, or @mention ${assistant} in the team for a link of your own.`;
    case 'team_linked_elsewhere':
      return `This team is connected to another ${brand} workspace. To move it, remove ${assistant} from the team, add it back, and open the new link.`;
    case 'other_microsoft_tenant':
      return `This workspace's Microsoft Teams teams are in another Microsoft tenant. Disconnect Microsoft Teams in this workspace first, or connect this team to another workspace.`;
    case 'already_connected':
      return 'This team is already connected to this workspace.';
    case 'workspace_changed':
      return 'Your workspace changed in another tab. Open the link from Teams again.';
    default:
      return 'Something went wrong. Try again from Teams.';
  }
}

export default function MsTeamsConnectPage(): JSX.Element {
  const { data: session, status } = useSession();
  const { title: brand, assistantName, loading: brandingLoading } = useBrandingConfig();
  const assistant = toAssistantLabel(assistantName);
  // null until the fragment has been read, so the first render is the same for everyone.
  const [token, setToken] = useState<string | null>(null);
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const fragmentRead = useRef(false);

  useEffect(() => {
    if (fragmentRead.current) return;
    fragmentRead.current = true;
    const linkToken = new URLSearchParams(window.location.hash.slice(1)).get('t') || '';
    // Before anything can report window.location.href (the client-error reporter does).
    window.history.replaceState(window.history.state, '', window.location.pathname + window.location.search);
    setToken(linkToken);
  }, []);

  const tenant = (session as { tenant?: SessionTenant } | null)?.tenant;
  const details = token ? readLink(token) : null;

  const connect = async () => {
    setSubmitting(true);
    try {
      // Plain fetch, not the app's HTTP client, whose error reporting logs request bodies.
      const response = await fetch('/api/integrations/ms-teams/link', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ token, tenant_id: tenant?.id }),
      });
      const body = await response.json().catch(() => ({}));
      setOutcome(response.ok && body?.status === 'connected' ? { code: 'connected' } : { code: body?.code || 'error' });
    } catch {
      setOutcome({ code: 'error' });
    } finally {
      setSubmitting(false);
    }
  };

  const goToIntegrations = (
    <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
      <Button href={INTEGRATIONS_PATH} data-testid='ms-teams-connect-integrations-btn'>
        Go to Integrations
      </Button>
    </Box>
  );

  let body: React.ReactNode;
  // Copy waits for the brand, so no sentence renders with the name missing.
  if (token === null || brandingLoading || (token && !outcome && status === 'loading')) {
    body = <Skeleton shape='text' size='text' />;
  } else if (!token) {
    body = <Banner surface='section' tone='warning' message='Open the link from Teams again.' />;
  } else if (!details) {
    body = <Banner surface='section' tone='critical' message={outcomeMessage({ code: 'invalid' }, brand, assistant)} />;
  } else if (outcome?.code === 'connected') {
    body = (
      <>
        <Banner
          surface='section'
          tone='success'
          message={
            <>
              Team <b>{details.team}</b> is connected to <b>{tenant?.name}</b>.
            </>
          }
        />
        {goToIntegrations}
      </>
    );
  } else if (outcome) {
    const expired = outcome.code === 'expired';
    body = (
      <>
        <Banner surface='section' tone={expired ? 'warning' : 'critical'} message={outcomeMessage(outcome, brand, assistant)} />
        {!expired && goToIntegrations}
      </>
    );
  } else if (status === 'unauthenticated') {
    body = (
      <>
        <Typography sx={{ fontSize: ds.text.bodyLg, color: ds.gray[600] }}>Sign in to {brand}, then open the link from Teams again.</Typography>
        <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
          {/* Never this page's URL as the callback: NextAuth would default to it. */}
          <Button onClick={() => signIn(undefined, { callbackUrl: INTEGRATIONS_PATH })} data-testid='ms-teams-connect-sign-in-btn'>
            Sign in
          </Button>
        </Box>
      </>
    );
  } else {
    const team = details.team;
    body = (
      <>
        <Typography sx={{ fontSize: ds.text.bodyLg }}>
          Connect team <b>{team}</b> to workspace <b>{tenant?.name}</b>?
        </Typography>
        <Card variant='tinted' tone='neutral' size='sm'>
          <Box
            component='dl'
            sx={{
              m: 0,
              display: 'grid',
              gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
              rowGap: ds.space[2],
              columnGap: ds.space[4],
              fontSize: ds.text.body,
            }}
          >
            <Box component='dt' sx={{ color: ds.gray[600] }}>
              Microsoft Teams team
            </Box>
            <Box component='dd' sx={{ m: 0 }}>
              {team}
            </Box>
            <Box component='dt' sx={{ color: ds.gray[600] }}>
              {brand} workspace
            </Box>
            <Box component='dd' sx={{ m: 0 }}>
              {tenant?.name}
            </Box>
          </Box>
        </Card>
        <Typography sx={{ fontSize: ds.text.body, color: ds.gray[600] }}>
          Wrong workspace? Switch workspace in {brand}, then open the link from Teams again.
        </Typography>
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[2] }}>
          <Button tone='secondary' href={INTEGRATIONS_PATH} data-testid='ms-teams-connect-cancel-btn'>
            Cancel
          </Button>
          <Button
            onClick={connect}
            disabled={status !== 'authenticated' || submitting}
            loading={submitting}
            data-testid='ms-teams-connect-confirm-btn'
          >
            Connect
          </Button>
        </Box>
      </>
    );
  }

  return (
    <>
      <Head>
        <meta name='referrer' content='no-referrer' />
      </Head>
      <Box
        data-testid='ms-teams-connect-page'
        sx={{
          minHeight: '100vh',
          backgroundColor: ds.background[200],
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          gap: ds.space[5],
          p: ds.space[4],
        }}
      >
        <Typography
          component='div'
          sx={{ fontFamily: ds.font.display, fontSize: ds.text.heading, fontWeight: ds.weight.semibold, color: ds.brand[600] }}
        >
          {brand}
        </Typography>
        <Card variant='outlined' size='lg' sx={{ width: '100%', maxWidth: 440 }}>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }} data-testid='ms-teams-connect-body'>
            <Typography component='h1' sx={{ fontFamily: ds.font.display, fontSize: ds.text.heading, fontWeight: ds.weight.semibold }}>
              Connect Microsoft Teams
            </Typography>
            {body}
          </Box>
        </Card>
      </Box>
    </>
  );
}

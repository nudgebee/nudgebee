import crypto from 'crypto';
import type { GetServerSideProps } from 'next';
import { Box, Paper, Typography } from '@mui/material';
import { Button } from '@ui/Button';
import { ds } from '@utils/colors';

type Outcome = 'confirm' | 'recorded' | 'already_processed' | 'invalid_link' | 'invalid_decision' | 'error';

interface Props {
  outcome: Outcome;
  status?: string;
  detail?: string;
}

const WORKFLOW_SERVER_URL = process.env.WORKFLOW_SERVER_URL ?? 'http://workflow-server:8000';
const APPROVAL_SIGNING_KEY = process.env.APPROVAL_SIGNING_KEY ?? '';
const APPROVAL_TIMEOUT_MS = 30_000;

function verifySignature(token: string, status: string, approver: string, sig: string): boolean {
  if (!APPROVAL_SIGNING_KEY) {
    console.error('approval link rejected: APPROVAL_SIGNING_KEY env var is not set on the app server');
    return false;
  }
  if (!sig) {
    return false;
  }
  const mac = crypto.createHmac('sha256', APPROVAL_SIGNING_KEY);
  mac.update(`${token}|${status}|${approver}`);
  const expected = `v0=${mac.digest('hex')}`;
  const a = Buffer.from(expected);
  const b = Buffer.from(sig);
  if (a.length !== b.length) {
    return false;
  }
  return crypto.timingSafeEqual(a, b);
}

// Same labels as the email's buttons: "needs-info" -> "Needs info".
function decisionLabel(status: string): string {
  const spaced = status.replace(/[-_]/g, ' ');
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

export const getServerSideProps: GetServerSideProps<Props> = async (ctx) => {
  ctx.res.setHeader('X-Robots-Tag', 'noindex, nofollow');
  ctx.res.setHeader('Cache-Control', 'no-store');

  const token = Array.isArray(ctx.params?.token) ? ctx.params?.token[0] : ctx.params?.token;
  const status = typeof ctx.query.status === 'string' ? ctx.query.status : '';
  const approver = typeof ctx.query.approver === 'string' ? ctx.query.approver : '';
  const sig = typeof ctx.query.sig === 'string' ? ctx.query.sig : '';

  if (!token || !status || !approver || !sig) {
    return { props: { outcome: 'invalid_link', detail: 'This approval link is missing required parameters.' } };
  }

  if (!verifySignature(token, status, approver, sig)) {
    return { props: { outcome: 'invalid_link', detail: 'The signature on this approval link could not be verified.' } };
  }

  // Opening the link only asks; mail scanners that fetch every link must not
  // decide the run. The confirm form posts back to this URL.
  if (ctx.req.method !== 'POST') {
    return { props: { outcome: 'confirm', status } };
  }

  try {
    const resp = await fetch(`${WORKFLOW_SERVER_URL}/approvals/${encodeURIComponent(token)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      signal: AbortSignal.timeout(APPROVAL_TIMEOUT_MS),
      body: JSON.stringify({
        status,
        approver,
        sig,
        result: { approver: `email:${approver}` },
      }),
    });

    if (resp.status === 200 || resp.status === 201) {
      return { props: { outcome: 'recorded', status } };
    }
    if (resp.status === 409) {
      return { props: { outcome: 'already_processed' } };
    }
    if (resp.status === 400) {
      const body = await resp.json().catch(() => ({}));
      return { props: { outcome: 'invalid_decision', detail: body.error ?? 'Invalid decision.' } };
    }
    const body = await resp.text().catch(() => '');
    console.error('approval proxy failed', resp.status, body);
    return { props: { outcome: 'error', detail: 'Upstream error while recording your decision.' } };
  } catch (err) {
    console.error('approval proxy error', err);
    return { props: { outcome: 'error', detail: 'Network error while recording your decision.' } };
  }
};

const HEADINGS: Record<Outcome, string> = {
  confirm: 'Confirm your decision',
  recorded: 'Decision recorded',
  already_processed: 'Already processed',
  invalid_link: 'Invalid link',
  invalid_decision: 'Invalid decision',
  error: 'Something went wrong',
};

export default function WorkflowApprovalPage({ outcome, status, detail }: Readonly<Props>): JSX.Element {
  const heading = HEADINGS[outcome];
  let body: React.ReactNode;
  switch (outcome) {
    case 'confirm':
      body = (
        <>
          You chose <strong>{decisionLabel(status ?? '')}</strong> in the approval email. Nothing is recorded until you confirm.
        </>
      );
      break;
    case 'recorded':
      body = (
        <>
          You recorded <strong>{status}</strong> as the decision. The workflow is resuming now.
        </>
      );
      break;
    case 'already_processed':
      body = 'This approval has already been recorded. No further action is needed.';
      break;
    case 'invalid_link':
    case 'invalid_decision':
    case 'error':
      body = detail ?? 'Please contact your administrator.';
      break;
  }

  return (
    <Box
      data-testid='workflow-approval-page'
      sx={{
        minHeight: '100vh',
        bgcolor: 'var(--ds-brand-100)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        p: ds.space[4],
      }}
    >
      <Paper
        elevation={0}
        sx={{
          maxWidth: ds.space.mul(4, 30),
          width: '100%',
          p: { xs: ds.space[6], sm: ds.space.mul(2, 5) },
          borderRadius: ds.radius.sm,
          boxShadow: '0 1px 3px rgba(0,0,0,0.06), 0 4px 20px rgba(0,0,0,0.04)',
        }}
      >
        <Box
          sx={{
            height: ds.space[1],
            width: ds.space.mul(2, 7),
            bgcolor: ds.amber[400],
            borderRadius: ds.radius.sm,
            mb: ds.space.mul(1, 5),
          }}
        />
        <Typography
          variant='h5'
          component='h1'
          sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-heading)', mb: ds.space[3], color: ds.blue[500] }}
          data-testid='workflow-approval-heading'
        >
          {heading}
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', lineHeight: 1.6, color: ds.brand[500] }} data-testid='workflow-approval-body'>
          {body}
        </Typography>
        {outcome === 'confirm' && (
          <Box component='form' method='post' sx={{ mt: ds.space[6] }}>
            <Button type='submit' data-testid='workflow-approval-confirm-btn'>
              Confirm {decisionLabel(status ?? '')}
            </Button>
            <Typography sx={{ mt: ds.space[4], fontSize: 'var(--ds-text-body)', color: ds.gray[600] }}>
              Not what you meant? Just close this page.
            </Typography>
          </Box>
        )}
      </Paper>
    </Box>
  );
}

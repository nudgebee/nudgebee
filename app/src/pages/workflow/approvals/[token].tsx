import crypto from 'crypto';
import type { GetServerSideProps } from 'next';
import { Box, Paper, Typography } from '@mui/material';
import { colors } from '@utils/colors';

type Outcome = 'recorded' | 'already_processed' | 'invalid_link' | 'invalid_decision' | 'error';

interface Props {
  outcome: Outcome;
  status?: string;
  detail?: string;
}

const WORKFLOW_SERVER_URL = process.env.WORKFLOW_SERVER_URL ?? 'http://workflow-server:8000';
const APPROVAL_SIGNING_KEY = process.env.APPROVAL_SIGNING_KEY ?? '';

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

  try {
    const resp = await fetch(`${WORKFLOW_SERVER_URL}/approvals/${encodeURIComponent(token)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        status,
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
        bgcolor: '#F0F2F5',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        p: 2,
      }}
    >
      <Paper
        elevation={0}
        sx={{
          maxWidth: 480,
          width: '100%',
          p: { xs: 4, sm: 5 },
          borderRadius: 1,
          boxShadow: '0 1px 3px rgba(0,0,0,0.06), 0 4px 20px rgba(0,0,0,0.04)',
        }}
      >
        <Box
          sx={{
            height: 4,
            width: 56,
            bgcolor: colors.yellow,
            borderRadius: 1,
            mb: 2.5,
          }}
        />
        <Typography
          variant='h5'
          component='h1'
          sx={{ fontWeight: 600, fontSize: 22, mb: 1.5, color: colors.text.primary }}
          data-testid='workflow-approval-heading'
        >
          {heading}
        </Typography>
        <Typography sx={{ fontSize: 15, lineHeight: 1.6, color: colors.text.secondary }} data-testid='workflow-approval-body'>
          {body}
        </Typography>
      </Paper>
    </Box>
  );
}

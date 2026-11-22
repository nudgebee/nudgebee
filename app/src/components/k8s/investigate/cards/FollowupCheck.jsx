import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { Card } from '@ui/Card';
import { Link } from '@ui/Link';
import { formatClockCompact } from '@shared/format/Datetime';
import MarkDowns from '@shared/viewers/MarkDowns';
import { buildAskNudgebeeHref } from '@components/workflow/utils/llmChat';

// followupText says what a follow-up check found, that it is still running, or when it looks again (the
// agent asked for a re-check because it was too early to tell).
export function followupText(f) {
  if (!f?.done_at && f?.recheck_at) {
    const when = formatClockCompact(f.recheck_at);
    const why = f.recheck_reason ? ` ${f.recheck_reason}` : '';
    return `Too early to tell. Checking again${when ? ` at ${when}` : ' later'}.${why}`;
  }
  if (!f?.done_at) return 'Checking whether the last step fixed the problem…';
  switch (f.outcome) {
    case 'proposed':
      return 'The problem persists. A new plan is proposed above; confirm each command before it runs.';
    case 'reported':
      return f.summary ? 'Follow-up check finished. What it found:' : 'Follow-up check finished.';
    case 'stopped':
      return 'The follow-up check was stopped.';
    case 'needs_human':
      return f.reason === 'attempt_limit'
        ? 'Several steps were tried for this alert in the last 24 hours, so no new plan will be proposed. Decide what to try next.'
        : 'The follow-up check needs your input. Check the alert yourself.';
    case 'superseded':
      return 'A newer step replaced this check.';
    default:
      return 'The follow-up check could not complete. Check the alert yourself.';
  }
}

export default function FollowupCheck({ followup, accountId }) {
  return (
    <Card variant='tinted' tone='neutral' size='sm' elevation='flat'>
      <Box
        sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)', fontSize: 'var(--ds-text-small)' }}
        data-testid='remediation-followup'
      >
        <Box>{followupText(followup)}</Box>
        {/* The agent's answer is markdown; the full one is in the follow-up's conversation. */}
        {followup?.summary && ((followup.done_at && followup.outcome === 'reported') || (!followup.done_at && followup.recheck_at)) ? (
          <Box sx={{ maxHeight: 240, overflowY: 'auto' }} data-testid='remediation-followup-summary'>
            <MarkDowns data={followup.summary} />
          </Box>
        ) : null}
        {followup?.session ? (
          <Link href={buildAskNudgebeeHref(accountId, followup.session)} openInNew secondaryText>
            See the follow-up investigation
          </Link>
        ) : null}
      </Box>
    </Card>
  );
}

FollowupCheck.propTypes = {
  followup: PropTypes.shape({
    session: PropTypes.string,
    done_at: PropTypes.string,
    outcome: PropTypes.string,
    reason: PropTypes.string,
    summary: PropTypes.string,
    recheck_at: PropTypes.string,
    recheck_reason: PropTypes.string,
  }).isRequired,
  accountId: PropTypes.string,
};

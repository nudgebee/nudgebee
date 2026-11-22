import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { Card } from '@ui/Card';
import { Label } from '@ui/Label';
import { formatClockCompact } from '@shared/format/Datetime';
import { refireLabel, resolutionSummary } from './remediationLoop';

// What the Investigation tab says about the remediation loop (#40734): once the loop resolved the event, the cause,
// the fix that ran, who approved it and the check that proved it (or that the alert cleared before any change); on a
// re-fired event, whether the earlier fix did not hold. Both come from labels only the loop writes, so with the
// tenant's loop off this renders nothing.
export default function RemediationResolutionCard({ labels, nbStatus, remediation, resolutions }) {
  const summary = resolutionSummary({ labels, nbStatus, res: remediation, rows: resolutions });
  const refire = refireLabel(labels);
  if (!summary && !refire) return null;
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)', mb: 'var(--ds-space-4)' }}>
      {refire ? (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }} data-testid='remediation-refire-label'>
          <Label tone={refire.tone} size='sm' text={refire.text} />
          <Box component='span' sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>
            {refire.text === 'Fix did not hold'
              ? 'This alert was resolved by a fix within the last day and fired again for the same cause.'
              : 'This alert was resolved within the last day and fired again.'}
          </Box>
        </Box>
      ) : null}
      {summary ? (
        <Card variant='tinted' tone='success' size='sm' elevation='flat' data-testid='remediation-resolution-card'>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)', fontSize: 'var(--ds-text-small)' }}>
            <Box sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-green-700)' }}>
              {summary.clearedBeforeChange ? '✓ Resolved · cleared before the change' : '✓ Resolved by a fix'}
              {summary.resolvedAt ? ` · ${formatClockCompact(summary.resolvedAt)}` : ''}
            </Box>
            {[
              ['Cause', summary.cause],
              [summary.clearedBeforeChange ? 'Change' : 'Fix', summary.fix],
              ['Proof', summary.proof],
              [summary.clearedBeforeChange ? 'Approved by' : 'Fixed by', summary.fixedBy],
            ]
              .filter(([, v]) => v)
              .map(([k, v]) => (
                <Box key={k} sx={{ display: 'flex', gap: 'var(--ds-space-3)' }}>
                  <Box sx={{ minWidth: 80, color: 'var(--ds-gray-600)', flexShrink: 0 }}>{k}</Box>
                  <Box sx={{ wordBreak: 'break-word', fontFamily: k === 'Fix' || k === 'Change' ? 'var(--ds-font-mono)' : undefined }}>{v}</Box>
                </Box>
              ))}
            {summary.clearedBeforeChange ? (
              <Box sx={{ color: 'var(--ds-gray-600)' }}>
                The alert cleared before the change took effect, so the change is not credited as the fix.
              </Box>
            ) : null}
          </Box>
        </Card>
      ) : null}
    </Box>
  );
}

RemediationResolutionCard.propTypes = {
  labels: PropTypes.object,
  nbStatus: PropTypes.string,
  remediation: PropTypes.object,
  resolutions: PropTypes.array,
};

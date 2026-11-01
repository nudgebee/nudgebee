import React from 'react';
import PropTypes from 'prop-types';
import dayjs from 'dayjs';
import { Box } from '@mui/material';
import Tooltip from '@ui/Tooltip';
import { Label } from '@ui/Label';
import { Chip } from '@ui/Chip';
import Divider from '@ui/Divider';
import { ds } from '@utils/colors';
import { describeFollowup } from '../utils/followupRecord';

const OUTCOME = {
  approved: { text: 'Approved', tone: 'success' },
  denied: { text: 'Denied', tone: 'critical' },
  answered: { text: 'Answered', tone: 'info' },
  dismissed: { text: 'Dismissed', tone: 'warning' },
  unanswered: { text: 'No answer', tone: 'neutral' },
};

// `DD-MMM HH:mm` in the browser's local timezone, as on the response meta-rail.
const formatTime = (iso) => {
  if (!iso) {
    return null;
  }
  const d = dayjs(iso);
  return d.isValid() ? d.format('DD-MMM HH:mm') : null;
};

const clamp = (lines) => ({
  display: '-webkit-box',
  WebkitLineClamp: lines,
  WebkitBoxOrient: 'vertical',
  overflow: 'hidden',
  wordBreak: 'break-word',
});

const fullTextTooltip = (text) => (
  <Box sx={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', maxHeight: ds.space.mul(2, 10), overflow: 'auto' }}>{text}</Box>
);

const FollowupRow = ({ followup }) => {
  const record = describeFollowup(followup);
  const outcome = OUTCOME[record.outcome];
  const askedAt = formatTime(record.askedAt);
  const endedAt = formatTime(record.endedAt);
  const kind = record.isApproval ? 'Approval requested' : 'Clarification';
  // For an approval the command is what the user judged; the "Tool(x) is trying to…"
  // preamble only repeats the tool name, so show it only when there is no command.
  const showQuestion = record.question && !(record.isApproval && record.command);

  return (
    <Box
      data-testid='followup-history-item'
      data-outcome={record.outcome}
      sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1], minWidth: 0 }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
        <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>
          {kind}
        </Box>
        {record.isApproval && record.toolName && (
          <Box component='span' sx={{ fontFamily: ds.font.mono }}>
            {record.toolName}
          </Box>
        )}
        {record.target && (
          <Tooltip
            title={`${record.target.kind ? `${record.target.kind} ` : ''}${record.target.name}${
              record.target.integration ? `, ${record.target.integration}` : ''
            }`}
            placement='top'
          >
            <Box component='span' sx={{ display: 'inline-flex' }} data-testid='followup-target'>
              {/* Same colours as the "ran on" chip: another account stands out, the conversation's own stays grey. */}
              <Chip variant='tag' size='xs' tone={record.target.crossAccount ? 'info' : 'neutral'}>
                {[record.target.name, record.target.integration].filter(Boolean).join(' · ')}
              </Chip>
            </Box>
          </Tooltip>
        )}
        {askedAt && <Box component='span'>{askedAt}</Box>}
      </Box>
      {showQuestion && (
        <Tooltip title={fullTextTooltip(record.question)}>
          <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', ...clamp(2) }}>{record.question}</Box>
        </Tooltip>
      )}
      {record.command && (
        <Tooltip title={fullTextTooltip(record.command)}>
          <Box
            component='code'
            sx={{
              fontFamily: ds.font.mono,
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-gray-700)',
              background: 'var(--ds-background-200)',
              borderRadius: ds.radius.sm,
              padding: `${ds.space[1]} ${ds.space[2]}`,
              ...clamp(2),
            }}
          >
            {record.command}
          </Box>
        </Tooltip>
      )}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], minWidth: 0 }}>
        <Label tone={outcome.tone}>{outcome.text}</Label>
        {record.detail && (
          <Tooltip title={fullTextTooltip(record.detail)}>
            <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', minWidth: 0, ...clamp(1) }}>{record.detail}</Box>
          </Tooltip>
        )}
        {endedAt && (
          <Box component='span' sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', flexShrink: 0 }}>
            {endedAt}
          </Box>
        )}
      </Box>
    </Box>
  );
};

FollowupRow.propTypes = {
  followup: PropTypes.object.isRequired,
};

/**
 * The questions and approval requests of a finished turn, with how each ended —
 * the body of the drawer behind the response meta-rail's approvals/clarifications pills.
 */
const FollowupHistory = ({ followups }) => {
  if (!followups?.length) {
    return null;
  }
  return (
    <Box
      data-testid='followup-history'
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: ds.space[4],
      }}
    >
      {followups.map((f, i) => (
        <React.Fragment key={f.originalIndex ?? i}>
          {i > 0 && <Divider />}
          <FollowupRow followup={f} />
        </React.Fragment>
      ))}
    </Box>
  );
};

FollowupHistory.propTypes = {
  followups: PropTypes.array,
};

export default FollowupHistory;

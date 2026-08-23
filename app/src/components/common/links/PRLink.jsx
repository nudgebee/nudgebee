import { Box } from '@mui/material';
import React from 'react';
import CallMergeIcon from '@mui/icons-material/CallMerge';
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline';
import HourglassEmptyIcon from '@mui/icons-material/HourglassEmpty';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import PropTypes from 'prop-types';
import Tooltip from '@ui/Tooltip';

const extractPRIdentifier = (url) => {
  if (!url) return url;
  const match = url.match(/\/pull\/(\d+)/);
  if (match) return `#${match[1]}`;
  const parts = url.split('/').filter(Boolean);
  return parts.length > 0 ? `#${parts[parts.length - 1]}` : url;
};

/**
 * Deep link to the Resolutions listing scoped to one recommendation, where the
 * full failure reason and retry live. Returns '' when there is no id to scope
 * by, so callers can pass it straight through as an optional href.
 */
export const resolutionsDeepLink = (recommendationId) => (recommendationId ? `/optimise?id=${recommendationId}#resolutions` : '');

const TONES = {
  success: { bg: 'var(--ds-green-100)', bgHover: 'var(--ds-green-200)', border: 'var(--ds-green-200)', fg: 'var(--ds-green-600)' },
  failed: { bg: 'var(--ds-red-100)', bgHover: 'var(--ds-red-200)', border: 'var(--ds-red-200)', fg: 'var(--ds-red-600)' },
  neutral: { bg: 'var(--ds-gray-100)', bgHover: 'var(--ds-gray-200)', border: 'var(--ds-gray-200)', fg: 'var(--ds-gray-600)' },
};

const Chip = ({ tone, icon: Icon, label, tooltip, href }) => {
  const palette = TONES[tone];
  const linkProps = href ? { component: 'a', href, target: '_blank', rel: 'noopener noreferrer' } : {};

  return (
    <Tooltip title={tooltip} arrow>
      <Box
        {...linkProps}
        onClick={(e) => e.stopPropagation()}
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: 'var(--ds-space-1)',
          backgroundColor: palette.bg,
          border: `1px solid ${palette.border}`,
          borderRadius: 'var(--ds-radius-sm)',
          padding: 'var(--ds-space-1) var(--ds-space-2)',
          textDecoration: 'none',
          cursor: href ? 'pointer' : 'default',
          mt: 'var(--ds-space-1)',
          transition: 'background-color 120ms ease',
          '&:hover': {
            backgroundColor: href ? palette.bgHover : palette.bg,
          },
        }}
      >
        <Icon sx={{ fontSize: 'var(--ds-text-small)', color: palette.fg }} />
        <Box
          component='span'
          sx={{
            fontSize: 'var(--ds-text-caption)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: palette.fg,
            lineHeight: 'var(--ds-text-caption-lh)',
          }}
        >
          {label}
        </Box>
      </Box>
    </Tooltip>
  );
};

Chip.propTypes = {
  tone: PropTypes.oneOf(['success', 'failed', 'neutral']).isRequired,
  icon: PropTypes.elementType.isRequired,
  label: PropTypes.node.isRequired,
  tooltip: PropTypes.node,
  href: PropTypes.string,
};

// Chip shown for a resolution that has no PR url yet, keyed by resolution status.
// `Success` means no PR was raised at all — a PR that was raised successfully
// keeps its resolution InProgress and carries a url, so it never lands here.
const STATE_BY_STATUS = {
  Failed: { tone: 'failed', icon: ErrorOutlineIcon, label: 'PR Failed', fallbackTooltip: 'PR creation failed' },
  InProgress: { tone: 'neutral', icon: HourglassEmptyIcon, label: 'PR Pending', fallbackTooltip: 'PR creation is in progress' },
  Success: { tone: 'neutral', icon: CheckCircleOutlineIcon, label: 'No PR Needed', fallbackTooltip: 'No change was required' },
};

/**
 * Whether PRLink will render anything for this resolution. Callers that wrap it
 * in their own chrome (a bordered container, a section heading) must check this
 * first, otherwise an unrecognised status renders the chrome around nothing.
 */
export const hasRenderablePRState = (resolution) => Boolean(resolution?.type_reference_id || STATE_BY_STATUS[resolution?.status]);

/**
 * Renders the outcome of a "raise a PR" resolution.
 *
 * PR creation is asynchronous: the resolution row exists (and the task that
 * requested it has already completed) long before a PR url does, and a failed
 * run never gets one at all. Rendering only on `prURL` therefore hides both the
 * pending and the failed outcome behind an empty cell, so `status` drives the
 * fallback states. `resolutionHref` makes those states clickable, pointing at
 * the Resolutions listing where the full failure reason lives.
 *
 * Callers that pass only `prURL` (no resolution to report on) keep the previous
 * behaviour: a chip when there is a url, nothing otherwise.
 */
const PRLink = ({ prURL, statusMessage, status, resolutionHref }) => {
  if (prURL) {
    return (
      <Chip
        tone='success'
        icon={CallMergeIcon}
        label={`PR ${extractPRIdentifier(prURL)}`}
        tooltip={statusMessage || 'Open Pull Request'}
        href={prURL}
      />
    );
  }

  const state = STATE_BY_STATUS[status];
  if (!state) {
    return null;
  }

  return <Chip tone={state.tone} icon={state.icon} label={state.label} tooltip={statusMessage || state.fallbackTooltip} href={resolutionHref} />;
};

PRLink.propTypes = {
  prURL: PropTypes.string,
  statusMessage: PropTypes.string,
  status: PropTypes.string,
  resolutionHref: PropTypes.string,
};

export default PRLink;

import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import Tooltip from '@ui/Tooltip';
import { Chip } from '@ui/Chip';

const TARGET_KIND = {
  kubernetes: 'cluster',
  aws: 'AWS account',
  gcp: 'GCP account',
  azure: 'Azure account',
  vm: 'VM account',
};

// Step statuses that mean the call did not complete, so it "tried" rather than "ran".
const FAILED_STATUSES = ['error', 'fail', 'failed'];

/**
 * Where a tool call ran, from its persisted metadata (`nb_executed_target`).
 * Set only when the call ran on a cluster or account other than the
 * conversation's own; returns null otherwise.
 */
export const parseExecutedTarget = (metadata) => {
  if (!metadata) {
    return null;
  }
  let parsed = metadata;
  if (typeof parsed === 'string') {
    try {
      parsed = JSON.parse(parsed);
    } catch {
      return null;
    }
  }
  const target = parsed?.nb_executed_target;
  return target?.name ? target : null;
};

// Values llm-server has written into nb_resolved_target that are not integration
// names: the "no config" sentinel and an agent-as-tool's own name. Skipped so
// they never render as an integration (being removed at the source, #38588).
const NOT_AN_INTEGRATION = ['NoConfigToolsFound', 'Agent'];

// How the integration was chosen (nb_resolved_by), when llm-server recorded it.
const RESOLVED_BY = {
  followup: 'you picked it',
  explicit_name_match: 'named in the request',
  sole_config: 'the only one configured',
};

// Metadata arrives as a JSON string or an already-parsed object. Parse it once
// and hand the object to the readers below (they accept either form).
const parseMetadataOnce = (metadata) => {
  if (!metadata || typeof metadata !== 'string') {
    return metadata || null;
  }
  try {
    return JSON.parse(metadata);
  } catch {
    return null;
  }
};

/** The integration a tool call used (`nb_resolved_target`), or null. */
export const parseResolvedIntegration = (metadata) => {
  const name = parseMetadataOnce(metadata)?.nb_resolved_target;
  return typeof name === 'string' && name.trim() && !NOT_AN_INTEGRATION.includes(name) ? name : null;
};

/** One key per account + integration, for "did these calls share a target?". */
export const targetKey = (metadata) => {
  const parsed = parseMetadataOnce(metadata);
  const account = parseExecutedTarget(parsed)?.name ?? '';
  const integration = parseResolvedIntegration(parsed) ?? '';
  return account || integration ? `${account}|${integration}` : '';
};

/**
 * Small tag naming where a step ran: the cluster or account (only when it is not
 * the conversation's own) and the integration it used, e.g. `k8s-prod · prod`.
 * An own-account step shows the integration alone, in neutral grey.
 */
const ExecutedTargetChip = ({ metadata, status }) => {
  const parsed = React.useMemo(() => parseMetadataOnce(metadata), [metadata]);
  const target = parseExecutedTarget(parsed);
  const resolved = parseResolvedIntegration(parsed);
  // A cluster's kubectl integration is named after the cluster; don't print it twice.
  const integration = resolved && resolved !== target?.name ? resolved : null;
  if (!target && !integration) {
    return null;
  }
  const kind = target ? TARGET_KIND[target.provider] || 'account' : null;
  // Failed steps carry their target too (#39951); don't claim they ran there.
  const failed = typeof status === 'string' && FAILED_STATUSES.includes(status.toLowerCase());
  let title;
  if (target) {
    title = `${failed ? 'Tried on' : 'Ran on'} the ${kind} ${target.name}`;
    if (integration) {
      title += `, using the ${integration} integration`;
    }
  } else {
    title = `${failed ? 'Tried' : 'Used'} the ${integration} integration`;
  }
  const resolvedBy = integration ? RESOLVED_BY[parsed?.nb_resolved_by] : null;
  if (resolvedBy) {
    title += ` (${resolvedBy})`;
  }
  const label = [target?.name, integration].filter(Boolean).join(' · ');
  // One fixed colour for another account: a per-name hue could land on red and read
  // as an error next to the step's status. Own-account integrations stay grey.
  const colour = target ? { tone: 'info' } : { tone: 'neutral' };
  return (
    <Tooltip title={title} placement='top'>
      <Box component='span' sx={{ display: 'inline-flex' }} data-testid='executed-target-chip'>
        <Chip variant='tag' size='xs' {...colour}>
          {label}
        </Chip>
      </Box>
    </Tooltip>
  );
};

ExecutedTargetChip.propTypes = {
  metadata: PropTypes.oneOfType([PropTypes.string, PropTypes.object]),
  // The step's status; a failed step's tooltip says "Tried on" instead of "Ran on".
  status: PropTypes.string,
};

export default ExecutedTargetChip;

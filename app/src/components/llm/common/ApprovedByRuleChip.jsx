import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import Tooltip from '@ui/Tooltip';
import { Chip } from '@ui/Chip';

/**
 * The tool approval rule that let a step run without asking, from its persisted
 * metadata (`nb_approved_by_rule`); null when the step was not auto-approved.
 */
export const parseApprovedByRule = (metadata) => {
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
  const rule = parsed?.nb_approved_by_rule;
  return rule?.rule_name ? rule : null;
};

/** Small tag on a step that ran without a confirmation because a rule approved it. */
const ApprovedByRuleChip = ({ metadata }) => {
  const rule = React.useMemo(() => parseApprovedByRule(metadata), [metadata]);
  if (!rule) {
    return null;
  }
  const revision = rule.rule_revision ? ` (revision ${rule.rule_revision})` : '';
  return (
    <Tooltip title={`Ran without asking: approved by the rule "${rule.rule_name}"${revision}`} placement='top'>
      <Box component='span' sx={{ display: 'inline-flex' }} data-testid='approved-by-rule-chip'>
        <Chip variant='tag' size='xs' tone='success'>
          Auto-approved
        </Chip>
      </Box>
    </Tooltip>
  );
};

ApprovedByRuleChip.propTypes = {
  metadata: PropTypes.oneOfType([PropTypes.string, PropTypes.object]),
};

export default ApprovedByRuleChip;

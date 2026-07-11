import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { Chip } from '@ui/Chip';
import Text from '@shared/format/Text';

// Renders the effective owner from a resolved OwnerResult. Shows owner_name as a
// chip with a muted hint when the owner is inherited (via namespace/cluster) or
// derived from a rule. Renders an em dash when unowned. Reads owner_name off the
// response — no directory lookup. onClick (when provided) opens the assign modal.
const VIA_HINT = { namespace: 'via namespace', cluster: 'via cluster' };

export default function OwnerBadge({ owner, onClick }) {
  if (!owner || !owner.found) {
    if (onClick) {
      return (
        <Chip variant='tag' size='xs' tone='neutral' onClick={onClick}>
          — Assign
        </Chip>
      );
    }
    return <Text value='—' secondaryText />;
  }

  const isGroup = owner.owner_type === 'group';
  let hint = '';
  if (owner.source === 'rule') {
    hint = 'rule';
  } else if (owner.via && owner.via !== 'self') {
    hint = VIA_HINT[owner.via] || `via ${owner.via}`;
  }

  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, minWidth: 0 }}>
      <Chip variant='tag' size='xs' tone={isGroup ? 'info' : 'neutral'} onClick={onClick}>
        {owner.owner_name || owner.owner_id}
      </Chip>
      {hint ? <Text value={hint} secondaryText showAutoEllipsis /> : null}
    </Box>
  );
}

OwnerBadge.propTypes = {
  owner: PropTypes.shape({
    found: PropTypes.bool,
    owner_type: PropTypes.string,
    owner_id: PropTypes.string,
    owner_name: PropTypes.string,
    source: PropTypes.string,
    via: PropTypes.string,
  }),
  onClick: PropTypes.func,
};

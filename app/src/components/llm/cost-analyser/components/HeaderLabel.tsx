/**
 * HeaderLabel — table header cell with an optional gray secondary label and an
 * info (ⓘ) tooltip rendered BEFORE the sort caret. Pass via a CustomTable2
 * header's `component` (keep `name` for sort-key matching).
 */
import * as React from 'react';
import { Box } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import Tooltip from '@ui/Tooltip';

export function HeaderLabel({ label, info, secondary }: { label: string; info: string; secondary?: string }) {
  return (
    <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}>
      {label}
      {secondary && (
        <Box component='span' sx={{ color: 'var(--ds-gray-500)', fontWeight: 'var(--ds-font-weight-regular)', fontSize: 'var(--ds-text-small)' }}>
          {secondary}
        </Box>
      )}
      <Tooltip title={info}>
        <Box
          component='span'
          onClick={(e) => e.stopPropagation()}
          sx={{ display: 'inline-flex', alignItems: 'center', color: 'var(--ds-gray-400)', cursor: 'help' }}
        >
          <InfoOutlinedIcon sx={{ fontSize: 13 }} />
        </Box>
      </Tooltip>
    </Box>
  );
}

export default HeaderLabel;

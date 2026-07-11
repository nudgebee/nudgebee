import { formatMemory } from '@lib/formatter';
import { Typography } from '@mui/material';
import Tooltip from '@ui/Tooltip';

export default function Memory({
  value,
  sourceUnit = 'bytes',
  targetUnit = 'gb',
  suffix = true,
  sx = { fontSize: 'var(--ds-text-body-lg)' },
  suffixSx = {
    color: 'var(--ds-gray-400)',
    fontSize: 'var(--ds-text-small)',
  },
}) {
  if (value == undefined || value == null) {
    return (
      <Typography sx={sx} display={'inline'}>
        -
      </Typography>
    );
  }
  return (
    <Tooltip title={formatMemory(value, sourceUnit, targetUnit, true, 6)}>
      <span>
        <Typography sx={sx} display={'inline'}>
          {formatMemory(value, sourceUnit, targetUnit, false)}
        </Typography>
        {suffix && (
          <Typography
            sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-500)', ...suffixSx }}
            display={'inline'}
            className='sufix'
          >
            {targetUnit.toUpperCase()}
          </Typography>
        )}
      </span>
    </Tooltip>
  );
}

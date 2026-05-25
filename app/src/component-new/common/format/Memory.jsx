import { formatMemory } from '@lib/formatter';
import { Typography } from '@mui/material';
import Tooltip from '@mui/material/Tooltip';
import { colors } from 'src/utils/colors';

export default function Memory({
  value,
  sourceUnit = 'bytes',
  targetUnit = 'gb',
  suffix = true,
  sx = { fontSize: 'var(--ds-text-body-lg)' },
  suffixSx = {
    color: colors.text.darkGray,
    fontSize: 'var(--ds-text-small)',
  },
}) {
  if (value == undefined || value == null) {
    return (
      <Tooltip>
        <Typography sx={sx} display={'inline'}>
          -
        </Typography>
      </Tooltip>
    );
  }
  return (
    <Tooltip title={value}>
      <>
        <Typography sx={sx} display={'inline'}>
          {formatMemory(value, sourceUnit, targetUnit, false)}
        </Typography>
        {suffix && (
          <Typography
            sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 400, color: colors.text.secondaryDark, ...suffixSx }}
            display={'inline'}
            className='sufix'
          >
            {targetUnit.toUpperCase()}
          </Typography>
        )}
      </>
    </Tooltip>
  );
}

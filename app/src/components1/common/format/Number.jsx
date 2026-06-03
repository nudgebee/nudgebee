import { formatNumber } from '@lib/formatter';
import { Typography } from '@mui/material';
import Tooltip from '@mui/material/Tooltip';
import { colors } from 'src/utils/colors';

export default function NumberComponent({
  value,
  defaultVal = '-',
  minimumFractionDigits = 0,
  maximumFractionDigits = 2,
  sx = {
    textAlign: 'right',
    fontSize: 'var(--ds-text-body-lg)',
    fontWeight: 'var(--ds-font-weight-regular)',
    color: colors.text.secondary,
  },
  suffix = '',
  suffixSx = {
    color: colors.text.numberComponent,
  },
}) {
  return (
    <Tooltip title={value}>
      <>
        <Typography sx={sx} display={'inline'}>
          {formatNumber(value, defaultVal, minimumFractionDigits, maximumFractionDigits)}
        </Typography>
        {suffix && (
          <Typography sx={suffixSx} display={'inline'} className='suffix'>
            {suffix}
          </Typography>
        )}
      </>
    </Tooltip>
  );
}

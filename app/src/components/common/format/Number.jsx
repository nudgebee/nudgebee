import { formatNumber } from '@lib/formatter';
import { Typography } from '@mui/material';
import Tooltip from '@ui/Tooltip';

export default function NumberComponent({
  value,
  defaultVal = '-',
  minimumFractionDigits = 0,
  maximumFractionDigits = 2,
  sx = {
    textAlign: 'right',
    fontSize: 'var(--ds-text-body-lg)',
    fontWeight: 'var(--ds-font-weight-regular)',
    color: 'var(--ds-gray-700)',
  },
  suffix = '',
  suffixSx = {
    color: 'var(--ds-gray-700)',
  },
}) {
  return (
    <Tooltip title={formatNumber(value, defaultVal, minimumFractionDigits, Math.max(6, maximumFractionDigits))}>
      <span>
        <Typography sx={sx} display={'inline'}>
          {formatNumber(value, defaultVal, minimumFractionDigits, maximumFractionDigits)}
        </Typography>
        {suffix && (
          <Typography sx={suffixSx} display={'inline'} className='suffix'>
            {suffix}
          </Typography>
        )}
      </span>
    </Tooltip>
  );
}

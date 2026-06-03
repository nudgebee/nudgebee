import { Box, Typography } from '@mui/material';
import CustomTooltip from 'src/components1/common/CustomTooltip';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

export function formatCurrency(value) {
  if (value === '') {
    return '-';
  }
  const parsedValue = parseFloat(value);
  if (!isNaN(parsedValue) && isFinite(parsedValue)) {
    return `$${parsedValue.toLocaleString('en-US', { minimumFractionDigits: 0, maximumFractionDigits: 2 })}`;
  }
  return '-';
}
export default function Currency({
  value,
  defaultVal = '-',
  suffix = '',
  prefix = '$',
  varient = 'default',
  sx = {},
  sxSuffix = {},
  sxPrefix = {},
  withTooltip = true,
  precison = 0,
  percent = '',
  isSavingPotential = false,
  recommendationLabel = 'This recommendation',
}) {
  const variantStyles = {
    default: {},
    savings: { color: 'green' },
    expense: { color: 'red' },
  };

  if (Object.entries(sx).length === 0) {
    sx = variantStyles[varient] || {};
  }

  if (value == null || value == undefined || isNaN(value) || value === '' || value === 0) {
    return withTooltip ? (
      <CustomTooltip title={defaultVal !== '-' ? defaultVal : ''} placement='bottom-start' tooltipStyle={{ margin: '0px' }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body)',
            color: colors.text.secondary,
            fontWeight: 'var(--ds-font-weight-regular)',
            lineHeight: 'auto',
            ...sx,
          }}
          display='inline'
        >
          {defaultVal}
        </Typography>
      </CustomTooltip>
    ) : (
      <Typography
        sx={{ fontSize: 'var(--ds-text-body)', color: colors.text.secondary, fontWeight: 'var(--ds-font-weight-regular)', lineHeight: 'auto', ...sx }}
        display='inline'
      >
        {defaultVal}
      </Typography>
    );
  }

  const formattedValue =
    isNaN(value) || value === '' || value === 0 || !isFinite(parseFloat(value))
      ? '-'
      : (() => {
          const formatted = parseFloat(value).toLocaleString('en-US', { minimumFractionDigits: precison, maximumFractionDigits: precison });
          // Avoid displaying "-0", "-0.0", "-0.00" etc. after rounding
          return parseFloat(formatted.replace(/,/g, '')) === 0 ? formatted.replace('-', '') : formatted;
        })();
  const actualValue = isNaN(value) || value === '' || value === 0 || !isFinite(parseFloat(value)) ? 0 : parseFloat(value);
  const displayValue = actualValue < 1 && actualValue >= 0 && precison === 0 ? '1' : formattedValue;

  const negPotentialSaving = -actualValue.toFixed(2);

  if (isSavingPotential && negPotentialSaving > 0) {
    const description = `${recommendationLabel} prioritizes system performance over cost reduction and may increase spend (~${prefix}${negPotentialSaving} ${
      suffix ? `${suffix}` : ''
    })`;
    return (
      <CustomTooltip title={description} placement='top'>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body)',
            color: colors.text.secondary,
            fontWeight: 'var(--ds-font-weight-regular)',
            letterSpacing: '0.5px',
            ...sx,
          }}
          display='inline'
        >
          {'~ '}
          {prefix}
          {'0'}
          {suffix && (
            <Typography
              component='span'
              sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, fontWeight: 'var(--ds-font-weight-regular)', ...sxSuffix }}
            >
              {suffix}
            </Typography>
          )}
        </Typography>
      </CustomTooltip>
    );
  }

  return withTooltip ? (
    <CustomTooltip
      title={Object.is(actualValue, -0) || (actualValue > -0.005 && actualValue < 0) ? '0.00' : actualValue.toFixed(2)}
      placement='bottom-start'
      tooltipStyle={{ margin: '0px' }}
    >
      <Box>
        {parseFloat(actualValue) < 1 && precison === 0 && (
          <Typography
            sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, fontWeight: 'var(--ds-font-weight-regular)', ...sxPrefix }}
            display='inline'
          >
            {'<'}
          </Typography>
        )}
        <Typography
          sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, fontWeight: 'var(--ds-font-weight-regular)', ...sxPrefix }}
          display='inline'
        >
          {prefix}
        </Typography>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body)',
            color: colors.text.secondary,
            fontWeight: 'var(--ds-font-weight-regular)',
            lineHeight: 'auto',
            ...sx,
          }}
          display='inline'
        >
          {displayValue}
        </Typography>
        {suffix && (
          <Typography
            sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, fontWeight: 'var(--ds-font-weight-regular)', ...sxSuffix }}
            display='inline'
          >
            {suffix}
          </Typography>
        )}
      </Box>
    </CustomTooltip>
  ) : (
    <Box>
      {parseFloat(actualValue) < 1 && (
        <Typography
          sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, fontWeight: 'var(--ds-font-weight-regular)', ...sxPrefix }}
          display='inline'
        >
          {'<'}
        </Typography>
      )}
      <Typography
        sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, fontWeight: 'var(--ds-font-weight-regular)', ...sxPrefix }}
        display='inline'
      >
        {prefix}
      </Typography>
      <Typography
        sx={{ fontSize: 'var(--ds-text-body)', color: colors.text.secondary, fontWeight: 'var(--ds-font-weight-regular)', lineHeight: 'auto', ...sx }}
        display='inline'
      >
        {displayValue}
      </Typography>
      {suffix && (
        <Typography
          sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, fontWeight: 'var(--ds-font-weight-regular)', ...sxSuffix }}
          display='inline'
        >
          {suffix}
        </Typography>
      )}
      {percent && (
        <Typography
          sx={{
            ...sx,
            marginLeft: 'var(--ds-space-2)',
            color: parseFloat(actualValue) > 0 ? colors.text.lowest : colors.text.red,
            fontSize: 'var(--ds-text-heading)',
          }}
          display='inline'
        >
          ({percent})
        </Typography>
      )}
    </Box>
  );
}

Currency.propTypes = {
  value: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  defaultVal: PropTypes.string,
  suffix: PropTypes.string,
  prefix: PropTypes.string,
  varient: PropTypes.string,
  sx: PropTypes.object,
  sxSuffix: PropTypes.any,
  sxPrefix: PropTypes.any,
  withTooltip: PropTypes.bool,
  precison: PropTypes.number,
  percent: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  isSavingPotential: PropTypes.bool,
  recommendationLabel: PropTypes.string,
};

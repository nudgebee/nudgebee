import { getDateDiff, convertToLocalTime } from '@lib/datetime';
import { Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import CustomTooltip from '@components1/common/CustomTooltip';

export default function Datetime({
  value,
  baseDate = new Date(),
  suffix = '',
  sx = {},
  sxSuffix = {},
  sxPrefix = {},
  emptyValue = '-',
  maxLevel = 1,
  showTooltip = true,
  prefix = '',
  sxSuffixSecondary = true,
  sxSecondary = false,
  sxPrefixSecondary = true,
}) {
  if (!value) {
    return (
      <Typography
        key='empty'
        display={'inline'}
        sx={{
          color: sxSuffixSecondary ? colors.text.secondaryDark : colors.text.secondary,
          fontSize: sxSuffixSecondary ? '12px' : '13px',
          fontWeight: 'var(--ds-font-weight-regular)',
          marginLeft: 'var(--ds-space-1)',
          ...sxSuffix,
        }}
      >
        {emptyValue}
      </Typography>
    );
  }

  let dateValue;
  if (value instanceof Date) {
    dateValue = value;
  } else if (typeof value === 'string') {
    const timezoneRegex = /([Zz]|[+-]\d{2}:?\d{2})$/;
    if (!timezoneRegex.test(value)) {
      value += 'Z';
    }
    dateValue = new Date(value);
  } else if (typeof value === 'number') {
    // detect if value is in nanoseconds
    // Anything > 1e12 is almost certainly nanoseconds (since ms since epoch is ~1.7e12 in 2025)
    if (value > 1e12 * 1e3) {
      // nanoseconds → milliseconds
      dateValue = new Date(Math.floor(value / 1e6));
    } else {
      // assume milliseconds
      dateValue = new Date(value);
    }
  } else {
    dateValue = new Date(value);
  }

  if (!baseDate) {
    baseDate = new Date();
  }

  const isFuture = dateValue > baseDate;
  let dateDiff = getDateDiff(dateValue, baseDate, isFuture ? 'future' : 'previous');
  const dayUnit = 'd';
  const hourUnit = 'h';
  const minuteUnit = 'm';
  const secondUnit = 's';

  // Create an array with the time units
  let _result = [];
  if (prefix) {
    _result.push(
      <Typography
        key='prefix'
        sx={{
          color: sxPrefixSecondary ? colors.text.secondaryDark : colors.text.secondary,
          fontSize: sxPrefixSecondary ? '12px' : '13px',
          fontWeight: 'var(--ds-font-weight-regular)',
          marginBottom: '0px',
          ...sxPrefix,
        }}
        display={'inline'}
      >
        {prefix}
      </Typography>
    );
  }

  let levelCount = 1;
  if (dateDiff.days > 0 && levelCount <= maxLevel) {
    _result.push(
      <Typography
        key='days'
        sx={{
          color: sxSecondary ? colors.text.secondaryDark : colors.text.secondary,
          fontSize: sxSecondary ? '12px' : '13px',
          fontWeight: 'var(--ds-font-weight-regular)',
          marginBottom: '0px',
          ...sx,
        }}
        display={'inline'}
      >
        {dateDiff.days}
      </Typography>
    );
    _result.push(
      <Typography
        key='dayUnit'
        sx={{
          color: sxSuffixSecondary ? colors.text.secondaryDark : colors.text.secondary,
          fontSize: sxSuffixSecondary ? '12px' : '13px',
          fontWeight: 'var(--ds-font-weight-regular)',
          marginBottom: '0px',
          ...sxSuffix,
        }}
        display={'inline'}
      >
        {dayUnit}
      </Typography>
    );
    levelCount = levelCount + 1;
  }
  if (dateDiff.hours > 0 && levelCount <= maxLevel) {
    if (_result.length > 0) {
      _result.push(
        <Typography
          key='hours'
          sx={{
            color: sxSuffixSecondary ? colors.text.secondaryDark : colors.text.secondary,
            fontSize: sxSuffixSecondary ? '12px' : '13px',
            fontWeight: 'var(--ds-font-weight-regular)',
            marginBottom: '0px',
            ...sxSuffix,
          }}
          display={'inline'}
        >
          {' '}
        </Typography>
      );
    }
    _result.push(
      <Typography
        key='hoursValue'
        sx={{
          color: sxSecondary ? colors.text.secondaryDark : colors.text.secondary,
          fontSize: sxSecondary ? '12px' : '13px',
          fontWeight: 'var(--ds-font-weight-regular)',
          marginBottom: '0px',
          ...sx,
        }}
        display={'inline'}
      >
        {dateDiff.hours}
      </Typography>
    );
    _result.push(
      <Typography
        key='hoursUnit'
        sx={{
          color: sxSuffixSecondary ? colors.text.secondaryDark : colors.text.secondary,
          fontSize: sxSuffixSecondary ? '12px' : '13px',
          fontWeight: 'var(--ds-font-weight-regular)',
          marginBottom: '0px',
          ...sxSuffix,
        }}
        display={'inline'}
      >
        {hourUnit}
      </Typography>
    );
    levelCount = levelCount + 1;
  }

  if (dateDiff.minutes > 0 && levelCount <= maxLevel) {
    if (_result.length > 0) {
      _result.push(
        <Typography
          key='minutes'
          sx={{
            color: sxSuffixSecondary ? colors.text.secondaryDark : colors.text.secondary,
            fontSize: sxSuffixSecondary ? '12px' : '13px',
            fontWeight: 'var(--ds-font-weight-regular)',
            marginBottom: '0px',
            ...sxSuffix,
          }}
          display={'inline'}
        >
          {' '}
        </Typography>
      );
    }
    _result.push(
      <Typography
        key='minutesValue'
        sx={{
          color: sxSecondary ? colors.text.secondaryDark : colors.text.secondary,
          fontSize: sxSecondary ? '12px' : '13px',
          fontWeight: 'var(--ds-font-weight-regular)',
          marginBottom: '0px',
          ...sx,
        }}
        display={'inline'}
      >
        {dateDiff.minutes}
      </Typography>
    );
    _result.push(
      <Typography
        key='minuteUnit'
        s
        sx={{
          color: sxSuffixSecondary ? colors.text.secondaryDark : colors.text.secondary,
          fontSize: sxSuffixSecondary ? '12px' : '13px',
          fontWeight: 'var(--ds-font-weight-regular)',
          marginBottom: '0px',
          ...sxSuffix,
        }}
        display={'inline'}
      >
        {minuteUnit}
      </Typography>
    );
    levelCount = levelCount + 1;
  }

  if (dateDiff.seconds > 0 && levelCount <= maxLevel) {
    if (_result.length > 0) {
      _result.push(
        <Typography
          key='seconds'
          sx={{
            color: sxSuffixSecondary ? colors.text.secondaryDark : colors.text.secondary,
            fontSize: sxSuffixSecondary ? '12px' : '13px',
            fontWeight: 'var(--ds-font-weight-regular)',
            marginBottom: '0px',
            ...sxSuffix,
          }}
          display={'inline'}
        >
          {' '}
        </Typography>
      );
    }
    _result.push(
      <Typography
        key='secondsValue'
        sx={{
          color: sxSecondary ? colors.text.secondaryDark : colors.text.secondary,
          fontSize: sxSecondary ? '12px' : '13px',
          fontWeight: 'var(--ds-font-weight-regular)',
          marginBottom: '0px',
          ...sx,
        }}
        display={'inline'}
      >
        {dateDiff.seconds}
      </Typography>
    );
    _result.push(
      <Typography
        key='secondUnit'
        sx={{
          color: sxSuffixSecondary ? colors.text.secondaryDark : colors.text.secondary,
          fontSize: sxSuffixSecondary ? '12px' : '13px',
          fontWeight: 'var(--ds-font-weight-regular)',
          marginBottom: '0px',
          ...sxSuffix,
        }}
        display={'inline'}
      >
        {secondUnit}
      </Typography>
    );
  }

  if (suffix) {
    if (isFuture) {
      _result.unshift(
        <Typography
          key='suffix'
          sx={{
            color: sxSuffixSecondary ? colors.text.secondaryDark : colors.text.secondary,
            fontSize: sxSuffixSecondary ? '12px' : '13px',
            fontWeight: 'var(--ds-font-weight-regular)',
            marginBottom: '0px',
            ...sxSuffix,
          }}
          display={'inline'}
        >
          {suffix}
        </Typography>
      );
    } else {
      _result.push(
        <Typography
          key='suffix'
          sx={{
            color: sxSuffixSecondary ? colors.text.secondaryDark : colors.text.secondary,
            fontSize: sxSuffixSecondary ? '12px' : '13px',
            fontWeight: 'var(--ds-font-weight-regular)',
            marginBottom: '0px',
            ...sxSuffix,
          }}
          display={'inline'}
        >
          {suffix}
        </Typography>
      );
    }
  }

  return (
    <>
      {showTooltip ? (
        <CustomTooltip title={convertToLocalTime(dateValue)}>
          <Typography component='div' display={'inline-flex'} alignItems={'center'}>
            {isFuture ? (
              <>
                <Typography
                  component='span'
                  sx={{
                    color: sxSuffixSecondary ? colors.text.secondaryDark : colors.text.secondary,
                    fontSize: sxSuffixSecondary ? '12px' : '13px',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    marginRight: 'var(--ds-space-1)',
                    ...sxSuffix,
                  }}
                >
                  in
                </Typography>
                {_result.map((r) => r)}
              </>
            ) : (
              <>
                {_result.map((r) => r)}
                <Typography
                  component='span'
                  sx={{
                    color: sxSuffixSecondary ? colors.text.secondaryDark : colors.text.secondary,
                    fontSize: sxSuffixSecondary ? '12px' : '13px',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    marginLeft: 'var(--ds-space-1)',
                    ...sxSuffix,
                  }}
                >
                  {_result?.length > 0 ? 'ago' : 'Just now'}
                </Typography>
              </>
            )}
          </Typography>
        </CustomTooltip>
      ) : (
        <Typography component='div' display={'inline-flex'} alignItems={'center'}>
          {isFuture ? (
            <>
              <Typography
                component='span'
                sx={{
                  color: sxSuffixSecondary ? colors.text.secondaryDark : colors.text.secondary,
                  fontSize: sxSuffixSecondary ? '12px' : '13px',
                  fontWeight: 'var(--ds-font-weight-regular)',
                  marginRight: 'var(--ds-space-1)',
                  ...sxSuffix,
                }}
              >
                in
              </Typography>
              {_result.map((r) => r)}
            </>
          ) : (
            <>
              {_result.map((r) => r)}
              <Typography
                component='span'
                sx={{
                  color: sxSuffixSecondary ? colors.text.secondaryDark : colors.text.secondary,
                  fontSize: sxSuffixSecondary ? '12px' : '13px',
                  fontWeight: 'var(--ds-font-weight-regular)',
                  marginLeft: 'var(--ds-space-1)',
                  ...sxSuffix,
                }}
              >
                {_result?.length > 0 ? 'ago' : 'Just now'}
              </Typography>
            </>
          )}
        </Typography>
      )}
    </>
  );
}

Datetime.propTypes = {
  value: PropTypes.oneOfType([PropTypes.string, PropTypes.instanceOf(Date), PropTypes.number]),
  baseDate: PropTypes.instanceOf(Date),
  suffix: PropTypes.string,
  sx: PropTypes.object,
  sxSuffix: PropTypes.object,
  emptyValue: PropTypes.string,
  maxLevel: PropTypes.number,
  showTooltip: PropTypes.bool,
  prefix: PropTypes.string,
  sxSuffixSecondary: PropTypes.bool,
  sxSecondary: PropTypes.bool,
  sxPrefixSecondary: PropTypes.bool,
  sxPrefix: PropTypes.object,
};

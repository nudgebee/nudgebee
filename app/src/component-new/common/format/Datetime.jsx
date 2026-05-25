import { Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import CustomTooltip from '@components1/common/CustomTooltip';

const ONE_MIN = 60_000;
const ONE_HOUR = 60 * ONE_MIN;
const ONE_DAY = 24 * ONE_HOUR;

const MONTH_ABBR = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const pad2 = (n) => String(n).padStart(2, '0');

function parseDateValue(value) {
  if (value instanceof Date) return value;
  if (typeof value === 'string') {
    const timezoneRegex = /([Zz]|[+-]\d{2}:?\d{2})$/;
    const v = timezoneRegex.test(value) ? value : value + 'Z';
    return new Date(v);
  }
  if (typeof value === 'number') {
    // Anything > ~1e15 is almost certainly nanoseconds (ms-since-epoch is ~1.7e12 in 2025).
    if (value > 1e12 * 1e3) return new Date(Math.floor(value / 1e6));
    return new Date(value);
  }
  return new Date(value);
}

function formatDateShort(date) {
  return `${pad2(date.getDate())}-${MONTH_ABBR[date.getMonth()]}`;
}

function formatTooltip(date) {
  const hh = pad2(date.getHours());
  const mm = pad2(date.getMinutes());
  const dd = pad2(date.getDate());
  const mmm = MONTH_ABBR[date.getMonth()];
  const tzPart = new Intl.DateTimeFormat(undefined, { timeZoneName: 'short' }).formatToParts(date).find((p) => p.type === 'timeZoneName');
  const tz = tzPart ? tzPart.value : '';
  return `${hh}:${mm} ${tz}, ${dd}-${mmm}`;
}

// Buckets per redesign:
//   < 1 min   → "0 m"            + ago/in
//   < 1 hour  → "X m"            + ago/in
//   < 1 day   → "X hr[s] [Y m]"  + ago/in   (drops "0 m" when minutes are zero)
//   < 3 days  → "X d"            + ago/in
//   ≥ 3 days  → "dd-mmm"          (absolute, no ago/in)
function formatRelative(deltaMs) {
  if (deltaMs < ONE_MIN) {
    return { mainText: '0m', useRelativeAffix: true };
  }
  if (deltaMs < ONE_HOUR) {
    const mins = Math.floor(deltaMs / ONE_MIN);
    return { mainText: `${mins}m`, useRelativeAffix: true };
  }
  if (deltaMs < ONE_DAY) {
    const hrs = Math.floor(deltaMs / ONE_HOUR);
    const mins = Math.floor((deltaMs % ONE_HOUR) / ONE_MIN);
    if (mins === 0) {
      return { mainText: `${hrs}h`, useRelativeAffix: true };
    }
    return { mainText: `${hrs}h ${mins}m`, useRelativeAffix: true };
  }
  if (deltaMs < 3 * ONE_DAY) {
    const days = Math.floor(deltaMs / ONE_DAY);
    return { mainText: `${days}d`, useRelativeAffix: true };
  }
  // ≥ 3 days: caller renders the absolute date instead of mainText.
  return { mainText: null, useRelativeAffix: false };
}

export default function Datetime({
  value,
  baseDate = new Date(),
  suffix = '',
  sx = {},
  sxSuffix = {},
  sxPrefix = {},
  emptyValue = '-',
  // eslint-disable-next-line no-unused-vars
  maxLevel = 1, // accepted for API stability; new bucket logic auto-selects unit count
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
          fontSize: sxSuffixSecondary ? 'var(--ds-text-small)' : 'var(--ds-text-body)',
          fontWeight: 400,
          marginLeft: '2px',
          ...sxSuffix,
        }}
      >
        {emptyValue}
      </Typography>
    );
  }

  const dateValue = parseDateValue(value);
  const ref = baseDate || new Date();
  const deltaMs = Math.abs(ref.getTime() - dateValue.getTime());
  const isFuture = dateValue > ref;

  const { mainText: relativeText, useRelativeAffix } = formatRelative(deltaMs);
  const isAbsolute = relativeText === null;
  const mainText = isAbsolute ? formatDateShort(dateValue) : relativeText;

  const valueStyle = {
    color: sxSecondary ? colors.text.secondaryDark : colors.text.secondary,
    fontSize: sxSecondary ? 'var(--ds-text-small)' : 'var(--ds-text-body)',
    fontWeight: 400,
    marginBottom: '0px',
    ...sx,
  };
  const prefixStyle = {
    color: sxPrefixSecondary ? colors.text.secondaryDark : colors.text.secondary,
    fontSize: sxPrefixSecondary ? 'var(--ds-text-small)' : 'var(--ds-text-body)',
    fontWeight: 400,
    marginRight: '2px',
    ...sxPrefix,
  };
  const affixStyle = {
    color: sxSuffixSecondary ? colors.text.secondaryDark : colors.text.secondary,
    fontSize: sxSuffixSecondary ? 'var(--ds-text-small)' : 'var(--ds-text-body)',
    fontWeight: 400,
    marginBottom: '0px',
    ...sxSuffix,
  };

  const body = (
    <Typography component='div' display={'flex'} alignItems={'center'}>
      {prefix && (
        <Typography component='span' sx={prefixStyle}>
          {prefix}
        </Typography>
      )}
      {isFuture && useRelativeAffix && (
        <Typography component='span' sx={{ ...affixStyle, marginRight: '2px' }}>
          in
        </Typography>
      )}
      <Typography component='span' sx={valueStyle}>
        {mainText}
      </Typography>
      {suffix && (
        <Typography component='span' sx={{ ...affixStyle, marginLeft: '2px' }}>
          {suffix}
        </Typography>
      )}
      {!isFuture && useRelativeAffix && (
        <Typography component='span' sx={{ ...affixStyle, marginLeft: '2px' }}>
          ago
        </Typography>
      )}
    </Typography>
  );

  if (!showTooltip) return body;

  return <CustomTooltip title={formatTooltip(dateValue)}>{body}</CustomTooltip>;
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

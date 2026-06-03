export function formatNumber(number: string | number, defaultVal = '-', minimumFractionDigits = 0, maximumFractionDigits = 2): string {
  const numericValue = typeof number === 'string' ? parseFloat(number) : number;
  return isFinite(numericValue) && number !== null && number !== '' && number !== 0
    ? numericValue.toLocaleString('en-US', {
        minimumFractionDigits: minimumFractionDigits,
        maximumFractionDigits: maximumFractionDigits,
      })
    : defaultVal;
}

export function formatMemory(value: number, sourceUnit = 'bytes', targetUnit = 'gb', suffix = true): string {
  let result: string;
  switch (`${sourceUnit}-${targetUnit}`) {
    case 'mb-gb':
      result = formatNumber(value / 1024);
      break;
    case 'bytes-mb':
      result = formatNumber(value / (1024 * 1024));
      break;
    case 'kb-gb':
      result = formatNumber(value / (1024 * 1024));
      break;
    case 'gb-gb':
      result = formatNumber(value);
      break;
    default:
      result = formatNumber(value / (1024 * 1024 * 1024));
      break;
  }
  if (suffix && value) {
    result += ' GB';
  }
  return result;
}

export function titleCase(value?: string): string {
  if (!value) {
    return '';
  }
  return value.replace(/_/g, ' ').replace(/\b\w/g, (match) => match.toUpperCase());
}

export function formatDate(milliseconds: number) {
  // Format the millisecond in this format %Y-%m-%dT%H:%M:%S.%fZ
  // For eg: 2024-02-15T04:35:31.000Z
  const date = new Date(milliseconds);
  const year = date.getUTCFullYear();
  const month = (date.getUTCMonth() + 1).toString().padStart(2, '0');
  const day = date.getUTCDate().toString().padStart(2, '0');
  const hours = date.getUTCHours().toString().padStart(2, '0');
  const minutes = date.getUTCMinutes().toString().padStart(2, '0');
  const seconds = date.getUTCSeconds().toString().padStart(2, '0');
  const millisecondsFormatted = date.getUTCMilliseconds().toString().padStart(3, '0');
  const formattedDate = `${year}-${month}-${day}T${hours}:${minutes}:${seconds}.${millisecondsFormatted}Z`;
  return formattedDate;
}

export function formatNumberAbbreviation(value: number) {
  if (value >= 10000000) {
    return (value / 10000000).toFixed(1) + 'Cr';
  } else if (value >= 100000) {
    return (value / 100000).toFixed(1) + 'L';
  } else if (value >= 1000) {
    return (value / 1000).toFixed(1) + 'K';
  }
  return value.toString();
}

export function convertToGB(valueStr: string, suffix = true) {
  const valueStrLower = valueStr.toLowerCase();
  let value;
  let gbValue;

  if (valueStrLower.includes('ki')) {
    value = parseInt(valueStr.slice(0, -2), 10);
    gbValue = value / 1024 ** 2;
  } else if (valueStrLower.includes('kb')) {
    value = parseInt(valueStr.slice(0, -2), 10);
    gbValue = value / 1000 ** 2;
  } else if (valueStrLower.includes('mi')) {
    value = parseInt(valueStr.slice(0, -2), 10);
    gbValue = value / 1024;
  } else if (valueStrLower.includes('mb')) {
    value = parseInt(valueStr.slice(0, -2), 10);
    gbValue = value / 1000;
  } else if (valueStrLower.includes('bytes')) {
    value = parseInt(valueStr.slice(0, -5), 10);
    gbValue = value / 1000 ** 3;
  } else {
    return valueStr;
  }

  return suffix ? gbValue.toFixed(2) + 'Gi' : gbValue.toFixed(2);
}

const formatDateWithTimezone = (date: Date): string => {
  const pad = (num: number) => String(num).padStart(2, '0');

  const year = date.getFullYear();
  const month = pad(date.getMonth() + 1);
  const day = pad(date.getDate());
  const hours = pad(date.getHours());
  const minutes = pad(date.getMinutes());
  const seconds = pad(date.getSeconds());

  const tzOffset = -date.getTimezoneOffset();
  const offsetHours = pad(Math.floor(Math.abs(tzOffset) / 60));
  const offsetMinutes = pad(Math.abs(tzOffset) % 60);
  const sign = tzOffset >= 0 ? '+' : '-';

  return `${year}-${month}-${day}T${hours}:${minutes}:${seconds}${sign}${offsetHours}:${offsetMinutes}`;
};

const formatter = {
  formatNumber: formatNumber,
  formatMemory: formatMemory,
  formatDate: formatDate,
  formatNumberAbbreviation: formatNumberAbbreviation,
  convertToGB: convertToGB,
  formatDateWithTimezone: formatDateWithTimezone,
};

export default formatter;

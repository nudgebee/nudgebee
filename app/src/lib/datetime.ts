// Converts a Date object or date string to a formatted date string
// Example: toDateString(new Date(2023, 0, 15)) -> "2023-1-15"
export function toDateString(date: Date | string, hour = false, minute = false, second = false): string {
  let parsed: Date;
  if (typeof date === 'string') {
    parsed = new Date(Date.parse(date));
  } else if (typeof date === 'object' && date != null) {
    parsed = date;
  } else {
    return '';
  }

  if (second) {
    return `${parsed.getFullYear()}-${parsed.getMonth() + 1}-${parsed.getDate()} ${parsed.getHours()}:${parsed.getMinutes()}:${parsed.getSeconds()}`;
  } else if (minute) {
    return `${parsed.getFullYear()}-${parsed.getMonth() + 1}-${parsed.getDate()} ${parsed.getHours()}:${parsed.getMinutes()}`;
  } else if (hour) {
    return `${parsed.getFullYear()}-${parsed.getMonth() + 1}-${parsed.getDate()} ${parsed.getHours()}`;
  }
  return `${parsed.getFullYear()}-${parsed.getMonth() + 1}-${parsed.getDate()}`;
}

// Converts a Date to a GraphQL compatible ISO string
// Example: getGqlString(new Date(2023, 0, 15)) -> "2023-01-15T00:00:00.000Z"
export function getGqlString(d: Date | any): string {
  if (d.toDate) {
    d = d.toDate();
  }
  const utc = new Date(d.getTime() - d.getTimezoneOffset() * 60000);
  return utc.toISOString();
}

// Returns a GraphQL string date from N days before the given date
// Example: getLastNDaysDateAsGqlString(new Date(2023, 0, 31), 7) -> Date string from Jan 24, 2023
export function getLastNDaysDateAsGqlString(d: Date | null, n: number | null): string {
  d = d || new Date();
  n = n || 30;
  const time = d.getTime() - n * 24 * 60 * 60 * 1000;
  const d1 = new Date(time);
  return getGqlString(d1);
}

// Array of month names for date formatting
const monthNames = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];

// Returns the month name from a Date object
// Example: getMonthNameFromDate(new Date(2023, 0, 15)) -> "January"
export function getMonthNameFromDate(d?: Date): string {
  d = d || new Date();
  return monthNames[d.getMonth()];
}

// Returns a Date object representing the start of the day (00:00:00)
// Example: getStartOfDay(new Date(2023, 0, 15, 14, 30)) -> Jan 15, 2023 00:00:00
export function getStartOfDay(d?: Date): Date {
  d = d || new Date();
  return new Date(d.getFullYear(), d.getMonth(), d.getDate(), 0, 0, 0, 0);
}

// Returns a Date object representing the end of the day (23:59:59.999)
// Example: getEndOfDay(new Date(2023, 0, 15)) -> Jan 15, 2023 23:59:59.999
export function getEndOfDay(d?: Date): Date {
  d = d || new Date();
  return new Date(new Date(d.getFullYear(), d.getMonth(), d.getDate() + 1, 0, 0, 0, 0).getTime() - 1);
}

// Returns a Date object representing the start of the previous month
// Example: getStartOfLastMonth(new Date(2023, 1, 15)) -> Feb 1, 2023 00:00:00
export function getStartOfLastMonth(d?: Date): Date {
  d = d || new Date();
  return new Date(d.getFullYear(), d.getMonth() - 1, 1, 0, 0, 0, 0);
}

// Returns a Date object representing the end of the previous month
// Example: getEndOfLastMonth(new Date(2023, 1, 15)) -> Jan 31, 2023 00:00:00
export function getEndOfLastMonth(d?: Date): Date {
  d = d || new Date();
  return new Date(d.getFullYear(), d.getMonth(), 0, 0, 0, 0, 0);
}

// Returns a Date object representing the start of the current month
// Example: getStartOfMonth(new Date(2023, 0, 15)) -> Jan 1, 2023 00:00:00
export function getStartOfMonth(d?: Date): Date {
  d = d || new Date();
  return new Date(d.getFullYear(), d.getMonth(), 1, 0, 0, 0, 0);
}

// Returns a Date object representing the end of the current month
// Example: getEndOfMonth(new Date(2023, 0, 15)) -> Jan 31, 2023 23:59:59.999
export function getEndOfMonth(d?: Date): Date {
  d = d || new Date();
  return new Date(new Date(d.getFullYear(), d.getMonth() + 1, 1, 0, 0, 0, 0).getTime() - 1);
}

// Returns a Date object representing the start of the current year
// Example: getStartOfYear(new Date(2023, 5, 15)) -> Jan 1, 2023 00:00:00
export function getStartOfYear(d?: Date): Date {
  d = d || new Date();
  return new Date(d.getFullYear(), 0, 1, 0, 0, 0, 0);
}

// Returns a Date object representing the end of the current year
// Example: getEndOfYear(new Date(2023, 5, 15)) -> Dec 31, 2023 23:59:59.999
export function getEndOfYear(d?: Date): Date {
  d = d || new Date();
  return new Date(new Date(d.getFullYear() + 1, 0, 0, 0, 0, 0, 0).getTime() - 1);
}

// Returns a Date object for yesterday relative to the input date
// Example: getYesterday(new Date(2023, 0, 15)) -> Jan 14, 2023
export function getYesterday(d?: Date): Date {
  d = d || new Date();
  d.setDate(d.getDate() - 1);
  return d;
}

// Returns a Date object from 7 days ago relative to the input date
// Example: getLast7Days(new Date(2023, 0, 15)) -> Jan 8, 2023
export function getLast7Days(d?: Date): Date {
  d = d || new Date();
  const sevenDaysAgo = new Date(d.getTime() - 7 * 24 * 60 * 60 * 1000);
  return sevenDaysAgo;
}

// Returns a Date object from 24 hours ago relative to the input date
// Example: getLast24Hrs(new Date(2023, 0, 15, 12)) -> Jan 14, 2023 12:00
export function getLast24Hrs(d?: Date): Date {
  d = d || new Date();
  const last24Hrs = new Date(d.getTime() - 24 * 60 * 60 * 1000);
  return last24Hrs;
}

// Returns a Date object from 12 hours ago relative to the input date
// Example: getLast12Hrs(new Date(2023, 0, 15, 12)) -> Jan 15, 2023 00:00
export function getLast12Hrs(d?: Date): Date {
  d = d || new Date();
  const last12Hrs = new Date(d.getTime() - 12 * 60 * 60 * 1000);
  return last12Hrs;
}

// Returns a Date object from 30 days ago relative to the input date
// Example: getLast30Days(new Date(2023, 0, 31)) -> Jan 1, 2023
export function getLast30Days(d?: Date): Date {
  d = d || new Date();
  const thirtyDaysAgo = new Date(d.getTime() - 30 * 24 * 60 * 60 * 1000);
  return thirtyDaysAgo;
}

// Returns a Date object from 6 months ago (start of that month)
// Example: getLastSixMonths(new Date(2023, 6, 15)) -> Jan 1, 2023
export function getLastSixMonths(d?: Date): Date {
  d = d || new Date();
  let sixMonthsAgo = new Date(d);
  sixMonthsAgo.setMonth(d.getMonth() - 6);
  sixMonthsAgo = getStartOfMonth(sixMonthsAgo);
  return sixMonthsAgo;
}

// Returns a Date object from 3 months ago (start of that month)
// Example: getLastThreeMonths(new Date(2023, 3, 15)) -> Jan 1, 2023
export function getLastThreeMonths(d?: Date): Date {
  d = d || new Date();
  let threeMonthsAgo = new Date(d);
  threeMonthsAgo.setMonth(d.getMonth() - 3);
  threeMonthsAgo = getStartOfMonth(threeMonthsAgo);
  return threeMonthsAgo;
}

// Returns a Date object representing the start of the week (Monday)
// Example: getStartOfWeek(new Date(2023, 0, 18)) -> Jan 16, 2023 (if Jan 18 is Wednesday)
export function getStartOfWeek(d?: Date): Date {
  d = d || new Date();
  const date = d.getDate() + (d.getDay() === 0 ? -6 : 1 - d.getDay());
  return new Date(d.getFullYear(), date > d.getDate() ? d.getMonth() - 1 : d.getMonth(), date, 0, 0, 0, 0);
}

// Returns a Date object representing the end of the week (Sunday)
// Example: getEndOfWeek(new Date(2023, 0, 18)) -> Jan 22, 2023 (if Jan 18 is Wednesday)
export function getEndOfWeek(d?: Date): Date {
  d = d || new Date();
  const date = d.getDate() + (d.getDay() === 0 ? 0 : 7 - d.getDay());
  return new Date(d.getFullYear(), date < d.getDate() ? d.getMonth() + 1 : d.getMonth(), date, 0, 0, 0, 0);
}

// Calculates time difference between two dates and returns days, hours, minutes, seconds
// Example: getDateDiff('2023-01-01', '2023-01-02') -> { days: 1, hours: 0, minutes: 0, seconds: 0 }
export function getDateDiff(input: Date | string, currentDate: Date | string = new Date(), type = 'previous') {
  if (typeof input === 'string' && input.length > 0 && input.indexOf('Z') < 0) {
    if (input.indexOf('+') < 0) {
      input = input + 'Z';
    }
  }
  const inputTimestamp = new Date(input);

  if (currentDate == null || currentDate == undefined) {
    currentDate = new Date();
  }

  if (typeof currentDate === 'string' && currentDate.length > 0 && currentDate.indexOf('Z') < 0) {
    currentDate = currentDate + 'Z';
  }
  currentDate = new Date(currentDate);

  // Calculate the time difference in milliseconds
  let timeDifference = 0;
  if (type === 'future') {
    timeDifference = inputTimestamp.getTime() - currentDate.getTime();
  } else {
    timeDifference = currentDate.getTime() - inputTimestamp.getTime();
  }

  // Calculate days, hours, and minutes
  const days = Math.floor(timeDifference / (1000 * 60 * 60 * 24));
  const hours = Math.floor((timeDifference % (1000 * 60 * 60 * 24)) / (1000 * 60 * 60));
  const minutes = Math.floor((timeDifference % (1000 * 60 * 60)) / (1000 * 60));
  const second = Math.floor((timeDifference % (1000 * 60)) / 1000);

  return {
    days: days,
    hours: hours,
    minutes: minutes,
    seconds: second,
  };
}

// Returns a formatted date string based on date unit (day, week, month)
// Example: getDateStringFromDateUnit(new Date(2023, 0, 15), 'month') -> "2023-1"
export function getDateStringFromDateUnit(d: Date | string = new Date(), dateUnit = 'day') {
  if (typeof d === 'string') {
    d = new Date(d);
  }
  if (dateUnit?.toLowerCase() === 'month') {
    return `${d.getFullYear()}-${d.getMonth() + 1}`;
  } else if (dateUnit?.toLowerCase() === 'week') {
    return `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`;
  }
  return `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`;
}

// Returns a date string in "YYYY-M-D" format
// Example: getDateString(new Date(2023, 0, 15)) -> "2023-1-15"
export function getDateString(d: Date | string = new Date()) {
  if (typeof d === 'string') {
    d = new Date(d);
  }
  return `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`;
}

// Returns a time string in "HH:MM:SS" format
// Example: getTimeString(new Date(2023, 0, 15, 14, 30, 45)) -> "14:30:45"
export function getTimeString(d: Date | string = new Date()) {
  if (typeof d === 'string') {
    d = new Date(d);
  }
  const hours = String(d.getHours()).padStart(2, '0');
  const minutes = String(d.getMinutes()).padStart(2, '0');
  const seconds = String(d.getSeconds()).padStart(2, '0');

  return `${hours}:${minutes}:${seconds}`;
}

// Returns a month string in "YYYY-M" format
// Example: getMonthString(new Date(2023, 0, 15)) -> "2023-1"
export function getMonthString(d: Date | string = new Date()) {
  if (typeof d === 'string') {
    d = new Date(d);
  }
  return `${d.getFullYear()}-${d.getMonth() + 1}`;
}

// Returns a week string in "YYYY-M-D" format (same as getDateString)
// Example: getWeekString(new Date(2023, 0, 15)) -> "2023-1-15"
export function getWeekString(d: Date | string = new Date()) {
  if (typeof d === 'string') {
    d = new Date(d);
  }
  return `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`;
}

// Converts a date string or Date object to locale string format
// Example: convertToLocalTime("2023-01-15T12:30:00Z") -> "1/15/2023, 12:30:00 PM" (depends on locale)
export const convertToLocalTime = (value: string | Date | null | undefined): string => {
  if (typeof value === 'string') {
    let dateString = value.trim();

    if (!dateString.endsWith('Z')) {
      dateString += 'Z';
    }
    const date = new Date(dateString);
    return date.toLocaleString();
  } else if (value instanceof Date) {
    return value.toLocaleString();
  }
  return '';
};

// Returns a timestamp from specific minutes ago
// Example: getSpecificTime(60) -> timestamp from 1 hour ago
export const getSpecificTime = (minutes: number) => {
  const pastTime = new Date(new Date().getTime() - minutes * 60 * 1000);
  return pastTime.getTime();
};

// Formats a timestamp to date/time string based on input format
// Example: determineAndFormatTime(1673784000000) -> "01/15 12:00:00"
export const determineAndFormatTime = (inputValue: number, timeDiffLessThan24 = false) => {
  let totalMilliseconds;
  const nowInSeconds = Math.floor(Date.now() / 1000);

  if (inputValue < nowInSeconds * 10) {
    totalMilliseconds = inputValue * 1000;
  } else if (inputValue < 1e12) {
    totalMilliseconds = inputValue;
  } else if (inputValue < 1e15) {
    totalMilliseconds = inputValue / 1000;
  } else {
    totalMilliseconds = inputValue / 1e6;
  }

  const date = new Date(totalMilliseconds);

  const month = date.getMonth();
  const day = date.getDate();
  const hours = date.getHours();
  const minutes = date.getMinutes();
  const seconds = date.getSeconds();

  if (!timeDiffLessThan24) {
    return `${(month + 1).toString().padStart(2, '0')}/${day.toString().padStart(2, '0')} ${hours.toString().padStart(2, '0')}:${minutes
      .toString()
      .padStart(2, '0')}:${seconds.toString().padStart(2, '0')}`;
  }
  return `${hours.toString().padStart(2, '0')}:${minutes.toString().padStart(2, '0')}:${seconds.toString().padStart(2, '0')}`;
};

// Checks if a time period is within a specified duration
// Example: isWithinTimeFrame(1673784000000, 1673870400000, 24, 'hours') -> true (24 hour difference)
export const isWithinTimeFrame = (startDateMs: number, endDateMs: number, duration: number, unit = 'hours') => {
  const difference = endDateMs - startDateMs;
  let maxDifference;
  switch (unit.toLowerCase()) {
    case 'hours':
      maxDifference = duration * 60 * 60 * 1000;
      break;
    case 'month':
      maxDifference = 30.44 * 24 * 60 * 60 * 1000;
      break;
    default:
      throw new Error('Invalid unit. Use "hours" or "month".');
  }
  return difference < maxDifference && difference >= 0;
};

// Formats a timestamp to MM/DD/YY HH:MM:SS AM/PM format
// Example: formatDateTime(1673784000000) -> "01/15/23 12:00:00 PM"
export const formatDateTime = (inputValue: number) => {
  let totalMilliseconds;
  const nowInSeconds = Math.floor(Date.now() / 1000);
  if (inputValue < nowInSeconds * 10) {
    totalMilliseconds = inputValue * 1000;
  } else if (inputValue < 1e14) {
    totalMilliseconds = inputValue;
  } else if (inputValue < 1e17) {
    totalMilliseconds = inputValue / 1000;
  } else {
    totalMilliseconds = inputValue / 1e6;
  }
  const date = new Date(totalMilliseconds);
  const formattedDate = date.toLocaleDateString('en-US', {
    year: '2-digit',
    month: '2-digit',
    day: '2-digit',
  });
  const formattedTime = date.toLocaleTimeString('en-US', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: true,
  });
  return `${formattedDate.replace(',', '')} ${formattedTime}`;
};

// Formats a timestamp to YYYY/MM/DD HH:MM:SS 24-hour format
// Example: timeFormatIn24Hours(1673784000000) -> "2023/01/15 12:00:00"
export const timeFormatIn24Hours = (inputMilliSeconds: number) => {
  const d = new Date(inputMilliSeconds);
  return d
    .toLocaleString('en-US', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    })
    .replace(',', '');
};

// Formats a timestamp to MM/DD, HH:MM 24-hour format
// Example: timeFormatIn24HoursCompact(1673784000000) -> "01/15, 12:00"
export const timeFormatIn24HoursCompact = (inputMilliSeconds: number) => {
  const d = new Date(inputMilliSeconds);
  return d.toLocaleString('en-US', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
};

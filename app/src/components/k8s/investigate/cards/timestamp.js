export const withUtcTimezone = (timestamp) => {
  if (/(T.*)([+-]\d{2})$/i.test(timestamp)) return `${timestamp}:00`;
  return /(?:Z|[+-]\d{2}:?\d{2})$/i.test(timestamp) ? timestamp : `${timestamp}Z`;
};

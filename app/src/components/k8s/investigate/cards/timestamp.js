export const withUtcTimezone = (timestamp) => {
  const hourOffset = timestamp.match(/(T.*)([+-]\d{2})$/i);
  if (hourOffset) return `${timestamp}:00`;
  return /(?:Z|[+-]\d{2}:?\d{2})$/i.test(timestamp) ? timestamp : `${timestamp}Z`;
};

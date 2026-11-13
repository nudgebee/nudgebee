export const withUtcTimezone = (timestamp) => (/(?:Z|[+-]\d{2}:?\d{2})$/i.test(timestamp) ? timestamp : `${timestamp}Z`);

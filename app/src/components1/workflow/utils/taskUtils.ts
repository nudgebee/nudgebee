// Sanitize task ID to conform to server validation rules
export const sanitizeTaskId = (id: string): string => {
  // Replace periods and special characters with underscores, ensure it starts with letter/underscore
  return id
    .replace(/\./g, '_')
    .replace(/[^a-zA-Z0-9_-]/g, '_')
    .replace(/^[^a-zA-Z_]/, '_$&'); // Prefix with underscore if it starts with number or special char
};

/**
 * Parses a Go duration string (e.g., "5m", "1h30m", "300s") to total seconds.
 * Supports: h (hours), m (minutes), s (seconds), ms/us/µs/ns (sub-second).
 * Falls back to treating plain numbers as seconds for backward compatibility.
 * Returns NaN if the string cannot be parsed.
 */
export function parseDurationToSeconds(duration: string | undefined | null): number {
  if (!duration || !duration.trim()) return NaN;

  const trimmed = duration.trim();

  // Plain number without unit — treat as seconds for backward compat
  if (/^\d+$/.test(trimmed)) {
    return parseInt(trimmed, 10);
  }

  const unitMap: Record<string, number> = {
    h: 3600,
    m: 60,
    s: 1,
    ms: 0.001,
    us: 0.000001,
    µs: 0.000001,
    ns: 0.000000001,
  };

  let total = 0;
  const regex = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g;
  let match;
  let lastIndex = 0;

  while ((match = regex.exec(trimmed)) !== null) {
    if (match.index !== lastIndex) {
      return NaN; // unexpected characters between segments
    }
    const value = parseFloat(match[1]);
    const unit = match[2];
    total += value * unitMap[unit];
    lastIndex = regex.lastIndex;
  }

  // Ensure the entire string was consumed
  if (lastIndex === 0 || lastIndex !== trimmed.length) return NaN;

  return total;
}

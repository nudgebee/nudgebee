// Sanitize task ID to conform to server validation rules.
// Case is preserved exactly — only special characters are normalized.
// Server validator (taskIDRegex = ^[a-zA-Z0-9_-]+$) accepts mixed case,
// and templating references (Tasks['MyTask']) are case-exact.
export const sanitizeTaskId = (id: string): string => {
  return id
    .replace(/\./g, '_')
    .replace(/[^a-zA-Z0-9_-]/g, '_')
    .replace(/^[^a-zA-Z_]/, '_$&'); // Prefix with underscore if it starts with number or special char
};

// Mirrors backend ValidateTaskID (runbook-server/internal/model/validation.go:251):
//   regex /^[a-zA-Z0-9_-]+$/, length 3..64.
// Backend rejects anything else at workflow save (validator tag `taskid`).
// Frontend gate at rename time prevents silent sanitize-on-export drift
// (e.g. user types `Core Print` → exported id becomes `Core_Print`,
// breaking template refs that still say `Core Print`).
export const TASK_ID_REGEX = /^[a-zA-Z0-9_-]+$/;
export const TASK_ID_MIN_LEN = 3;
export const TASK_ID_MAX_LEN = 64;

// Returns null when valid, human-readable error string otherwise.
// Spaces and length get dedicated branches so the error message points at
// the actual problem (most common mistake is spaces).
export const validateTaskId = (id: string): string | null => {
  const trimmed = id.trim();
  if (!trimmed) return 'Task name cannot be empty';
  if (/\s/.test(trimmed)) return 'Spaces are not allowed. Use _ or - instead';
  if (trimmed.length < TASK_ID_MIN_LEN) return `Task name must be at least ${TASK_ID_MIN_LEN} characters`;
  if (trimmed.length > TASK_ID_MAX_LEN) return `Task name must be at most ${TASK_ID_MAX_LEN} characters`;
  if (!TASK_ID_REGEX.test(trimmed)) {
    return 'Only letters, digits, _ and - allowed';
  }
  return null;
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

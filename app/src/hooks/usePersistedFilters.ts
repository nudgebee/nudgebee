const STORAGE_PREFIX = 'nudgebee:';

const isEmpty = (value: unknown): boolean => {
  if (value == null || value === '') return true;
  if (Array.isArray(value)) return value.length === 0;
  if (typeof value === 'object') return Object.keys(value as object).length === 0;
  return false;
};

export function readPersistedFilters<T extends Record<string, unknown> = Record<string, unknown>>(key: string | null | undefined): Partial<T> {
  if (!key || typeof window === 'undefined') return {};
  try {
    const raw = window.localStorage.getItem(STORAGE_PREFIX + key);
    return raw ? (JSON.parse(raw) as Partial<T>) : {};
  } catch {
    return {};
  }
}

export function writePersistedFilters(key: string | null | undefined, patch: Record<string, unknown>): void {
  if (!key || typeof window === 'undefined') return;
  try {
    const raw = window.localStorage.getItem(STORAGE_PREFIX + key);
    const merged: Record<string, unknown> = { ...(raw ? JSON.parse(raw) : {}), ...patch };
    Object.keys(merged).forEach((k) => {
      if (isEmpty(merged[k])) delete merged[k];
    });
    if (Object.keys(merged).length === 0) {
      window.localStorage.removeItem(STORAGE_PREFIX + key);
    } else {
      window.localStorage.setItem(STORAGE_PREFIX + key, JSON.stringify(merged));
    }
  } catch {
    // localStorage unavailable (Safari private mode, quota, etc.) — silently no-op
  }
}

export function clearPersistedFilters(key: string | null | undefined): void {
  if (!key || typeof window === 'undefined') return;
  try {
    window.localStorage.removeItem(STORAGE_PREFIX + key);
  } catch {
    // no-op
  }
}

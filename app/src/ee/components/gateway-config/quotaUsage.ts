import type { GatewayRateLimit } from '@api1/gateway-config';

// A count or US-dollar amount in the row's unit.
export function fmtAmount(l: GatewayRateLimit, v: number): string {
  if (l.metric === 'cost') return `$${v.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
  return Math.round(v).toLocaleString();
}

// "in 6h", "in 12m", "in 3d" until the window resets; '' when unknown or past.
export function resetIn(resetsAt: string | undefined, now: number = Date.now()): string {
  if (!resetsAt) return '';
  const ms = new Date(resetsAt).getTime() - now;
  if (!Number.isFinite(ms) || ms <= 0) return '';
  const mins = Math.ceil(ms / 60000);
  if (mins < 60) return `in ${mins}m`;
  const hours = Math.round(mins / 60);
  return hours < 48 ? `in ${hours}h` : `in ${Math.round(hours / 24)}d`;
}

// Share of the limit used in this window, or null when the list carried no usage.
export function usedPct(l: GatewayRateLimit): number | null {
  if (l.used == null || l.limit_value <= 0) return null;
  return (l.used / l.limit_value) * 100;
}

// Whether the quota has passed its warn level this window.
export function softExceeded(l: GatewayRateLimit): boolean {
  const pct = usedPct(l);
  return pct != null && l.soft_pct != null && pct >= l.soft_pct;
}

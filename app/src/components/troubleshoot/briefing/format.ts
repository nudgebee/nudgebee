export const weightedMedian = (buckets: { value: number; weight: number }[]): number => {
  const total = buckets.reduce((sum, b) => sum + b.weight, 0);
  if (total === 0) return 0;
  const sorted = [...buckets].sort((a, b) => a.value - b.value);
  let seen = 0;
  for (const bucket of sorted) {
    seen += bucket.weight;
    if (seen * 2 >= total) return bucket.value;
  }
  return sorted[sorted.length - 1].value;
};

export const formatCount = (value: number): string => (value ?? 0).toLocaleString();

export const formatShare = (value: number, total: number): string => {
  if (!total) return '0%';
  const pct = (value / total) * 100;
  return Number.isInteger(pct) ? `${pct}%` : `${pct.toFixed(1)}%`;
};

export const formatPercent = (value: number, total: number): string => (total ? `${Math.round((value / total) * 100)}%` : '0%');

export const formatDuration = (milliseconds: number): string => {
  if (!milliseconds || milliseconds <= 0) return '0m';
  const totalMinutes = Math.floor(milliseconds / 60000);
  const days = Math.floor(totalMinutes / 1440);
  const hours = Math.floor((totalMinutes % 1440) / 60);
  const minutes = totalMinutes % 60;
  if (days > 0) return hours > 0 ? `${days}d ${hours}h` : `${days}d`;
  if (hours > 0) return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`;
  return `${minutes}m`;
};

export const formatDays = (days: number): string => `${days} ${days === 1 ? 'day' : 'days'}`;

export const formatWindowLength = (startMs: number, endMs: number): string => {
  const elapsed = Math.max(0, endMs - startMs);
  if (elapsed >= 48 * 3600000) return `${Math.round(elapsed / 86400000)}d`;
  if (elapsed >= 3600000) return `${Math.round(elapsed / 3600000)}h`;
  return `${Math.max(1, Math.round(elapsed / 60000))}m`;
};

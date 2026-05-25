export function formatSizeUnits(value: number): string {
  return (value / (1024 * 1024 * 1024)).toLocaleString('en-US', { minimumFractionDigits: 0, maximumFractionDigits: 2 });
}

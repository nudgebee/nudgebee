import { formatDateTime } from '@lib/datetime';
import { Box } from '@mui/material';
import Text from '@shared/format/Text';

export const LOG_LEVEL_COLORS: Record<string, string> = {
  error: 'var(--ds-red-500)',
  info: 'var(--ds-blue-300)',
  debug: 'var(--ds-gray-500)',
};

// Normalize a log timestamp to epoch milliseconds for formatDateTime (which then
// auto-detects the unit). Providers return it in different shapes: a number, an
// epoch-millis string (OTel/Elasticsearch, e.g. "1781559826466"), or an ISO-8601
// string (Datadog, etc.). new Date("1781559826466") is Invalid Date — JS only
// parses a bare epoch value when it is a number — so coerce numeric strings
// before falling back to Date.parse for ISO strings.
function toEpochMillis(ts: number | string): number {
  if (typeof ts === 'number') return ts;
  const numeric = Number(ts);
  if (!Number.isNaN(numeric)) return numeric;
  return Date.parse(ts);
}

export function LogDate({ timestamp, log }: Readonly<{ timestamp: number | string; log: string }>) {
  let level = 'debug';
  const message = log?.toLowerCase() ?? '';
  if (message.includes('error') || message.includes('exception') || message.includes('critical')) {
    level = 'error';
  } else if (message.includes('info')) {
    level = 'info';
  }

  return (
    <Box display={'flex'} gap={2} alignItems={'center'}>
      <div
        style={{
          width: 2,
          backgroundColor: LOG_LEVEL_COLORS[level],
          paddingRight: 'var(--ds-space-1)',
          borderRadius: 'var(--ds-radius-sm)',
          height: '28px',
        }}
      />
      <Text value={timestamp ? formatDateTime(toEpochMillis(timestamp)) : '--'} />{' '}
    </Box>
  );
}

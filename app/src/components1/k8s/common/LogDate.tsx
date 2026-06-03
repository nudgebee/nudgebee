import { formatDateTime } from '@lib/datetime';
import { Box } from '@mui/material';
import Text from '@common-new/format/Text';

export const LOG_LEVEL_COLORS: Record<string, string> = {
  error: 'var(--ds-red-500)',
  info: 'var(--ds-blue-300)',
  debug: 'var(--ds-gray-500)',
};

export function LogDate({ timestamp, log }: Readonly<{ timestamp: number; log: string }>) {
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
      <Text value={timestamp ? formatDateTime(new Date(timestamp).getTime()) : '--'} />{' '}
    </Box>
  );
}

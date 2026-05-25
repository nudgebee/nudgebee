import { formatDateTime } from '@lib/datetime';
import { Box } from '@mui/material';
import { Text } from '@components1/common';
import { colors } from 'src/utils/colors';

export const LOG_LEVEL_COLORS: any = {
  error: colors.high,
  info: colors.toDo,
  debug: colors.debug,
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
      <div style={{ width: 2, backgroundColor: LOG_LEVEL_COLORS[level], paddingRight: 4, borderRadius: '2px', height: '28px' }} />
      <Text value={timestamp ? formatDateTime(new Date(timestamp).getTime()) : '--'} />{' '}
    </Box>
  );
}

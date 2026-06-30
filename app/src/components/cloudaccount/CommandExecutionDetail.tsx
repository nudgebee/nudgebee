import React, { useEffect, useState } from 'react';
import { Box, CircularProgress } from '@mui/material';
import CopyButton from '@shared/buttons/CopyButton';
import { ds } from '@utils/colors';
import { Card } from '@ui/Card';
import { Label } from '@ui/Label';

export interface CommandEntry {
  command: string;
  output?: string;
  error?: string;
  status?: string;
}

interface CommandExecutionDetailProps {
  commands: CommandEntry[];
  status?: string;
}

type LabelMetaKey = 'FAILED' | 'SUCCESS' | 'RUNNING' | 'NOT_EXECUTED';

const LabelMetaData: Record<LabelMetaKey, { text: string; tone: 'critical' | 'success' | 'info' | 'warning' }> = {
  FAILED: { text: 'Failed', tone: 'critical' },
  SUCCESS: { text: 'Success', tone: 'success' },
  RUNNING: { text: 'Running', tone: 'info' },
  NOT_EXECUTED: { text: 'Not Executed', tone: 'warning' },
};

const CommandEntryDetail: React.FC<{ entry: CommandEntry }> = ({ entry }) => (
  <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
    {entry.command && (
      <Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', mb: 'var(--ds-space-1)' }}>
          <Box
            sx={{
              fontWeight: 'var(--ds-font-weight-medium)',
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-gray-500)',
              textTransform: 'uppercase',
            }}
          >
            Command
          </Box>
          {(() => {
            const key = ((entry?.status?.toUpperCase() ?? '') in LabelMetaData ? entry!.status!.toUpperCase() : 'NOT_EXECUTED') as LabelMetaKey;
            const meta = LabelMetaData[key];
            return <Label tone={meta.tone} text={meta.text} size='sm' />;
          })()}
        </Box>
        <Card variant='tinted' tone='neutral' size='sm' elevation='flat'>
          <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--ds-space-2)' }}>
            <Box sx={{ flex: 1, fontFamily: 'var(--ds-font-mono)', fontSize: 'var(--ds-text-small)', wordBreak: 'break-all' }}>{entry.command}</Box>
            <Box sx={{ position: 'sticky', top: 0 }}>
              <CopyButton text={entry.command} />
            </Box>
          </Box>
        </Card>
      </Box>
    )}
    {entry.output && (
      <Box>
        <Box
          sx={{
            fontWeight: 'var(--ds-font-weight-medium)',
            mb: 'var(--ds-space-1)',
            fontSize: 'var(--ds-text-small)',
            color: 'var(--ds-gray-500)',
            textTransform: 'uppercase',
          }}
        >
          Output
        </Box>
        <Card variant='tinted' tone='neutral' size='sm' elevation='flat'>
          <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--ds-space-2)', maxHeight: ds.space.mul(2, 50), overflowY: 'auto' }}>
            <Box
              sx={{ flex: 1, fontFamily: 'var(--ds-font-mono)', fontSize: 'var(--ds-text-small)', whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}
            >
              {entry.output}
            </Box>
            <Box sx={{ position: 'sticky', top: 0 }}>
              <CopyButton text={entry.output} />
            </Box>
          </Box>
        </Card>
      </Box>
    )}
    {entry.error && (
      <Box>
        <Box
          sx={{
            fontWeight: 'var(--ds-font-weight-medium)',
            mb: 'var(--ds-space-1)',
            fontSize: 'var(--ds-text-small)',
            color: 'var(--ds-red-500)',
            textTransform: 'uppercase',
          }}
        >
          Error
        </Box>
        <Card
          variant='tinted'
          tone='danger'
          size='sm'
          elevation='flat'
          sx={{ fontFamily: 'var(--ds-font-mono)', fontSize: 'var(--ds-text-small)', whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}
        >
          {entry.error}
        </Card>
      </Box>
    )}
  </Box>
);

const CommandExecutionDetail: React.FC<CommandExecutionDetailProps> = ({ commands, status }) => {
  const isRunning = status?.toUpperCase() === 'RUNNING';
  const [elapsed, setElapsed] = useState(0);
  useEffect(() => {
    if (!isRunning) {
      setElapsed(0);
      return;
    }
    const interval = setInterval(() => setElapsed((s) => s + 1), 1000);
    return () => clearInterval(interval);
  }, [isRunning]);

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
      {isRunning && (
        <Box>
          <Box
            sx={{
              fontWeight: 'var(--ds-font-weight-medium)',
              mb: 'var(--ds-space-1)',
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-blue-500)',
              textTransform: 'uppercase',
            }}
          >
            Running
          </Box>
          <Card variant='tinted' tone='info' size='sm' elevation='flat'>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', fontSize: 'var(--ds-text-small)' }}>
                <CircularProgress size={14} thickness={5} />
                <Box>
                  Executing {commands.length} command{commands.length !== 1 ? 's' : ''}…{' '}
                  <Box component='span' sx={{ color: 'var(--ds-gray-500)' }}>
                    {elapsed}s
                  </Box>
                </Box>
              </Box>
              {commands.map((cmd, i) => (
                <Box
                  key={i}
                  sx={{
                    fontFamily: 'var(--ds-font-mono)',
                    fontSize: 'var(--ds-text-small)',
                    color: 'var(--ds-gray-400)',
                    wordBreak: 'break-all',
                  }}
                >
                  {cmd.command}
                </Box>
              ))}
            </Box>
          </Card>
        </Box>
      )}
      {!isRunning &&
        commands.map((cmd, i) =>
          commands.length > 1 ? (
            <Card
              key={i}
              variant='outlined'
              tone='neutral'
              size='sm'
              elevation='flat'
              header={
                <Box sx={{ fontWeight: 'var(--ds-font-weight-medium)', fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>
                  Command {i + 1} of {commands.length}
                </Box>
              }
            >
              <CommandEntryDetail entry={cmd} />
            </Card>
          ) : (
            <CommandEntryDetail key={i} entry={cmd} />
          )
        )}
    </Box>
  );
};

export default CommandExecutionDetail;

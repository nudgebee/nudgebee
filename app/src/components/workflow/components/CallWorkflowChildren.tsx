import React, { useState } from 'react';
import { Box, Typography, Collapse } from '@mui/material';
import { Button } from '@ui/Button';
import { ExpandMore, ExpandLess, ContentCopy, AccessTime } from '@mui/icons-material';
import { ds } from 'src/utils/colors';
import JsonTreeView from '@shared/viewers/JsonTreeView';
import { Label } from '@ui/Label';

// Tasks that came back from the called workflow's execution detail. Matches the
// shape produced by backend processWorkflowHistory (id, type, status, input,
// output, start_time, end_time).
export interface ChildTask {
  id: string;
  type?: string;
  status?: string;
  input?: any;
  output?: any;
  rendered_params?: any;
  error?: string;
  start_time?: string;
  end_time?: string;
}

interface CallWorkflowChildrenProps {
  tasks: ChildTask[];
  copyToClipboard?: (text: string, label: string) => void;
}

export const formatDuration = (start?: string, end?: string) => {
  if (!start || !end) return '';
  const ms = new Date(end).getTime() - new Date(start).getTime();
  if (!Number.isFinite(ms) || ms < 0) return '';
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
};

export const ChildTaskCard: React.FC<{ task: ChildTask; copyToClipboard?: (text: string, label: string) => void }> = ({ task, copyToClipboard }) => {
  const [expanded, setExpanded] = useState(true);
  const duration = formatDuration(task.start_time, task.end_time);

  return (
    <Box
      data-testid={`call-workflow-child-${task.id}`}
      sx={{
        border: `1px solid ${ds.blue[300]}`,
        borderRadius: 'var(--ds-radius-md)',
        marginBottom: 'var(--ds-space-2)',
        backgroundColor: ds.background[100],
        overflow: 'hidden',
      }}
    >
      <Box
        role='button'
        tabIndex={0}
        onClick={() => setExpanded((v) => !v)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            setExpanded((v) => !v);
          }
        }}
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          padding: 'var(--ds-space-2) var(--ds-space-3)',
          cursor: 'pointer',
          backgroundColor: ds.background[200],
          '&:hover': { opacity: 0.92 },
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', minWidth: 0 }}>
          <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
            {task.id}
          </Typography>
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], fontFamily: 'monospace' }}>{task.type}</Typography>
        </Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
          {duration && (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
              <AccessTime sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600] }} />
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600] }}>{duration}</Typography>
            </Box>
          )}
          {task.status && <Label text={task.status.toUpperCase()} />}
          {expanded ? (
            <ExpandLess sx={{ fontSize: 'var(--ds-text-title)', color: ds.gray[600] }} />
          ) : (
            <ExpandMore sx={{ fontSize: 'var(--ds-text-title)', color: ds.gray[600] }} />
          )}
        </Box>
      </Box>
      <Collapse in={expanded} unmountOnExit>
        <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', padding: 'var(--ds-space-2) var(--ds-space-3) var(--ds-space-3) var(--ds-space-3)' }}>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 0.5 }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
                Input
              </Typography>
              {task.input != null && copyToClipboard && (
                <Button
                  composition='icon-only'
                  tone='ghost'
                  size='xs'
                  aria-label='Copy input'
                  icon={<ContentCopy sx={{ fontSize: 'var(--ds-text-small)' }} />}
                  onClick={() =>
                    copyToClipboard(typeof task.input === 'string' ? task.input : JSON.stringify(task.input, null, 2), `${task.id} input`)
                  }
                />
              )}
            </Box>
            {task.input != null ? (
              <Box
                sx={{
                  backgroundColor: ds.red[100],
                  border: `1px solid ${ds.blue[300]}`,
                  borderRadius: 'var(--ds-radius-sm)',
                  padding: 'var(--ds-space-1) var(--ds-space-2)',
                }}
              >
                <JsonTreeView data={task.input} defaultExpanded={1} maxHeight='160px' fontSize='var(--ds-text-caption)' />
              </Box>
            ) : (
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], fontStyle: 'italic' }}>No input</Typography>
            )}
          </Box>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 0.5 }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
                Output
              </Typography>
              {task.output != null && copyToClipboard && (
                <Button
                  composition='icon-only'
                  tone='ghost'
                  size='xs'
                  aria-label='Copy output'
                  icon={<ContentCopy sx={{ fontSize: 'var(--ds-text-small)' }} />}
                  onClick={() =>
                    copyToClipboard(typeof task.output === 'string' ? task.output : JSON.stringify(task.output, null, 2), `${task.id} output`)
                  }
                />
              )}
            </Box>
            {task.output != null ? (
              <Box
                sx={{
                  backgroundColor: ds.blue[100],
                  border: `1px solid ${ds.blue[300]}`,
                  borderRadius: 'var(--ds-radius-sm)',
                  padding: 'var(--ds-space-1) var(--ds-space-2)',
                }}
              >
                <JsonTreeView data={task.output} defaultExpanded={1} maxHeight='160px' fontSize={ds.text.caption} />
              </Box>
            ) : (
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], fontStyle: 'italic' }}>No output</Typography>
            )}
          </Box>
        </Box>
        {task.error && (
          <Box sx={{ padding: '0 var(--ds-space-3) var(--ds-space-3) var(--ds-space-3)' }}>
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.red[600], mb: 0.5 }}>
              Error
            </Typography>
            <Box
              sx={{
                backgroundColor: ds.red[100],
                border: `1px solid ${ds.red[200]}`,
                borderRadius: 'var(--ds-radius-sm)',
                padding: 'var(--ds-space-1) var(--ds-space-2)',
                fontFamily: 'monospace',
                fontSize: 'var(--ds-text-caption)',
                color: ds.red[600],
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
              }}
            >
              {task.error}
            </Box>
          </Box>
        )}
      </Collapse>
    </Box>
  );
};

// Renders the tasks executed by a workflow that was invoked via core.call-workflow.
// The backend's processWorkflowHistory populates `task.children` for completed
// call-workflow nodes (see runbook-server/internal/workflow/service.go); this
// component surfaces those nested executions in the Executions panel so users can
// see each step's Input/Output without leaving the parent run.
const CallWorkflowChildren: React.FC<CallWorkflowChildrenProps> = ({ tasks, copyToClipboard }) => {
  if (!Array.isArray(tasks) || tasks.length === 0) {
    return null;
  }
  return (
    // Bounded flex column: content-sized for a couple of tasks, capped at half the
    // panel beyond that so the list scrolls internally instead of overflowing the
    // (clipped, non-scrolling) detail panel and squashing Input/Output to 0.
    <Box
      sx={{
        marginTop: 'var(--ds-space-4)',
        padding: 'var(--ds-space-3)',
        borderTop: `1px solid ${ds.blue[300]}`,
        display: 'flex',
        flexDirection: 'column',
        flex: '0 1 auto',
        minHeight: 0,
        maxHeight: '50%',
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', marginBottom: 'var(--ds-space-2)', flexShrink: 0 }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-small)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: ds.gray[700],
            fontFamily: ds.font.display,
          }}
        >
          Called Workflow Tasks
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600] }}>
          ({tasks.length} {tasks.length === 1 ? 'task' : 'tasks'})
        </Typography>
      </Box>
      <Box className='custom-scrollbar' sx={{ flex: 1, minHeight: 0, overflowY: 'auto' }}>
        {tasks.map((child) => (
          <ChildTaskCard key={child.id} task={child} copyToClipboard={copyToClipboard} />
        ))}
      </Box>
    </Box>
  );
};

export default CallWorkflowChildren;

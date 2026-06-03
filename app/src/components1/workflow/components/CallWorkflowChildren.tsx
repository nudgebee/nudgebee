import React, { useState } from 'react';
import { Box, Typography, Collapse } from '@mui/material';
import { Button } from '@components1/ds/Button';
import { ExpandMore, ExpandLess, ContentCopy, AccessTime } from '@mui/icons-material';
import { colors } from 'src/utils/colors';
import JsonTreeView from '@components1/common/JsonTreeView';
import CustomLabels from '@components1/common/widgets/CustomLabels';

// Tasks that came back from the called workflow's execution detail. Matches the
// shape produced by backend processWorkflowHistory (id, type, status, input,
// output, start_time, end_time).
interface ChildTask {
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

const formatDuration = (start?: string, end?: string) => {
  if (!start || !end) return '';
  const ms = new Date(end).getTime() - new Date(start).getTime();
  if (!Number.isFinite(ms) || ms < 0) return '';
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
};

const ChildTaskCard: React.FC<{ task: ChildTask; copyToClipboard?: (text: string, label: string) => void }> = ({ task, copyToClipboard }) => {
  const [expanded, setExpanded] = useState(true);
  const duration = formatDuration(task.start_time, task.end_time);

  return (
    <Box
      data-testid={`call-workflow-child-${task.id}`}
      sx={{
        border: `1px solid ${colors.border.primaryLight}`,
        borderRadius: 'var(--ds-radius-md)',
        marginBottom: 'var(--ds-space-2)',
        backgroundColor: colors.background.white,
        overflow: 'hidden',
      }}
    >
      <Box
        onClick={() => setExpanded((v) => !v)}
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          padding: 'var(--ds-space-2) var(--ds-space-3)',
          cursor: 'pointer',
          backgroundColor: colors.background.tertiaryLightestestest,
          '&:hover': { opacity: 0.92 },
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', minWidth: 0 }}>
          <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}>
            {task.id}
          </Typography>
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary, fontFamily: 'monospace' }}>{task.type}</Typography>
        </Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
          {duration && (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
              <AccessTime sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.tertiary }} />
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary }}>{duration}</Typography>
            </Box>
          )}
          {task.status && <CustomLabels text={task.status.toUpperCase()} />}
          {expanded ? (
            <ExpandLess sx={{ fontSize: 'var(--ds-text-title)', color: colors.text.tertiary }} />
          ) : (
            <ExpandMore sx={{ fontSize: 'var(--ds-text-title)', color: colors.text.tertiary }} />
          )}
        </Box>
      </Box>
      <Collapse in={expanded} unmountOnExit>
        <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', padding: 'var(--ds-space-2) var(--ds-space-3) var(--ds-space-3) var(--ds-space-3)' }}>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 0.5 }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}>
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
                  backgroundColor: colors.background.accordionSummay,
                  border: `1px solid ${colors.border.primaryLight}`,
                  borderRadius: 'var(--ds-radius-sm)',
                  padding: 'var(--ds-space-1) var(--ds-space-2)',
                }}
              >
                <JsonTreeView data={task.input} defaultExpanded={1} maxHeight='160px' fontSize='11px' />
              </Box>
            ) : (
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary, fontStyle: 'italic' }}>No input</Typography>
            )}
          </Box>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 0.5 }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}>
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
                  backgroundColor: colors.background.primaryLightest,
                  border: `1px solid ${colors.border.primaryLight}`,
                  borderRadius: 'var(--ds-radius-sm)',
                  padding: 'var(--ds-space-1) var(--ds-space-2)',
                }}
              >
                <JsonTreeView data={task.output} defaultExpanded={1} maxHeight='160px' fontSize='11px' />
              </Box>
            ) : (
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary, fontStyle: 'italic' }}>No output</Typography>
            )}
          </Box>
        </Box>
        {task.error && (
          <Box sx={{ padding: '0 var(--ds-space-3) var(--ds-space-3) var(--ds-space-3)' }}>
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.error, mb: 0.5 }}>
              Error
            </Typography>
            <Box
              sx={{
                backgroundColor: colors.background.accordionSummay,
                border: `1px solid ${colors.background.errorLight}`,
                borderRadius: 'var(--ds-radius-sm)',
                padding: 'var(--ds-space-1) var(--ds-space-2)',
                fontFamily: 'monospace',
                fontSize: 'var(--ds-text-caption)',
                color: colors.error,
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
    <Box sx={{ marginTop: 'var(--ds-space-4)', padding: 'var(--ds-space-3)', borderTop: `1px solid ${colors.border.primaryLight}` }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', marginBottom: 'var(--ds-space-2)' }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-small)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: colors.text.secondary,
            fontFamily: 'Poppins, sans-serif',
          }}
        >
          Called Workflow Tasks
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary }}>
          ({tasks.length} {tasks.length === 1 ? 'task' : 'tasks'})
        </Typography>
      </Box>
      {tasks.map((child) => (
        <ChildTaskCard key={child.id} task={child} copyToClipboard={copyToClipboard} />
      ))}
    </Box>
  );
};

export default CallWorkflowChildren;

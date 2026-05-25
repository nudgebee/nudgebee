import React, { useState } from 'react';
import { Box, Typography, IconButton, Collapse } from '@mui/material';
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
        borderRadius: '6px',
        marginBottom: '8px',
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
          padding: '8px 12px',
          cursor: 'pointer',
          backgroundColor: colors.background.tertiaryLightestestest,
          '&:hover': { opacity: 0.92 },
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px', minWidth: 0 }}>
          <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary }}>{task.id}</Typography>
          <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, fontFamily: 'monospace' }}>{task.type}</Typography>
        </Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
          {duration && (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
              <AccessTime sx={{ fontSize: '12px', color: colors.text.tertiary }} />
              <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>{duration}</Typography>
            </Box>
          )}
          {task.status && <CustomLabels text={task.status.toUpperCase()} />}
          {expanded ? (
            <ExpandLess sx={{ fontSize: '18px', color: colors.text.tertiary }} />
          ) : (
            <ExpandMore sx={{ fontSize: '18px', color: colors.text.tertiary }} />
          )}
        </Box>
      </Box>
      <Collapse in={expanded} unmountOnExit>
        <Box sx={{ display: 'flex', gap: '8px', padding: '8px 12px 12px 12px' }}>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 0.5 }}>
              <Typography sx={{ fontSize: '11px', fontWeight: 600, color: colors.text.secondary }}>Input</Typography>
              {task.input != null && copyToClipboard && (
                <IconButton
                  size='small'
                  onClick={() =>
                    copyToClipboard(typeof task.input === 'string' ? task.input : JSON.stringify(task.input, null, 2), `${task.id} input`)
                  }
                  sx={{ color: colors.tertiary, padding: '2px' }}
                >
                  <ContentCopy sx={{ fontSize: '12px' }} />
                </IconButton>
              )}
            </Box>
            {task.input != null ? (
              <Box
                sx={{
                  backgroundColor: colors.background.accordionSummay,
                  border: `1px solid ${colors.border.primaryLight}`,
                  borderRadius: '4px',
                  padding: '6px 8px',
                }}
              >
                <JsonTreeView data={task.input} defaultExpanded={1} maxHeight='160px' fontSize='11px' />
              </Box>
            ) : (
              <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, fontStyle: 'italic' }}>No input</Typography>
            )}
          </Box>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 0.5 }}>
              <Typography sx={{ fontSize: '11px', fontWeight: 600, color: colors.text.secondary }}>Output</Typography>
              {task.output != null && copyToClipboard && (
                <IconButton
                  size='small'
                  onClick={() =>
                    copyToClipboard(typeof task.output === 'string' ? task.output : JSON.stringify(task.output, null, 2), `${task.id} output`)
                  }
                  sx={{ color: colors.tertiary, padding: '2px' }}
                >
                  <ContentCopy sx={{ fontSize: '12px' }} />
                </IconButton>
              )}
            </Box>
            {task.output != null ? (
              <Box
                sx={{
                  backgroundColor: colors.background.primaryLightest,
                  border: `1px solid ${colors.border.primaryLight}`,
                  borderRadius: '4px',
                  padding: '6px 8px',
                }}
              >
                <JsonTreeView data={task.output} defaultExpanded={1} maxHeight='160px' fontSize='11px' />
              </Box>
            ) : (
              <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, fontStyle: 'italic' }}>No output</Typography>
            )}
          </Box>
        </Box>
        {task.error && (
          <Box sx={{ padding: '0 12px 12px 12px' }}>
            <Typography sx={{ fontSize: '11px', fontWeight: 600, color: colors.error, mb: 0.5 }}>Error</Typography>
            <Box
              sx={{
                backgroundColor: colors.background.accordionSummay,
                border: `1px solid ${colors.background.errorLight}`,
                borderRadius: '4px',
                padding: '6px 8px',
                fontFamily: 'monospace',
                fontSize: '11px',
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
    <Box sx={{ marginTop: '16px', padding: '12px', borderTop: `1px solid ${colors.border.primaryLight}` }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '8px' }}>
        <Typography sx={{ fontSize: '12px', fontWeight: 600, color: colors.text.secondary, fontFamily: 'Poppins, sans-serif' }}>
          Called Workflow Tasks
        </Typography>
        <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>
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

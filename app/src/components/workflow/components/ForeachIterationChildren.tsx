import React, { useState } from 'react';
import { Box, Typography, Collapse } from '@mui/material';
import { ExpandMore, ExpandLess, AccessTime } from '@mui/icons-material';
import { ds } from 'src/utils/colors';
import { Label } from '@ui/Label';
import { ChildTaskCard, formatDuration, type ChildTask } from './CallWorkflowChildren';

// An iteration container synthesized by the backend for core.foreach
// executions: {id: "Iteration N", type: "iteration", status, start_time,
// end_time, children: [sub-task details]}.
interface IterationEntry extends ChildTask {
  children?: ChildTask[];
}

interface ForeachIterationChildrenProps {
  // The foreach task's execution `children` (iteration containers). Named
  // `entries` to avoid colliding with React's implicit children prop.
  entries: IterationEntry[];
  copyToClipboard?: (text: string, label: string) => void;
}

const isUnfinished = (status?: string) => {
  const s = String(status ?? '').toUpperCase();
  return s === 'FAILED' || s === 'STARTED' || s === 'SCHEDULED' || s === 'RUNNING';
};

const IterationCard: React.FC<{ iteration: IterationEntry; copyToClipboard?: (text: string, label: string) => void }> = ({
  iteration,
  copyToClipboard,
}) => {
  // Failed / still-running iterations open by default — those are the ones
  // users drill into; completed ones stay collapsed to keep long loops scannable.
  const [expanded, setExpanded] = useState(() => isUnfinished(iteration.status));
  const duration = formatDuration(iteration.start_time, iteration.end_time);
  const subTasks = Array.isArray(iteration.children) ? iteration.children : [];

  return (
    <Box
      data-testid={`foreach-iteration-${iteration.id}`}
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
        <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
          {iteration.id}
        </Typography>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
          {duration && (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
              <AccessTime sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600] }} />
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600] }}>{duration}</Typography>
            </Box>
          )}
          {iteration.status && <Label text={iteration.status.toUpperCase()} />}
          {expanded ? (
            <ExpandLess sx={{ fontSize: 'var(--ds-text-title)', color: ds.gray[600] }} />
          ) : (
            <ExpandMore sx={{ fontSize: 'var(--ds-text-title)', color: ds.gray[600] }} />
          )}
        </Box>
      </Box>
      <Collapse in={expanded} unmountOnExit>
        <Box sx={{ padding: 'var(--ds-space-2) var(--ds-space-3) var(--ds-space-3) var(--ds-space-3)' }}>
          {iteration.error && (
            <Box
              sx={{
                backgroundColor: ds.red[100],
                border: `1px solid ${ds.red[200]}`,
                borderRadius: 'var(--ds-radius-sm)',
                padding: 'var(--ds-space-1) var(--ds-space-2)',
                marginBottom: 'var(--ds-space-2)',
                fontFamily: 'monospace',
                fontSize: 'var(--ds-text-caption)',
                color: ds.red[600],
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
              }}
            >
              {iteration.error}
            </Box>
          )}
          {subTasks.length > 0 ? (
            subTasks.map((child) => <ChildTaskCard key={child.id} task={child} copyToClipboard={copyToClipboard} />)
          ) : (
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], fontStyle: 'italic' }}>No tasks ran</Typography>
          )}
        </Box>
      </Collapse>
    </Box>
  );
};

// Renders the per-iteration drill-down for a core.foreach execution in the
// Executions right panel. Mirrors CallWorkflowChildren's card pattern but adds
// an iteration grouping level (Iteration N → sub-task cards).
const ForeachIterationChildren: React.FC<ForeachIterationChildrenProps> = ({ entries, copyToClipboard }) => {
  if (!Array.isArray(entries) || entries.length === 0) {
    return null;
  }

  const iterations = entries.filter((c) => c?.type === 'iteration');
  // Defensive: surface unexpected non-iteration entries directly instead of dropping them
  const others = entries.filter((c) => c?.type !== 'iteration');
  const completed = iterations.filter((it) => String(it.status ?? '').toUpperCase() === 'COMPLETED').length;
  const failed = iterations.filter((it) => String(it.status ?? '').toUpperCase() === 'FAILED').length;

  return (
    <Box sx={{ marginTop: 'var(--ds-space-4)', padding: 'var(--ds-space-3)', borderTop: `1px solid ${ds.blue[300]}` }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', marginBottom: 'var(--ds-space-2)' }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-small)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: ds.gray[700],
            fontFamily: 'Poppins, sans-serif',
          }}
        >
          Iterations ({iterations.length})
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600] }}>
          {completed} completed{failed > 0 ? `, ${failed} failed` : ''}
        </Typography>
      </Box>
      {iterations.map((iteration) => (
        <IterationCard key={iteration.id} iteration={iteration} copyToClipboard={copyToClipboard} />
      ))}
      {others.map((child) => (
        <ChildTaskCard key={child.id} task={child} copyToClipboard={copyToClipboard} />
      ))}
    </Box>
  );
};

export default ForeachIterationChildren;

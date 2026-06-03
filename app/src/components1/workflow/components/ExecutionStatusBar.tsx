import { useState } from 'react';
import { Box, Typography, CircularProgress } from '@mui/material';
import { Input } from '@components1/ds/Input';
import { Button } from '@components1/ds/Button';
import HourglassEmptyIcon from '@mui/icons-material/HourglassEmpty';

export interface PendingApproval {
  taskId: string;
  options: string[];
}

interface ExecutionStatusBarProps {
  visible: boolean;
  completedTasks?: number;
  totalTasks?: number;
  pendingApprovals?: PendingApproval[];
  onApprove?: (taskId: string, status: string, comments?: string) => Promise<void> | void;
  approvalLoading?: string | null; // value: `${taskId}:${status}`
  top?: number | string;
}

const ExecutionStatusBar: React.FC<ExecutionStatusBarProps> = ({
  visible,
  completedTasks = 0,
  totalTasks = 0,
  pendingApprovals = [],
  onApprove,
  approvalLoading = null,
  top = 80,
}) => {
  const [commentsByTask, setCommentsByTask] = useState<Record<string, string>>({});

  const hasPendingApproval = pendingApprovals.length > 0;
  if (!visible && !hasPendingApproval) {
    return null;
  }

  return (
    <Box
      sx={{
        position: 'absolute',
        top,
        left: '50%',
        transform: 'translateX(-50%)',
        zIndex: 15,
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        gap: 'var(--ds-space-2)',
        width: 'max-content',
        maxWidth: '480px',
      }}
    >
      <Box
        sx={{
          backgroundColor: 'rgba(255, 255, 255, 0.95)',
          border: `2px solid ${hasPendingApproval ? '#f59e0b' : '#2563eb'}`,
          borderRadius: 'var(--ds-radius-xl)',
          padding: 'var(--ds-space-2) var(--ds-space-4)',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-3)',
          boxShadow: '0 4px 12px rgba(0, 0, 0, 0.15)',
        }}
      >
        <CircularProgress size={18} thickness={4} sx={{ color: hasPendingApproval ? '#f59e0b' : '#2563eb' }} />
        <Typography variant='body2' sx={{ fontWeight: 'var(--ds-font-weight-medium)', color: hasPendingApproval ? '#f59e0b' : '#2563eb' }}>
          Manual run in progress...
        </Typography>
        {totalTasks > 0 && (
          <Typography variant='caption' sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-caption)' }}>
            {completedTasks}/{totalTasks} tasks
          </Typography>
        )}
        {hasPendingApproval && (
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--ds-space-1)',
              borderLeft: '1px solid var(--ds-brand-150)',
              paddingLeft: 'var(--ds-space-3)',
            }}
          >
            <HourglassEmptyIcon sx={{ fontSize: 'var(--ds-text-title)', color: 'var(--ds-amber-400)' }} />
            <Typography
              variant='caption'
              sx={{ color: 'var(--ds-amber-700)', fontWeight: 'var(--ds-font-weight-medium)', fontSize: 'var(--ds-text-small)' }}
            >
              Waiting for approval
            </Typography>
          </Box>
        )}
        <div
          style={{
            width: '12px',
            height: '12px',
            borderRadius: '50%',
            backgroundColor: hasPendingApproval ? '#f59e0b' : '#2563eb',
            animation: 'pulse 1.5s ease-in-out infinite',
          }}
        />
      </Box>

      {hasPendingApproval &&
        onApprove &&
        pendingApprovals.map((pa) => (
          <Box
            key={pa.taskId}
            sx={{
              backgroundColor: 'var(--ds-background-100)',
              border: '1px solid var(--ds-brand-150)',
              borderRadius: 'var(--ds-radius-xl)',
              padding: 'var(--ds-space-2) var(--ds-space-3)',
              width: '460px',
              display: 'flex',
              flexDirection: 'column',
              gap: 'var(--ds-space-2)',
              boxShadow: '0 4px 12px rgba(0, 0, 0, 0.08)',
            }}
          >
            <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)' }}>
              {pa.taskId}: respond below or via Slack/MS Teams
            </Typography>
            <Input
              size='sm'
              type='textarea'
              rows={2}
              placeholder='Optional comments'
              value={commentsByTask[pa.taskId] ?? ''}
              onChange={(next) => setCommentsByTask((prev) => ({ ...prev, [pa.taskId]: next }))}
              disabled={approvalLoading !== null}
            />
            {pa.options.length === 0 ? (
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', fontStyle: 'italic' }}>
                No approval_options configured on this task.
              </Typography>
            ) : (
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-2)' }}>
                {pa.options.map((opt, i) => {
                  const key = `${pa.taskId}:${opt}`;
                  return (
                    <Button
                      key={opt}
                      id={`workflow-approval-${opt}-btn`}
                      tone={i === 0 ? 'primary' : 'secondary'}
                      size='sm'
                      disabled={approvalLoading !== null}
                      loading={approvalLoading === key}
                      onClick={() => onApprove(pa.taskId, opt, commentsByTask[pa.taskId] || undefined)}
                    >
                      {opt}
                    </Button>
                  );
                })}
              </Box>
            )}
          </Box>
        ))}
    </Box>
  );
};

export default ExecutionStatusBar;

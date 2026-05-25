import { useState } from 'react';
import { Box, Typography, CircularProgress, Button, TextField } from '@mui/material';
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
        gap: '8px',
        width: 'max-content',
        maxWidth: '480px',
      }}
    >
      <Box
        sx={{
          backgroundColor: 'rgba(255, 255, 255, 0.95)',
          border: `2px solid ${hasPendingApproval ? '#f59e0b' : '#2563eb'}`,
          borderRadius: '25px',
          padding: '8px 20px',
          display: 'flex',
          alignItems: 'center',
          gap: '12px',
          boxShadow: '0 4px 12px rgba(0, 0, 0, 0.15)',
        }}
      >
        <CircularProgress size={18} thickness={4} sx={{ color: hasPendingApproval ? '#f59e0b' : '#2563eb' }} />
        <Typography variant='body2' sx={{ fontWeight: 500, color: hasPendingApproval ? '#f59e0b' : '#2563eb' }}>
          Manual run in progress...
        </Typography>
        {totalTasks > 0 && (
          <Typography variant='caption' sx={{ color: '#6b7280', fontSize: '11px' }}>
            {completedTasks}/{totalTasks} tasks
          </Typography>
        )}
        {hasPendingApproval && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px', borderLeft: '1px solid #e5e7eb', paddingLeft: '12px' }}>
            <HourglassEmptyIcon sx={{ fontSize: '16px', color: '#f59e0b' }} />
            <Typography variant='caption' sx={{ color: '#92400e', fontWeight: 500, fontSize: '12px' }}>
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
              backgroundColor: '#ffffff',
              border: '1px solid #e5e7eb',
              borderRadius: '12px',
              padding: '10px 14px',
              width: '460px',
              display: 'flex',
              flexDirection: 'column',
              gap: '8px',
              boxShadow: '0 4px 12px rgba(0, 0, 0, 0.08)',
            }}
          >
            <Typography sx={{ fontSize: '12px', fontWeight: 600, color: '#374151' }}>{pa.taskId}: respond below or via Slack/MS Teams</Typography>
            <TextField
              size='small'
              multiline
              rows={2}
              placeholder='Optional comments'
              value={commentsByTask[pa.taskId] ?? ''}
              onChange={(e) => setCommentsByTask((prev) => ({ ...prev, [pa.taskId]: e.target.value }))}
              disabled={approvalLoading !== null}
              sx={{ backgroundColor: 'white' }}
            />
            {pa.options.length === 0 ? (
              <Typography sx={{ fontSize: '11px', color: '#6b7280', fontStyle: 'italic' }}>No approval_options configured on this task.</Typography>
            ) : (
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: '8px' }}>
                {pa.options.map((opt, i) => {
                  const key = `${pa.taskId}:${opt}`;
                  return (
                    <Button
                      key={opt}
                      data-testid={`workflow-approval-${opt}-btn`}
                      variant={i === 0 ? 'contained' : 'outlined'}
                      size='small'
                      disabled={approvalLoading !== null}
                      onClick={() => onApprove(pa.taskId, opt, commentsByTask[pa.taskId] || undefined)}
                      sx={{ textTransform: 'none', minWidth: '88px' }}
                    >
                      {approvalLoading === key ? <CircularProgress size={14} sx={{ color: 'inherit' }} /> : opt}
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

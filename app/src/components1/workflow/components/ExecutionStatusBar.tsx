import { Box, Typography, CircularProgress } from '@mui/material';
import HourglassEmptyIcon from '@mui/icons-material/HourglassEmpty';
import type { Node } from 'reactflow';

interface ExecutionStatusBarProps {
  isTestRunning: boolean;
  nodes: Node[];
}

const ExecutionStatusBar: React.FC<ExecutionStatusBarProps> = ({ isTestRunning, nodes }) => {
  if (!isTestRunning) {
    return null;
  }

  const completedTasks = nodes.filter(
    (node) => node.type === 'action' && (node.data.executionStatus === 'COMPLETED' || node.data.executionStatus === 'FAILED')
  ).length;
  const totalTasks = nodes.filter((node) => node.type === 'action').length;

  const hasPendingApproval = nodes.some(
    (node) => node.type === 'action' && node.data.taskConfig?.type === 'core.approval' && node.data.executionStatus === 'SCHEDULED'
  );

  return (
    <Box
      sx={{
        position: 'absolute',
        top: '80px',
        left: '50%',
        transform: 'translateX(-50%)',
        backgroundColor: 'rgba(255, 255, 255, 0.95)',
        border: `2px solid ${hasPendingApproval ? '#f59e0b' : '#2563eb'}`,
        borderRadius: '25px',
        padding: '8px 20px',
        display: 'flex',
        alignItems: 'center',
        gap: '12px',
        zIndex: 15,
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
  );
};

export default ExecutionStatusBar;

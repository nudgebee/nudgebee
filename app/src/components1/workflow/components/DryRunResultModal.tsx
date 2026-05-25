import { Box, Typography, Chip } from '@mui/material';
import { Modal } from '@components1/common/modal';
import JsonTreeView from '@components1/common/JsonTreeView';
import type { WorkflowDryRunResponse } from '@api1/workflow/types';

interface DryRunResultModalProps {
  open: boolean;
  onClose: () => void;
  result: WorkflowDryRunResponse | null;
}

const DryRunResultModal: React.FC<DryRunResultModalProps> = ({ open, onClose, result }) => {
  if (!result) {
    return null;
  }

  const isSuccess = result.status === 'COMPLETED';
  const isFailed = result.status === 'FAILED';

  return (
    <Modal open={open} handleClose={onClose} title='Dry Run Result' width='lg' maxHeight='80vh'>
      <Box sx={{ padding: '16px 0' }}>
        {/* Status Badge */}
        <Box sx={{ mb: 3, display: 'flex', alignItems: 'center', gap: 2 }}>
          <Typography variant='subtitle2' sx={{ fontWeight: 600, color: '#374151' }}>
            Status:
          </Typography>
          <Chip
            label={result.status}
            size='small'
            sx={{
              backgroundColor: isSuccess ? '#d1fae5' : isFailed ? '#fee2e2' : '#e5e7eb',
              color: isSuccess ? '#065f46' : isFailed ? '#991b1b' : '#374151',
              fontWeight: 600,
            }}
          />
        </Box>

        {/* Error Message */}
        {result.error && (
          <Box sx={{ mb: 3 }}>
            <Typography variant='subtitle2' sx={{ fontWeight: 600, mb: 1, color: '#991b1b' }}>
              Error:
            </Typography>
            <Box
              sx={{
                backgroundColor: '#fef2f2',
                border: '1px solid #fecaca',
                borderRadius: '8px',
                padding: '12px',
                fontFamily: 'monospace',
                fontSize: '12px',
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-all',
                color: '#991b1b',
                overflow: 'auto',
                maxHeight: '300px',
              }}
            >
              {result.error}
            </Box>
          </Box>
        )}

        {/* Output */}
        {result.output && (
          <Box sx={{ mb: 3 }}>
            <Typography variant='subtitle2' sx={{ fontWeight: 600, mb: 1, color: '#374151' }}>
              Output:
            </Typography>
            <JsonTreeView data={result.output} defaultExpanded={2} maxHeight='300px' fontSize='12px' />
          </Box>
        )}

        {/* Per-Task Results (if available) */}
        {result.tasks && result.tasks.length > 0 && (
          <Box>
            <Typography variant='subtitle2' sx={{ fontWeight: 600, mb: 2, color: '#374151' }}>
              Task Results:
            </Typography>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
              {result.tasks.map((task) => (
                <Box
                  key={task.id}
                  sx={{
                    backgroundColor: '#f8f9fa',
                    border: '1px solid #e5e7eb',
                    borderRadius: '8px',
                    padding: '12px',
                  }}
                >
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, mb: 1 }}>
                    <Typography variant='body2' sx={{ fontWeight: 600, color: '#374151' }}>
                      {task.id}
                    </Typography>
                    <Chip
                      label={task.status}
                      size='small'
                      sx={{
                        backgroundColor: task.status === 'COMPLETED' ? '#d1fae5' : task.status === 'FAILED' ? '#fee2e2' : '#e5e7eb',
                        color: task.status === 'COMPLETED' ? '#065f46' : task.status === 'FAILED' ? '#991b1b' : '#374151',
                        fontSize: '10px',
                      }}
                    />
                  </Box>
                  {task.error && (
                    <Typography variant='caption' sx={{ color: '#991b1b', display: 'block', mb: 1 }}>
                      Error: {task.error}
                    </Typography>
                  )}
                  {task.output && <JsonTreeView data={task.output} defaultExpanded={1} maxHeight='150px' fontSize='11px' />}
                </Box>
              ))}
            </Box>
          </Box>
        )}
      </Box>
    </Modal>
  );
};

export default DryRunResultModal;

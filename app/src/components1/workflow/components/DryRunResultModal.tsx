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
      <Box sx={{ padding: 'var(--ds-space-4) 0' }}>
        {/* Status Badge */}
        <Box sx={{ mb: 3, display: 'flex', alignItems: 'center', gap: 2 }}>
          <Typography variant='subtitle2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)' }}>
            Status:
          </Typography>
          <Chip
            label={result.status}
            size='small'
            sx={{
              backgroundColor: isSuccess ? '#d1fae5' : isFailed ? '#fee2e2' : '#e5e7eb',
              color: isSuccess ? '#065f46' : isFailed ? '#991b1b' : '#374151',
              fontWeight: 'var(--ds-font-weight-semibold)',
            }}
          />
        </Box>

        {/* Error Message */}
        {result.error && (
          <Box sx={{ mb: 3 }}>
            <Typography variant='subtitle2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 1, color: 'var(--ds-red-700)' }}>
              Error:
            </Typography>
            <Box
              sx={{
                backgroundColor: 'var(--ds-red-100)',
                border: '1px solid var(--ds-red-200)',
                borderRadius: 'var(--ds-radius-lg)',
                padding: 'var(--ds-space-3)',
                fontFamily: 'monospace',
                fontSize: 'var(--ds-text-small)',
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-all',
                color: 'var(--ds-red-700)',
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
            <Typography variant='subtitle2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 1, color: 'var(--ds-brand-500)' }}>
              Output:
            </Typography>
            <JsonTreeView data={result.output} defaultExpanded={2} maxHeight='300px' fontSize='12px' />
          </Box>
        )}

        {/* Per-Task Results (if available) */}
        {result.tasks && result.tasks.length > 0 && (
          <Box>
            <Typography variant='subtitle2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 2, color: 'var(--ds-brand-500)' }}>
              Task Results:
            </Typography>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
              {result.tasks.map((task) => (
                <Box
                  key={task.id}
                  sx={{
                    backgroundColor: 'var(--ds-background-200)',
                    border: '1px solid var(--ds-brand-150)',
                    borderRadius: 'var(--ds-radius-lg)',
                    padding: 'var(--ds-space-3)',
                  }}
                >
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, mb: 1 }}>
                    <Typography variant='body2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)' }}>
                      {task.id}
                    </Typography>
                    <Chip
                      label={task.status}
                      size='small'
                      sx={{
                        backgroundColor: task.status === 'COMPLETED' ? '#d1fae5' : task.status === 'FAILED' ? '#fee2e2' : '#e5e7eb',
                        color: task.status === 'COMPLETED' ? '#065f46' : task.status === 'FAILED' ? '#991b1b' : '#374151',
                        fontSize: 'var(--ds-text-caption)',
                      }}
                    />
                  </Box>
                  {task.error && (
                    <Typography variant='caption' sx={{ color: 'var(--ds-red-700)', display: 'block', mb: 1 }}>
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

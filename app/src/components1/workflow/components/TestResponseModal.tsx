import { Box, Typography } from '@mui/material';
import { Modal } from '@components1/common/modal';
import JsonTreeView from '@components1/common/JsonTreeView';

interface TestResponseModalProps {
  open: boolean;
  onClose: () => void;
  taskType: string;
  responseData: any;
}

const renderResult = (result: any) => {
  if (result?.data) {
    return (
      <Box>
        <Typography variant='subtitle2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 2, color: 'var(--ds-brand-500)' }}>
          Task Output:
        </Typography>
        <Box
          sx={{
            backgroundColor: 'var(--ds-background-200)',
            border: '1px solid var(--ds-brand-150)',
            borderRadius: 'var(--ds-radius-lg)',
            padding: 'var(--ds-space-3)',
            fontFamily: 'monospace',
            fontSize: 'var(--ds-text-small)',
            whiteSpace: 'pre-wrap',
            overflowX: 'auto',
            maxHeight: '400px',
            overflow: 'auto',
          }}
        >
          {typeof result.data === 'string' ? result.data : JSON.stringify(result.data, null, 2)}
        </Box>
      </Box>
    );
  }
  if (result) {
    return (
      <Box>
        <Typography variant='subtitle2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 2, color: 'var(--ds-brand-500)' }}>
          Full Response:
        </Typography>
        <JsonTreeView data={result} defaultExpanded={2} maxHeight='400px' fontSize='12px' />
      </Box>
    );
  }
  return null;
};

const TestResponseModal: React.FC<TestResponseModalProps> = ({ open, onClose, taskType, responseData }) => {
  const status = responseData?.status;
  const result = responseData?.result;
  const error = responseData?.error;

  return (
    <Modal open={open} handleClose={onClose} title={`Test Task Response: ${taskType}`} width='lg' maxHeight='80vh'>
      <Box sx={{ padding: 'var(--ds-space-4) 0' }}>
        {responseData && (
          <Box>
            {status && (
              <Typography
                variant='subtitle2'
                sx={{
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  mb: 2,
                  color: status === 'COMPLETED' ? '#059669' : '#dc2626',
                }}
              >
                Status: {status}
              </Typography>
            )}
            {error && (
              <Box
                sx={{
                  backgroundColor: 'var(--ds-red-100)',
                  border: '1px solid var(--ds-red-200)',
                  borderRadius: 'var(--ds-radius-lg)',
                  padding: 'var(--ds-space-3)',
                  mb: 2,
                  fontFamily: 'monospace',
                  fontSize: 'var(--ds-text-small)',
                  color: 'var(--ds-red-600)',
                  whiteSpace: 'pre-wrap',
                }}
              >
                {typeof error === 'string' ? error : JSON.stringify(error, null, 2)}
              </Box>
            )}
            {renderResult(result)}
          </Box>
        )}
      </Box>
    </Modal>
  );
};

export default TestResponseModal;

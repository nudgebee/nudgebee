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
        <Typography variant='subtitle2' sx={{ fontWeight: 600, mb: 2, color: '#374151' }}>
          Task Output:
        </Typography>
        <Box
          sx={{
            backgroundColor: '#f8f9fa',
            border: '1px solid #e5e7eb',
            borderRadius: '8px',
            padding: '12px',
            fontFamily: 'monospace',
            fontSize: '12px',
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
        <Typography variant='subtitle2' sx={{ fontWeight: 600, mb: 2, color: '#374151' }}>
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
      <Box sx={{ padding: '16px 0' }}>
        {responseData && (
          <Box>
            {status && (
              <Typography
                variant='subtitle2'
                sx={{
                  fontWeight: 600,
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
                  backgroundColor: '#fef2f2',
                  border: '1px solid #fecaca',
                  borderRadius: '8px',
                  padding: '12px',
                  mb: 2,
                  fontFamily: 'monospace',
                  fontSize: '12px',
                  color: '#dc2626',
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

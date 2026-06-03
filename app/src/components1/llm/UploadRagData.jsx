import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import Text from '@common-new/format/Text';
import { Button } from '@components1/ds/Button';
import { UploadIcon } from '@assets';
import { toast as snackbar } from '@components1/ds/Toast';
import apiAskNudgebee from '@api1/ask-nudgebee';
import SafeIcon from '@components1/common/SafeIcon';
import { ds } from '@utils/colors';

const UploadRagData = ({ handleClose, agentInternalName, accountId, agentDisplayName }) => {
  const [selectedFile, setSelectedFile] = React.useState(null);
  const [isDragging, setIsDragging] = React.useState(false);
  const fileInputRef = React.useRef(null);
  const dropZoneRef = React.useRef(null);

  const validateFile = (file) => {
    const validTypes = ['text/plain', 'text/csv', 'application/json', 'text/xml', 'application/xml'];
    if (validTypes.includes(file.type)) {
      setSelectedFile(file);
      return true;
    }
    snackbar.error('Please select only .txt, .csv, .json, or .xml files');
    fileInputRef.current.value = '';
    return false;
  };

  const handleFileSelect = (event) => {
    const file = event.target.files[0];
    if (file) {
      validateFile(file);
    }
  };

  const handleDragEnter = (e) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragging(true);
  };

  const handleDragLeave = (e) => {
    e.preventDefault();
    e.stopPropagation();
    if (e.target === dropZoneRef.current) {
      setIsDragging(false);
    }
  };

  const handleDragOver = (e) => {
    e.preventDefault();
    e.stopPropagation();
  };

  const handleDrop = (e) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragging(false);

    const file = e.dataTransfer.files[0];
    if (file) {
      validateFile(file);
    }
  };

  const handleUpload = async () => {
    if (!selectedFile) {
      snackbar.error('Please select a file to upload');
      return;
    }

    try {
      const reader = new FileReader();
      reader.onload = async (e) => {
        try {
          const fileContent = e.target.result;

          let format = 'text';
          if (selectedFile.type === 'text/csv') {
            format = 'csv';
          } else if (selectedFile.type === 'application/json') {
            format = 'json';
          } else if (selectedFile.type === 'text/xml' || selectedFile.type === 'application/xml') {
            format = 'xml';
          }

          const response = await apiAskNudgebee.createRagData({
            account_id: accountId,
            agent: agentInternalName,
            data: fileContent,
            format: format,
            file_name: selectedFile.name,
          });

          if (response.data?.errors) {
            snackbar.error('Failed to upload RAG data');
            console.error('Upload error:', response.data.errors);
            return;
          }

          snackbar.success('RAG data uploaded successfully');
          handleClose('success');
        } catch (error) {
          snackbar.error('Failed to upload RAG data');
          console.error('Upload error:', error);
        }
      };

      reader.onerror = () => {
        snackbar.error('Failed to read file');
      };

      reader.readAsText(selectedFile);
    } catch (error) {
      snackbar.error('Failed to process file');
      console.error('File processing error:', error);
    }
  };

  return (
    <Box sx={{ padding: ds.space[5] }}>
      <Text
        value={`Upload RAG Data for ${agentDisplayName}`}
        sx={{
          fontSize: 'var(--ds-text-title)',
          fontWeight: 'var(--ds-font-weight-semibold)',
          color: 'var(--ds-blue-500)',
          marginBottom: ds.space[0],
          textAlign: 'center',
        }}
      />
      <Text
        value={`The system processes each line as an independent record, irrespective of the file's format or structure.`}
        sx={{
          fontSize: 'var(--ds-text-body)',
          color: 'var(--ds-gray-700)',
          marginBottom: ds.space[4],
          textAlign: 'center',
        }}
      />
      <Box
        ref={dropZoneRef}
        onDragEnter={handleDragEnter}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
        sx={{
          border: `${ds.space[0]} dashed ${isDragging ? 'var(--ds-blue-600)' : 'var(--ds-gray-200)'}`,
          borderRadius: ds.radius.lg,
          padding: `${ds.space[6]} ${ds.space[5]}`,
          marginBottom: ds.space[5],
          backgroundColor: isDragging ? `var(--ds-blue-600)10` : 'var(--ds-background-200)',
          transition: 'all 0.2s ease-in-out',
          cursor: 'pointer',
          '&:hover': {
            borderColor: 'var(--ds-blue-600)',
            backgroundColor: `var(--ds-blue-600)10`,
          },
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          gap: ds.space[3],
        }}
        onClick={() => fileInputRef.current?.click()}
      >
        <input ref={fileInputRef} type='file' accept='.txt,.csv,.json,.xml' onChange={handleFileSelect} style={{ display: 'none' }} />
        <Box
          sx={{
            width: ds.space[7],
            height: ds.space[7],
            borderRadius: '50%',
            backgroundColor: `var(--ds-blue-600)20`,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            marginBottom: ds.space[2],
          }}
        >
          <SafeIcon src={UploadIcon} alt='Upload' />
        </Box>
        <Text
          value={selectedFile ? selectedFile.name : 'Click or drag file to upload'}
          sx={{
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-blue-500)',
            textAlign: 'center',
          }}
        />
        <Text
          value='Supported formats: .txt, .csv, .json, .xml'
          sx={{
            fontSize: 'var(--ds-text-small)',
            color: 'var(--ds-gray-700)',
            textAlign: 'center',
          }}
        />
      </Box>

      <Box
        sx={{
          display: 'flex',
          justifyContent: 'center',
          gap: ds.space[3],
          marginTop: ds.space[2],
        }}
      >
        <Button tone='secondary' size='sm' onClick={() => handleClose()} sx={{ minWidth: ds.space.mul(2, 15) }}>
          Cancel
        </Button>
        <Button tone='primary' size='sm' onClick={handleUpload} disabled={!selectedFile} sx={{ minWidth: ds.space.mul(2, 15) }}>
          {selectedFile ? 'Upload' : 'Select File'}
        </Button>
      </Box>
    </Box>
  );
};

UploadRagData.propTypes = {
  handleClose: PropTypes.func.isRequired,
  agentInternalName: PropTypes.string.isRequired,
  accountId: PropTypes.string.isRequired,
};

export default UploadRagData;

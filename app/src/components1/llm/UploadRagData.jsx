import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { Text } from '@components1/common';
import CustomButton from '@components1/common/NewCustomButton';
import { UploadIcon } from '@assets';
import { colors } from 'src/utils/colors';
import { snackbar } from '@components1/common/snackbarService';
import apiAskNudgebee from '@api1/ask-nudgebee';
import SafeIcon from '@components1/common/SafeIcon';

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
    <Box sx={{ padding: '24px' }}>
      <Text
        value={`Upload RAG Data for ${agentDisplayName}`}
        sx={{
          fontSize: '16px',
          fontWeight: 600,
          color: colors.text.primary,
          marginBottom: '2px',
          textAlign: 'center',
        }}
      />
      <Text
        value={`The system processes each line as an independent record, irrespective of the file's format or structure.`}
        sx={{
          fontSize: '13px',
          color: colors.text.secondary,
          marginBottom: '16px',
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
          border: `2px dashed ${isDragging ? colors.primary : colors.border.secondaryLightest}`,
          borderRadius: '8px',
          padding: '32px 24px',
          marginBottom: '24px',
          backgroundColor: isDragging ? `${colors.primary}10` : colors.background.tertiaryLightestestest,
          transition: 'all 0.2s ease-in-out',
          cursor: 'pointer',
          '&:hover': {
            borderColor: colors.primary,
            backgroundColor: `${colors.primary}10`,
          },
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          gap: '12px',
        }}
        onClick={() => fileInputRef.current?.click()}
      >
        <input ref={fileInputRef} type='file' accept='.txt,.csv,.json,.xml' onChange={handleFileSelect} style={{ display: 'none' }} />
        <Box
          sx={{
            width: '48px',
            height: '48px',
            borderRadius: '50%',
            backgroundColor: `${colors.primary}20`,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            marginBottom: '8px',
          }}
        >
          <SafeIcon src={UploadIcon} alt='Upload' />
        </Box>
        <Text
          value={selectedFile ? selectedFile.name : 'Click or drag file to upload'}
          sx={{
            fontSize: '14px',
            fontWeight: 500,
            color: colors.text.primary,
            textAlign: 'center',
          }}
        />
        <Text
          value='Supported formats: .txt, .csv, .json, .xml'
          sx={{
            fontSize: '12px',
            color: colors.text.secondary,
            textAlign: 'center',
          }}
        />
      </Box>

      <Box
        sx={{
          display: 'flex',
          justifyContent: 'center',
          gap: '12px',
          marginTop: '8px',
        }}
      >
        <CustomButton
          variant='secondary'
          size='Small'
          text='Cancel'
          onClick={() => handleClose()}
          sx={{
            minWidth: '120px',
            transition: 'all 0.2s ease-in-out',
            '&:hover': {
              backgroundColor: `${colors.error}10`,
              color: colors.error,
            },
          }}
        />
        <CustomButton
          variant='primary'
          size='Small'
          text={selectedFile ? 'Upload' : 'Select File'}
          onClick={handleUpload}
          disabled={!selectedFile}
          sx={{
            minWidth: '120px',
            transition: 'all 0.2s ease-in-out',
            opacity: selectedFile ? 1 : 0.7,
            backgroundColor: colors.primary,
            '&:hover': {
              backgroundColor: selectedFile ? colors.primary : undefined,
              filter: selectedFile ? 'brightness(0.9)' : undefined,
            },
          }}
        />
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

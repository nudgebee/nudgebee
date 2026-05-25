import React from 'react';
import { Box, Typography, Alert, CircularProgress } from '@mui/material';
import CodeMirror from '@uiw/react-codemirror';
import { json } from '@codemirror/lang-json';
import CustomButton from '@components1/common/NewCustomButton';
import { colors } from 'src/utils/colors';

interface JsonEditorTabProps {
  jsonText: string;
  onChange: (text: string) => void;
  onApply: () => void;
  isValid: boolean;
  parseError: string;
  hasUnsavedChanges: boolean;
  disabled?: boolean;
  canRevert?: boolean;
  onRevert?: () => void;
  isLoading?: boolean;
}

const JsonEditorTab: React.FC<JsonEditorTabProps> = ({
  jsonText,
  onChange,
  onApply,
  isValid,
  parseError,
  hasUnsavedChanges,
  disabled = false,
  canRevert = false,
  onRevert,
  isLoading = false,
}) => {
  return (
    <Box
      sx={{
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
        backgroundColor: '#f6f6f6',
        p: 1.5,
        position: 'relative',
      }}
    >
      {/* Loading overlay */}
      {isLoading && (
        <Box
          sx={{
            position: 'absolute',
            top: 0,
            left: 0,
            right: 0,
            bottom: 0,
            backgroundColor: 'rgba(255, 255, 255, 0.8)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            zIndex: 10,
            borderRadius: '8px',
          }}
        >
          <Box sx={{ textAlign: 'center' }}>
            <CircularProgress size={40} sx={{ color: colors.primary }} />
            <Typography sx={{ mt: 2, color: colors.text.secondary, fontSize: '14px' }}>Applying automation...</Typography>
          </Box>
        </Box>
      )}
      {/* Header with instructions */}
      <Box sx={{ mb: 1 }}>
        <Typography variant='h6' sx={{ color: colors.text.secondary, mb: 1, fontSize: '16px', fontWeight: 500 }}>
          Automation JSON Editor
        </Typography>
      </Box>

      {/* Error Alert */}
      {parseError && (
        <Alert severity='error' sx={{ mb: 2 }}>
          <Typography variant='body2' sx={{ fontSize: '13px' }}>
            <strong>JSON Parse Error:</strong> {parseError}
          </Typography>
        </Alert>
      )}

      {/* Revert banner - shows after LLM apply */}
      {canRevert && onRevert && (
        <Box
          sx={{
            backgroundColor: '#e3f2fd',
            border: '1px solid #2196f3',
            borderRadius: '6px',
            p: 1.5,
            mb: 2,
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
          }}
        >
          <Typography variant='body2' sx={{ color: '#1565c0', fontSize: '13px' }}>
            LLM changes were applied. You can revert to the previous version.
          </Typography>
          <CustomButton text='Revert' variant='secondary' size='Small' onClick={onRevert} />
        </Box>
      )}

      {/* Unsaved changes banner */}
      {hasUnsavedChanges && !parseError && (
        <Box
          sx={{
            backgroundColor: '#fff4e5',
            border: '1px solid #ffa726',
            borderRadius: '6px',
            p: 2,
            mb: 2,
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
          }}
        >
          <Typography variant='body2' sx={{ color: '#e65100', fontSize: '13px' }}>
            You have unsaved JSON changes. Click "Apply" to sync with visual editor.
          </Typography>
          <CustomButton text='Apply' variant='primary' size='Medium' onClick={onApply} disabled={!isValid || disabled} />
        </Box>
      )}

      {/* JSON Editor */}
      <Box
        sx={{
          flex: 1,
          border: parseError ? '2px solid #ef4444' : '1px solid #d1d5db',
          borderRadius: '8px',
          height: 'calc( 100% - 32px )',
          width: '100%',
          overflow: 'auto',
          backgroundColor: '#ffffff',
          minHeight: 0,
          scrollBehavior: 'revert',
        }}
      >
        <CodeMirror
          value={jsonText}
          height='100%'
          extensions={[json()]}
          onChange={onChange}
          theme={undefined}
          basicSetup={{
            lineNumbers: true,
            foldGutter: true,
            dropCursor: false,
            allowMultipleSelections: false,
            indentOnInput: true,
            bracketMatching: true,
            closeBrackets: true,
            autocompletion: true,
            highlightActiveLine: true,
            highlightSelectionMatches: true,
          }}
        />
      </Box>
    </Box>
  );
};

export default JsonEditorTab;

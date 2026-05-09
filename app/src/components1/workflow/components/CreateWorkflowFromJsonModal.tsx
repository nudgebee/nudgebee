import React, { useEffect, useState } from 'react';
import { Box, Alert, Typography } from '@mui/material';
import CodeMirror from '@uiw/react-codemirror';
import { json } from '@codemirror/lang-json';
import { useRouter } from 'next/router';
import { Modal } from '@components1/common/modal';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import apiWorkflow from '@api1/workflow';
import type { WorkflowCreateRequest } from '@api1/workflow/types';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import { colors } from 'src/utils/colors';

interface CreateWorkflowFromJsonModalProps {
  open: boolean;
  onClose: () => void;
  accountId: string;
  onCreated?: () => void;
}

const DEFAULT_JSON = JSON.stringify(
  {
    name: 'New Automation',
    definition: {
      version: 'v1',
      timeout: '5m',
      inputs: [],
      output: {},
      tasks: [],
      triggers: [{ type: 'manual', params: {} }],
    },
  },
  null,
  2
);

const CreateWorkflowFromJsonModal: React.FC<CreateWorkflowFromJsonModalProps> = ({ open, onClose, accountId, onCreated }) => {
  const router = useRouter();
  const [jsonText, setJsonText] = useState<string>(DEFAULT_JSON);
  const [parseError, setParseError] = useState<string>('');
  const [submitting, setSubmitting] = useState<boolean>(false);

  useEffect(() => {
    if (open) {
      setJsonText(DEFAULT_JSON);
      setParseError('');
      setSubmitting(false);
    }
  }, [open]);

  const handleChange = (value: string) => {
    setJsonText(value);
    if (!value.trim()) {
      setParseError('');
      return;
    }
    try {
      JSON.parse(value);
      setParseError('');
    } catch (err: any) {
      setParseError(err?.message || 'Invalid JSON');
    }
  };

  const handleCreate = async () => {
    let parsed: any;
    try {
      parsed = JSON.parse(jsonText);
    } catch (err: any) {
      setParseError(err?.message || 'Invalid JSON');
      return;
    }

    if (!parsed || typeof parsed !== 'object') {
      snackbar.error('JSON must be an object with "name" and "definition"');
      return;
    }
    if (!parsed.name || typeof parsed.name !== 'string') {
      snackbar.error('JSON must include a string "name" field');
      return;
    }
    if (!parsed.definition || typeof parsed.definition !== 'object') {
      snackbar.error('JSON must include a "definition" object');
      return;
    }

    const request: WorkflowCreateRequest = {
      account_id: accountId,
      workflow: {
        name: parsed.name,
        definition: parsed.definition,
        tags: parsed.tags && typeof parsed.tags === 'object' ? parsed.tags : {},
        status: typeof parsed.status === 'string' ? parsed.status : undefined,
      },
    };

    setSubmitting(true);
    try {
      const response: any = await apiWorkflow.createWorkflow(request);
      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(errorMessage);
        return;
      }
      const newWorkflowId = response?.data?.workflow_create?.id;
      if (!newWorkflowId) {
        snackbar.error('Automation was created but no id was returned');
        return;
      }
      snackbar.success(`Automation "${parsed.name}" created successfully`);
      onCreated?.();
      onClose();
      router.push(`/workflow/${newWorkflowId}?accountId=${accountId}`);
    } catch (err: any) {
      snackbar.error(err?.message || 'Failed to create automation');
    } finally {
      setSubmitting(false);
    }
  };

  const isValid = !parseError && jsonText.trim().length > 0;

  return (
    <Modal
      open={open}
      handleClose={onClose}
      width='lg'
      hideTitleBackground={true}
      title='Create Automation from JSON'
      subtitle='Paste or edit the automation JSON below'
      actionButtons={
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: '12px', padding: '12px 24px' }}>
          <CustomButton text='Cancel' variant='secondary' size='Medium' onClick={onClose} disabled={submitting} />
          <CustomButton text='Create Automation' variant='primary' size='Medium' onClick={handleCreate} disabled={!isValid || submitting} />
        </Box>
      }
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: '12px', padding: '16px 0' }}>
        {parseError && (
          <Alert severity='error'>
            <Typography variant='body2' sx={{ fontSize: '13px' }}>
              <strong>JSON Parse Error:</strong> {parseError}
            </Typography>
          </Alert>
        )}
        <Box
          sx={{
            border: parseError ? '2px solid #ef4444' : '1px solid #d1d5db',
            borderRadius: '8px',
            height: '480px',
            overflow: 'auto',
            backgroundColor: '#ffffff',
          }}
        >
          <CodeMirror
            value={jsonText}
            height='480px'
            extensions={[json()]}
            onChange={handleChange}
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
        <Typography sx={{ fontSize: '12px', color: colors.text.secondaryDark }}>
          The JSON must include a <strong>name</strong> string and a <strong>definition</strong> object. <strong>tags</strong> and{' '}
          <strong>status</strong> are optional.
        </Typography>
      </Box>
    </Modal>
  );
};

export default CreateWorkflowFromJsonModal;

import React, { useEffect, useState } from 'react';
import { Box, Alert, Typography } from '@mui/material';
import CodeMirror from '@uiw/react-codemirror';
import { yaml } from '@codemirror/lang-yaml';
import jsYaml from 'js-yaml';
import { useRouter } from 'next/router';
import { Modal } from '@components1/common/modal';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import apiWorkflow from '@api1/workflow';
import type { WorkflowCreateRequest } from '@api1/workflow/types';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import { colors } from 'src/utils/colors';

interface CreateWorkflowFromYamlModalProps {
  open: boolean;
  onClose: () => void;
  accountId: string;
  onCreated?: () => void;
}

const DEFAULT_YAML = `name: New Automation
definition:
  version: v1
  timeout: 5m
  inputs: []
  output: {}
  tasks: []
  triggers:
    - type: manual
      params: {}
`;

const CreateWorkflowFromYamlModal: React.FC<CreateWorkflowFromYamlModalProps> = ({ open, onClose, accountId, onCreated }) => {
  const router = useRouter();
  const [yamlText, setYamlText] = useState<string>(DEFAULT_YAML);
  const [parseError, setParseError] = useState<string>('');
  const [submitting, setSubmitting] = useState<boolean>(false);

  useEffect(() => {
    if (open) {
      setYamlText(DEFAULT_YAML);
      setParseError('');
      setSubmitting(false);
    }
  }, [open]);

  const handleChange = (value: string) => {
    setYamlText(value);
    if (!value.trim()) {
      setParseError('');
      return;
    }
    try {
      jsYaml.load(value);
      setParseError('');
    } catch (err: any) {
      setParseError(err?.message || 'Invalid YAML');
    }
  };

  const handleCreate = async () => {
    let parsed: any;
    try {
      parsed = jsYaml.load(yamlText);
    } catch (err: any) {
      setParseError(err?.message || 'Invalid YAML');
      return;
    }

    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      snackbar.error('YAML must be a mapping with "name" and "definition"');
      return;
    }
    if (!parsed.name || typeof parsed.name !== 'string') {
      snackbar.error('YAML must include a string "name" field');
      return;
    }
    if (!parsed.definition || typeof parsed.definition !== 'object' || Array.isArray(parsed.definition)) {
      snackbar.error('YAML must include a "definition" mapping');
      return;
    }

    const request: WorkflowCreateRequest = {
      account_id: accountId,
      workflow: {
        name: parsed.name,
        definition: parsed.definition,
        tags: parsed.tags && typeof parsed.tags === 'object' && !Array.isArray(parsed.tags) ? parsed.tags : {},
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
      router.push(`/workflow/${encodeURIComponent(newWorkflowId)}?accountId=${encodeURIComponent(accountId)}`);
    } catch (err: any) {
      snackbar.error(err?.message || 'Failed to create automation');
    } finally {
      setSubmitting(false);
    }
  };

  const isValid = !parseError && yamlText.trim().length > 0;

  return (
    <Modal
      open={open}
      handleClose={onClose}
      width='lg'
      hideTitleBackground={true}
      title='Create Automation from YAML'
      subtitle='Paste or edit the automation YAML below'
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
              <strong>YAML Parse Error:</strong> {parseError}
            </Typography>
          </Alert>
        )}
        <Box
          sx={{
            border: parseError ? `2px solid ${colors.border.error}` : `1px solid ${colors.border.primary}`,
            borderRadius: '8px',
            height: '480px',
            overflow: 'auto',
            backgroundColor: '#ffffff',
          }}
        >
          <CodeMirror
            value={yamlText}
            height='480px'
            extensions={[yaml()]}
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
          The YAML must include a <strong>name</strong> string and a <strong>definition</strong> mapping. <strong>tags</strong> and{' '}
          <strong>status</strong> are optional.
        </Typography>
      </Box>
    </Modal>
  );
};

export default CreateWorkflowFromYamlModal;

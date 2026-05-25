import React, { useEffect, useMemo, useState } from 'react';
import { Box, Alert, Typography } from '@mui/material';
import CodeMirror from '@uiw/react-codemirror';
import { json } from '@codemirror/lang-json';
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
import NewToggleButtons from '../NewToggleButtons';

type CodeFormat = 'json' | 'yaml';

interface CreateWorkflowFromCodeModalProps {
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

const CreateWorkflowFromCodeModal: React.FC<CreateWorkflowFromCodeModalProps> = ({ open, onClose, accountId, onCreated }) => {
  const router = useRouter();
  const [format, setFormat] = useState<CodeFormat>('json');
  const [jsonText, setJsonText] = useState<string>(DEFAULT_JSON);
  const [yamlText, setYamlText] = useState<string>(DEFAULT_YAML);
  const [parseError, setParseError] = useState<string>('');
  const [submitting, setSubmitting] = useState<boolean>(false);

  useEffect(() => {
    if (open) {
      setFormat('json');
      setJsonText(DEFAULT_JSON);
      setYamlText(DEFAULT_YAML);
      setParseError('');
      setSubmitting(false);
    }
  }, [open]);

  const currentText = format === 'json' ? jsonText : yamlText;

  const validate = (value: string, fmt: CodeFormat) => {
    if (!value.trim()) {
      setParseError('');
      return;
    }
    try {
      if (fmt === 'json') {
        JSON.parse(value);
      } else {
        jsYaml.load(value);
      }
      setParseError('');
    } catch (err: any) {
      setParseError(err?.message || `Invalid ${fmt.toUpperCase()}`);
    }
  };

  const handleChange = (value: string) => {
    if (format === 'json') {
      setJsonText(value);
    } else {
      setYamlText(value);
    }
    validate(value, format);
  };

  const handleFormatChange = (next: string) => {
    if (next === format) return;
    const nextFormat = next as CodeFormat;
    setFormat(nextFormat);
    validate(nextFormat === 'json' ? jsonText : yamlText, nextFormat);
  };

  const formatOptions = useMemo(
    () => [
      { value: 'json', label: 'JSON' },
      { value: 'yaml', label: 'YAML' },
    ],
    []
  );

  const handleCreate = async () => {
    let parsed: any;
    try {
      parsed = format === 'json' ? JSON.parse(currentText) : jsYaml.load(currentText);
    } catch (err: any) {
      setParseError(err?.message || `Invalid ${format.toUpperCase()}`);
      return;
    }

    const formatLabel = format.toUpperCase();
    const containerWord = format === 'json' ? 'object' : 'mapping';

    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      snackbar.error(`${formatLabel} must be ${format === 'json' ? 'an' : 'a'} ${containerWord} with "name" and "definition"`);
      return;
    }
    if (!parsed.name || typeof parsed.name !== 'string') {
      snackbar.error(`${formatLabel} must include a string "name" field`);
      return;
    }
    if (!parsed.definition || typeof parsed.definition !== 'object' || Array.isArray(parsed.definition)) {
      snackbar.error(`${formatLabel} must include a "definition" ${containerWord}`);
      return;
    }

    const tagsValid = parsed.tags && typeof parsed.tags === 'object' && !Array.isArray(parsed.tags);
    const request: WorkflowCreateRequest = {
      account_id: accountId,
      workflow: {
        name: parsed.name,
        definition: parsed.definition,
        tags: tagsValid ? parsed.tags : {},
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

  const extensions = useMemo(() => (format === 'json' ? [json()] : [yaml()]), [format]);
  const isValid = !parseError && currentText.trim().length > 0;
  const formatLabel = format.toUpperCase();
  const containerWord = format === 'json' ? 'object' : 'mapping';

  return (
    <Modal
      open={open}
      handleClose={onClose}
      width='lg'
      hideTitleBackground={true}
      title='Create Automation from Code'
      subtitle='Paste or edit the automation definition below'
      actionButtons={
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: '12px', padding: '12px 24px' }}>
          <CustomButton text='Cancel' variant='secondary' size='Medium' onClick={onClose} disabled={submitting} />
          <CustomButton text='Create Automation' variant='primary' size='Medium' onClick={handleCreate} disabled={!isValid || submitting} />
        </Box>
      }
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: '12px', padding: '16px 0' }}>
        <Box sx={{ display: 'flex', justifyContent: 'flex-start' }}>
          <NewToggleButtons options={formatOptions} activeValue={format} onChange={handleFormatChange} size='sm' width='180px' />
        </Box>

        {parseError && (
          <Alert severity='error'>
            <Typography variant='body2' sx={{ fontSize: '13px' }}>
              <strong>{formatLabel} Parse Error:</strong> {parseError}
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
            value={currentText}
            height='480px'
            extensions={extensions}
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
          The {formatLabel} must include a <strong>name</strong> string and a <strong>definition</strong> {containerWord}. <strong>tags</strong> and{' '}
          <strong>status</strong> are optional.
        </Typography>
      </Box>
    </Modal>
  );
};

export default CreateWorkflowFromCodeModal;

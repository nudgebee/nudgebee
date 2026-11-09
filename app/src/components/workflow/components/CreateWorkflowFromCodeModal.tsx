import React, { useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import * as jsYaml from 'js-yaml';
import { useRouter } from 'next/router';
import { CodeEditor } from '@ui/CodeEditor';
import { Modal } from '@ui/Modal';
import { Banner } from '@ui/Banner';
import { Button } from '@ui/Button';
import { snackbar } from '@shared/snackbarService';
import apiWorkflow from '@api1/workflow';
import type { WorkflowCreateRequest } from '@api1/workflow/types';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import { ds } from 'src/utils/colors';
import { Toggle } from '@ui/Toggle';
import { buildWorkflowRequestFromCode } from '../workflowExport';

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

    // Refused rather than dropped: an optional field that is silently ignored is
    // only noticed after the automation exists without it.
    if (parsed.description != null && typeof parsed.description !== 'string') {
      snackbar.error(`"description" must be a string`);
      return;
    }
    if (
      parsed.tags != null &&
      (typeof parsed.tags !== 'object' || (Array.isArray(parsed.tags) && parsed.tags.some((tag: unknown) => typeof tag !== 'string')))
    ) {
      snackbar.error(`"tags" must be ${format === 'json' ? 'an object' : 'a mapping'} or a list of "key:value" strings`);
      return;
    }
    if (parsed.status != null && typeof parsed.status !== 'string') {
      snackbar.error(`"status" must be a string`);
      return;
    }

    const request: WorkflowCreateRequest = {
      account_id: accountId,
      workflow: buildWorkflowRequestFromCode(parsed),
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
      router.push(`/automation/${encodeURIComponent(newWorkflowId)}?accountId=${encodeURIComponent(accountId)}`);
    } catch (err: any) {
      snackbar.error(err?.message || 'Failed to create automation');
    } finally {
      setSubmitting(false);
    }
  };

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
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-3)', padding: 'var(--ds-space-3) var(--ds-space-5)' }}>
          <Button id='wf-code-cancel-btn' tone='secondary' size='md' onClick={onClose} disabled={submitting}>
            Cancel
          </Button>
          <Button id='wf-code-create-btn' tone='primary' size='md' onClick={handleCreate} disabled={!isValid || submitting} loading={submitting}>
            Create Automation
          </Button>
        </Box>
      }
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)', padding: 'var(--ds-space-4) 0' }}>
        <Box id='wf-code-format-tabs' sx={{ display: 'flex', justifyContent: 'flex-start' }}>
          <Toggle options={formatOptions} activeValue={format} onChange={handleFormatChange} size='sm' width='180px' />
        </Box>

        {parseError && (
          <Banner
            tone='critical'
            surface='section'
            message={
              <>
                <strong>{formatLabel} Parse Error:</strong> {parseError}
              </>
            }
          />
        )}
        <CodeEditor
          value={currentText}
          onChange={handleChange}
          language={format}
          height='480px'
          showLanguageLabel={false}
          error={Boolean(parseError)}
        />
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[400] }}>
          The {formatLabel} must include a <strong>name</strong> string and a <strong>definition</strong> {containerWord}.{' '}
          <strong>description</strong>, <strong>tags</strong> and <strong>status</strong> are optional.
        </Typography>
      </Box>
    </Modal>
  );
};

export default CreateWorkflowFromCodeModal;

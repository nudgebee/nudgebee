import { useEffect, useMemo, useState, useRef } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { Input } from '@ui/Input';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Select } from '@ui/Select';
import apiUserManagement from '@api1/user';
import Tooltip from '@ui/Tooltip';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import { KB_AGENT_WILDCARD } from '@api1/knowledge-base';
import { toast as snackbar } from '@ui/Toast';
import Text from '@shared/format/Text';
import { Button } from '@ui/Button';
import { Modal } from '@ui/Modal';
import { UploadIcon } from '@assets';
import { ds } from '@utils/colors';
import SafeIcon from '@shared/icons/SafeIcon';
import MentionContentInput from '@components/llm/MentionContentInput';
import KnowledgeContextTags from '@components/llm/KnowledgeContextTags';
import { KB_FORMAT_PDF, KB_FORMAT_TEXT, MAX_UPLOAD_BYTES, arrayBufferToBase64, isPdfFile, isTextFile } from '@components/llm/kbUploadFormat';

const MAX_CONTENT_LENGTH = 40000;

// Note type — manual knowledge treatment; does not affect agent routing or retrieval.
const NOTE_CATEGORY_OPTIONS = [
  { value: 'fact', label: 'Fact' },
  { value: 'sop', label: 'SOP' },
];
const DEFAULT_NOTE_CATEGORY = 'fact';
const SCOPE_SELECTED_ACCOUNTS = 'selected_accounts';

// Shown as placeholder text, not pre-filled content: the only way left to tell
// people they can @-mention an agent or #-mention a tool right in the note now
// that the agent-picker checklist is gone.
const CONTENT_PLACEHOLDER = `## When to use
Describe the situation this applies to — e.g. "When auth-svc returns 5xx and a restart is being considered."

## Agent usage
Mention agents that should use this, e.g. @k8s_ops

## Tool usage
Mention tools relevant to this, e.g. #query_logs`;

/**
 * Create/edit form for a single knowledge base — self-contained: takes the
 * account's agents/tools/context-tag options as props and reports back a
 * plain payload via `onSubmit`, so any account-scoped caller can mount it
 * without also owning KnowledgeBaseTab's list, table or retrieval-test UI.
 * `KnowledgeBaseTab.jsx` (the full "Knowledge Base" surface) and
 * `SetupCenter.tsx` (the Skills item's direct Add/Edit) both mount this.
 */
const KnowledgeBaseFormModal = ({
  open,
  onClose,
  onSubmit,
  editKnowledgeBase,
  loading,
  agents = [],
  agentsLoading = false,
  tools = [],
  toolsLoading = false,
  contextTagOptions = [],
  contextTagsLoading = false,
  initialAgentIds = [],
  // Tenant admins choose who can use the KB: [{ value, label }] of
  // 'account' / 'organisation' / 'selected_accounts'. Unset hides the field.
  scopeOptions = null,
  initialVisibility = 'account',
  initialAccountIds = [],
}) => {
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [content, setContent] = useState('');
  const [fileContent, setFileContent] = useState('');
  const [selectedFile, setSelectedFile] = useState(null);
  const [isDragging, setIsDragging] = useState(false);
  // 'all' persists a single wildcard mapping row that every agent resolves;
  // 'specific' persists one row per picked agent. A knowledge base with no row
  // at all is invisible to every agent — it never reaches the <skill-lists>
  // menu and load_skills cannot fetch it — which is why creates default to 'all'.
  const [agentMode, setAgentMode] = useState('all');
  const [selectedAgentIds, setSelectedAgentIds] = useState([]);
  const [noteCategory, setNoteCategory] = useState(DEFAULT_NOTE_CATEGORY);
  const [contextTags, setContextTags] = useState([]);
  // 'text' or 'pdf' — decides how the selected file was read, and what format
  // the backend is told to parse it as.
  const [fileFormat, setFileFormat] = useState(KB_FORMAT_TEXT);
  const [visibility, setVisibility] = useState(initialVisibility);
  const [scopeAccountIds, setScopeAccountIds] = useState(initialAccountIds);
  const [tenantAccounts, setTenantAccounts] = useState([]);
  const [tenantAccountsLoading, setTenantAccountsLoading] = useState(false);
  const fileInputRef = useRef(null);
  const dropZoneRef = useRef(null);

  useEffect(() => {
    if (editKnowledgeBase) {
      setName(editKnowledgeBase.name || '');
      setDescription(editKnowledgeBase.description || '');
      // A PDF's stored content is base64, so it must never be loaded into the
      // editor: it is not readable, and re-submitting it would re-index the
      // encoding as if it were prose. Leaving it empty also means the backend
      // keeps the existing document unless a new file is actually chosen.
      setContent(editKnowledgeBase.format === KB_FORMAT_PDF ? '' : editKnowledgeBase.content || '');
      setNoteCategory(editKnowledgeBase.note_category || DEFAULT_NOTE_CATEGORY);
      setContextTags(editKnowledgeBase.context_tags || []);
    } else {
      setName('');
      setDescription('');
      setContent('');
      setNoteCategory(DEFAULT_NOTE_CATEGORY);
      setContextTags([]);
    }
    setSelectedFile(null);
    setFileContent('');
    setFileFormat(KB_FORMAT_TEXT);
    setVisibility(initialVisibility);
    setScopeAccountIds(initialAccountIds);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- reset only when the form opens or targets another KB
  }, [editKnowledgeBase, open]);

  // The account picker for "Selected accounts" lists the tenant's accounts.
  const showScope = Array.isArray(scopeOptions) && scopeOptions.length > 0;
  useEffect(() => {
    if (!open || !showScope) {
      // A load cut short by closing the form must not leave the picker loading.
      setTenantAccountsLoading(false);
      return undefined;
    }
    let active = true;
    setTenantAccountsLoading(true);
    apiUserManagement
      .listAccounts()
      .then((rows) => active && setTenantAccounts(Array.isArray(rows) ? rows : []))
      .catch(() => active && setTenantAccounts([]))
      .finally(() => active && setTenantAccountsLoading(false));
    return () => {
      active = false;
    };
  }, [open, showScope]);
  const tenantAccountOptions = useMemo(
    () => tenantAccounts.map((a) => ({ value: a.id, label: a.account_name || a.id, group: a.cloud_provider || 'Other' })),
    [tenantAccounts]
  );

  // Carry the KB's existing mappings through the form untouched. Routing is
  // expressed in the note itself now — an `@agent` in the content is what points
  // the knowledge at an agent — so the form deliberately has no mapping control,
  // and this is the only thing standing between an edit and a rewritten mapping
  // set: a create lands on 'all' (the wildcard row, so the KB is visible to every
  // agent), and an edit re-submits exactly what it opened with, including the
  // empty set for a KB nobody mapped.
  useEffect(() => {
    if (initialAgentIds.includes(KB_AGENT_WILDCARD)) {
      setAgentMode('all');
      setSelectedAgentIds([]);
      return;
    }
    if (initialAgentIds.length > 0 || editKnowledgeBase) {
      setAgentMode('specific');
      setSelectedAgentIds(initialAgentIds);
      return;
    }
    setAgentMode('all');
    setSelectedAgentIds([]);
  }, [editKnowledgeBase, open, initialAgentIds]);

  const validateFile = (file) => {
    const maxSize = MAX_UPLOAD_BYTES;

    if (!isTextFile(file) && !isPdfFile(file)) {
      snackbar.error('Please select a .txt or .pdf file');
      return false;
    }

    if (file.size > maxSize) {
      snackbar.error('File size must be less than 5MB');
      return false;
    }

    return true;
  };

  const readAndSetFile = (file) => {
    if (isPdfFile(file)) {
      readAndSetPdf(file);
      return;
    }
    const reader = new FileReader();
    reader.onload = (e) => {
      const text = e.target.result;
      if (text.trim().length > MAX_CONTENT_LENGTH) {
        snackbar.error(
          `File content is ${text
            .trim()
            .length.toLocaleString()} characters — over the ${MAX_CONTENT_LENGTH.toLocaleString()}-character limit. Please shorten it.`
        );
        setSelectedFile(null);
        setFileContent('');
        return;
      }
      setSelectedFile(file);
      setFileContent(text);
      setFileFormat(KB_FORMAT_TEXT);
    };
    reader.readAsText(file);
  };

  // A PDF travels as base64: its figures have to reach the server intact so a
  // vision model can read them, and the character limit that guards typed notes
  // has no meaning for a binary document.
  const readAndSetPdf = (file) => {
    const reader = new FileReader();
    reader.onload = (e) => {
      try {
        setFileContent(arrayBufferToBase64(e.target.result));
        setSelectedFile(file);
        setFileFormat(KB_FORMAT_PDF);
      } catch {
        snackbar.error('That PDF could not be read. Please try another file.');
        setSelectedFile(null);
        setFileContent('');
      }
    };
    reader.onerror = () => snackbar.error('That PDF could not be read. Please try another file.');
    reader.readAsArrayBuffer(file);
  };

  const handleFileSelect = (event) => {
    const file = event.target.files[0];
    if (file && validateFile(file)) {
      readAndSetFile(file);
    }
    // Reset file input
    if (fileInputRef.current) {
      fileInputRef.current.value = '';
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
    if (file && validateFile(file)) {
      readAndSetFile(file);
    }
  };

  const isEditWithOverflow = !!(editKnowledgeBase && (editKnowledgeBase.content?.length || 0) > MAX_CONTENT_LENGTH);
  // Editing a PDF-backed KB with no replacement file: name, description and
  // tags are editable, the document itself stays as it is.
  const isPdfEdit = editKnowledgeBase?.format === KB_FORMAT_PDF;

  const handleSubmit = () => {
    const trimmedName = name?.trim() || '';

    if (!trimmedName) {
      snackbar.error('Please enter a valid name for the knowledge base');
      return;
    }
    if (!/^[a-zA-Z]/.test(trimmedName)) {
      snackbar.error('Name must start with a letter (a-z or A-Z)');
      return;
    }
    if (!/^[a-zA-Z]\w*$/.test(trimmedName)) {
      snackbar.error('Name can only contain letters, numbers, and underscores');
      return;
    }
    if (trimmedName.length < 3 || trimmedName.length > 50) {
      snackbar.error('Name must be between 3 and 50 characters');
      return;
    }
    if (showScope && visibility === SCOPE_SELECTED_ACCOUNTS && scopeAccountIds.length === 0) {
      snackbar.error('Pick at least one account to share this knowledge base with');
      return;
    }
    if (!selectedFile && !(isPdfEdit && content.trim().length === 0)) {
      if (content.trim().length === 0) {
        snackbar.error('Provide valid content or upload file for knowledge base');
        return;
      }
      if (!isEditWithOverflow && content.trim().length > MAX_CONTENT_LENGTH) {
        snackbar.error(`Content must not exceed ${MAX_CONTENT_LENGTH} characters`);
        return;
      }
    }

    onSubmit({
      name: trimmedName,
      description: description.trim(),
      content: selectedFile ? fileContent.trim() : content.trim(),
      format: selectedFile ? fileFormat : KB_FORMAT_TEXT,
      fileName: selectedFile ? selectedFile.name : editKnowledgeBase?.fileName || `${trimmedName}.txt`,
      noteCategory,
      contextTags,
      agentIds: agentMode === 'all' ? [KB_AGENT_WILDCARD] : selectedAgentIds,
      ...(showScope ? { visibility, accountIds: visibility === SCOPE_SELECTED_ACCOUNTS ? scopeAccountIds : [] } : {}),
    });
  };

  const contentOverBy = content.length - MAX_CONTENT_LENGTH;
  const contentOverLimit = !fileContent && !isEditWithOverflow && contentOverBy > 0;

  return (
    <Modal
      open={open}
      handleClose={onClose}
      title={editKnowledgeBase ? 'Edit Knowledge Base' : 'Create Knowledge Base'}
      subtitle={
        showScope && visibility !== 'account'
          ? 'Provide organisation-wide knowledge that the AI uses for more precise responses in the accounts that can see it.'
          : 'Provide account-level knowledge that will be used by the AI for more precise responses.'
      }
      width='md'
    >
      <Box sx={{ padding: ds.space[5] }}>
        {/* Type Field — knowledge treatment; does not change agent routing */}
        <Box sx={{ marginBottom: ds.space[4] }}>
          <Box sx={{ display: 'flex', alignItems: 'center', marginBottom: ds.space.mul(0, 3), gap: ds.space[1] }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: 'var(--ds-blue-500)',
              }}
            >
              Type
            </Typography>
            <Tooltip
              title={
                <div style={{ padding: `${ds.space[0]} 0` }}>
                  <div
                    style={{
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      fontSize: 'var(--ds-text-small)',
                      marginBottom: ds.space.mul(0, 3),
                      color: 'var(--ds-brand-600)',
                    }}
                  >
                    What kind of knowledge this is
                  </div>
                  {[
                    { label: 'Fact', value: "background that's true (e.g. an etcd quorum limit)" },
                    { label: 'SOP', value: 'how to do something, step by step' },
                  ].map(({ label, value }, i) => (
                    <div
                      key={label}
                      style={{ display: 'flex', gap: ds.space.mul(0, 3), alignItems: 'flex-start', marginBottom: i < 2 ? ds.space[1] : 0 }}
                    >
                      <span style={{ color: 'var(--ds-blue-500)', fontWeight: 'var(--ds-font-weight-semibold)', flexShrink: 0 }}>·</span>
                      <span style={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)' }}>
                        <span style={{ fontWeight: 'var(--ds-font-weight-semibold)' }}>{label}:</span> {value}
                      </span>
                    </div>
                  ))}
                  <div
                    style={{
                      marginTop: ds.space[2],
                      padding: `${ds.space[1]} ${ds.space[2]}`,
                      background: 'var(--ds-brand-100)',
                      borderRadius: ds.radius.sm,
                      fontSize: 'var(--ds-text-caption)',
                      color: 'var(--ds-brand-400)',
                    }}
                  >
                    Fact provides reference information. SOP guides agents through applicable steps, subject to existing permissions and approvals.
                  </div>
                </div>
              }
              placement='right'
            >
              <InfoOutlinedIcon sx={{ fontSize: 'var(--ds-text-title)', color: 'var(--ds-gray-700)', cursor: 'pointer' }} />
            </Tooltip>
          </Box>
          <ToggleGroup
            selection='single'
            size='sm'
            ariaLabel='Note type'
            value={noteCategory}
            onChange={(next) => setNoteCategory(next)}
            options={NOTE_CATEGORY_OPTIONS}
          />
        </Box>

        {showScope && (
          <Box sx={{ marginBottom: ds.space[4] }} data-testid='kb-scope-field'>
            <Box sx={{ display: 'flex', alignItems: 'center', marginBottom: ds.space.mul(0, 3), gap: ds.space[1] }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: 'var(--ds-blue-500)',
                }}
              >
                Who can use it
              </Typography>
              <Tooltip
                title='Organisation: every account in the organisation, for all of its agents; an account admin can switch it off for their account. Selected accounts: only the accounts you pick.'
                placement='right'
              >
                <InfoOutlinedIcon sx={{ fontSize: 'var(--ds-text-title)', color: 'var(--ds-gray-700)', cursor: 'pointer' }} />
              </Tooltip>
            </Box>
            <ToggleGroup
              selection='single'
              size='sm'
              ariaLabel='Who can use this knowledge base'
              value={visibility}
              onChange={(next) => next && setVisibility(next)}
              options={scopeOptions}
            />
            {visibility === SCOPE_SELECTED_ACCOUNTS && (
              <Box sx={{ marginTop: ds.space[3] }}>
                <Select
                  id='kb-scope-accounts'
                  label='Accounts'
                  placeholder='Select accounts'
                  multiple
                  grouped
                  searchable
                  options={tenantAccountOptions}
                  value={scopeAccountIds}
                  onChange={(next) => setScopeAccountIds(Array.isArray(next) ? next : [])}
                  loading={tenantAccountsLoading}
                />
              </Box>
            )}
          </Box>
        )}

        {/* Name Field */}
        <Box sx={{ marginBottom: ds.space[4] }}>
          <Box sx={{ display: 'flex', alignItems: 'center', marginBottom: ds.space.mul(0, 3), gap: ds.space[1] }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: 'var(--ds-blue-500)',
              }}
            >
              Name *
            </Typography>
            <Tooltip
              title={
                <div style={{ padding: `${ds.space[0]} 0` }}>
                  <div
                    style={{
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      fontSize: 'var(--ds-text-small)',
                      marginBottom: ds.space.mul(0, 3),
                      color: 'var(--ds-brand-600)',
                    }}
                  >
                    Name Rules
                  </div>
                  {[
                    { label: 'Allowed characters', value: 'a-z, A-Z, 0-9, underscore ( _ )' },
                    { label: 'Must start with', value: 'a letter (a-z or A-Z)' },
                    { label: 'Length', value: '3 to 50 characters' },
                  ].map(({ label, value }, i) => (
                    <div
                      key={label}
                      style={{ display: 'flex', gap: ds.space.mul(0, 3), alignItems: 'flex-start', marginBottom: i < 2 ? ds.space[1] : 0 }}
                    >
                      <span style={{ color: 'var(--ds-blue-500)', fontWeight: 'var(--ds-font-weight-semibold)', flexShrink: 0 }}>·</span>
                      <span style={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)' }}>
                        <span style={{ fontWeight: 'var(--ds-font-weight-semibold)' }}>{label}:</span> {value}
                      </span>
                    </div>
                  ))}
                  <div
                    style={{
                      marginTop: ds.space[2],
                      padding: `${ds.space[1]} ${ds.space[2]}`,
                      background: 'var(--ds-brand-100)',
                      borderRadius: ds.radius.sm,
                      fontSize: 'var(--ds-text-caption)',
                      color: 'var(--ds-brand-400)',
                      fontFamily: ds.font.mono,
                    }}
                  >
                    e.g. aws_ec2_runbook
                  </div>
                </div>
              }
              placement='right'
            >
              <InfoOutlinedIcon sx={{ fontSize: 'var(--ds-text-title)', color: 'var(--ds-gray-700)', cursor: 'pointer' }} />
            </Tooltip>
          </Box>
          <Input size='sm' placeholder='e.g. aws_ec2_runbook' value={name} onChange={(next) => setName(next)} />
        </Box>

        {/* Description Field */}
        <Box sx={{ marginBottom: ds.space[4] }}>
          <Box sx={{ display: 'flex', alignItems: 'center', marginBottom: ds.space.mul(0, 3), gap: ds.space[1] }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: 'var(--ds-blue-500)',
              }}
            >
              Description
            </Typography>
            <Tooltip
              title={
                <div style={{ padding: `${ds.space[0]} 0` }}>
                  <div
                    style={{
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      fontSize: 'var(--ds-text-small)',
                      marginBottom: ds.space.mul(0, 3),
                      color: 'var(--ds-brand-600)',
                    }}
                  >
                    Description Tips
                  </div>
                  {[
                    { label: 'What it covers', value: 'Topic or domain of this knowledge base' },
                    { label: 'When to use', value: 'Scenarios where the AI should refer to it' },
                    { label: 'Optional', value: 'Affected services, components, or environments' },
                  ].map(({ label, value }, i) => (
                    <div
                      key={label}
                      style={{ display: 'flex', gap: ds.space.mul(0, 3), alignItems: 'flex-start', marginBottom: i < 2 ? ds.space[1] : 0 }}
                    >
                      <span style={{ color: 'var(--ds-blue-500)', fontWeight: 'var(--ds-font-weight-semibold)', flexShrink: 0 }}>·</span>
                      <span style={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)' }}>
                        <span style={{ fontWeight: 'var(--ds-font-weight-semibold)' }}>{label}:</span> {value}
                      </span>
                    </div>
                  ))}
                  <div
                    style={{
                      marginTop: ds.space[2],
                      padding: `${ds.space[1]} ${ds.space[2]}`,
                      background: 'var(--ds-brand-100)',
                      borderRadius: ds.radius.sm,
                      fontSize: 'var(--ds-text-caption)',
                      color: 'var(--ds-brand-400)',
                      fontStyle: 'italic',
                    }}
                  >
                    e.g. Steps to debug OOM errors in production K8s pods
                  </div>
                </div>
              }
              placement='right'
            >
              <InfoOutlinedIcon sx={{ fontSize: 'var(--ds-text-title)', color: 'var(--ds-gray-700)', cursor: 'pointer' }} />
            </Tooltip>
          </Box>
          <Input
            size='sm'
            type='textarea'
            rows={2}
            placeholder='e.g. Steps to debug OOM errors in production K8s pods'
            value={description}
            onChange={(next) => setDescription(next)}
          />
        </Box>

        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: ds.space.mul(0, 3),
            marginBottom: ds.space[3],
            py: ds.space[2],
            px: ds.space[3],
            backgroundColor: 'var(--ds-amber-100)',
            border: `1px solid ${'var(--ds-amber-500)'}`,
            borderRadius: ds.radius.md,
          }}
        >
          <InfoOutlinedIcon sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-amber-700)', flexShrink: 0 }} />
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-700)' }}>
            At least one field is required — upload a file{' '}
            <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-blue-500)' }}>
              or
            </Box>{' '}
            type content directly below
          </Typography>
        </Box>

        {/* File Upload Zone */}
        <Box sx={{ marginBottom: ds.space[4] }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: 'var(--ds-blue-500)',
              marginBottom: ds.space.mul(0, 3),
            }}
          >
            Upload File
          </Typography>
          <Box
            ref={dropZoneRef}
            onDragEnter={handleDragEnter}
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            onDrop={handleDrop}
            onClick={() => fileInputRef.current?.click()}
            sx={{
              border: `${ds.space[0]} dashed ${isDragging ? 'var(--ds-blue-600)' : 'var(--ds-gray-300)'}`,
              borderRadius: ds.radius.lg,
              padding: ds.space.mul(1, 5),
              backgroundColor: isDragging ? 'var(--ds-blue-100)' : 'var(--ds-background-200)',
              transition: 'all 0.2s ease-in-out',
              cursor: 'pointer',
              '&:hover': {
                borderColor: 'var(--ds-blue-600)',
                backgroundColor: 'var(--ds-blue-100)',
              },
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              gap: ds.space[2],
            }}
          >
            <input ref={fileInputRef} type='file' accept='.txt,.pdf' onChange={handleFileSelect} style={{ display: 'none' }} />
            <Box
              sx={{
                width: ds.space.mul(1, 10),
                height: ds.space.mul(1, 10),
                borderRadius: '50%',
                backgroundColor: 'var(--ds-gray-100)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
            >
              <SafeIcon src={UploadIcon} alt='Upload' width={20} height={20} />
            </Box>
            <Text
              value={selectedFile ? selectedFile.name : 'Click or drag a .txt or .pdf file to upload'}
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: 'var(--ds-blue-500)',
                textAlign: 'center',
              }}
            />
            {selectedFile ? (
              <Typography
                onClick={(e) => {
                  e.stopPropagation();
                  setSelectedFile(null);
                  setFileContent('');
                  setFileFormat(KB_FORMAT_TEXT);
                }}
                sx={{
                  fontSize: 'var(--ds-text-caption)',
                  color: 'var(--ds-red-600)',
                  cursor: 'pointer',
                  textDecoration: 'underline',
                  textAlign: 'center',
                }}
              >
                Remove file
              </Typography>
            ) : (
              <Text
                value={`.txt up to ${MAX_CONTENT_LENGTH.toLocaleString()} characters · .pdf up to 5MB`}
                sx={{
                  fontSize: 'var(--ds-text-caption)',
                  color: 'var(--ds-gray-500)',
                  textAlign: 'center',
                }}
              />
            )}
          </Box>
        </Box>

        {/* A PDF is not just parsed locally the way a .txt is — its figures are
            sent to the account's configured model to be read. Say so at the
            point of upload, where the decision is actually being made. */}
        {fileFormat === KB_FORMAT_PDF && selectedFile ? (
          <Box sx={{ display: 'flex', gap: ds.space[2], marginBottom: ds.space[4], alignItems: 'flex-start' }}>
            <InfoOutlinedIcon sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-blue-500)', flexShrink: 0 }} />
            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-700)' }}>
              Diagrams and screenshots in this PDF are sent to your configured AI provider so their contents can be read and indexed.
            </Typography>
          </Box>
        ) : null}

        {/* OR Divider */}
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            marginBottom: ds.space[4],
            gap: ds.space[4],
          }}
        >
          <Box sx={{ flex: 1, height: 'var(--ds-space-0)', backgroundColor: 'var(--ds-gray-300)' }} />
          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: 'var(--ds-gray-500)',
              textTransform: 'uppercase',
            }}
          >
            OR
          </Typography>
          <Box sx={{ flex: 1, height: 'var(--ds-space-0)', backgroundColor: 'var(--ds-gray-300)' }} />
        </Box>

        {/* Content Text Area */}
        <Box sx={{ marginBottom: ds.space[5] }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: 'var(--ds-blue-500)',
              marginBottom: ds.space.mul(0, 3),
            }}
          >
            Content
          </Typography>
          <MentionContentInput
            minRows={8}
            maxRows={15}
            placeholder={
              fileContent
                ? 'File content will be used — remove the file to type manually'
                : isPdfEdit
                ? 'This knowledge base holds a PDF. Upload a new file to replace it, or type here to replace it with text.'
                : CONTENT_PLACEHOLDER
            }
            value={fileContent ? '' : content}
            onChange={(next) => !fileContent && setContent(next)}
            disabled={!!fileContent}
            agents={agents}
            tools={tools}
            suggestionsLoading={agentsLoading || toolsLoading}
            data-testid='kb-content-input'
          >
            {contentOverLimit && (
              <Tooltip
                title={`Content is ${contentOverBy.toLocaleString()} character(s) over the ${MAX_CONTENT_LENGTH.toLocaleString()} limit`}
                placement='top'
              >
                <Box
                  sx={{
                    position: 'absolute',
                    bottom: ds.space[2],
                    right: ds.space[3],
                    px: ds.space[2],
                    py: ds.space[0],
                    borderRadius: ds.radius.sm,
                    backgroundColor: 'var(--ds-red-500)',
                    color: 'var(--ds-background-100)',
                    fontSize: 'var(--ds-text-caption)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    lineHeight: 1.4,
                    cursor: 'default',
                    userSelect: 'none',
                  }}
                >
                  -{contentOverBy.toLocaleString()}
                </Box>
              </Tooltip>
            )}
          </MentionContentInput>
        </Box>

        {/* Context Tags — scope labels folded into searchable text to sharpen
            retrieval; they don't restrict which agents can see the note. */}
        <Box sx={{ marginBottom: ds.space[5] }}>
          <Box sx={{ display: 'flex', alignItems: 'center', marginBottom: ds.space.mul(0, 3), gap: ds.space[1] }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: 'var(--ds-blue-500)',
              }}
            >
              Context tags
            </Typography>
            <Tooltip
              title={
                <div style={{ padding: `${ds.space[0]} 0` }}>
                  <div
                    style={{
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      fontSize: 'var(--ds-text-small)',
                      marginBottom: ds.space.mul(0, 3),
                      color: 'var(--ds-brand-600)',
                    }}
                  >
                    Pin this note to a situation
                  </div>
                  {[
                    { label: 'What', value: 'services, namespaces, topics or other context this note is about' },
                    { label: 'Why', value: "added to the note's searchable text so the right note surfaces for the right question" },
                    { label: 'Note', value: "sharpens matching only — doesn't restrict which agents can see it" },
                  ].map(({ label, value }, i) => (
                    <div
                      key={label}
                      style={{ display: 'flex', gap: ds.space.mul(0, 3), alignItems: 'flex-start', marginBottom: i < 2 ? ds.space[1] : 0 }}
                    >
                      <span style={{ color: 'var(--ds-blue-500)', fontWeight: 'var(--ds-font-weight-semibold)', flexShrink: 0 }}>·</span>
                      <span style={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)' }}>
                        <span style={{ fontWeight: 'var(--ds-font-weight-semibold)' }}>{label}:</span> {value}
                      </span>
                    </div>
                  ))}
                  <div
                    style={{
                      marginTop: ds.space[2],
                      padding: `${ds.space[1]} ${ds.space[2]}`,
                      background: 'var(--ds-brand-100)',
                      borderRadius: ds.radius.sm,
                      fontSize: 'var(--ds-text-caption)',
                      color: 'var(--ds-brand-400)',
                    }}
                  >
                    Choose services and namespaces, or add your own tags. Tag changes refresh search indexing.
                  </div>
                </div>
              }
              placement='right'
            >
              <InfoOutlinedIcon sx={{ fontSize: 'var(--ds-text-title)', color: 'var(--ds-gray-700)', cursor: 'pointer' }} />
            </Tooltip>
          </Box>
          <KnowledgeContextTags value={contextTags} options={contextTagOptions} loading={contextTagsLoading} onChange={setContextTags} />
        </Box>

        {/* Agent mapping has no control here. A create maps to every agent via
            the wildcard row; an edit keeps whatever mapping the knowledge base
            already had — both seeded into state above from `initialAgentIds`. */}

        {/* Action Buttons */}
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'flex-end',
            gap: ds.space[3],
          }}
        >
          <Button tone='secondary' size='md' onClick={onClose} disabled={loading}>
            Cancel
          </Button>
          <Button tone='primary' size='md' onClick={handleSubmit} loading={loading} disabled={loading || !name.trim() || contentOverLimit}>
            {editKnowledgeBase ? 'Update' : 'Create'}
          </Button>
        </Box>
      </Box>
    </Modal>
  );
};

KnowledgeBaseFormModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  onSubmit: PropTypes.func.isRequired,
  editKnowledgeBase: PropTypes.object,
  loading: PropTypes.bool,
  agents: PropTypes.array,
  agentsLoading: PropTypes.bool,
  tools: PropTypes.array,
  toolsLoading: PropTypes.bool,
  contextTagOptions: PropTypes.array,
  contextTagsLoading: PropTypes.bool,
  initialAgentIds: PropTypes.array,
  scopeOptions: PropTypes.arrayOf(PropTypes.shape({ value: PropTypes.string, label: PropTypes.string })),
  initialVisibility: PropTypes.string,
  initialAccountIds: PropTypes.arrayOf(PropTypes.string),
};

export default KnowledgeBaseFormModal;

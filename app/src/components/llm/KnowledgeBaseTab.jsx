import { useEffect, useMemo, useState, useRef } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography, Alert } from '@mui/material';
import { Label } from '@ui/Label';
import { Chip } from '@ui/Chip';
import CustomTable from '@shared/tables/CustomTable';
import { Input } from '@ui/Input';
import Tooltip from '@ui/Tooltip';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import HistoryIcon from '@mui/icons-material/History';
import RefreshIcon from '@mui/icons-material/Refresh';
import apiKnowledgeBase from '@api1/knowledge-base';
import Loader from '@shared/Loader';
import { toast as snackbar } from '@ui/Toast';
import Text from '@shared/format/Text';
import { Button } from '@ui/Button';
import { EmptyState } from '@ui/EmptyState';
import ThreeDotsMenu from '@ui/ThreeDotsMenu';
import { Modal } from '@ui/Modal';
import { UploadIcon, EditIcon, DeleteIconRed as deleteIcon, serviceNowIcon, jiraIcon, ManualTriggerIconBlue } from '@assets';
import { hasWriteAccess } from '@lib/auth';
import { ds } from '@utils/colors';
import SafeIcon from '@shared/icons/SafeIcon';
import WidgetCard from '@ui/WidgetCard';
import Tabs from '@shared/navigation/Tabs';
import { MemoryTable } from '@components/llm/MemoryTable';
import ScopeChip from '@components/llm/ScopeChip';
import { formatTrigger, formatDuration, formatDocuments } from '@components/llm/kbLoadHistoryFormat';

const MAX_CONTENT_LENGTH = 5000;

const formatExactDate = (dateString) => {
  if (!dateString) return '-';
  return new Date(dateString).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
};

const formatDate = (dateString) => {
  if (!dateString) {
    return '-';
  }
  const date = new Date(dateString);
  const now = new Date();
  const diffMs = now - date;
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMins / 60);
  const diffDays = Math.floor(diffHours / 24);

  if (diffMins < 1) return 'just now';
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  if (diffDays < 7) return `${diffDays}d ago`;

  return date.toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric' });
};

// Integration logo for a KB row/card, based on its source.
const getIntegrationLogo = (knowledgeBase) => {
  if (knowledgeBase.kb_type === 'manual') {
    return ManualTriggerIconBlue;
  }
  if (!knowledgeBase.kb_source) {
    return null;
  }
  switch (knowledgeBase.kb_source) {
    case 'servicenow':
      return serviceNowIcon;
    case 'confluence':
      return jiraIcon;
    default:
      return null;
  }
};

// Status → Label tone mapping.
const getKbStatusTone = (status) =>
  status === 'active' ? 'success' : status === 'processing' ? 'warning' : status === 'error' ? 'critical' : 'neutral';

// Three-dots menu items for a KB (retrigger / history / delete), gated by type + access.
const getKbMenuItems = (knowledgeBase, hasAccess) => [
  // Retrigger only for integration KBs — manual KBs have no external source to re-sync
  ...(knowledgeBase.kb_type === 'integration'
    ? [
        {
          label: 'Retrigger',
          value: 'retrigger',
          icon: RefreshIcon,
          // An archived KB's integration is disabled — nothing to re-sync.
          disabled: knowledgeBase.status === 'processing' || knowledgeBase.status === 'archived',
        },
      ]
    : []),
  {
    label: 'Load History',
    value: 'history',
    icon: HistoryIcon,
  },
  // Delete: manual KBs, or archived integration KBs (their integration is
  // disabled, so the sync won't recreate the row).
  ...(knowledgeBase.kb_type === 'manual' || knowledgeBase.status === 'archived'
    ? [
        {
          label: 'Delete',
          value: 'delete',
          icon: deleteIcon,
          disabled: !hasAccess,
        },
      ]
    : []),
];

// Status pill (with error tooltip) — shared by the card and table views.
const KbStatusLabel = ({ knowledgeBase }) =>
  knowledgeBase.status === 'error' && knowledgeBase.error_message ? (
    <Tooltip title={knowledgeBase.error_message} placement='top'>
      <Box component='span' sx={{ display: 'inline-flex', cursor: 'help' }}>
        <Label text={knowledgeBase.status} tone='critical' />
      </Box>
    </Tooltip>
  ) : (
    <Label text={knowledgeBase.status} tone={getKbStatusTone(knowledgeBase.status)} />
  );
KbStatusLabel.propTypes = { knowledgeBase: PropTypes.object };

// Edit button (manual KBs) + three-dots menu — shared by the card and table views.
const KnowledgeBaseActions = ({ knowledgeBase, onEdit, onDelete, onRetrigger, onViewHistory, hasAccess }) => {
  const handleMenuClick = (item) => {
    if (item.value === 'edit') {
      onEdit(knowledgeBase);
    } else if (item.value === 'delete') {
      onDelete(knowledgeBase);
    } else if (item.value === 'retrigger') {
      onRetrigger(knowledgeBase);
    } else if (item.value === 'history') {
      onViewHistory(knowledgeBase);
    }
  };

  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3), flexShrink: 0 }}>
      {hasAccess && knowledgeBase.kb_type === 'manual' && (
        <Button
          tone='ghost'
          size='sm'
          icon={<SafeIcon src={EditIcon} alt='Edit' width={14} height={14} />}
          onClick={() => onEdit(knowledgeBase)}
          aria-label='Edit knowledge base'
        />
      )}
      <ThreeDotsMenu
        menuItems={getKbMenuItems(knowledgeBase, hasAccess)}
        data={knowledgeBase}
        onMenuClick={handleMenuClick}
        sx={{ p: ds.space[1], '& .MuiSvgIcon-root': { fontSize: 'var(--ds-text-title)' } }}
      />
    </Box>
  );
};
KnowledgeBaseActions.propTypes = {
  knowledgeBase: PropTypes.object,
  onEdit: PropTypes.func,
  onDelete: PropTypes.func,
  onRetrigger: PropTypes.func,
  onViewHistory: PropTypes.func,
  hasAccess: PropTypes.bool,
};

const KnowledgeBaseFormModal = ({ open, onClose, onSubmit, editKnowledgeBase, loading }) => {
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [content, setContent] = useState('');
  const [fileContent, setFileContent] = useState('');
  const [selectedFile, setSelectedFile] = useState(null);
  const [isDragging, setIsDragging] = useState(false);
  const fileInputRef = useRef(null);
  const dropZoneRef = useRef(null);

  useEffect(() => {
    if (editKnowledgeBase) {
      setName(editKnowledgeBase.name || '');
      setDescription(editKnowledgeBase.description || '');
      setContent(editKnowledgeBase.content || '');
    } else {
      setName('');
      setDescription('');
      setContent('');
    }
    setSelectedFile(null);
    setFileContent('');
  }, [editKnowledgeBase, open]);

  const validateFile = (file) => {
    const validTypes = ['text/plain'];
    const maxSize = 5 * 1024 * 1024; // 5MB limit

    if (!validTypes.includes(file.type) && !file.name.endsWith('.txt')) {
      snackbar.error('Please select only .txt files');
      return false;
    }

    if (file.size > maxSize) {
      snackbar.error('File size must be less than 5MB');
      return false;
    }

    return true;
  };

  const readAndSetFile = (file) => {
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
    };
    reader.readAsText(file);
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
    if (!selectedFile) {
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
    });
  };

  const contentOverBy = content.length - MAX_CONTENT_LENGTH;
  const contentOverLimit = !fileContent && !isEditWithOverflow && contentOverBy > 0;

  return (
    <Modal open={open} handleClose={onClose} title={editKnowledgeBase ? 'Edit Knowledge Base' : 'Create Knowledge Base'} width='md'>
      <Box sx={{ padding: ds.space[5] }}>
        <Text
          value='Provide account-level knowledge that will be used by the AI for more precise responses.'
          sx={{
            fontSize: 'var(--ds-text-body)',
            color: 'var(--ds-gray-700)',
            marginBottom: ds.space.mul(1, 5),
          }}
        />

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
                      fontFamily: 'monospace',
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
            Upload Text File
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
            <input ref={fileInputRef} type='file' accept='.txt' onChange={handleFileSelect} style={{ display: 'none' }} />
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
              value={selectedFile ? selectedFile.name : 'Click or drag a .txt file to upload'}
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
                value={`Max ${MAX_CONTENT_LENGTH.toLocaleString()} characters · up to 5MB`}
                sx={{
                  fontSize: 'var(--ds-text-caption)',
                  color: 'var(--ds-gray-500)',
                  textAlign: 'center',
                }}
              />
            )}
          </Box>
        </Box>

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
          <Box sx={{ position: 'relative' }}>
            <Input
              type='textarea'
              minRows={8}
              maxRows={15}
              placeholder={
                fileContent ? 'File content will be used — remove the file to type manually' : 'Paste or type your knowledge base content here...'
              }
              value={fileContent ? '' : content}
              onChange={(next) => !fileContent && setContent(next)}
              disabled={!!fileContent}
            />
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
                    py: '2px',
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
          </Box>
        </Box>

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
};

const statusTone = (status) => {
  switch (status) {
    case 'success':
      return 'success';
    case 'failure':
      return 'critical';
    case 'partial':
      return 'warning';
    default:
      return 'neutral';
  }
};

const KBLoadHistoryModal = ({ open, onClose, accountId, kbId, kbName }) => {
  const [history, setHistory] = useState([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (open && kbId) {
      // Guard against a stale response overwriting fresh data when `kbId`
      // changes (or the modal closes) while a request is still in flight.
      let active = true;
      setLoading(true);
      apiKnowledgeBase
        .getKBLoadHistory(accountId, kbId)
        .then((response) => {
          if (active) setHistory(response.data || []);
        })
        .catch(() => {
          if (active) setHistory([]);
        })
        .finally(() => {
          if (active) setLoading(false);
        });
      return () => {
        active = false;
      };
    }
  }, [open, kbId, accountId]);

  return (
    <Modal open={open} handleClose={onClose} title={`Load History: ${kbName || ''}`} width='md'>
      <Box sx={{ padding: ds.space[4] }}>
        {loading ? (
          <Loader />
        ) : history.length === 0 ? (
          <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)', textAlign: 'center', py: ds.space[6] }}>
            No load history found
          </Typography>
        ) : (
          <CustomTable
            headers={[{ name: 'Date' }, { name: 'Trigger' }, { name: 'Status' }, { name: 'Documents' }, { name: 'Duration' }, { name: 'Error' }]}
            tableData={history.map((entry) => [
              { text: formatDate(entry.created_at) },
              { text: formatTrigger(entry) },
              { component: <Label text={entry.request_status} tone={statusTone(entry.request_status)} /> },
              { text: formatDocuments(entry) },
              { text: formatDuration(entry.load_duration_seconds) },
              { text: entry.error_message || '-' },
            ])}
            rowsPerPage={history.length}
            totalRows={history.length}
          />
        )}
      </Box>
    </Modal>
  );
};

KBLoadHistoryModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  accountId: PropTypes.string.isRequired,
  kbId: PropTypes.string,
  kbName: PropTypes.string,
};

const KnowledgeBaseTab = ({ accountId }) => {
  // Tenant-wide read-only mode: when no accountId is in scope (b-Cortex
  // opened from the global sidebar where the page has no current
  // account), fetch every KB the caller can read across the tenant. The
  // backend handler routes empty account_id to a tenant-wide read and
  // row-filters by HasAccountAccess; the UI hides write affordances.
  const isTenantWide = !accountId;
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [knowledgeBases, setKnowledgeBases] = useState([]);
  const [error, setError] = useState(null);
  const [formModalOpen, setFormModalOpen] = useState(false);
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const [selectedKnowledgeBase, setSelectedKnowledgeBase] = useState(null);
  const [historyModalOpen, setHistoryModalOpen] = useState(false);
  const [historyKB, setHistoryKB] = useState(null);
  const [activeTab, setActiveTab] = useState('integration');

  // In tenant-wide mode every write affordance is hidden — writes always
  // require the per-account context, so we never paint Create / Edit /
  // Delete / Retrigger when the caller has no account to act in.
  const hasAccess = !isTenantWide && hasWriteAccess(accountId);

  // silent = background poll: skip the full-page spinner and error toasts.
  // isStale lets an account-scoped caller discard a response that resolved after
  // the account changed; mutation-driven refreshes leave it at the default and
  // always apply.
  const fetchKnowledgeBases = async (silent = false, isStale = () => false) => {
    try {
      if (!silent) setLoading(true);
      // Empty accountId is intentional in tenant-wide mode — the backend
      // routes that to ListKnowledgebasesForTenant + ACL row-filtering.
      const response = await apiKnowledgeBase.getKnowledgeBases(accountId || '');
      if (isStale()) return;
      if (response.errors && response.errors.length > 0) {
        if (!silent) {
          setError('Failed to fetch knowledge bases');
          snackbar.error('Failed to fetch knowledge bases');
        }
      } else if (response.data) {
        setKnowledgeBases(response.data);
        setError(null);
      } else {
        setKnowledgeBases([]);
      }
    } catch (err) {
      console.error('Error fetching knowledge bases:', err);
      if (!silent && !isStale()) {
        setError('An error occurred while fetching knowledge bases');
        snackbar.error('An error occurred while fetching knowledge bases');
      }
    } finally {
      if (!silent && !isStale()) setLoading(false);
    }
  };

  useEffect(() => {
    let cancelled = false;
    fetchKnowledgeBases(false, () => cancelled);
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId]);

  // Poll every 60s so async KB status changes (processing -> active) appear
  // without a manual reload. Skipped in tenant-wide mode — that surface is
  // read-only, so the per-row status doesn't transition under the user.
  useEffect(() => {
    if (isTenantWide) return undefined;
    let cancelled = false;
    const intervalId = setInterval(() => fetchKnowledgeBases(true, () => cancelled), 60000);
    return () => {
      cancelled = true;
      clearInterval(intervalId);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId, isTenantWide]);

  const handleCreate = () => {
    setSelectedKnowledgeBase(null);
    setFormModalOpen(true);
  };

  const handleEdit = async (knowledgeBase) => {
    // Fetch full knowledge base data (including content) before editing
    try {
      setSubmitting(true);
      const response = await apiKnowledgeBase.getKnowledgeBase(accountId, knowledgeBase.id);
      if (response.errors && response.errors.length > 0) {
        snackbar.error('Failed to fetch knowledge base details');
        return;
      }
      if (response.data) {
        setSelectedKnowledgeBase(response.data);
        setFormModalOpen(true);
      } else {
        snackbar.error('Failed to fetch knowledge base details');
      }
    } catch (err) {
      console.error('Error fetching knowledge base:', err);
      snackbar.error('An error occurred while fetching knowledge base details');
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = (knowledgeBase) => {
    setSelectedKnowledgeBase(knowledgeBase);
    setDeleteModalOpen(true);
  };

  const handleRetrigger = async (knowledgeBase) => {
    try {
      setSubmitting(true);
      const response = await apiKnowledgeBase.retriggerKB(accountId, knowledgeBase.id);
      if (response.errors && response.errors.length > 0) {
        snackbar.error(response.errors[0]?.message || 'Failed to retrigger knowledge base');
        return;
      }
      snackbar.success('Knowledge base re-processing started');
      fetchKnowledgeBases();
    } catch (err) {
      console.error('Error retriggering KB:', err);
      snackbar.error('An error occurred while retriggering');
    } finally {
      setSubmitting(false);
    }
  };

  const handleViewHistory = (knowledgeBase) => {
    setHistoryKB(knowledgeBase);
    setHistoryModalOpen(true);
  };

  // Memoised column definitions.
  //
  // handleEdit / handleRetrigger close over `accountId` (via the
  // apiKnowledgeBase calls), so the deps include accountId — switching
  // accounts must rebuild the handlers, otherwise the row actions fire
  // against the previous account. hasAccess is derived from accountId
  // (hasWriteAccess(accountId)) and added explicitly so the memo also
  // tracks role-only flips against the same account.
  //
  // The handler functions themselves are intentionally omitted: they're
  // re-declared on every render (recreated by React, not memoised), so
  // including them would invalidate the memo every render and defeat the
  // optimisation. eslint-disable below suppresses exhaustive-deps for
  // exactly this reason.
  const columns = useMemo(
    () => [
      {
        key: 'name',
        label: 'Name',
        type: 'text-short',
        wrap: true,
        render: (kb) => {
          const logo = getIntegrationLogo(kb);
          return (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], minWidth: 0 }}>
              {logo && <SafeIcon src={logo} alt={kb.kb_source || 'integration'} width={18} height={18} style={{ flexShrink: 0 }} />}
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: 'var(--ds-gray-700)',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}
              >
                {kb.name}
              </Typography>
            </Box>
          );
        },
      },
      { key: 'status', label: 'Status', type: 'status', render: (kb) => <KbStatusLabel knowledgeBase={kb} /> },
      {
        key: 'document_count',
        label: 'Docs',
        type: 'count',
        render: (kb) => (
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
            {kb.document_count != null ? kb.document_count : '—'}
          </Typography>
        ),
      },
      ...(isTenantWide
        ? [
            {
              key: 'account_name',
              label: 'Account',
              type: 'text-short',
              render: (kb) => {
                // Grouped tenant-wide rows carry `accounts` (one entry per
                // member of the integration cluster); single-row KBs fall
                // back to the per-row account_name.
                const accounts = kb.accounts && kb.accounts.length > 0 ? kb.accounts : [{ id: kb.account_id, name: kb.account_name }];
                const MAX_VISIBLE = 3;
                const visible = accounts.slice(0, MAX_VISIBLE);
                const overflow = accounts.length - visible.length;
                const fullList = accounts.map((a) => a.name || a.id || '—').join(', ');
                return (
                  <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[1], alignItems: 'center' }}>
                    {visible.map((a) => (
                      <Chip key={a.id || a.name} tone='neutral' size='2xs' variant='tag'>
                        {a.name || a.id || '—'}
                      </Chip>
                    ))}
                    {overflow > 0 && (
                      <Tooltip title={fullList} placement='top'>
                        <Box component='span' sx={{ display: 'inline-flex', cursor: 'default' }}>
                          <Chip tone='info' size='2xs' variant='count'>
                            +{overflow}
                          </Chip>
                        </Box>
                      </Tooltip>
                    )}
                    {accounts.length === 0 && <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>—</Typography>}
                  </Box>
                );
              },
            },
          ]
        : []),
      {
        key: 'created_at',
        label: 'Added',
        type: 'date',
        render: (kb) =>
          kb.created_at ? (
            <Tooltip
              title={
                // Grouped tenant-wide rows show the most recent creation across
                // member accounts (#33339), so label it as such to avoid
                // implying a single provisioning event.
                (isTenantWide ? 'Last added ' : '') +
                formatExactDate(kb.created_at) +
                (kb.created_by?.display_name ? ` by ${kb.created_by.display_name}` : '')
              }
              placement='top'
            >
              <Box component='span' sx={{ cursor: 'help' }}>
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>{formatDate(kb.created_at)}</Typography>
              </Box>
            </Tooltip>
          ) : (
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)' }}>—</Typography>
          ),
      },
      // Actions column is omitted entirely in tenant-wide mode — every
      // write requires per-account context, so we don't paint affordances
      // the user can't act on.
      ...(isTenantWide
        ? []
        : [
            {
              key: 'actions',
              label: '',
              type: 'actions',
              render: (kb) => (
                <KnowledgeBaseActions
                  knowledgeBase={kb}
                  onEdit={handleEdit}
                  onDelete={handleDelete}
                  onRetrigger={handleRetrigger}
                  onViewHistory={handleViewHistory}
                  hasAccess={hasAccess}
                />
              ),
            },
          ]),
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps -- handlers intentionally omitted, see docstring above
    [hasAccess, accountId, isTenantWide]
  );

  const handleFormSubmit = async (data) => {
    try {
      setSubmitting(true);
      let response;

      if (selectedKnowledgeBase) {
        response = await apiKnowledgeBase.updateKnowledgeBase(accountId, selectedKnowledgeBase.id, data);
        if (response.errors && response.errors.length > 0) {
          const errorMessage = response.errors[0]?.message || 'Failed to update knowledge base';
          snackbar.error(errorMessage);
          return;
        }
        snackbar.success('Knowledge base updated successfully');
      } else {
        response = await apiKnowledgeBase.createKnowledgeBase(accountId, data);
        if (response.errors && response.errors.length > 0) {
          const errorMessage = response.errors[0]?.message || 'Failed to create knowledge base';
          snackbar.error(errorMessage);
          return;
        }
        snackbar.success('Knowledge base created successfully');
      }

      setFormModalOpen(false);
      setSelectedKnowledgeBase(null);
      fetchKnowledgeBases();
    } catch (err) {
      console.error('Error submitting knowledge base:', err);
      snackbar.error('An error occurred while saving the knowledge base');
    } finally {
      setSubmitting(false);
    }
  };

  const handleConfirmDelete = async () => {
    if (!selectedKnowledgeBase) {
      return;
    }

    try {
      setSubmitting(true);
      const response = await apiKnowledgeBase.deleteKnowledgeBase(accountId, selectedKnowledgeBase.id);

      if (response.errors && response.errors.length > 0) {
        const errorMessage = response.errors[0]?.message || 'Failed to delete knowledge base';
        snackbar.error(errorMessage);
        return;
      }

      snackbar.success('Knowledge base deleted successfully');
      setDeleteModalOpen(false);
      setSelectedKnowledgeBase(null);
      fetchKnowledgeBases();
    } catch (err) {
      console.error('Error deleting knowledge base:', err);
      snackbar.error('An error occurred while deleting the knowledge base');
    } finally {
      setSubmitting(false);
    }
  };

  if (loading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: ds.space.mul(1, 75) }}>
        <Loader />
      </Box>
    );
  }

  if (error && knowledgeBases.length === 0) {
    return (
      <Box sx={{ p: ds.space[5] }}>
        <Alert severity='error'>{error}</Alert>
      </Box>
    );
  }

  return (
    <Box sx={{ p: 0 }}>
      {/* Header Section */}
      <WidgetCard
        sx={{ py: ds.space[4], px: ds.space.mul(1, 5), mt: 0, mb: 0, display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}
      >
        <Box>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[1] }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body-lg)',
                color: 'var(--ds-gray-700)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                fontFamily: 'var(--ds-font-display)',
              }}
            >
              Knowledge Base
            </Typography>
            <ScopeChip accountId={accountId} />
          </Box>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-gray-500)',
            }}
          >
            {isTenantWide
              ? 'Viewing all knowledge bases across this tenant. Switch to an account-scoped page to create, edit, or retrigger a knowledge base.'
              : "Account-scoped document library with AI semantic search-upload docs, map to agents, and they'll automatically search when needed."}
          </Typography>
        </Box>
        {hasAccess && (
          <Button tone='primary' size='sm' onClick={handleCreate}>
            Add Knowledge Base
          </Button>
        )}
      </WidgetCard>

      {/* Empty State */}
      {knowledgeBases.length === 0 && (
        <EmptyState
          surface
          size='section'
          illustration='first-time'
          title='No knowledge bases found'
          description='Create a knowledge base to provide the AI with account-specific documentation and context.'
          sx={{ marginBottom: ds.space[3] }}
        />
      )}

      {/* Knowledge Base List — tabbed by source */}
      {knowledgeBases.length > 0 &&
        (() => {
          // Tenant-wide rollup: integration-backed KBs (ServiceNow,
          // Confluence, etc.) get provisioned once per account, so a
          // tenant operator sees N near-identical rows. Collapse by
          // (integration_id, name) into a single logical row that lists
          // the member accounts as chips. Custom (no integration_id)
          // and per-account views keep one row per KB.
          const groupedKBs = isTenantWide
            ? (() => {
                const grouped = new Map();
                const standalone = [];
                for (const kb of knowledgeBases) {
                  if (!kb.integration_id) {
                    standalone.push(kb);
                    continue;
                  }
                  const key = `${kb.integration_id}|${kb.name}`;
                  const existing = grouped.get(key);
                  if (existing) {
                    // Defensive dedup — if an account ends up with two KBs
                    // sharing the same (integration_id, name) (backfill
                    // drift, migration bug), skip the duplicate account so
                    // the Account-chip column doesn't surface the same name
                    // twice and React doesn't warn about duplicate keys.
                    if (!existing.accounts.some((acc) => acc.id === kb.account_id)) {
                      existing.accounts.push({ id: kb.account_id, name: kb.account_name });
                    }
                    // "Added" for a grouped row = the most recent creation
                    // across member accounts (deterministic; #33339). Without
                    // this the row inherited whichever member the backend
                    // returned first, so the same KB showed different times in
                    // the tenant rollup vs a per-account view. Keep created_by
                    // aligned with the chosen timestamp so the tooltip stays
                    // coherent.
                    if (kb.created_at && (!existing.created_at || new Date(kb.created_at) > new Date(existing.created_at))) {
                      existing.created_at = kb.created_at;
                      existing.created_by = kb.created_by;
                    }
                  } else {
                    grouped.set(key, { ...kb, accounts: [{ id: kb.account_id, name: kb.account_name }] });
                  }
                }
                return [...grouped.values(), ...standalone];
              })()
            : knowledgeBases;
          const integrationKBs = groupedKBs.filter((kb) => kb.kb_type === 'integration');
          const userKBs = groupedKBs.filter((kb) => kb.kb_type === 'manual');
          const visibleKBs = activeTab === 'integration' ? integrationKBs : userKBs;
          // columns lives at component scope via useMemo so CustomTable
          // gets a stable reference across renders.
          return (
            <Box sx={{ mt: ds.space[2] }}>
              <Tabs
                value={activeTab}
                onChange={(val) => setActiveTab(val)}
                variant='secondary'
                behavior='filter'
                smallSize
                options={{
                  tabOptions: [
                    { value: 'integration', text: 'Integration', count: integrationKBs.length },
                    { value: 'manual', text: 'User', count: userKBs.length },
                  ],
                }}
              />
              <Box sx={{ mt: ds.space[2] }}>
                <MemoryTable
                  columns={columns}
                  rows={visibleKBs}
                  emptyText={`No ${activeTab === 'integration' ? 'integration' : 'user'} knowledge bases yet.`}
                />
              </Box>
            </Box>
          );
        })()}

      {/* Create/Edit Modal */}
      <KnowledgeBaseFormModal
        open={formModalOpen}
        onClose={() => {
          setFormModalOpen(false);
          setSelectedKnowledgeBase(null);
        }}
        onSubmit={handleFormSubmit}
        editKnowledgeBase={selectedKnowledgeBase}
        loading={submitting}
      />

      {/* Delete Confirmation Modal */}
      <Modal
        open={deleteModalOpen}
        handleClose={() => {
          if (submitting) return;
          setDeleteModalOpen(false);
          setSelectedKnowledgeBase(null);
        }}
        title={`Delete Knowledge Base: ${selectedKnowledgeBase?.name || ''}`}
        width='sm'
        loader={submitting}
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[3], py: ds.space[3], px: ds.space[5] }}>
            <Button
              tone='secondary'
              size='sm'
              onClick={() => {
                setDeleteModalOpen(false);
                setSelectedKnowledgeBase(null);
              }}
              disabled={submitting}
            >
              Cancel
            </Button>
            <Button tone='danger' size='sm' onClick={handleConfirmDelete} loading={submitting}>
              Delete
            </Button>
          </Box>
        }
      >
        <Box sx={{ padding: ds.space[5] }}>
          <Typography
            sx={{
              mb: ds.space[4],
              fontFamily: 'var(--ds-font-display)',
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-regular)',
              color: 'var(--ds-gray-700)',
              lineHeight: 1.5,
            }}
          >
            Are you sure you want to delete the knowledge base &quot;<strong>{selectedKnowledgeBase?.name}</strong>&quot;?
          </Typography>
          <Typography
            sx={{
              fontFamily: 'var(--ds-font-display)',
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-regular)',
              color: 'var(--ds-gray-500)',
              lineHeight: 1.5,
            }}
          >
            This action cannot be undone. The AI will no longer have access to this knowledge.
          </Typography>
        </Box>
      </Modal>

      {/* Load History Modal */}
      <KBLoadHistoryModal
        open={historyModalOpen}
        onClose={() => {
          setHistoryModalOpen(false);
          setHistoryKB(null);
        }}
        accountId={accountId}
        kbId={historyKB?.id}
        kbName={historyKB?.name}
      />
    </Box>
  );
};

KnowledgeBaseTab.propTypes = {
  // Optional: empty / unset means tenant-wide read-only mode (b-Cortex
  // opened from the global sidebar, no current account in scope).
  accountId: PropTypes.string,
};

export default KnowledgeBaseTab;

import { useEffect, useState, useRef } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography, Alert } from '@mui/material';
import { Label } from '@components1/ds/Label';
import CustomTable from '@common-new/tables/CustomTable2';
import { Input } from '@components1/ds/Input';
import Tooltip from '@components1/ds/Tooltip';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import HistoryIcon from '@mui/icons-material/History';
import RefreshIcon from '@mui/icons-material/Refresh';
import DescriptionOutlinedIcon from '@mui/icons-material/DescriptionOutlined';
import StorageOutlinedIcon from '@mui/icons-material/StorageOutlined';
import AccessTimeOutlinedIcon from '@mui/icons-material/AccessTimeOutlined';
import AttachFileOutlinedIcon from '@mui/icons-material/AttachFileOutlined';
import apiKnowledgeBase from '@api1/knowledge-base';
import Loader from '@components1/common/Loader';
import { toast as snackbar } from '@components1/ds/Toast';
import Text from '@common-new/format/Text';
import { Button } from '@components1/ds/Button';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import { Modal } from '@components1/ds/Modal';
import { UploadIcon, EditIcon, DeleteIconRed as deleteIcon, serviceNowIcon, jiraIcon, ManualTriggerIconBlue } from '@assets';
import { hasWriteAccess } from '@lib/auth';
import { ds } from '@utils/colors';
import SafeIcon from '@components1/common/SafeIcon';
import WidgetCard from '@components1/ds/WidgetCard';
import CustomTabs from '@common-new/CustomTabs';
import { formatTrigger, formatDuration, formatDocuments } from '@components1/llm/kbLoadHistoryFormat';

const MAX_CONTENT_LENGTH = 10000;

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

const KnowledgeBaseCard = ({ knowledgeBase, onEdit, onDelete, onRetrigger, onViewHistory, hasAccess }) => {
  const MENU_ITEMS = [
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

  // Get integration logo based on kb_source
  const getIntegrationLogo = () => {
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

  const integrationLogo = getIntegrationLogo();

  return (
    <WidgetCard sx={{ p: ds.space[4], mt: 0 }}>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', flexDirection: 'column', alignItems: 'flex-start', height: '100%' }}>
        <Box sx={{ flex: 1, width: '100%' }}>
          {/* Primary: Name + icon */}
          <Box sx={{ display: 'flex', flexDirection: 'row', gap: ds.space[2], mb: ds.space.mul(0, 3), justifyContent: 'space-between' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
              {integrationLogo && (
                <SafeIcon src={integrationLogo} alt={knowledgeBase.kb_source || 'integration'} width={24} height={24} style={{ flexShrink: 0 }} />
              )}
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body-lg)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  color: 'var(--ds-gray-700)',
                  whiteSpace: 'nowrap',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                }}
              >
                {knowledgeBase.name}
              </Typography>
            </Box>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3), flexShrink: 0 }}>
              <Label
                text={knowledgeBase.status}
                tone={
                  knowledgeBase.status === 'active'
                    ? 'success'
                    : knowledgeBase.status === 'processing'
                    ? 'warning'
                    : knowledgeBase.status === 'error'
                    ? 'critical'
                    : 'neutral'
                }
              />
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
                menuItems={MENU_ITEMS}
                data={knowledgeBase}
                onMenuClick={handleMenuClick}
                sx={{ p: ds.space[1], '& .MuiSvgIcon-root': { fontSize: 'var(--ds-text-title)' } }}
              />
            </Box>
          </Box>

          {/* Secondary: Description — most useful context */}
          {knowledgeBase.description ? (
            <Typography
              sx={{
                fontSize: 'var(--ds-text-small)',
                color: 'var(--ds-gray-400)',
                fontWeight: 'var(--ds-font-weight-regular)',
                mb: ds.space[3],
                display: '-webkit-box',
                WebkitLineClamp: 2,
                WebkitBoxOrient: 'vertical',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                lineHeight: '1.4',
              }}
            >
              {knowledgeBase.description}
            </Typography>
          ) : (
            <Box sx={{ mb: ds.space.mul(0, 7) }} />
          )}

          {/* KB metadata — compact icon rows */}
          {(() => {
            const hasDocs = knowledgeBase.document_count != null && knowledgeBase.document_count > 0;
            const hasLoaded = !!knowledgeBase.last_loaded_at;
            const visibleCount = 1 + (hasDocs ? 1 : 0) + (hasLoaded ? 1 : 0);
            return (
              <Box
                sx={{
                  display: 'flex',
                  flexDirection: 'column',
                  gap: ds.space.mul(0, 3),
                  mb: ds.space[2],
                  py: ds.space.mul(0, 5),
                  px: ds.space[3],
                  borderRadius: ds.radius.md,
                  backgroundColor: 'var(--ds-background-200)',
                }}
              >
                {/* Row 1: Format · Docs · Loaded — equal-width columns, only present fields shown */}
                <Box sx={{ display: 'grid', gridTemplateColumns: `repeat(${visibleCount}, 1fr)`, alignItems: 'center', gap: ds.space[4] }}>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3) }}>
                    <DescriptionOutlinedIcon sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-gray-400)' }} />
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-400)' }}>
                      Format:{' '}
                      <Box component='span' sx={{ color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-regular)' }}>
                        {knowledgeBase.format || '—'}
                      </Box>
                    </Typography>
                  </Box>
                  {hasDocs && (
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3) }}>
                      <StorageOutlinedIcon sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-gray-400)' }} />
                      <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-400)' }}>
                        Docs:{' '}
                        <Box component='span' sx={{ color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-regular)' }}>
                          {knowledgeBase.document_count}
                        </Box>
                      </Typography>
                    </Box>
                  )}
                  {hasLoaded && (
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3) }}>
                      <AccessTimeOutlinedIcon sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-gray-400)' }} />
                      <Tooltip title={formatExactDate(knowledgeBase.last_loaded_at)} placement='top'>
                        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-400)', cursor: 'default' }}>
                          Loaded:{' '}
                          <Box component='span' sx={{ color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-regular)' }}>
                            {formatDate(knowledgeBase.last_loaded_at)}
                          </Box>
                        </Typography>
                      </Tooltip>
                    </Box>
                  )}
                </Box>

                {/* Row 2: File (only when present) */}
                {knowledgeBase.fileName && (
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3), minWidth: 0 }}>
                    <AttachFileOutlinedIcon sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-gray-400)', flexShrink: 0 }} />
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-400)', flexShrink: 0 }}>File:</Typography>
                    <Text value={knowledgeBase.fileName} showAutoEllipsis sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-700)' }} />
                  </Box>
                )}
              </Box>
            );
          })()}
        </Box>
        {/* Footer: Updated (primary) + Created (secondary) */}
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mt: ds.space[4], width: '100%' }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Updated</Typography>
            <Tooltip title={formatExactDate(knowledgeBase.updated_at)} placement='top'>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 400, color: 'var(--ds-gray-700)', cursor: 'default' }}>
                {formatDate(knowledgeBase.updated_at)}
              </Typography>
            </Tooltip>
            {knowledgeBase.updated_by?.display_name && (
              <>
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)' }}>by</Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-700)' }}>
                  {knowledgeBase.updated_by.display_name}
                </Typography>
              </>
            )}
          </Box>
          <Tooltip
            title={
              formatExactDate(knowledgeBase.created_at) +
              (knowledgeBase.created_by?.display_name ? ` by ${knowledgeBase.created_by.display_name}` : '')
            }
            placement='top'
          >
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)', cursor: 'default' }}>
              Created {formatDate(knowledgeBase.created_at)}
            </Typography>
          </Tooltip>
        </Box>
      </Box>
    </WidgetCard>
  );
};

KnowledgeBaseCard.propTypes = {
  knowledgeBase: PropTypes.object.isRequired,
  onEdit: PropTypes.func.isRequired,
  onDelete: PropTypes.func.isRequired,
  onRetrigger: PropTypes.func.isRequired,
  onViewHistory: PropTypes.func.isRequired,
  hasAccess: PropTypes.bool.isRequired,
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

  const handleFileSelect = (event) => {
    const file = event.target.files[0];
    if (file && validateFile(file)) {
      setSelectedFile(file);
      const reader = new FileReader();
      reader.onload = (e) => {
        setFileContent(e.target.result);
      };
      reader.readAsText(file);
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
      setSelectedFile(file);
      const reader = new FileReader();
      reader.onload = (ev) => {
        setFileContent(ev.target.result);
      };
      reader.readAsText(file);
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

  let contentCounterColor = 'var(--ds-gray-500)';
  if (!isEditWithOverflow) {
    if (content.length >= MAX_CONTENT_LENGTH) contentCounterColor = 'var(--ds-red-600)';
    else if (content.length >= MAX_CONTENT_LENGTH * 0.9) contentCounterColor = 'var(--ds-amber-500)';
  }

  let contentWarning = <span />;
  if (!isEditWithOverflow) {
    if (content.length >= MAX_CONTENT_LENGTH) {
      contentWarning = (
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-red-600)' }}>
          {MAX_CONTENT_LENGTH.toLocaleString()}-character limit reached. For larger content, upload a .txt file instead.
        </Typography>
      );
    } else if (content.length >= MAX_CONTENT_LENGTH * 0.9) {
      contentWarning = <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-amber-500)' }}>Approaching limit</Typography>;
    }
  }

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
                value='Maximum file size: 5MB'
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
          <Input
            type='textarea'
            minRows={8}
            maxRows={15}
            placeholder={
              fileContent ? 'File content will be used — remove the file to type manually' : 'Paste or type your knowledge base content here...'
            }
            value={fileContent ? '' : content}
            onChange={(next) => !fileContent && setContent(isEditWithOverflow ? next : next.slice(0, MAX_CONTENT_LENGTH))}
            disabled={!!fileContent}
          />
          {!fileContent && (
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: ds.space[1] }}>
              {contentWarning}
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-caption)',
                  color: contentCounterColor,
                  fontWeight: !isEditWithOverflow && content.length >= MAX_CONTENT_LENGTH * 0.9 ? 600 : 400,
                }}
              >
                {isEditWithOverflow ? `${content.length} chars` : `${content.length} / ${MAX_CONTENT_LENGTH}`}
              </Typography>
            </Box>
          )}
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
          <Button tone='primary' size='md' onClick={handleSubmit} loading={loading} disabled={loading || !name.trim()}>
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

  const hasAccess = hasWriteAccess(accountId);

  // silent = background poll: skip the full-page spinner and error toasts.
  const fetchKnowledgeBases = async (silent = false) => {
    if (!accountId) {
      setError('Account ID is required');
      setLoading(false);
      return;
    }

    try {
      if (!silent) setLoading(true);
      const response = await apiKnowledgeBase.getKnowledgeBases(accountId);
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
      if (!silent) {
        setError('An error occurred while fetching knowledge bases');
        snackbar.error('An error occurred while fetching knowledge bases');
      }
    } finally {
      if (!silent) setLoading(false);
    }
  };

  useEffect(() => {
    fetchKnowledgeBases();
  }, [accountId]);

  // Poll every 60s so async KB status changes (processing -> active) appear
  // without a manual reload.
  useEffect(() => {
    if (!accountId) return undefined;
    const intervalId = setInterval(() => fetchKnowledgeBases(true), 60000);
    return () => clearInterval(intervalId);
  }, [accountId]);

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
          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-gray-500)',
            }}
          >
            Account-scoped document library with AI semantic search-upload docs, map to agents, and they'll automatically search when needed.
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
        <Box
          sx={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            justifyContent: 'center',
            py: ds.space[7],
            px: ds.space[5],
            marginBottom: ds.space[3],
            border: `1px dashed var(--ds-gray-300)`,
            borderRadius: ds.radius.lg,
            backgroundColor: 'var(--ds-background-200)',
          }}
        >
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body)',
              color: 'var(--ds-gray-700)',
              mb: ds.space[2],
            }}
          >
            No knowledge bases found
          </Typography>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-gray-500)',
              mb: ds.space[4],
              textAlign: 'center',
            }}
          >
            Create a knowledge base to provide the AI with account-specific documentation and context.
          </Typography>
          {hasAccess && (
            <Button tone='secondary' size='sm' onClick={handleCreate}>
              Create Knowledge Base
            </Button>
          )}
        </Box>
      )}

      {/* Knowledge Base List — tabbed by source */}
      {knowledgeBases.length > 0 &&
        (() => {
          const integrationKBs = knowledgeBases.filter((kb) => kb.kb_type === 'integration');
          const userKBs = knowledgeBases.filter((kb) => kb.kb_type === 'manual');
          const visibleKBs = activeTab === 'integration' ? integrationKBs : userKBs;
          return (
            <Box sx={{ mt: ds.space[2] }}>
              <CustomTabs
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
              {visibleKBs.length === 0 ? (
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', textAlign: 'center', py: ds.space[6] }}>
                  No {activeTab === 'integration' ? 'integration' : 'user'} knowledge bases yet.
                </Typography>
              ) : (
                <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: ds.space[3], mt: ds.space[2] }}>
                  {visibleKBs.map((kb) => (
                    <KnowledgeBaseCard
                      key={kb.id}
                      knowledgeBase={kb}
                      onEdit={handleEdit}
                      onDelete={handleDelete}
                      onRetrigger={handleRetrigger}
                      onViewHistory={handleViewHistory}
                      hasAccess={hasAccess}
                    />
                  ))}
                </Box>
              )}
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
          setDeleteModalOpen(false);
          setSelectedKnowledgeBase(null);
        }}
        title={`Delete Knowledge Base: ${selectedKnowledgeBase?.name || ''}`}
        width='sm'
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
  accountId: PropTypes.string.isRequired,
};

export default KnowledgeBaseTab;

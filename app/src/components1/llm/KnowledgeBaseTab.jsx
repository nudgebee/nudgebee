import { useEffect, useState, useRef } from 'react';
import PropTypes from 'prop-types';
import {
  Box,
  Typography,
  Alert,
  TextField,
  TextareaAutosize,
  Chip,
  IconButton,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
} from '@mui/material';
import CustomTooltip from '@components1/common/CustomTooltip';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import HistoryIcon from '@mui/icons-material/History';
import RefreshIcon from '@mui/icons-material/Refresh';
import DescriptionOutlinedIcon from '@mui/icons-material/DescriptionOutlined';
import StorageOutlinedIcon from '@mui/icons-material/StorageOutlined';
import AccessTimeOutlinedIcon from '@mui/icons-material/AccessTimeOutlined';
import AttachFileOutlinedIcon from '@mui/icons-material/AttachFileOutlined';
import { colors } from 'src/utils/colors';
import apiKnowledgeBase from '@api1/knowledge-base';
import Loader from '@components1/common/Loader';
import { snackbar } from '@components1/common/snackbarService';
import { Text } from '@components1/common';
import CustomButton from '@components1/common/NewCustomButton';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { Modal } from '@components1/common/modal';
import { UploadIcon, PlusIcon, EditIcon, DeleteIconRed as deleteIcon, serviceNowIcon, jiraIcon, ManualTriggerIconBlue } from '@assets';
import { hasWriteAccess } from '@lib/auth';
import SafeIcon from '@components1/common/SafeIcon';
import WidgetCard from '@components1/common/WidgetCard';
import CustomTabs from '@components1/common/CustomTabs';
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
    <WidgetCard sx={{ p: '16px', mt: 0 }}>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', flexDirection: 'column', alignItems: 'flex-start', height: '100%' }}>
        <Box sx={{ flex: 1, width: '100%' }}>
          {/* Primary: Name + icon */}
          <Box sx={{ display: 'flex', flexDirection: 'row', gap: '8px', mb: '6px', justifyContent: 'space-between' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
              {integrationLogo && (
                <SafeIcon src={integrationLogo} alt={knowledgeBase.kb_source || 'integration'} width={24} height={24} style={{ flexShrink: 0 }} />
              )}
              <Typography
                sx={{
                  fontSize: '14px',
                  fontWeight: 600,
                  color: colors.text.secondary,
                  whiteSpace: 'nowrap',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                }}
              >
                {knowledgeBase.name}
              </Typography>
            </Box>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', flexShrink: 0 }}>
              <Chip
                label={knowledgeBase.status}
                size='small'
                sx={{
                  fontSize: '11px',
                  fontWeight: 500,
                  backgroundColor:
                    knowledgeBase.status === 'active'
                      ? colors.background.successLight || '#e6f4ea'
                      : knowledgeBase.status === 'processing'
                      ? '#fff3e0'
                      : knowledgeBase.status === 'error'
                      ? colors.background.errorLight || '#fce8e6'
                      : colors.background.tertiaryLightest,
                  color:
                    knowledgeBase.status === 'active'
                      ? colors.text.success || '#1e7e34'
                      : knowledgeBase.status === 'processing'
                      ? '#e65100'
                      : knowledgeBase.status === 'error'
                      ? colors.text.error || '#d32f2f'
                      : colors.text.secondary,
                }}
              />
              {hasAccess && knowledgeBase.kb_type === 'manual' && (
                <CustomTooltip title='Edit' placement='top'>
                  <IconButton size='small' onClick={() => onEdit(knowledgeBase)} sx={{ p: '4px' }}>
                    <SafeIcon src={EditIcon} alt='Edit' width={14} height={14} />
                  </IconButton>
                </CustomTooltip>
              )}
              <ThreeDotsMenu
                menuItems={MENU_ITEMS}
                data={knowledgeBase}
                onMenuClick={handleMenuClick}
                sx={{ p: '4px', '& .MuiSvgIcon-root': { fontSize: '18px' } }}
              />
            </Box>
          </Box>

          {/* Secondary: Description — most useful context */}
          {knowledgeBase.description ? (
            <Typography
              sx={{
                fontSize: '12px',
                color: colors.text.tertiaryLightest,
                fontWeight: 300,
                mb: '12px',
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
            <Box sx={{ mb: '14px' }} />
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
                  gap: '6px',
                  mb: 1,
                  p: '10px 12px',
                  borderRadius: '6px',
                  backgroundColor: colors.background.tertiaryLightestest || '#f8f8f8',
                }}
              >
                {/* Row 1: Format · Docs · Loaded — equal-width columns, only present fields shown */}
                <Box sx={{ display: 'grid', gridTemplateColumns: `repeat(${visibleCount}, 1fr)`, alignItems: 'center', gap: '16px' }}>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                    <DescriptionOutlinedIcon sx={{ fontSize: '14px', color: colors.text.tertiaryLight }} />
                    <Typography sx={{ fontSize: '12px', color: colors.text.tertiaryLight }}>
                      Format:{' '}
                      <Box component='span' sx={{ color: colors.text.secondary, fontWeight: 400 }}>
                        {knowledgeBase.format || '—'}
                      </Box>
                    </Typography>
                  </Box>
                  {hasDocs && (
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                      <StorageOutlinedIcon sx={{ fontSize: '14px', color: colors.text.tertiaryLight }} />
                      <Typography sx={{ fontSize: '12px', color: colors.text.tertiaryLight }}>
                        Docs:{' '}
                        <Box component='span' sx={{ color: colors.text.secondary, fontWeight: 400 }}>
                          {knowledgeBase.document_count}
                        </Box>
                      </Typography>
                    </Box>
                  )}
                  {hasLoaded && (
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                      <AccessTimeOutlinedIcon sx={{ fontSize: '14px', color: colors.text.tertiaryLight }} />
                      <CustomTooltip title={formatExactDate(knowledgeBase.last_loaded_at)} placement='top'>
                        <Typography sx={{ fontSize: '12px', color: colors.text.tertiaryLight, cursor: 'default' }}>
                          Loaded:{' '}
                          <Box component='span' sx={{ color: colors.text.secondary, fontWeight: 400 }}>
                            {formatDate(knowledgeBase.last_loaded_at)}
                          </Box>
                        </Typography>
                      </CustomTooltip>
                    </Box>
                  )}
                </Box>

                {/* Row 2: File (only when present) */}
                {knowledgeBase.fileName && (
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', minWidth: 0 }}>
                    <AttachFileOutlinedIcon sx={{ fontSize: '14px', color: colors.text.tertiaryLight, flexShrink: 0 }} />
                    <Typography
                      sx={{
                        fontSize: '12px',
                        color: colors.text.tertiaryLight,
                        minWidth: 0,
                        overflow: 'hidden',
                        whiteSpace: 'nowrap',
                        textOverflow: 'ellipsis',
                      }}
                    >
                      File:{' '}
                      <Box component='span' sx={{ color: colors.text.secondary, fontWeight: 400 }} title={knowledgeBase.fileName}>
                        {knowledgeBase.fileName}
                      </Box>
                    </Typography>
                  </Box>
                )}
              </Box>
            );
          })()}
        </Box>
        {/* Footer: Updated (primary) + Created (secondary) */}
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mt: '16px', width: '100%' }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
            <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>Updated</Typography>
            <CustomTooltip title={formatExactDate(knowledgeBase.updated_at)} placement='top'>
              <Typography sx={{ fontSize: '11px', fontWeight: 400, color: colors.text.secondary, cursor: 'default' }}>
                {formatDate(knowledgeBase.updated_at)}
              </Typography>
            </CustomTooltip>
            {knowledgeBase.updated_by?.display_name && (
              <>
                <Typography sx={{ fontSize: '11px', color: colors.text.tertiaryLight }}>by</Typography>
                <Typography sx={{ fontSize: '11px', fontWeight: 400, color: colors.text.secondary }}>
                  {knowledgeBase.updated_by.display_name}
                </Typography>
              </>
            )}
          </Box>
          <CustomTooltip
            title={
              formatExactDate(knowledgeBase.created_at) +
              (knowledgeBase.created_by?.display_name ? ` by ${knowledgeBase.created_by.display_name}` : '')
            }
            placement='top'
          >
            <Typography sx={{ fontSize: '11px', color: colors.text.tertiaryLight, cursor: 'default' }}>
              Created {formatDate(knowledgeBase.created_at)}
            </Typography>
          </CustomTooltip>
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

  let contentBorderColor = colors.border.secondary;
  if (!isEditWithOverflow) {
    if (content.length >= MAX_CONTENT_LENGTH) contentBorderColor = colors.error;
    else if (content.length >= MAX_CONTENT_LENGTH * 0.9) contentBorderColor = colors.yellow;
  }

  let contentCounterColor = colors.text.tertiary;
  if (!isEditWithOverflow) {
    if (content.length >= MAX_CONTENT_LENGTH) contentCounterColor = colors.error;
    else if (content.length >= MAX_CONTENT_LENGTH * 0.9) contentCounterColor = colors.yellow;
  }

  let contentWarning = <span />;
  if (!isEditWithOverflow) {
    if (content.length >= MAX_CONTENT_LENGTH) {
      contentWarning = (
        <Typography sx={{ fontSize: '11px', color: colors.error }}>
          {MAX_CONTENT_LENGTH.toLocaleString()}-character limit reached. For larger content, upload a .txt file instead.
        </Typography>
      );
    } else if (content.length >= MAX_CONTENT_LENGTH * 0.9) {
      contentWarning = <Typography sx={{ fontSize: '11px', color: colors.yellow }}>Approaching limit</Typography>;
    }
  }

  return (
    <Modal open={open} handleClose={onClose} title={editKnowledgeBase ? 'Edit Knowledge Base' : 'Create Knowledge Base'} width='md'>
      <Box sx={{ padding: '24px' }}>
        <Text
          value='Provide account-level knowledge that will be used by the AI for more precise responses.'
          sx={{
            fontSize: '13px',
            color: colors.text.secondary,
            marginBottom: '20px',
          }}
        />

        {/* Name Field */}
        <Box sx={{ marginBottom: '16px' }}>
          <Box sx={{ display: 'flex', alignItems: 'center', marginBottom: '6px', gap: '4px' }}>
            <Typography
              sx={{
                fontSize: '13px',
                fontWeight: 500,
                color: colors.text.primary,
              }}
            >
              Name *
            </Typography>
            <CustomTooltip
              title={
                <div style={{ padding: '2px 0' }}>
                  <div style={{ fontWeight: 600, fontSize: '12px', marginBottom: '6px', color: '#1e293b' }}>Name Rules</div>
                  {[
                    { label: 'Allowed characters', value: 'a-z, A-Z, 0-9, underscore ( _ )' },
                    { label: 'Must start with', value: 'a letter (a-z or A-Z)' },
                    { label: 'Length', value: '3 to 50 characters' },
                  ].map(({ label, value }, i) => (
                    <div key={label} style={{ display: 'flex', gap: '6px', alignItems: 'flex-start', marginBottom: i < 2 ? '4px' : 0 }}>
                      <span style={{ color: colors.text.primary, fontWeight: 700, flexShrink: 0 }}>·</span>
                      <span style={{ fontSize: '11px', color: colors.text.secondary }}>
                        <span style={{ fontWeight: 600 }}>{label}:</span> {value}
                      </span>
                    </div>
                  ))}
                  <div
                    style={{
                      marginTop: '8px',
                      padding: '4px 8px',
                      background: '#f1f5f9',
                      borderRadius: '4px',
                      fontSize: '11px',
                      color: '#64748b',
                      fontFamily: 'monospace',
                    }}
                  >
                    e.g. aws_ec2_runbook
                  </div>
                </div>
              }
              placement='right'
            >
              <InfoOutlinedIcon sx={{ fontSize: '15px', color: colors.text.secondary, cursor: 'pointer' }} />
            </CustomTooltip>
          </Box>
          <TextField
            fullWidth
            placeholder='e.g. aws_ec2_runbook'
            value={name}
            onChange={(e) => setName(e.target.value)}
            size='small'
            sx={{
              '& .MuiOutlinedInput-root': {
                fontSize: '13px',
              },
            }}
          />
        </Box>

        {/* Description Field */}
        <Box sx={{ marginBottom: '16px' }}>
          <Box sx={{ display: 'flex', alignItems: 'center', marginBottom: '6px', gap: '4px' }}>
            <Typography
              sx={{
                fontSize: '13px',
                fontWeight: 500,
                color: colors.text.primary,
              }}
            >
              Description
            </Typography>
            <CustomTooltip
              title={
                <div style={{ padding: '2px 0' }}>
                  <div style={{ fontWeight: 600, fontSize: '12px', marginBottom: '6px', color: '#1e293b' }}>Description Tips</div>
                  {[
                    { label: 'What it covers', value: 'Topic or domain of this knowledge base' },
                    { label: 'When to use', value: 'Scenarios where the AI should refer to it' },
                    { label: 'Optional', value: 'Affected services, components, or environments' },
                  ].map(({ label, value }, i) => (
                    <div key={label} style={{ display: 'flex', gap: '6px', alignItems: 'flex-start', marginBottom: i < 2 ? '4px' : 0 }}>
                      <span style={{ color: colors.text.primary, fontWeight: 700, flexShrink: 0 }}>·</span>
                      <span style={{ fontSize: '11px', color: colors.text.secondary }}>
                        <span style={{ fontWeight: 600 }}>{label}:</span> {value}
                      </span>
                    </div>
                  ))}
                  <div
                    style={{
                      marginTop: '8px',
                      padding: '4px 8px',
                      background: '#f1f5f9',
                      borderRadius: '4px',
                      fontSize: '11px',
                      color: '#64748b',
                      fontStyle: 'italic',
                    }}
                  >
                    e.g. Steps to debug OOM errors in production K8s pods
                  </div>
                </div>
              }
              placement='right'
            >
              <InfoOutlinedIcon sx={{ fontSize: '15px', color: colors.text.secondary, cursor: 'pointer' }} />
            </CustomTooltip>
          </Box>
          <TextField
            fullWidth
            placeholder='e.g. Steps to debug OOM errors in production K8s pods'
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            size='small'
            multiline
            rows={2}
            sx={{
              '& .MuiOutlinedInput-root': {
                fontSize: '13px',
              },
            }}
          />
        </Box>

        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: '6px',
            marginBottom: '12px',
            padding: '8px 12px',
            backgroundColor: colors.background.warningLight,
            border: `1px solid ${colors.border.warning}`,
            borderRadius: '6px',
          }}
        >
          <InfoOutlinedIcon sx={{ fontSize: '15px', color: colors.text.warning, flexShrink: 0 }} />
          <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>
            At least one field is required — upload a file{' '}
            <Box component='span' sx={{ fontWeight: 600, color: colors.text.primary }}>
              or
            </Box>{' '}
            type content directly below
          </Typography>
        </Box>

        {/* File Upload Zone */}
        <Box sx={{ marginBottom: '16px' }}>
          <Typography
            sx={{
              fontSize: '13px',
              fontWeight: 500,
              color: colors.text.primary,
              marginBottom: '6px',
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
              border: `2px dashed ${isDragging ? colors.primary : '#e0e0e0'}`,
              borderRadius: '8px',
              padding: '20px',
              backgroundColor: isDragging ? `${colors.primary}10` : '#fafafa',
              transition: 'all 0.2s ease-in-out',
              cursor: 'pointer',
              '&:hover': {
                borderColor: colors.primary,
                backgroundColor: `${colors.primary}10`,
              },
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              gap: '8px',
            }}
          >
            <input ref={fileInputRef} type='file' accept='.txt' onChange={handleFileSelect} style={{ display: 'none' }} />
            <Box
              sx={{
                width: '40px',
                height: '40px',
                borderRadius: '50%',
                backgroundColor: `${colors.primary}20`,
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
                fontSize: '13px',
                fontWeight: 500,
                color: colors.text.primary,
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
                  fontSize: '11px',
                  color: colors.error,
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
                  fontSize: '11px',
                  color: colors.text.tertiary,
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
            marginBottom: '16px',
            gap: '16px',
          }}
        >
          <Box sx={{ flex: 1, height: '1px', backgroundColor: colors.border.secondary }} />
          <Typography
            sx={{
              fontSize: '12px',
              fontWeight: 500,
              color: colors.text.tertiary,
              textTransform: 'uppercase',
            }}
          >
            OR
          </Typography>
          <Box sx={{ flex: 1, height: '1px', backgroundColor: colors.border.secondary }} />
        </Box>

        {/* Content Text Area */}
        <Box sx={{ marginBottom: '24px' }}>
          <Typography
            sx={{
              fontSize: '13px',
              fontWeight: 500,
              color: colors.text.primary,
              marginBottom: '6px',
            }}
          >
            Content
          </Typography>
          <TextareaAutosize
            minRows={8}
            maxRows={15}
            placeholder={
              fileContent ? 'File content will be used — remove the file to type manually' : 'Paste or type your knowledge base content here...'
            }
            value={fileContent ? '' : content}
            onChange={(e) => !fileContent && setContent(isEditWithOverflow ? e.target.value : e.target.value.slice(0, MAX_CONTENT_LENGTH))}
            disabled={!!fileContent}
            style={{
              width: '100%',
              padding: '12px',
              fontSize: '13px',
              fontFamily: 'Roboto, sans-serif',
              border: `1px solid ${fileContent ? colors.border.secondary : contentBorderColor}`,
              borderRadius: '8px',
              resize: 'vertical',
              outline: 'none',
              boxSizing: 'border-box',
              backgroundColor: fileContent ? '#f5f5f5' : undefined,
              color: fileContent ? colors.text.tertiary : undefined,
              cursor: fileContent ? 'not-allowed' : undefined,
            }}
          />
          {!fileContent && (
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: '4px' }}>
              {contentWarning}
              <Typography
                sx={{
                  fontSize: '11px',
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
            gap: '12px',
          }}
        >
          <CustomButton variant='secondary' size='Medium' text='Cancel' onClick={onClose} disabled={loading} />
          <CustomButton
            variant='primary'
            size='Medium'
            text={editKnowledgeBase ? 'Update' : 'Create'}
            onClick={handleSubmit}
            loading={loading}
            disabled={loading || !name.trim()}
          />
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

const statusColor = (status) => {
  switch (status) {
    case 'success':
      return { bg: colors.background.successLight || '#e6f4ea', text: colors.text.success || '#1e7e34' };
    case 'failure':
      return { bg: colors.background.errorLight || '#fce8e6', text: colors.text.error || '#d32f2f' };
    case 'partial':
      return { bg: '#fff3e0', text: '#e65100' };
    default:
      return { bg: colors.background.tertiaryLightest, text: colors.text.secondary };
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
      <Box sx={{ padding: '16px' }}>
        {loading ? (
          <Loader />
        ) : history.length === 0 ? (
          <Typography sx={{ fontSize: '13px', color: colors.text.tertiary, textAlign: 'center', py: 4 }}>No load history found</Typography>
        ) : (
          <TableContainer>
            <Table size='small'>
              <TableHead>
                <TableRow>
                  <TableCell sx={{ fontSize: '11px', fontWeight: 600, color: colors.text.tertiary }}>Date</TableCell>
                  <TableCell sx={{ fontSize: '11px', fontWeight: 600, color: colors.text.tertiary }}>Trigger</TableCell>
                  <TableCell sx={{ fontSize: '11px', fontWeight: 600, color: colors.text.tertiary }}>Status</TableCell>
                  <TableCell sx={{ fontSize: '11px', fontWeight: 600, color: colors.text.tertiary }}>Documents</TableCell>
                  <TableCell sx={{ fontSize: '11px', fontWeight: 600, color: colors.text.tertiary }}>Duration</TableCell>
                  <TableCell sx={{ fontSize: '11px', fontWeight: 600, color: colors.text.tertiary }}>Error</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {history.map((entry) => {
                  const sc = statusColor(entry.request_status);
                  return (
                    <TableRow key={entry.id}>
                      <TableCell sx={{ fontSize: '12px' }}>{formatDate(entry.created_at)}</TableCell>
                      <TableCell sx={{ fontSize: '12px' }}>{formatTrigger(entry)}</TableCell>
                      <TableCell>
                        <Chip
                          label={entry.request_status}
                          size='small'
                          sx={{ fontSize: '11px', fontWeight: 500, backgroundColor: sc.bg, color: sc.text }}
                        />
                      </TableCell>
                      <TableCell sx={{ fontSize: '12px' }}>{formatDocuments(entry)}</TableCell>
                      <TableCell sx={{ fontSize: '12px' }}>{formatDuration(entry.load_duration_seconds)}</TableCell>
                      <TableCell sx={{ fontSize: '12px', color: colors.text.error, maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                        {entry.error_message || '-'}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </TableContainer>
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
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '300px' }}>
        <Loader />
      </Box>
    );
  }

  if (error && knowledgeBases.length === 0) {
    return (
      <Box sx={{ p: 3 }}>
        <Alert severity='error'>{error}</Alert>
      </Box>
    );
  }

  return (
    <Box sx={{ p: 0 }}>
      {/* Header Section */}
      <WidgetCard sx={{ p: '16px 20px', mt: 0, mb: 0, display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <Box>
          <Typography
            sx={{
              fontSize: '14px',
              color: colors.text.secondary,
              fontWeight: 600,
              fontFamily: 'Poppins',
            }}
          >
            Knowledge Base
          </Typography>
          <Typography
            sx={{
              fontSize: '12px',
              color: colors.text.tertiary,
            }}
          >
            Account-scoped document library with AI semantic search-upload docs, map to agents, and they'll automatically search when needed.
          </Typography>
        </Box>
        {hasAccess && (
          <CustomButton
            variant='primary'
            size='Small'
            text={
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', fontFamily: 'Roboto', fontSize: '12px', fontWeight: 500 }}>
                <SafeIcon src={PlusIcon} alt='plus' width={14} height={14} />
                Add Knowledge Base
              </Box>
            }
            onClick={handleCreate}
          />
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
            padding: '48px 24px',
            marginBottom: '12px',
            border: `1px dashed ${colors.border.secondary}`,
            borderRadius: '8px',
            backgroundColor: colors.background.tertiaryLightest,
          }}
        >
          <Typography
            sx={{
              fontSize: '13px',
              color: colors.text.secondary,
              mb: 1,
            }}
          >
            No knowledge bases found
          </Typography>
          <Typography
            sx={{
              fontSize: '12px',
              color: colors.text.tertiary,
              mb: 2,
              textAlign: 'center',
            }}
          >
            Create a knowledge base to provide the AI with account-specific documentation and context.
          </Typography>
          {hasAccess && <CustomButton variant='secondary' size='Small' text='Create Knowledge Base' onClick={handleCreate} />}
        </Box>
      )}

      {/* Knowledge Base List — tabbed by source */}
      {knowledgeBases.length > 0 &&
        (() => {
          const integrationKBs = knowledgeBases.filter((kb) => kb.kb_type === 'integration');
          const userKBs = knowledgeBases.filter((kb) => kb.kb_type === 'manual');
          const visibleKBs = activeTab === 'integration' ? integrationKBs : userKBs;
          return (
            <Box sx={{ mt: 1 }}>
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
                <Typography sx={{ fontSize: '12px', color: colors.text.tertiary, textAlign: 'center', py: 4 }}>
                  No {activeTab === 'integration' ? 'integration' : 'user'} knowledge bases yet.
                </Typography>
              ) : (
                <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '12px', mt: 1 }}>
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
      >
        <Box sx={{ padding: '24px' }}>
          <Typography variant='body1' sx={{ mb: 2 }}>
            Are you sure you want to delete the knowledge base &quot;<strong>{selectedKnowledgeBase?.name}</strong>&quot;?
          </Typography>
          <Typography variant='body2' sx={{ color: colors.text.tertiary, mb: 3 }}>
            This action cannot be undone. The AI will no longer have access to this knowledge.
          </Typography>
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: '12px' }}>
            <CustomButton
              variant='secondary'
              size='Small'
              text='Cancel'
              onClick={() => {
                setDeleteModalOpen(false);
                setSelectedKnowledgeBase(null);
              }}
              disabled={submitting}
            />
            <CustomButton
              variant='primary'
              size='Small'
              text='Delete'
              onClick={handleConfirmDelete}
              loading={submitting}
              sx={{
                backgroundColor: colors.error,
                '&:hover': {
                  backgroundColor: colors.error,
                  filter: 'brightness(0.9)',
                },
              }}
            />
          </Box>
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

import React, { useEffect, useState, useRef } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography, Alert, TextField, TextareaAutosize } from '@mui/material';
import CustomTooltip from '@components1/common/CustomTooltip';
import { colors } from 'src/utils/colors';
import apiGlobalContext from '@api1/global-context';
import Loader from '@components1/common/Loader';
import { snackbar } from '@components1/common/snackbarService';
import { Text } from '@components1/common';
import CustomButton from '@components1/common/NewCustomButton';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { Modal } from '@components1/common/modal';
import { UploadIcon, PlusIcon, EditIcon, DeleteIconRed as deleteIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { hasWriteAccess } from '@lib/auth';
import WidgetCard from '@components1/common/WidgetCard';

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
  if (!dateString) return '-';
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

// Global Context Card Component
const GlobalContextCard = ({ context, onEdit, onDelete, hasAccess }) => {
  const MENU_ITEMS = [
    {
      label: 'Edit',
      value: 'edit',
      icon: EditIcon,
      disabled: !hasAccess,
    },
    {
      label: 'Delete',
      value: 'delete',
      icon: deleteIcon,
      disabled: !hasAccess,
    },
  ];

  const handleMenuClick = (item) => {
    if (item.value === 'edit') {
      onEdit(context);
    } else if (item.value === 'delete') {
      onDelete(context);
    }
  };

  const createdTooltip = formatExactDate(context.created_at) + (context.created_by?.display_name ? ` by ${context.created_by.display_name}` : '');

  return (
    <WidgetCard sx={{ p: '16px', mt: 0, mb: '12px' }}>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <Box sx={{ flex: 1, minWidth: 0 }}>
          {/* Primary: Name */}
          <Typography
            sx={{
              fontSize: '14px',
              fontWeight: 600,
              color: colors.text.primary,
              mb: '6px',
              whiteSpace: 'nowrap',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
            }}
          >
            {context.name}
          </Typography>

          {/* Secondary: Description */}
          {context.description ? (
            <Typography
              sx={{
                fontSize: '12px',
                color: colors.text.secondary,
                mb: '14px',
                display: '-webkit-box',
                WebkitLineClamp: 2,
                WebkitBoxOrient: 'vertical',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                lineHeight: '1.5',
              }}
            >
              {context.description}
            </Typography>
          ) : (
            <Box sx={{ mb: '14px' }} />
          )}

          {/* Tertiary: Updated (primary) + Created (secondary) */}
          <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
              <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>Updated</Typography>
              <CustomTooltip title={formatExactDate(context.updated_at)} placement='top'>
                <Typography sx={{ fontSize: '11px', fontWeight: 500, color: colors.text.secondary, cursor: 'default' }}>
                  {formatDate(context.updated_at)}
                </Typography>
              </CustomTooltip>
              {context.updated_by?.display_name && (
                <>
                  <Typography sx={{ fontSize: '11px', color: colors.text.tertiaryLight }}>by</Typography>
                  <Typography sx={{ fontSize: '11px', fontWeight: 500, color: colors.text.secondary }}>{context.updated_by.display_name}</Typography>
                </>
              )}
            </Box>
            <CustomTooltip title={createdTooltip} placement='top'>
              <Typography sx={{ fontSize: '11px', color: colors.text.tertiaryLight, cursor: 'default' }}>
                Created {formatDate(context.created_at)}
              </Typography>
            </CustomTooltip>
          </Box>
        </Box>
        <ThreeDotsMenu menuItems={MENU_ITEMS} data={context} onMenuClick={handleMenuClick} />
      </Box>
    </WidgetCard>
  );
};

GlobalContextCard.propTypes = {
  context: PropTypes.object.isRequired,
  onEdit: PropTypes.func.isRequired,
  onDelete: PropTypes.func.isRequired,
  hasAccess: PropTypes.bool.isRequired,
};

// Create/Edit Global Context Modal Component
const GlobalContextFormModal = ({ open, onClose, onSubmit, editContext, loading }) => {
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [content, setContent] = useState('');
  const [selectedFile, setSelectedFile] = useState(null);
  const [isDragging, setIsDragging] = useState(false);
  const fileInputRef = useRef(null);
  const dropZoneRef = useRef(null);

  useEffect(() => {
    if (editContext) {
      setName(editContext.name || '');
      setDescription(editContext.description || '');
      setContent(editContext.content || '');
    } else {
      setName('');
      setDescription('');
      setContent('');
    }
    setSelectedFile(null);
  }, [editContext, open]);

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
      // Read file content
      const reader = new FileReader();
      reader.onload = (e) => {
        setContent(e.target.result);
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
        setContent(ev.target.result);
      };
      reader.readAsText(file);
    }
  };

  const handleSubmit = () => {
    if (!name.trim()) {
      snackbar.error('Please enter a name for the global context');
      return;
    }
    onSubmit({
      name: name.trim(),
      description: description.trim(),
      content: content.trim(),
    });
  };

  return (
    <Modal open={open} handleClose={onClose} title={editContext ? 'Edit Global Context' : 'Create Global Context'} width='md'>
      <Box sx={{ padding: '24px' }}>
        <Text
          value='Provide account-level knowledge that will be used by the AI planner for more precise responses.'
          sx={{
            fontSize: '13px',
            color: colors.text.secondary,
            marginBottom: '20px',
          }}
        />

        {/* Name Field */}
        <Box sx={{ marginBottom: '16px' }}>
          <Typography
            sx={{
              fontSize: '13px',
              fontWeight: 500,
              color: colors.text.primary,
              marginBottom: '6px',
            }}
          >
            Name *
          </Typography>
          <TextField
            fullWidth
            placeholder='Enter a name for this global context (eg: global_gc_01, tenant_knowledge)'
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
          <Typography
            sx={{
              fontSize: '13px',
              fontWeight: 500,
              color: colors.text.primary,
              marginBottom: '6px',
            }}
          >
            Description
          </Typography>
          <TextField
            fullWidth
            placeholder='Enter a description for this global context'
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
            <Text
              value='Maximum file size: 5MB'
              sx={{
                fontSize: '11px',
                color: colors.text.tertiary,
                textAlign: 'center',
              }}
            />
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
            placeholder='Paste or type your global context content here...'
            value={content}
            onChange={(e) => setContent(e.target.value)}
            style={{
              width: '100%',
              padding: '12px',
              fontSize: '13px',
              fontFamily: 'Roboto, sans-serif',
              border: `1px solid ${colors.border.secondary}`,
              borderRadius: '8px',
              resize: 'vertical',
              outline: 'none',
              boxSizing: 'border-box',
            }}
          />
          <Typography
            sx={{
              fontSize: '11px',
              color: colors.text.tertiary,
              marginTop: '4px',
            }}
          >
            {content.length} characters
          </Typography>
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
            text={editContext ? 'Update' : 'Create'}
            onClick={handleSubmit}
            loading={loading}
            disabled={loading || !name.trim()}
          />
        </Box>
      </Box>
    </Modal>
  );
};

GlobalContextFormModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  onSubmit: PropTypes.func.isRequired,
  editContext: PropTypes.object,
  loading: PropTypes.bool,
};

const GlobalContextTab = ({ accountId }) => {
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [globalContexts, setGlobalContexts] = useState([]);
  const [error, setError] = useState(null);
  const [formModalOpen, setFormModalOpen] = useState(false);
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const [selectedContext, setSelectedContext] = useState(null);

  const hasAccess = hasWriteAccess(accountId);

  const fetchGlobalContexts = async () => {
    if (!accountId) {
      setError('Account ID is required');
      setLoading(false);
      return;
    }

    try {
      setLoading(true);
      const response = await apiGlobalContext.getGlobalContexts(accountId);
      if (response.errors && response.errors.length > 0) {
        setError('Failed to fetch global contexts');
        snackbar.error('Failed to fetch global contexts');
      } else if (response.data) {
        setGlobalContexts(response.data);
        setError(null);
      } else {
        setGlobalContexts([]);
      }
    } catch (err) {
      console.error('Error fetching global contexts:', err);
      setError('An error occurred while fetching global contexts');
      snackbar.error('An error occurred while fetching global contexts');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchGlobalContexts();
  }, [accountId]);

  const handleCreate = () => {
    setSelectedContext(null);
    setFormModalOpen(true);
  };

  const handleEdit = async (context) => {
    // Fetch full context data (including content) before editing
    try {
      setSubmitting(true);
      const response = await apiGlobalContext.getGlobalContext(accountId, context.id);
      if (response.errors && response.errors.length > 0) {
        snackbar.error('Failed to fetch global context details');
        return;
      }
      if (response.data) {
        setSelectedContext(response.data);
        setFormModalOpen(true);
      } else {
        snackbar.error('Failed to fetch global context details');
      }
    } catch (err) {
      console.error('Error fetching global context:', err);
      snackbar.error('An error occurred while fetching global context details');
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = (context) => {
    setSelectedContext(context);
    setDeleteModalOpen(true);
  };

  const handleFormSubmit = async (data) => {
    try {
      setSubmitting(true);
      let response;

      if (selectedContext) {
        // Update existing
        response = await apiGlobalContext.updateGlobalContext(accountId, selectedContext.id, data);
        if (response.errors && response.errors.length > 0) {
          const errorMessage = response.errors[0]?.message || 'Failed to update global context';
          snackbar.error(errorMessage);
          return;
        }
        snackbar.success('Global context updated successfully');
      } else {
        // Create new
        response = await apiGlobalContext.createGlobalContext(accountId, data);
        if (response.errors && response.errors.length > 0) {
          const errorMessage = response.errors[0]?.message || 'Failed to create global context';
          snackbar.error(errorMessage);
          return;
        }
        snackbar.success('Global context created successfully');
      }

      setFormModalOpen(false);
      setSelectedContext(null);
      fetchGlobalContexts();
    } catch (err) {
      console.error('Error submitting global context:', err);
      snackbar.error('An error occurred while saving the global context');
    } finally {
      setSubmitting(false);
    }
  };

  const handleConfirmDelete = async () => {
    if (!selectedContext) {
      return;
    }

    try {
      setSubmitting(true);
      const response = await apiGlobalContext.deleteGlobalContext(accountId, selectedContext.id);

      if (response.errors && response.errors.length > 0) {
        const errorMessage = response.errors[0]?.message || 'Failed to delete global context';
        snackbar.error(errorMessage);
        return;
      }

      snackbar.success('Global context deleted successfully');
      setDeleteModalOpen(false);
      setSelectedContext(null);
      fetchGlobalContexts();
    } catch (err) {
      console.error('Error deleting global context:', err);
      snackbar.error('An error occurred while deleting the global context');
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

  if (error && globalContexts.length === 0) {
    return (
      <Box sx={{ p: 3 }}>
        <Alert severity='error'>{error}</Alert>
      </Box>
    );
  }

  return (
    <Box sx={{ p: 0 }}>
      {/* Header Section */}
      <WidgetCard sx={{ p: '16px 20px', mt: 0, mb: 2, display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <Box>
          <Typography
            sx={{
              fontSize: '14px',
              color: colors.text.secondary,
              fontWeight: 600,
              fontFamily: 'Poppins',
            }}
          >
            Global Context
          </Typography>
          <Typography
            sx={{
              fontSize: '12px',
              color: colors.text.tertiary,
            }}
          >
            Account-level rules and identity that define how your AI debugger/planner behaves and reasons - set once, applies to all sessions.
          </Typography>
        </Box>
        {hasAccess && (
          <CustomButton
            variant='primary'
            size='Small'
            text={
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', fontFamily: 'Roboto', fontSize: '12px', fontWeight: 500 }}>
                <SafeIcon src={PlusIcon} alt='plus' width={14} height={14} />
                Add Global Context
              </Box>
            }
            onClick={handleCreate}
            disabled={globalContexts.length > 0}
            tooltip={globalContexts.length > 0 ? 'Only one global context is allowed per account' : undefined}
          />
        )}
      </WidgetCard>

      {/* Empty State */}
      {globalContexts.length === 0 && (
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
            No global contexts found
          </Typography>
          <Typography
            sx={{
              fontSize: '12px',
              color: colors.text.tertiary,
              mb: 2,
              textAlign: 'center',
            }}
          >
            Create a global context to provide the AI with account-specific knowledge. Only one global context is allowed per account.
          </Typography>
          {hasAccess && <CustomButton variant='secondary' size='Small' text='Create Global Context' onClick={handleCreate} />}
        </Box>
      )}

      {/* Global Context List */}
      {globalContexts.length > 0 && (
        <Box>
          {globalContexts.map((context) => (
            <GlobalContextCard key={context.id} context={context} onEdit={handleEdit} onDelete={handleDelete} hasAccess={hasAccess} />
          ))}
        </Box>
      )}

      {/* Create/Edit Modal */}
      <GlobalContextFormModal
        open={formModalOpen}
        onClose={() => {
          setFormModalOpen(false);
          setSelectedContext(null);
        }}
        onSubmit={handleFormSubmit}
        editContext={selectedContext}
        loading={submitting}
      />

      {/* Delete Confirmation Modal */}
      <Modal
        open={deleteModalOpen}
        handleClose={() => {
          setDeleteModalOpen(false);
          setSelectedContext(null);
        }}
        title={`Delete Global Context: ${selectedContext?.name || ''}`}
        width='sm'
      >
        <Box sx={{ padding: '24px' }}>
          <Typography variant='body1' sx={{ mb: 2 }}>
            Are you sure you want to delete the global context &quot;<strong>{selectedContext?.name}</strong>&quot;?
          </Typography>
          <Typography variant='body2' sx={{ color: colors.text.tertiary, mb: 3 }}>
            This action cannot be undone. The AI planner for this account will no longer have access to this context.
          </Typography>
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: '12px' }}>
            <CustomButton
              variant='secondary'
              size='Small'
              text='Cancel'
              onClick={() => {
                setDeleteModalOpen(false);
                setSelectedContext(null);
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
    </Box>
  );
};

GlobalContextTab.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default GlobalContextTab;

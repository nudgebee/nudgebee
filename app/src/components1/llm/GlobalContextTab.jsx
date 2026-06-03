import React, { useEffect, useState, useRef } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography, Alert } from '@mui/material';
import { Input } from '@components1/ds/Input';
import Tooltip from '@components1/ds/Tooltip';
import apiGlobalContext from '@api1/global-context';
import Loader from '@components1/common/Loader';
import { toast as snackbar } from '@components1/ds/Toast';
import { Button } from '@components1/ds/Button';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import { Modal } from '@components1/ds/Modal';
import { UploadIcon, PlusIcon, EditIcon, DeleteIconRed as deleteIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { hasWriteAccess } from '@lib/auth';
import { ds } from '@utils/colors';
import WidgetCard from '@components1/ds/WidgetCard';

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
    <WidgetCard sx={{ p: ds.space[4], mt: 0, mb: ds.space[3] }}>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <Box sx={{ flex: 1, minWidth: 0 }}>
          {/* Primary: Name */}
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-blue-500)',
              mb: ds.space.mul(0, 3),
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
                fontSize: 'var(--ds-text-small)',
                color: 'var(--ds-gray-700)',
                mb: ds.space.mul(0, 7),
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
            <Box sx={{ mb: ds.space.mul(0, 7) }} />
          )}

          {/* Tertiary: Updated (primary) + Created (secondary) */}
          <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Updated</Typography>
              <Tooltip title={formatExactDate(context.updated_at)} placement='top'>
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 500, color: 'var(--ds-gray-700)', cursor: 'default' }}>
                  {formatDate(context.updated_at)}
                </Typography>
              </Tooltip>
              {context.updated_by?.display_name && (
                <>
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)' }}>by</Typography>
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>
                    {context.updated_by.display_name}
                  </Typography>
                </>
              )}
            </Box>
            <Tooltip title={createdTooltip} placement='top'>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)', cursor: 'default' }}>
                Created {formatDate(context.created_at)}
              </Typography>
            </Tooltip>
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
    <Modal
      open={open}
      handleClose={onClose}
      title={editContext ? 'Edit Global Context' : 'Create Global Context'}
      width='md'
      actionButtons={
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[3], py: ds.space[3], px: ds.space[5] }}>
          <Button tone='secondary' size='md' onClick={onClose} disabled={loading}>
            Cancel
          </Button>
          <Button tone='primary' size='md' onClick={handleSubmit} loading={loading} disabled={loading || !name.trim()}>
            {editContext ? 'Update' : 'Create'}
          </Button>
        </Box>
      }
    >
      <Box sx={{ padding: ds.space[5] }}>
        <Typography
          sx={{
            fontFamily: 'var(--ds-font-display)',
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-regular)',
            color: 'var(--ds-gray-700)',
            marginBottom: ds.space.mul(1, 5),
            lineHeight: 1.5,
          }}
        >
          Provide account-level knowledge that will be used by the AI planner for more precise responses.
        </Typography>

        {/* Name Field */}
        <Box sx={{ marginBottom: ds.space[4] }}>
          <Input
            label='Name'
            required
            size='sm'
            placeholder='Enter a name for this global context (eg: global_gc_01, tenant_knowledge)'
            value={name}
            onChange={(next) => setName(next)}
          />
        </Box>

        {/* Description Field */}
        <Box sx={{ marginBottom: ds.space[4] }}>
          <Input
            label='Description'
            size='sm'
            type='textarea'
            rows={2}
            placeholder='Enter a description for this global context'
            value={description}
            onChange={(next) => setDescription(next)}
          />
        </Box>

        {/* File Upload Zone */}
        <Box sx={{ marginBottom: ds.space[4] }}>
          <Typography
            sx={{
              fontFamily: 'var(--ds-font-display)',
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
            <Typography
              sx={{
                fontFamily: 'var(--ds-font-display)',
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: 'var(--ds-blue-500)',
                textAlign: 'center',
              }}
            >
              {selectedFile ? selectedFile.name : 'Click or drag a .txt file to upload'}
            </Typography>
            <Typography
              sx={{
                fontFamily: 'var(--ds-font-display)',
                fontSize: 'var(--ds-text-caption)',
                color: 'var(--ds-gray-500)',
                textAlign: 'center',
              }}
            >
              Maximum file size: 5MB
            </Typography>
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
          <Box sx={{ flex: 1, height: '1px', backgroundColor: 'var(--ds-gray-300)' }} />
          <Typography
            sx={{
              fontFamily: 'var(--ds-font-display)',
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: 'var(--ds-gray-500)',
              textTransform: 'uppercase',
            }}
          >
            OR
          </Typography>
          <Box sx={{ flex: 1, height: '1px', backgroundColor: 'var(--ds-gray-300)' }} />
        </Box>

        {/* Content Text Area */}
        <Box sx={{ marginBottom: ds.space[5] }}>
          <Input
            label='Content'
            type='textarea'
            minRows={8}
            maxRows={15}
            placeholder='Paste or type your global context content here...'
            value={content}
            onChange={(next) => setContent(next)}
          />
          <Typography
            sx={{
              fontFamily: 'var(--ds-font-display)',
              fontSize: 'var(--ds-text-caption)',
              color: 'var(--ds-gray-500)',
              marginTop: ds.space[1],
            }}
          >
            {content.length} characters
          </Typography>
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
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: ds.space.mul(1, 75) }}>
        <Loader />
      </Box>
    );
  }

  if (error && globalContexts.length === 0) {
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
        sx={{
          py: ds.space[4],
          px: ds.space.mul(1, 5),
          mt: 0,
          mb: ds.space[4],
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'flex-start',
        }}
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
            Global Context
          </Typography>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-gray-500)',
            }}
          >
            Account-level rules and identity that define how your AI debugger/planner behaves and reasons - set once, applies to all sessions.
          </Typography>
        </Box>
        {hasAccess && (
          <Button
            tone='primary'
            size='sm'
            composition='icon+text'
            icon={<SafeIcon src={PlusIcon} alt='plus' width={14} height={14} />}
            onClick={handleCreate}
            disabled={globalContexts.length > 0}
            tooltip={globalContexts.length > 0 ? 'Only one global context is allowed per account' : undefined}
          >
            Add Global Context
          </Button>
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
            No global contexts found
          </Typography>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-gray-500)',
              mb: ds.space[4],
              textAlign: 'center',
            }}
          >
            Create a global context to provide the AI with account-specific knowledge. Only one global context is allowed per account.
          </Typography>
          {hasAccess && (
            <Button tone='secondary' size='sm' onClick={handleCreate}>
              Create Global Context
            </Button>
          )}
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
        <Box sx={{ padding: ds.space[5] }}>
          <Typography variant='body1' sx={{ mb: ds.space[4] }}>
            Are you sure you want to delete the global context &quot;<strong>{selectedContext?.name}</strong>&quot;?
          </Typography>
          <Typography variant='body2' sx={{ color: 'var(--ds-gray-500)', mb: ds.space[5] }}>
            This action cannot be undone. The AI planner for this account will no longer have access to this context.
          </Typography>
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[3] }}>
            <Button
              tone='secondary'
              size='sm'
              onClick={() => {
                setDeleteModalOpen(false);
                setSelectedContext(null);
              }}
              disabled={submitting}
            >
              Cancel
            </Button>
            <Button tone='danger' size='sm' onClick={handleConfirmDelete} loading={submitting}>
              Delete
            </Button>
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

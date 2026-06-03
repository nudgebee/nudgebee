import { Handle, Position, useReactFlow, useStore } from 'reactflow';
import PlayArrowIcon from '@mui/icons-material/PlayArrow';
import { useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Modal } from '@components1/ds/Modal';
import SafeIcon from '@components1/common/SafeIcon';
import { manualTriggerIcon, workflowUserIcon, workflowWebhookIcon, workflowCalendarIcon, alertYellowIcon } from '@assets';
import BaseNode from './BaseNode';
import HalfEdgeAddButton from '@components1/workflow/components/HalfEdgeAddButton';
import { spliceEdgesOnNodeDelete } from '../utils/spliceNode';
import { colors } from 'src/utils/colors';
import { Button } from '@components1/ds/Button';

// Function to get trigger icon based on subcategory (matches nodeCategories.ts trigger subcategories exactly)
const getTriggerIcon = (subcategory: string) => {
  switch (subcategory) {
    case 'manual':
      return workflowUserIcon?.default || workflowUserIcon; // Green user icon
    case 'webhook':
      return workflowWebhookIcon?.default || workflowWebhookIcon; // Orange webhook icon
    case 'schedule':
      return workflowCalendarIcon?.default || workflowCalendarIcon; // Calendar icon
    case 'event':
      return '⚡'; // Lightning emoji for event trigger
    default:
      return manualTriggerIcon?.default || manualTriggerIcon; // Default fallback
  }
};

const TriggerNode = ({ id, data, isConnectable, selected, onTriggerRun, onAddFromHandle }: any) => {
  const { setNodes, setEdges } = useReactFlow();
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const edges = useStore((state) => state.edges);
  const hasOutgoingEdge = edges.some((e) => e.source === id && (e.sourceHandle === 'trigger-output' || !e.sourceHandle));
  const isEditorMode = typeof document !== 'undefined' && document.querySelector('.editor-mode') !== null;
  // The HalfEdgeAddButton owns the source Handle while no outgoing edge exists; otherwise the node
  // renders its own Handle so the existing edge stays anchored to the node.
  const showHalfEdgeAddButton = !hasOutgoingEdge && isEditorMode && !!onAddFromHandle;

  // Get validation icon - shows yellow alert when trigger is invalid
  const getValidationIcon = () => {
    if (!data.trigger) {
      return null;
    }
    if (data.trigger.valid === false) {
      return <SafeIcon src={alertYellowIcon} alt='alert-icon' width={24} height={24} />;
    }
    return null;
  };

  // Handle trigger run execution
  const handleTriggerRun = (_e: React.MouseEvent) => {
    if (onTriggerRun) {
      onTriggerRun();
    }
  };

  // Open delete confirmation modal
  const handleDeleteClick = () => {
    setDeleteModalOpen(true);
  };

  // Handle confirmed deletion
  const handleConfirmDelete = () => {
    setNodes((nodes) => nodes.filter((node) => node.id !== id));
    setEdges((edges) => spliceEdgesOnNodeDelete(id, edges));
    setDeleteModalOpen(false);
  };

  // Handle cancel deletion
  const handleCancelDelete = () => {
    setDeleteModalOpen(false);
  };

  // Get trigger icon
  const getTriggerIconElement = () => {
    const triggerType = data.trigger?.type || data.triggerType || 'manual';
    const icon = getTriggerIcon(triggerType);
    // Check if icon is an emoji (string without path characters) or image file
    if (typeof icon === 'string' && !icon.includes('/') && !icon.includes('.')) {
      // Render emoji as text
      return <span style={{ fontSize: 'var(--ds-text-body-lg)' }}>{icon}</span>;
    }
    // Render as Next.js Image for actual image files
    return (
      <SafeIcon
        src={icon}
        alt='trigger-icon'
        width={20}
        height={20}
        style={{ filter: 'brightness(0) saturate(100%) invert(1)' }} // Force icon white
      />
    );
  };

  // Get border style based on validation and selection state
  const getBorderStyle = () => {
    if (data.isDeleted) {
      return '2px dashed #9ca3af'; // Dashed gray for deleted nodes
    }
    if (data.connectionRejected) {
      return '3px solid #ef4444'; // Red for connection errors
    }
    if (data.trigger?.valid === false) {
      return '2px solid #fbbf24'; // Yellow for validation errors
    }
    if (selected) {
      return '2px solid #1D4ED8'; // Dark blue for selected
    }
    return `1px solid ${colors.iconColor}`; // Default gray
  };

  // Get status badges showing validation icon
  const getStatusBadges = () => {
    const validationIcon = getValidationIcon();
    if (!validationIcon) {
      return null;
    }
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            height: '28px',
            borderRadius: 'var(--ds-radius-xl)',
            width: '28px',
            backgroundColor: 'white',
            padding: 'var(--ds-space-1)',
          }}
        >
          {validationIcon}
        </div>
      </div>
    );
  };

  return (
    <div style={{ position: 'relative' }}>
      <BaseNode
        selected={selected}
        border={getBorderStyle()}
        background={data.isDeleted ? '#f9fafb' : 'white'}
        nodeStyle={data.isDeleted ? { opacity: 0.7 } : {}}
        onDelete={handleDeleteClick}
        content={{
          icon: getTriggerIconElement(),
          label: data.isDeleted ? (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
              <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'bold', color: 'var(--ds-gray-400)' }}>{data.label}</Typography>
              <Box
                sx={{
                  backgroundColor: 'var(--ds-background-300)',
                  border: '1px solid var(--ds-brand-200)',
                  borderRadius: 'var(--ds-radius-sm)',
                  padding: 'var(--ds-space-1) var(--ds-space-1)',
                  fontSize: 'var(--ds-text-caption)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: 'var(--ds-gray-600)',
                }}
              >
                Deleted
              </Box>
            </Box>
          ) : (
            data.label
          ),
          description: data.description,
          statusBadges: getStatusBadges(),
          badge: (
            <div
              style={{
                position: 'absolute',
                top: '-14px',
                left: '16px',
                scrollPaddingLeft: '24px',
                backgroundColor: data.isDeleted ? '#f3f4f6' : '#FFF7ED',
                border: data.isDeleted ? '1px solid #d1d5db' : '1px solid #F97316',
                color: data.isDeleted ? '#9ca3af' : '#F97316',
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'bold',
                padding: 'var(--ds-space-1) var(--ds-space-2)',
                borderRadius: 'var(--ds-radius-xl)',
                letterSpacing: '0.5px',
                zIndex: 10,
              }}
            >
              TRIGGER
            </div>
          ),
          iconContainerStyle: {
            background: data.isDeleted ? '#9ca3af' : '#F79009',
            color: 'white',
          },
          labelStyle: {
            color: data.isDeleted ? '#9ca3af' : colors.text.secondary,
          },
        }}
        additionalContent={
          showHalfEdgeAddButton ? null : (
            <Handle
              type='source'
              position={Position.Bottom}
              id='trigger-output'
              isConnectable={isConnectable}
              style={{
                width: '40px',
                borderRadius: '0%',
                height: '14px',
                backgroundColor: 'transparent',
                borderBottom: 'none',
                borderTop: '4px solid rgb(142, 185, 255)',
                borderLeft: 'none',
                borderRight: 'none',
                bottom: '-18px',
                opacity: 1,
                transition: 'opacity 0.2s',
                cursor: 'crosshair',
              }}
            />
          )
        }
        primaryButton={
          onTriggerRun
            ? {
                icon: <PlayArrowIcon sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-green-500)', pointerEvents: 'none' }} />,
                onClick: handleTriggerRun,
                title: 'Trigger automation execution',
                hoverBackgroundColor: 'rgb(220, 255, 233)',
                hoverBorderColor: 'rgb(126, 218, 160)',
              }
            : undefined
        }
        deleteButtonConfig={
          data.executionStatus || data.isDeleted
            ? { hidden: true }
            : {
                title: 'Delete trigger node',
              }
        }
      />

      {/* Delete Confirmation Modal */}
      <Modal
        open={deleteModalOpen}
        handleClose={handleCancelDelete}
        title='Delete Trigger'
        width='sm'
        actionButtons={
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)', justifyContent: 'flex-end', padding: 'var(--ds-space-3) var(--ds-space-5)' }}>
            <Button id='wf-node-trigger-delete-cancel-btn' tone='ghost' onClick={handleCancelDelete}>
              Cancel
            </Button>
            <Button id='wf-node-trigger-delete-confirm-btn' tone='danger' onClick={handleConfirmDelete}>
              Delete Action
            </Button>
          </Box>
        }
      >
        <Box sx={{ padding: 'var(--ds-space-4) 0' }}>
          <Typography variant='body1' sx={{ color: 'var(--ds-brand-500)', marginBottom: 'var(--ds-space-2)', lineHeight: 1.6 }}>
            Are you sure you want to delete the trigger <strong>&quot;{data.label}&quot;</strong>?
          </Typography>
          <Typography variant='body2' sx={{ color: 'var(--ds-gray-600)', marginBottom: 'var(--ds-space-5)', lineHeight: 1.5 }}>
            This will permanently remove the trigger and all its connected edges. This action cannot be undone.
          </Typography>
        </Box>
      </Modal>

      {/* Half-edge add button - rendered outside the node card. Owns the source Handle while unconnected. */}
      {showHalfEdgeAddButton && (
        <div style={{ position: 'absolute', left: '50%', transform: 'translateX(-50%)', top: '100%', zIndex: 10 }}>
          <HalfEdgeAddButton
            id='wf-node-trigger-add-edge-btn'
            onClick={() => onAddFromHandle(id, 'trigger-output')}
            sourceHandleId='trigger-output'
            isConnectable={isConnectable}
          />
        </div>
      )}
    </div>
  );
};

export default TriggerNode;

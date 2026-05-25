import { Handle, Position, useReactFlow, useStore } from 'reactflow';
import PlayArrowIcon from '@mui/icons-material/PlayArrow';
import { useState } from 'react';
import { Box, Typography, Modal } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { manualTriggerIcon, workflowUserIcon, workflowWebhookIcon, workflowCalendarIcon, alertYellowIcon } from '@assets';
import BaseNode from './BaseNode';
import HalfEdgeAddButton from '@components1/workflow/components/HalfEdgeAddButton';
import { spliceEdgesOnNodeDelete } from '../utils/spliceNode';
import { colors } from 'src/utils/colors';
import CustomButton from '@components1/common/NewCustomButton';

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
      return <span style={{ fontSize: '14px' }}>{icon}</span>;
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
      <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            height: '28px',
            borderRadius: '12px',
            width: '28px',
            backgroundColor: 'white',
            padding: '4px',
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
            <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
              <Typography sx={{ fontSize: '14px', fontWeight: 'bold', color: '#9ca3af' }}>{data.label}</Typography>
              <Box
                sx={{
                  backgroundColor: '#f3f4f6',
                  border: '1px solid #d1d5db',
                  borderRadius: '4px',
                  padding: '1px 6px',
                  fontSize: '9px',
                  fontWeight: 500,
                  color: '#6b7280',
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
                fontSize: '10px',
                fontWeight: 'bold',
                padding: '4px 8px',
                borderRadius: '12px',
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
                icon: <PlayArrowIcon sx={{ fontSize: '14px', color: '#16a34a', pointerEvents: 'none' }} />,
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
        onClose={handleCancelDelete}
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <Box
          sx={{
            backgroundColor: 'white',
            borderRadius: '12px',
            padding: '24px',
            minWidth: '400px',
            maxWidth: '500px',
            boxShadow: '0 20px 25px -5px rgba(0, 0, 0, 0.1), 0 10px 10px -5px rgba(0, 0, 0, 0.04)',
            border: '1px solid #e5e7eb',
          }}
        >
          <Typography
            variant='h6'
            sx={{
              fontWeight: 600,
              color: '#111827',
              marginBottom: '12px',
              display: 'flex',
              alignItems: 'center',
              gap: '8px',
            }}
          >
            Delete Trigger
          </Typography>

          <Typography
            variant='body1'
            sx={{
              color: '#374151',
              marginBottom: '8px',
              lineHeight: 1.6,
            }}
          >
            Are you sure you want to delete the trigger <strong>&quot;{data.label}&quot;</strong>?
          </Typography>

          <Typography
            variant='body2'
            sx={{
              color: '#6b7280',
              marginBottom: '24px',
              lineHeight: 1.5,
            }}
          >
            This will permanently remove the trigger and all its connected edges. This action cannot be undone.
          </Typography>

          <Box
            sx={{
              display: 'flex',
              gap: '12px',
              justifyContent: 'flex-end',
            }}
          >
            <CustomButton id='wf-node-trigger-delete-cancel-btn' text={'Cancel'} variant='tertiary' onClick={handleCancelDelete} />

            <CustomButton id='wf-node-trigger-delete-confirm-btn' text={'Delete Action'} variant='primary' onClick={handleConfirmDelete} />
          </Box>
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

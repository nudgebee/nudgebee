import { Handle, Position, useReactFlow, useStore, useUpdateNodeInternals } from 'reactflow';
import CheckIcon from '@mui/icons-material/Check';
import CloseIcon from '@mui/icons-material/Close';
import { useState, useEffect } from 'react';
import { Box, Typography } from '@mui/material';
import { Modal } from '@components1/ds/Modal';
import { Input } from '@components1/ds/Input';
import { Button } from '@components1/ds/Button';
import { coreOpsIcon, alertYellowIcon, SuccessIcon, ErrorIcon, RunningIcon, SkipForwardIcon, timerSVG } from '@assets';
import BaseNode from './BaseNode';
import HalfEdgeAddButton from '@components1/workflow/components/HalfEdgeAddButton';
import { colors } from 'src/utils/colors';
import SafeIcon from '@components1/common/SafeIcon';
import { validateTaskId } from '@components1/workflow/utils/taskUtils';

const SWITCH_COLORS = {
  case: 'var(--ds-purple-400)',
  default: 'var(--ds-gray-400)',
  border: 'var(--ds-purple-400)',
};

const SwitchNode = ({ id, data, isConnectable, selected, onAddFromHandle }: any) => {
  const { setNodes, setEdges, getNodes } = useReactFlow();
  const updateNodeInternals = useUpdateNodeInternals();
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const [isEditingId, setIsEditingId] = useState(false);
  const [editedId, setEditedId] = useState(id);
  const [idError, setIdError] = useState<string | null>(null);
  const storeEdges = useStore((state) => state.edges);
  const isEditorMode = typeof document !== 'undefined' && document.querySelector('.editor-mode') !== null;

  const cases: Array<{ value: string; next?: string }> = data.taskConfig?.config?.cases || [];
  const expression: string = data.taskConfig?.config?.expression || '';

  // Build output handles: one per case with non-empty value + always a default
  const validCases = cases.filter((c) => c.value);
  const allHandles = [
    ...validCases.map((c) => ({ id: `switch-case-${c.value}`, label: c.value, color: SWITCH_COLORS.case })),
    { id: 'switch-default', label: 'default', color: SWITCH_COLORS.default },
  ];

  // Per-handle ownership: when a case has no outgoing edge AND we're in editor mode with the
  // sidebar-opening callback, the HalfEdgeAddButton owns the source Handle for that case.
  // Otherwise the node renders its own Handle so an existing edge stays anchored to the node.
  const showHalfEdgeFor = (handleId: string) =>
    isEditorMode && !!onAddFromHandle && !storeEdges.some((e) => e.source === id && e.sourceHandle === handleId);

  // Force ReactFlow to recalculate handle positions when cases or their IDs change
  const handleIdsKey = allHandles.map((h) => h.id).join(',');
  useEffect(() => {
    updateNodeInternals(id);
  }, [id, handleIdsKey, updateNodeInternals]);

  // --- Status badges ---

  const getExecutionIcon = () => {
    if (!data.executionStatus) return null;
    const iconMap: Record<string, { src: any; alt: string }> = {
      RUNNING: { src: RunningIcon, alt: 'running' },
      COMPLETED: { src: SuccessIcon, alt: 'success' },
      SUCCESS: { src: SuccessIcon, alt: 'success' },
      FAILED: { src: ErrorIcon, alt: 'error' },
      ERROR: { src: ErrorIcon, alt: 'error' },
      TIMEOUT: { src: ErrorIcon, alt: 'error' },
      SKIPPED: { src: SkipForwardIcon, alt: 'skipped' },
      CANCELED: { src: ErrorIcon, alt: 'canceled' },
      CANCELLED: { src: ErrorIcon, alt: 'canceled' },
      PENDING: { src: timerSVG, alt: 'pending' },
      QUEUED: { src: timerSVG, alt: 'pending' },
    };
    const icon = iconMap[data.executionStatus];
    return icon ? <SafeIcon src={icon.src} alt={icon.alt} width={24} height={24} /> : null;
  };

  const getValidationIcon = () => {
    if (!data.taskConfig || data.taskConfig.valid !== false) return null;
    return <SafeIcon src={alertYellowIcon} alt='alert-icon' width={24} height={24} />;
  };

  const getStatusBadges = () => (
    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
      {getExecutionIcon() && (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            padding: 'var(--ds-space-1)',
            borderRadius: 'var(--ds-radius-xl)',
            animation: data.executionStatus === 'RUNNING' ? 'pulse 1.5s ease-in-out infinite' : 'none',
          }}
        >
          {getExecutionIcon()}
        </div>
      )}
      {!getExecutionIcon() && getValidationIcon() && (
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
            color: 'var(--ds-red-600)',
          }}
        >
          {getValidationIcon()}
        </div>
      )}
    </div>
  );

  // --- Node operations ---

  const handleDeleteClick = () => setDeleteModalOpen(true);

  const handleConfirmDelete = () => {
    setNodes((nodes) => nodes.filter((node) => node.id !== id));
    setEdges((edges) => edges.filter((edge) => edge.source !== id && edge.target !== id));
    setDeleteModalOpen(false);
  };

  const handleCancelDelete = () => setDeleteModalOpen(false);

  const handleEditId = () => {
    setIsEditingId(true);
    setEditedId(id);
    setIdError(null);
  };

  const handleSaveId = () => {
    const trimmed = editedId.trim();
    // Mirror backend ValidateTaskID so user sees the constraint at rename time
    // instead of getting a silent sanitize-on-export rewrite.
    const error = validateTaskId(trimmed);
    if (error) {
      setIdError(error);
      return;
    }
    if (trimmed && trimmed !== id) {
      const currentNodes = getNodes();
      const existingIds = new Set(currentNodes.filter((n) => n.id !== id).map((n) => n.id));

      let finalId = trimmed;
      if (existingIds.has(finalId)) {
        let counter = 1;
        let uniqueId = `${finalId}-${counter}`;
        while (existingIds.has(uniqueId)) {
          counter++;
          uniqueId = `${finalId}-${counter}`;
        }
        finalId = uniqueId;
        setEditedId(finalId);
      }

      setNodes((nodes) =>
        nodes.map((node) => {
          if (node.id === id) {
            return {
              ...node,
              id: finalId,
              data: {
                ...node.data,
                taskConfig: node.data.taskConfig ? { ...node.data.taskConfig, id: finalId } : node.data.taskConfig,
              },
            };
          }
          return node;
        })
      );

      setEdges((edges) =>
        edges.map((edge) => ({
          ...edge,
          source: edge.source === id ? finalId : edge.source,
          target: edge.target === id ? finalId : edge.target,
        }))
      );
    }
    setIsEditingId(false);
  };

  const handleCancelEditId = () => {
    setIsEditingId(false);
    setEditedId(id);
    setIdError(null);
  };

  const handleDuplicateTask = () => {
    const currentNodes = getNodes();
    const existingIds = new Set(currentNodes.map((n) => n.id));

    let counter = 1;
    let uniqueId = `${id}-${counter}`;
    while (existingIds.has(uniqueId)) {
      counter++;
      uniqueId = `${id}-${counter}`;
    }

    const currentNode = currentNodes.find((n) => n.id === id);
    if (!currentNode) return;

    const clonedTaskConfig = data.taskConfig ? JSON.parse(JSON.stringify(data.taskConfig)) : null;
    if (clonedTaskConfig) clonedTaskConfig.id = uniqueId;

    setNodes((nodes) => [
      ...nodes,
      {
        id: uniqueId,
        type: 'switch',
        position: { x: currentNode.position.x + 50, y: currentNode.position.y + 80 },
        data: { ...JSON.parse(JSON.stringify(data)), taskConfig: clonedTaskConfig, executionStatus: undefined },
        selected: false,
      },
    ]);
  };

  return (
    <div style={{ position: 'relative' }}>
      <BaseNode
        selected={selected}
        border={
          data.connectionRejected
            ? '3px solid #ef4444'
            : data.taskConfig?.valid === false
            ? '2px solid #fbbf24'
            : selected
            ? `2px solid ${SWITCH_COLORS.border}`
            : `1px solid ${colors.iconColor}`
        }
        minWidth='320px'
        maxWidth='400px'
        padding='14px 16px 0px 16px'
        onDelete={handleDeleteClick}
        content={{
          icon: <SafeIcon src={coreOpsIcon} alt='switch-icon' width={20} height={20} style={{ filter: 'brightness(0) saturate(100%) invert(1)' }} />,
          label: isEditingId ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', width: '100%' }}>
              <Box sx={{ flex: 1 }}>
                <Input
                  id='change-id'
                  value={editedId}
                  onChange={(v) => {
                    setEditedId(v);
                    setIdError(validateTaskId(v));
                  }}
                  onKeyDown={(e) => {
                    e.stopPropagation();
                    if (e.key === 'Enter') handleSaveId();
                    else if (e.key === 'Escape') handleCancelEditId();
                  }}
                  size='sm'
                  error={idError || undefined}
                />
              </Box>
              <button
                id='wf-node-switch-save-id-btn'
                type='button'
                className='nodrag nopan'
                onClick={handleSaveId}
                disabled={!!idError}
                tabIndex={0}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    handleSaveId();
                  }
                }}
                style={{
                  background: 'none',
                  cursor: idError ? 'not-allowed' : 'pointer',
                  display: 'flex',
                  alignItems: 'center',
                  padding: 'var(--ds-space-1)',
                  borderRadius: 'var(--ds-radius-sm)',
                  backgroundColor: idError ? '#f3f4f6' : '#f0f9ff',
                  border: `1px solid ${idError ? '#d1d5db' : '#0ea5e9'}`,
                  opacity: idError ? 0.5 : 1,
                }}
                title={idError || 'Save ID'}
              >
                <CheckIcon sx={{ fontSize: 'var(--ds-text-title)', color: idError ? '#9ca3af' : '#0ea5e9' }} />
              </button>
              <button
                id='wf-node-switch-cancel-id-btn'
                type='button'
                className='nodrag nopan'
                onClick={handleCancelEditId}
                tabIndex={0}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    handleCancelEditId();
                  }
                }}
                style={{
                  background: 'none',
                  cursor: 'pointer',
                  display: 'flex',
                  alignItems: 'center',
                  padding: 'var(--ds-space-1)',
                  borderRadius: 'var(--ds-radius-sm)',
                  backgroundColor: 'var(--ds-red-100)',
                  border: '1px solid var(--ds-red-500)',
                }}
                title='Cancel'
              >
                <CloseIcon sx={{ fontSize: 'var(--ds-text-title)', color: 'var(--ds-red-500)' }} />
              </button>
            </div>
          ) : (
            <Box sx={{ display: 'flex', flexDirection: 'column' }}>
              <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'bold', color: colors.text.secondary }}>
                {data.label || id}
              </Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary, mt: -0.5 }}>{id}</Typography>
            </Box>
          ),
          description: expression ? `switch(${expression})` : 'Configure expression...',
          iconContainerStyle: { backgroundColor: SWITCH_COLORS.case },
          statusBadges: getStatusBadges(),
        }}
        additionalContent={
          <>
            {/* Input handle (top) */}
            <Handle
              type='target'
              position={Position.Top}
              id='action-input'
              isConnectable={isConnectable}
              style={{
                width: '40px',
                borderRadius: '0%',
                height: '14px',
                backgroundColor: 'transparent',
                borderBottom: '4px solid rgb(142, 185, 255)',
                borderTop: 'none',
                borderLeft: 'none',
                borderRight: 'none',
                top: '-18px',
                opacity: 1,
                transition: 'opacity 0.2s',
                cursor: 'crosshair',
              }}
            />
            {/* Output footer: labeled columns per case + default */}
            <Box
              sx={{
                display: 'flex',
                borderTop: `1px solid ${colors.border.secondaryLight}`,
                mt: 1.5,
              }}
            >
              {allHandles.map((h, i) => (
                <Box
                  key={h.id}
                  sx={{
                    flex: 1,
                    textAlign: 'center',
                    py: 0.75,
                    borderRight: i < allHandles.length - 1 ? `1px solid ${colors.border.secondaryLight}` : 'none',
                  }}
                >
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-caption)',
                      color: h.color,
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      whiteSpace: 'nowrap',
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                    }}
                  >
                    {h.label}
                  </Typography>
                </Box>
              ))}
            </Box>

            {/* Output handles — one per footer column, centered. Skipped when the HalfEdgeAddButton below owns the Handle. */}
            {allHandles.map((h, i) => {
              if (showHalfEdgeFor(h.id)) return null;
              return (
                <Handle
                  key={h.id}
                  type='source'
                  position={Position.Bottom}
                  id={h.id}
                  isConnectable={isConnectable}
                  style={{
                    left: `${((i + 0.5) / allHandles.length) * 100}%`,
                    width: '40px',
                    borderRadius: '0%',
                    height: '14px',
                    backgroundColor: 'transparent',
                    borderTop: '4px solid rgb(142, 185, 255)',
                    borderBottom: 'none',
                    borderLeft: 'none',
                    borderRight: 'none',
                    cursor: 'crosshair',
                    bottom: '-18px',
                    opacity: 1,
                  }}
                />
              );
            })}
          </>
        }
        menuItems={[
          { label: 'Rename', onClick: handleEditId },
          { label: 'Duplicate', onClick: handleDuplicateTask },
        ]}
        deleteButtonConfig={{ title: 'Delete switch node' }}
      />

      {/* Delete Confirmation Modal */}
      <Modal
        open={deleteModalOpen}
        handleClose={handleCancelDelete}
        title='Delete Switch?'
        width='sm'
        actionButtons={
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)', justifyContent: 'flex-end', padding: 'var(--ds-space-3) var(--ds-space-5)' }}>
            <Button id='wf-node-switch-delete-cancel-btn' tone='ghost' onClick={handleCancelDelete}>
              Cancel
            </Button>
            <Button id='wf-node-switch-delete-confirm-btn' tone='danger' onClick={handleConfirmDelete}>
              Delete Switch
            </Button>
          </Box>
        }
      >
        <Box sx={{ padding: 'var(--ds-space-4) 0' }}>
          <Typography variant='body1' sx={{ color: 'var(--ds-brand-500)', marginBottom: 'var(--ds-space-2)', lineHeight: 1.6 }}>
            Are you sure you want to delete the switch node <strong>&quot;{data.label || id}&quot;</strong>?
          </Typography>
        </Box>
      </Modal>

      {/* Half-edge add buttons for unconnected switch outputs - rendered outside the node card.
          Each owns the source Handle for its case while unconnected. */}
      {allHandles.map((h, i) => {
        if (!showHalfEdgeFor(h.id)) return null;
        return (
          <div
            key={`half-edge-${h.id}`}
            style={{
              position: 'absolute',
              left: `${((i + 0.5) / allHandles.length) * 100}%`,
              transform: 'translateX(-50%)',
              top: '100%',
              zIndex: 10,
            }}
          >
            <HalfEdgeAddButton
              id={`wf-node-switch-add-edge-${h.id}-btn`}
              onClick={() => onAddFromHandle(id, h.id)}
              sourceHandleId={h.id}
              isConnectable={isConnectable}
            />
          </div>
        );
      })}
    </div>
  );
};

export default SwitchNode;

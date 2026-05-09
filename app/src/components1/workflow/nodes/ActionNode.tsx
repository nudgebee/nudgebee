import { Handle, Position, useReactFlow, useStore } from 'reactflow';
import PlayArrowIcon from '@mui/icons-material/PlayArrow';
import PauseCircleOutlineIcon from '@mui/icons-material/PauseCircleOutline';
import PlayCircleOutlineIcon from '@mui/icons-material/PlayCircleOutline';
import EditOutlinedIcon from '@mui/icons-material/EditOutlined';
import ContentCopyOutlinedIcon from '@mui/icons-material/ContentCopyOutlined';
import CheckIcon from '@mui/icons-material/Check';
import CloseIcon from '@mui/icons-material/Close';
import { useState } from 'react';
import { Box, Typography, Modal, TextField } from '@mui/material';
import CustomButton from '@components1/common/NewCustomButton';
import {
  workflowMessagingIcon,
  workflowSubWorkflowIcon,
  workflowFormatterIcon,
  workflowDatabaseIcon,
  workflowWebhookIcon,
  aiAgentIcon,
  TicketBlueIcon,
  coreOpsIcon,
  CloudUploadIcon,
  BarsBlueOutlineIcon,
  NotificationIcon1,
  PlayCircleIcon,
  IntegrationsIcon,
  LLMFunctionIcon,
  RabbitmqIcon,
  RedisLogoIcon,
  GithubIcon,
  GitLabIcon,
  ArgocdIcon,
  K8sIcon,
  SlackIcon,
  newAwsLogo,
  ouAzure,
  ouGoogle,
  alertYellowIcon,
  SuccessIcon,
  ErrorIcon,
  RunningIcon,
  SkipForwardIcon,
  timerSVG,
  McpIcon,
} from '@assets';
import BaseNode from './BaseNode';
import HalfEdgeAddButton from '@components1/workflow/components/HalfEdgeAddButton';
import { sanitizeTaskId } from '@components1/workflow/utils/taskUtils';
import { cleanupSwitchReferencesAfterDelete } from '@components1/workflow/utils/templateUtils';
import { spliceEdgesOnNodeDelete } from '@components1/workflow/utils/spliceNode';
import { disableTask, enableTask } from '@components1/workflow/utils/toggleTaskDisable';

import { colors } from 'src/utils/colors';
import { snakeToTitleCase } from 'src/utils/common';
import SafeIcon from '@components1/common/SafeIcon';

// Tasks marked as deprecated still execute, but render a Deprecated pill on the node.
// Keep in sync with DEPRECATED_TASKS in nodeCategories.ts — both go away once the backend
// exposes a deprecated flag on TaskDefinition.
const DEPRECATED_TASK_TYPES = new Set(['llm.router']);

// Function to get appropriate icon based on task type (matches nodeCategories.ts exactly)
const getTaskIcon = (taskType: string) => {
  if (!taskType) {
    return workflowSubWorkflowIcon?.default || workflowSubWorkflowIcon;
  } // Default fallback

  // First, check for specific task icons
  const specificTaskIcons: { [key: string]: any } = {
    // Cloud providers
    'cloud.aws.cli': newAwsLogo,
    'cloud.azure.cli': ouAzure,
    'cloud.gcp.cli': ouGoogle,
    'cloud.k8s.cli': K8sIcon,

    // Direct cloud provider tasks
    'aws.cli': newAwsLogo,
    'azure.cli': ouAzure,
    'gcp.cli': ouGoogle,
    'k8s.cli': K8sIcon,

    // Message queues
    'mq.rabbitmqadmin.cli': RabbitmqIcon,

    // Databases
    'dbms.redis.cli': RedisLogoIcon,

    // Source control
    'scm.github.cli': GithubIcon,
    'scm.gitlab.cli': GitLabIcon,

    // CI/CD
    'cicd.argocd.cli': ArgocdIcon,

    // MCP
    'llm.mcp_call': McpIcon,
  };

  // Return specific task icon if available
  if (specificTaskIcons[taskType]) {
    return specificTaskIcons[taskType];
  }

  // Fall back to category-based icons
  const prefix = taskType.split('.')[0];
  const categoryMap: { [key: string]: any } = {
    cloud: CloudUploadIcon,
    dbms: workflowDatabaseIcon?.default || workflowDatabaseIcon,
    notifications: NotificationIcon1,
    observability: BarsBlueOutlineIcon,
    scripting: PlayCircleIcon,
    integrations: workflowWebhookIcon?.default || workflowWebhookIcon,
    tickets: TicketBlueIcon,
    llm: aiAgentIcon,
    data: workflowFormatterIcon?.default || workflowFormatterIcon,
    core: coreOpsIcon,
    cicd: IntegrationsIcon,
    network: workflowMessagingIcon?.default || workflowMessagingIcon,
    mq: workflowMessagingIcon?.default || workflowMessagingIcon,
    scm: GithubIcon,
    crypto: LLMFunctionIcon,
    events: BarsBlueOutlineIcon,
    aws: newAwsLogo,
    gcp: ouGoogle,
    azure: ouAzure,
    k8s: K8sIcon,
    slack: SlackIcon,
    mcp: McpIcon,
  };

  return categoryMap[prefix] || '📦'; // Default for 'Other' operations (matches nodeCategories.ts line 48)
};

// Tasks that don't support individual task execution (task trigger)
const TASKS_WITHOUT_INDIVIDUAL_RUN = new Set([
  'core.group',
  'core.switch',
  'core.foreach',
  'core.call-workflow',
  'ai.router',
  'core.approval',
  'core.wait',
  'ai.llm_event_investigate',
]);

const ActionNode = ({ id, data, isConnectable, selected, onTestTask, accountId, onAddFromHandle }: any) => {
  const { setNodes, setEdges, getNodes, getEdges } = useReactFlow();
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const [disableConfirmOpen, setDisableConfirmOpen] = useState(false);
  const [disableConfirmCount, setDisableConfirmCount] = useState(0);
  const [isEditingId, setIsEditingId] = useState(false);
  const [editedId, setEditedId] = useState(id);
  const edges = useStore((state) => state.edges);
  const hasOutgoingEdge = edges.some((e) => e.source === id && (e.sourceHandle === 'action-output' || !e.sourceHandle));
  const isEditorMode = typeof document !== 'undefined' && document.querySelector('.editor-mode') !== null;
  const showHalfEdgeAddButton = !hasOutgoingEdge && isEditorMode && !!onAddFromHandle;

  const getValidationIcon = () => {
    if (!data.taskConfig) {
      return null;
    }
    if (data.taskConfig.valid === false) {
      return <SafeIcon src={alertYellowIcon} alt='alert-icon' width={24} height={24} />;
    }
    return null;
  };

  const getExecutionIcon = () => {
    if (!data.executionStatus) {
      return null;
    }
    switch (data.executionStatus) {
      case 'RUNNING':
        return <SafeIcon src={RunningIcon} alt='running' width={24} height={24} />;
      case 'COMPLETED':
      case 'SUCCESS':
        return <SafeIcon src={SuccessIcon} alt='success' width={24} height={24} />;
      case 'FAILED':
      case 'ERROR':
      case 'TIMEOUT':
        return <SafeIcon src={ErrorIcon} alt='error' width={24} height={24} />;
      case 'SKIPPED':
        return <SafeIcon src={SkipForwardIcon} alt='skipped' width={24} height={24} />;
      case 'CANCELED':
      case 'CANCELLED':
        return <SafeIcon src={ErrorIcon} alt='canceled' width={24} height={24} />;
      case 'PENDING':
      case 'QUEUED':
        return <SafeIcon src={timerSVG} alt='pending' width={24} height={24} />;
      default:
        return null;
    }
  };

  // Handle test task execution
  const handleTestTask = (_e: React.MouseEvent) => {
    if (onTestTask && data.taskConfig && accountId) {
      const taskType = data.taskConfig.type;
      const params = data.taskConfig.config || {};
      onTestTask(taskType, params);
    }
  };

  // Apply the disable/enable mutation. Used both directly (when no confirm
  // is needed) and from the confirm modal's "Disable" button.
  const applyToggleDisable = (currentlyDisabled: boolean) => {
    const currentNodes = getNodes();
    const currentEdges = getEdges();
    const result = currentlyDisabled ? enableTask(id, currentNodes, currentEdges) : disableTask(id, currentNodes, currentEdges);
    setNodes(() => result.nodes);
    setEdges(() => result.edges);
  };

  // Toggle disable/enable from the 3-dots menu. When disabling a task that
  // has direct dependents, prompt for confirmation first — same UX as the
  // sidebar Switch path.
  const handleToggleDisable = () => {
    const currentlyDisabled = data.taskConfig?.disabled === true;
    if (!currentlyDisabled) {
      const dependents = getEdges().filter((e) => e.source === id).length;
      if (dependents > 0) {
        setDisableConfirmCount(dependents);
        setDisableConfirmOpen(true);
        return;
      }
    }
    applyToggleDisable(currentlyDisabled);
  };

  // Open delete confirmation modal
  const handleDeleteClick = () => {
    setDeleteModalOpen(true);
  };

  // Handle confirmed deletion
  const handleConfirmDelete = () => {
    const nodeId = id;

    // Collect the task ids this action was known by so switch-case `next` / `default_next`
    // references get scrubbed — otherwise they leak into the exported workflow as orphans.
    const deletedTaskIds = new Set<string>();
    const configId = data?.taskConfig?.id;
    if (configId) deletedTaskIds.add(configId);
    deletedTaskIds.add(sanitizeTaskId(nodeId));

    // Remove the node + scrub stale switch references in the same set-state to keep state consistent.
    setNodes((nodes) =>
      cleanupSwitchReferencesAfterDelete(
        nodes.filter((node) => node.id !== nodeId),
        deletedTaskIds
      )
    );

    // Splice surrounding nodes back together when the deleted node sits in a linear/fan-in chain.
    setEdges((edges) => spliceEdgesOnNodeDelete(nodeId, edges));

    setDeleteModalOpen(false);
  };

  // Handle cancel deletion
  const handleCancelDelete = () => {
    setDeleteModalOpen(false);
  };

  // Handle node ID editing
  const handleEditId = () => {
    setIsEditingId(true);
    setEditedId(id);
  };

  const handleSaveId = () => {
    if (editedId.trim() && editedId !== id) {
      // Get current nodes synchronously to calculate final unique ID
      const currentNodes = getNodes();
      const existingIds = new Set(currentNodes.filter((n) => n.id !== id).map((n) => n.id));

      let finalId = editedId.trim();

      if (existingIds.has(finalId)) {
        // ID already exists, find a unique one
        let counter = 1;
        let uniqueId = `${finalId}-${counter}`;
        while (existingIds.has(uniqueId)) {
          counter++;
          uniqueId = `${finalId}-${counter}`;
        }
        finalId = uniqueId;
        setEditedId(finalId);
      }

      // Update nodes with the final unique ID
      setNodes((nodes) =>
        nodes.map((node) => {
          if (node.id === id) {
            const updatedNode = { ...node, id: finalId };
            // Update taskConfig.id to match the new node ID if this is an action node
            if (node.type === 'action' && node.data.taskConfig) {
              updatedNode.data = {
                ...node.data,
                taskConfig: {
                  ...node.data.taskConfig,
                  id: finalId,
                },
              };
            }
            return updatedNode;
          }
          return node;
        })
      );

      // Update edges to reference the same final unique ID
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
  };

  // Handle duplicate task
  const handleDuplicateTask = () => {
    const currentNodes = getNodes();
    const existingIds = new Set(currentNodes.map((n) => n.id));

    // Generate unique ID based on original
    let counter = 1;
    let uniqueId = `${id}-${counter}`;
    while (existingIds.has(uniqueId)) {
      counter++;
      uniqueId = `${id}-${counter}`;
    }

    // Find the current node to get its position
    const currentNode = currentNodes.find((n) => n.id === id);
    if (!currentNode) return;

    // Deep clone the task config and update its ID
    const clonedTaskConfig = data.taskConfig ? JSON.parse(JSON.stringify(data.taskConfig)) : null;
    if (clonedTaskConfig) {
      clonedTaskConfig.id = uniqueId;
    }

    // Create the duplicate node
    const newNode = {
      id: uniqueId,
      type: currentNode.type,
      position: {
        x: currentNode.position.x + 50,
        y: currentNode.position.y + 80,
      },
      data: {
        ...JSON.parse(JSON.stringify(data)),
        taskConfig: clonedTaskConfig,
        executionStatus: undefined,
      },
      selected: false,
    };

    setNodes((nodes) => [...nodes, newNode]);
  };

  // Get task icon element
  const getTaskIconElement = () => {
    const icon = getTaskIcon(data.taskConfig?.type);
    // Check if icon is an emoji (string without path characters) or image file
    if (typeof icon === 'string' && !icon.includes('/') && !icon.includes('.')) {
      // Render emoji as text
      return <span style={{ fontSize: '14px' }}>{icon}</span>;
    }
    // Define provider logos that should keep their brand colors
    const providerLogos = [newAwsLogo, ouAzure, ouGoogle, RabbitmqIcon, RedisLogoIcon, GithubIcon, GitLabIcon, ArgocdIcon, SlackIcon];

    // Check if this is a provider logo that should keep its colors
    const shouldKeepColors = providerLogos.includes(icon);

    // Render as Next.js Image for actual image files
    return (
      <SafeIcon
        src={icon}
        alt='task-icon'
        width={20}
        height={20}
        style={{
          filter: shouldKeepColors ? 'none' : 'brightness(0) saturate(100%) invert(1)', // Force icon white
          objectFit: 'contain',
        }}
      />
    );
  };

  // Get status badges
  const getStatusBadges = () => (
    <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
      {getExecutionIcon() && (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            padding: '6px',
            borderRadius: '12px',
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
            borderRadius: '12px',
            width: '28px',
            backgroundColor: 'white',
            padding: '4px',
            color: data.taskConfig?.valid === false ? '#dc2626' : '#16a34a',
          }}
        >
          {getValidationIcon()}
        </div>
      )}
    </div>
  );

  const isDisabled = data.taskConfig?.disabled === true;

  const getBorder = (): string => {
    if (data.isDeleted) return '2px dashed #999999';
    if (isDisabled) return '2px dashed #9ca3af';
    if (data.connectionRejected) return '3px solid #ef4444';
    if (data.taskConfig?.valid === false) return '2px solid #fbbf24';
    if (selected) return '2px solid #1D4ED8';
    return `1px solid ${colors.iconColor}`;
  };

  const getNodeStyle = (): React.CSSProperties => {
    if (data.isDeleted) return { opacity: 0.7 };
    if (isDisabled) return { opacity: 0.55 };
    return {};
  };

  return (
    <div style={{ position: 'relative' }}>
      {isDisabled && (
        <div
          data-testid='action-node-disabled-badge'
          style={{
            position: 'absolute',
            top: -10,
            left: 12,
            zIndex: 10,
            display: 'inline-flex',
            alignItems: 'center',
            gap: 4,
            padding: '2px 8px',
            borderRadius: 999,
            background: '#6b7280',
            color: 'white',
            fontSize: 10,
            fontWeight: 600,
            letterSpacing: 0.3,
            textTransform: 'uppercase',
            boxShadow: '0 1px 2px rgba(0,0,0,0.15)',
          }}
        >
          <PauseCircleOutlineIcon sx={{ fontSize: 12, color: 'white' }} />
          Disabled
        </div>
      )}
      <BaseNode
        selected={selected}
        border={getBorder()}
        background={data.isDeleted || isDisabled ? '#f9fafb' : 'white'}
        nodeStyle={getNodeStyle()}
        onDelete={handleDeleteClick}
        content={{
          icon: getTaskIconElement(),
          label: isEditingId ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: '4px', width: '100%' }}>
              <TextField
                id={'change-id'}
                value={editedId}
                onChange={(e) => setEditedId(e.target.value)}
                onKeyDown={(e) => {
                  e.stopPropagation();
                  if (e.key === 'Enter') {
                    handleSaveId();
                  } else if (e.key === 'Escape') {
                    handleCancelEditId();
                  }
                }}
                size='small'
                variant='outlined'
                sx={{
                  flex: 1,
                  '& .MuiOutlinedInput-root': {
                    fontSize: '14px',
                    fontWeight: 'bold',
                    height: '28px',
                    '& fieldset': {
                      borderColor: '#936AFF',
                    },
                    '&:hover fieldset': {
                      borderColor: '#936AFF',
                    },
                    '&.Mui-focused fieldset': {
                      borderColor: '#936AFF',
                    },
                  },
                }}
              />
              <button
                type='button'
                className='nodrag nopan'
                onClick={handleSaveId}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    handleSaveId();
                  }
                }}
                tabIndex={0}
                style={{
                  background: 'none',
                  cursor: 'pointer',
                  display: 'flex',
                  alignItems: 'center',
                  padding: '2px',
                  borderRadius: '4px',
                  backgroundColor: '#f0f9ff',
                  border: '1px solid #0ea5e9',
                }}
                title='Save ID'
              >
                <CheckIcon sx={{ fontSize: '16px', color: '#0ea5e9' }} />
              </button>
              <button
                type='button'
                className='nodrag nopan'
                onClick={handleCancelEditId}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    handleCancelEditId();
                  }
                }}
                tabIndex={0}
                style={{
                  background: 'none',
                  cursor: 'pointer',
                  display: 'flex',
                  alignItems: 'center',
                  padding: '2px',
                  borderRadius: '4px',
                  backgroundColor: '#fef2f2',
                  border: '1px solid #ef4444',
                }}
                title='Cancel'
              >
                <CloseIcon sx={{ fontSize: '16px', color: '#ef4444' }} />
              </button>
            </div>
          ) : (
            <Box sx={{ display: 'flex', flexDirection: 'column' }}>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                <Typography sx={{ fontSize: '14px', fontWeight: 'bold', color: data.isDeleted ? '#9ca3af' : colors.text.secondary }}>
                  {snakeToTitleCase(id)}
                </Typography>
                {data.isDeleted && (
                  <Box
                    sx={{
                      backgroundColor: colors.text.red,
                      borderRadius: '4px',
                      padding: '2px 8px',
                      fontSize: '12px',
                      fontWeight: 400,
                      color: colors.text.white,
                    }}
                  >
                    Deleted
                  </Box>
                )}
                {DEPRECATED_TASK_TYPES.has(data.taskConfig?.type) && (
                  <Box
                    sx={{
                      backgroundColor: '#FEF3C7',
                      color: '#92400E',
                      borderRadius: '999px',
                      padding: '2px 8px',
                      fontSize: '10px',
                      fontWeight: 600,
                    }}
                  >
                    Deprecated
                  </Box>
                )}
              </Box>
              <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, mt: -0.5 }}>{data.label}</Typography>
            </Box>
          ),
          description: data?.description || (data.isDeleted ? data.taskConfig?.type : ''),
          iconContainerStyle: {
            backgroundColor: data.isDeleted ? '#9ca3af' : '#6172F3',
            color: data.isDeleted ? '#6b7280' : '#3b82f6',
          },
          statusBadges: getStatusBadges(),
        }}
        additionalContent={
          <>
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
                borderTop: 'none',
                borderBottom: '4px solid rgb(142, 185, 255)',
                borderLeft: 'none',
                borderRight: 'none',
                top: '-18px',
                opacity: 1,
                transition: 'opacity 0.2s',
                cursor: 'crosshair',
              }}
            />
            {!showHalfEdgeAddButton && (
              <Handle
                type='source'
                position={Position.Bottom}
                id='action-output'
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
            )}
          </>
        }
        primaryButton={
          onTestTask && data.taskConfig && accountId && !TASKS_WITHOUT_INDIVIDUAL_RUN.has(data.taskConfig.type)
            ? {
                icon: <PlayArrowIcon sx={{ fontSize: '14px', color: '#16a34a', pointerEvents: 'none' }} />,
                onClick: handleTestTask,
                title: 'Manual run this task',
                hoverBackgroundColor: 'rgb(220, 255, 233)',
                hoverBorderColor: 'rgb(126, 218, 160)',
              }
            : undefined
        }
        menuItems={
          data.isDeleted
            ? []
            : [
                {
                  label: isDisabled ? 'Enable task' : 'Disable task',
                  icon: isDisabled ? (
                    <PlayCircleOutlineIcon sx={{ fontSize: 16, color: '#2563eb' }} />
                  ) : (
                    <PauseCircleOutlineIcon sx={{ fontSize: 16, color: '#6b7280' }} />
                  ),
                  onClick: handleToggleDisable,
                },
                {
                  label: 'Rename',
                  icon: <EditOutlinedIcon sx={{ fontSize: 16, color: '#6b7280' }} />,
                  onClick: handleEditId,
                },
                {
                  label: 'Duplicate',
                  icon: <ContentCopyOutlinedIcon sx={{ fontSize: 16, color: '#6b7280' }} />,
                  onClick: handleDuplicateTask,
                },
              ]
        }
        deleteButtonConfig={
          data.isDeleted
            ? { hidden: true }
            : {
                title: 'Delete action node',
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
            Delete Action?
          </Typography>

          <Typography
            variant='body1'
            sx={{
              color: '#374151',
              marginBottom: '8px',
              lineHeight: 1.6,
            }}
          >
            Are you sure you want to delete the action <strong>&quot;{data.label}&quot;</strong>?
          </Typography>
          <Box
            sx={{
              display: 'flex',
              gap: '12px',
              justifyContent: 'flex-end',
            }}
          >
            <CustomButton text={'Cancel'} variant='tertiary' onClick={handleCancelDelete} />

            <CustomButton text={'Delete Action'} variant='primary' onClick={handleConfirmDelete} />
          </Box>
        </Box>
      </Modal>

      {/* Disable Confirmation Modal — fires from the 3-dots menu when there are direct dependents.
          Mirrors the sidebar Switch path so both entry points behave identically. */}
      <Modal
        open={disableConfirmOpen}
        onClose={() => setDisableConfirmOpen(false)}
        sx={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}
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
          <Typography variant='h6' sx={{ fontWeight: 600, color: '#111827', marginBottom: '12px' }}>
            Disable this task?
          </Typography>
          <Typography variant='body1' sx={{ color: '#374151', marginBottom: '16px', lineHeight: 1.6 }}>
            {disableConfirmCount === 1
              ? '1 task currently depends on this one. Disabling will reroute the workflow to skip this task — its predecessor will connect directly to its successor.'
              : `${disableConfirmCount} tasks currently depend on this one. Disabling will reroute the workflow to skip this task — its predecessors will connect directly to its successors.`}
          </Typography>
          <Box sx={{ display: 'flex', gap: '12px', justifyContent: 'flex-end' }}>
            <CustomButton text={'Cancel'} variant='tertiary' onClick={() => setDisableConfirmOpen(false)} />
            <CustomButton
              text={'Disable'}
              variant='primary'
              onClick={() => {
                setDisableConfirmOpen(false);
                applyToggleDisable(false);
              }}
            />
          </Box>
        </Box>
      </Modal>

      {/* Half-edge add button - rendered outside the node card. Owns the source Handle while unconnected. */}
      {showHalfEdgeAddButton && (
        <div style={{ position: 'absolute', left: '50%', transform: 'translateX(-50%)', top: '100%', zIndex: 10 }}>
          <HalfEdgeAddButton onClick={() => onAddFromHandle(id, 'action-output')} sourceHandleId='action-output' isConnectable={isConnectable} />
        </div>
      )}
    </div>
  );
};

export default ActionNode;

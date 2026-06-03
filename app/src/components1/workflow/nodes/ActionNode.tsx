import { Handle, Position, useReactFlow, useStore } from 'reactflow';
import PlayArrowIcon from '@mui/icons-material/PlayArrow';
import PauseCircleOutlineIcon from '@mui/icons-material/PauseCircleOutline';
import PlayCircleOutlineIcon from '@mui/icons-material/PlayCircleOutline';
import EditOutlinedIcon from '@mui/icons-material/EditOutlined';
import ContentCopyOutlinedIcon from '@mui/icons-material/ContentCopyOutlined';
import CheckIcon from '@mui/icons-material/Check';
import CloseIcon from '@mui/icons-material/Close';
import { useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Input } from '@components1/ds/Input';
import { Button } from '@components1/ds/Button';
import { Modal } from '@components1/ds/Modal';
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
import { sanitizeTaskId, validateTaskId } from '@components1/workflow/utils/taskUtils';
import { cleanupSwitchReferencesAfterDelete, renameTaskReferencesInNodes } from '@components1/workflow/utils/templateUtils';
import { spliceEdgesOnNodeDelete } from '@components1/workflow/utils/spliceNode';
import { disableTask, enableTask } from '@components1/workflow/utils/toggleTaskDisable';
import { findTaskOutputReferences, type TaskOutputReference } from '@components1/workflow/utils/workflowValidation';

import { colors } from 'src/utils/colors';
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
  const [outputReferences, setOutputReferences] = useState<TaskOutputReference[]>([]);
  const [isEditingId, setIsEditingId] = useState(false);
  const [editedId, setEditedId] = useState(id);
  const [idError, setIdError] = useState<string | null>(null);
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

  // Resolve the task ID to scan for. taskConfig.id is the canonical workflow
  // task id; fall back to the sanitized node id for new/unsaved nodes.
  const getScanTargetTaskId = () => data?.taskConfig?.id || sanitizeTaskId(id);

  // Toggle disable/enable from the 3-dots menu. Prompt for confirmation only
  // when other tasks reference this task's output via Tasks['…'] templates;
  // those references will fail at runtime once the task stops producing output.
  const handleToggleDisable = () => {
    const currentlyDisabled = data.taskConfig?.disabled === true;
    if (!currentlyDisabled) {
      const refs = findTaskOutputReferences(getScanTargetTaskId(), getNodes());
      if (refs.length > 0) {
        setOutputReferences(refs);
        setDisableConfirmOpen(true);
        return;
      }
    }
    applyToggleDisable(currentlyDisabled);
  };

  // Open delete confirmation modal. Scan template references first so the
  // modal can name the consumers that will break if this task is removed.
  const handleDeleteClick = () => {
    setOutputReferences(findTaskOutputReferences(getScanTargetTaskId(), getNodes()));
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
    setIdError(null);
  };

  const handleSaveId = () => {
    const trimmed = editedId.trim();
    // Validate against backend ValidateTaskID rules (regex + length) so the user
    // sees the constraint at rename time instead of getting a silent sanitize-
    // on-export rewrite that drifts from any references they typed by hand.
    const error = validateTaskId(trimmed);
    if (error) {
      setIdError(error);
      return;
    }
    // validateTaskId already matches the backend regex exactly, so the saved
    // id flows through verbatim — no sanitize step, no silent rewrite of
    // legal-but-unusual ids like `3abc` or `-abc`.
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
      }
      setEditedId(finalId);

      // Canonical old id used by other tasks' templates: taskConfig.id when set,
      // otherwise the (sanitized) node id.
      const oldCanonicalId = data?.taskConfig?.id || sanitizeTaskId(id);

      setNodes((nodes) => {
        const renamed = nodes.map((node) => {
          if (node.id !== id) return node;
          const updatedNode = { ...node, id: finalId };
          if ((node.type === 'action' || node.type === 'switch') && node.data?.taskConfig) {
            updatedNode.data = {
              ...node.data,
              taskConfig: {
                ...node.data.taskConfig,
                id: finalId,
              },
            };
          }
          return updatedNode;
        });
        return renameTaskReferencesInNodes(renamed, oldCanonicalId, finalId, finalId);
      });

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
      return <span style={{ fontSize: 'var(--ds-text-body-lg)' }}>{icon}</span>;
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
            padding: 'var(--ds-space-1) var(--ds-space-2)',
            borderRadius: 999,
            background: 'var(--ds-gray-600)',
            color: 'white',
            fontSize: 10,
            fontWeight: 'var(--ds-font-weight-semibold)',
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
                    if (e.key === 'Enter') {
                      handleSaveId();
                    } else if (e.key === 'Escape') {
                      handleCancelEditId();
                    }
                  }}
                  size='sm'
                  error={idError || undefined}
                />
              </Box>
              <button
                id='wf-node-action-save-id-btn'
                type='button'
                className='nodrag nopan'
                onClick={handleSaveId}
                disabled={!!idError}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    handleSaveId();
                  }
                }}
                tabIndex={0}
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
                id='wf-node-action-cancel-id-btn'
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
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                <Typography
                  sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'bold', color: data.isDeleted ? '#9ca3af' : colors.text.secondary }}
                >
                  {id}
                </Typography>
                {data.isDeleted && (
                  <Box
                    sx={{
                      backgroundColor: colors.text.red,
                      borderRadius: 'var(--ds-radius-sm)',
                      padding: 'var(--ds-space-1) var(--ds-space-2)',
                      fontSize: 'var(--ds-text-small)',
                      fontWeight: 'var(--ds-font-weight-regular)',
                      color: colors.text.white,
                    }}
                  >
                    Deleted
                  </Box>
                )}
                {DEPRECATED_TASK_TYPES.has(data.taskConfig?.type) && (
                  <Box
                    sx={{
                      backgroundColor: 'var(--ds-yellow-200)',
                      color: 'var(--ds-amber-700)',
                      borderRadius: 'var(--ds-radius-pill)',
                      padding: 'var(--ds-space-1) var(--ds-space-2)',
                      fontSize: 'var(--ds-text-caption)',
                      fontWeight: 'var(--ds-font-weight-semibold)',
                    }}
                  >
                    Deprecated
                  </Box>
                )}
              </Box>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary, mt: -0.5 }}>{data.label}</Typography>
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
                icon: <PlayArrowIcon sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-green-500)', pointerEvents: 'none' }} />,
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
                    <PlayCircleOutlineIcon sx={{ fontSize: 16, color: 'var(--ds-blue-500)' }} />
                  ) : (
                    <PauseCircleOutlineIcon sx={{ fontSize: 16, color: 'var(--ds-gray-600)' }} />
                  ),
                  onClick: handleToggleDisable,
                },
                {
                  label: 'Rename',
                  icon: <EditOutlinedIcon sx={{ fontSize: 16, color: 'var(--ds-gray-600)' }} />,
                  onClick: handleEditId,
                },
                {
                  label: 'Duplicate',
                  icon: <ContentCopyOutlinedIcon sx={{ fontSize: 16, color: 'var(--ds-gray-600)' }} />,
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
        handleClose={handleCancelDelete}
        title='Delete Action?'
        width='sm'
        actionButtons={
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)', justifyContent: 'flex-end', padding: 'var(--ds-space-3) var(--ds-space-5)' }}>
            <Button id='wf-node-action-delete-cancel-btn' tone='ghost' onClick={handleCancelDelete}>
              Cancel
            </Button>
            <Button id='wf-node-action-delete-confirm-btn' tone='danger' onClick={handleConfirmDelete}>
              Delete Action
            </Button>
          </Box>
        }
      >
        <Box sx={{ padding: 'var(--ds-space-4) 0' }}>
          <Typography variant='body1' sx={{ color: 'var(--ds-brand-500)', marginBottom: 'var(--ds-space-2)', lineHeight: 1.6 }}>
            Are you sure you want to delete the action <strong>&quot;{data.label}&quot;</strong>?
          </Typography>

          {outputReferences.length > 0 && (
            <Box data-testid='delete-output-refs-warning' sx={{ marginTop: 'var(--ds-space-3)', marginBottom: 'var(--ds-space-3)' }}>
              <Typography
                variant='body2'
                sx={{ color: 'var(--ds-brand-500)', fontWeight: 'var(--ds-font-weight-semibold)', marginBottom: 'var(--ds-space-1)' }}
              >
                {outputReferences.length === 1
                  ? '1 task references this task’s output:'
                  : `${outputReferences.length} tasks reference this task’s output:`}
              </Typography>
              <Box component='ul' sx={{ margin: 0, paddingLeft: 'var(--ds-space-4)', color: 'var(--ds-brand-500)' }}>
                {outputReferences.map((ref) => (
                  <li key={ref.nodeId}>
                    <Typography variant='body2' component='span' sx={{ color: 'var(--ds-brand-500)' }}>
                      <strong>{ref.label}</strong>
                      {ref.label !== ref.taskId && <span style={{ opacity: 0.7 }}> ({ref.taskId})</span>}
                    </Typography>
                  </li>
                ))}
              </Box>
              <Typography variant='caption' sx={{ display: 'block', marginTop: 'var(--ds-space-1)', color: 'var(--ds-gray-600)' }}>
                Those tasks will fail at runtime until the references are updated.
              </Typography>
            </Box>
          )}
        </Box>
      </Modal>

      {/* Disable Confirmation Modal — fires from the 3-dots menu when there are direct dependents.
          Mirrors the sidebar Switch path so both entry points behave identically. */}
      <Modal
        open={disableConfirmOpen}
        handleClose={() => setDisableConfirmOpen(false)}
        title='Disable this task?'
        width='sm'
        actionButtons={
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)', justifyContent: 'flex-end', padding: 'var(--ds-space-3) var(--ds-space-5)' }}>
            <Button id='wf-node-action-disable-cancel-btn' tone='ghost' onClick={() => setDisableConfirmOpen(false)}>
              Cancel
            </Button>
            <Button
              id='wf-node-action-disable-confirm-btn'
              tone='primary'
              onClick={() => {
                setDisableConfirmOpen(false);
                applyToggleDisable(false);
              }}
            >
              Disable
            </Button>
          </Box>
        }
      >
        <Box sx={{ padding: 'var(--ds-space-4) 0' }}>
          {outputReferences.length > 0 && (
            <Box data-testid='disable-output-refs-warning' sx={{ marginBottom: 'var(--ds-space-4)' }}>
              <Typography
                variant='body2'
                sx={{ color: 'var(--ds-brand-500)', fontWeight: 'var(--ds-font-weight-semibold)', marginBottom: 'var(--ds-space-1)' }}
              >
                {outputReferences.length === 1
                  ? '1 task references this task’s output:'
                  : `${outputReferences.length} tasks reference this task’s output:`}
              </Typography>
              <Box component='ul' sx={{ margin: 0, paddingLeft: 'var(--ds-space-4)', color: 'var(--ds-brand-500)' }}>
                {outputReferences.map((ref) => (
                  <li key={ref.nodeId}>
                    <Typography variant='body2' component='span' sx={{ color: 'var(--ds-brand-500)' }}>
                      <strong>{ref.label}</strong>
                      {ref.label !== ref.taskId && <span style={{ opacity: 0.7 }}> ({ref.taskId})</span>}
                    </Typography>
                  </li>
                ))}
              </Box>
              <Typography variant='caption' sx={{ display: 'block', marginTop: 'var(--ds-space-1)', color: 'var(--ds-gray-600)' }}>
                Disabled tasks don’t produce output, so those references will fail at runtime.
              </Typography>
            </Box>
          )}
        </Box>
      </Modal>

      {/* Half-edge add button - rendered outside the node card. Owns the source Handle while unconnected. */}
      {showHalfEdgeAddButton && (
        <div style={{ position: 'absolute', left: '50%', transform: 'translateX(-50%)', top: '100%', zIndex: 10 }}>
          <HalfEdgeAddButton
            id='wf-node-action-add-edge-btn'
            onClick={() => onAddFromHandle(id, 'action-output')}
            sourceHandleId='action-output'
            isConnectable={isConnectable}
          />
        </div>
      )}
    </div>
  );
};

export default ActionNode;

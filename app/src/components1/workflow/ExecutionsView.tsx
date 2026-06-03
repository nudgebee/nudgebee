import React, { useState, useEffect, useMemo, useCallback, useRef } from 'react';
import { Alert, Box, Typography, Select, MenuItem, FormControl } from '@mui/material';
import { Button } from '@components1/ds/Button';
import {
  ContentCopy,
  Refresh,
  ExpandMore,
  ExpandLess,
  Input as InputIcon,
  Output as OutputIcon,
  DragIndicator,
  AccessTime,
  Schedule,
  PlaylistPlay,
  Replay,
  StopCircleOutlined,
} from '@mui/icons-material';
import { ReactFlow, Background, Controls, MiniMap, PanOnScrollMode, type Node, type Edge, type ReactFlowInstance } from 'reactflow';
import 'reactflow/dist/style.css';
import { colors } from 'src/utils/colors';
import apiWorkflow from '@api1/workflow';
import { hasWriteAccess } from '@lib/auth';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import Datetime from '@components1/common/format/Datetime';
import FieldRenderer from '@components1/common/FieldRenderer';
import AccordionSmall from '@components1/common/AccordionSmall';
import { FormCard } from '@components1/common/NewReusabeFormComponents';
import { snackbar } from '@common/snackbarService';
import type { WorkflowExecutionTaskResponse } from '@api1/workflow/types';
import CallWorkflowChildren from './components/CallWorkflowChildren';
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
  ArgocdIcon,
  K8sIcon,
  newAwsLogo,
  ouAzure,
  ouGoogle,
} from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { ActionNode, TriggerNode, SwitchNode } from './nodes';
import DeletableEdge from './components/DeletableEdge';
import { convertWorkflowToReactFlow } from './utils/workflowLayoutEngine';
import ConditionalEdge from './components/ConditionalEdge';
import TriggerDetailsPanel from './components/TriggerDetailsPanel';
import ExecutionStatusBar, { type PendingApproval } from './components/ExecutionStatusBar';
import { findExecutionTaskForNode, getSwitchAncestorChain, getSwitchChildNodeIds } from './utils/templateUtils';

// Function to get appropriate icon based on task type (matches ActionNode.tsx exactly)
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

    // Message queues
    'mq.rabbitmqadmin.cli': RabbitmqIcon,

    // Databases
    'dbms.redis.cli': RedisLogoIcon,

    // Source control
    'scm.github.cli': GithubIcon,

    // CI/CD
    'cicd.argocd.cli': ArgocdIcon,
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
    mq: workflowMessagingIcon?.default || workflowMessagingIcon,
    scm: LLMFunctionIcon,
  };

  return categoryMap[prefix] || '📦'; // Default for 'Other' operations (matches nodeCategories.ts line 48)
};

// Utility to generate a unique, stable key for each execution task.
// Consolidates task identification logic into a single function to avoid
// inconsistent keying across taskMap, ghost nodes, selection, and auto-select.
const getTaskKey = (task: WorkflowExecutionTaskResponse, index: number): string => {
  return task.id || `${task.type || 'task'}-${index}`;
};

interface ExecutionData {
  id: string;
  status: 'COMPLETE' | 'COMPLETE_WITH_ERROR' | 'SCHEDULED' | 'IN_PROGRESS' | 'FAILED' | 'SKIPPED' | 'RUNNING' | 'COMPLETED';
  close_time?: string;
  start_time?: string;
  parent_workflow_id?: string;
  trigger_type?: string;
  triggered_by?: string;
  workflow_id?: string;
  error?: string;
}

interface ExecutionsViewProps {
  workflowId: string;
  accountId: string;
  executions: ExecutionData[];
  loading: boolean;
  onRefresh: (reset?: boolean) => void;
  taskDefinitions: any[];
  // Canvas props
  editorNodes: Node[];
  editorEdges: Edge[];
  // Optional pagination props
  hasMore?: boolean;
  hasPrevious?: boolean;
  loadingMore?: boolean;
  onNext?: () => void;
  onPrevious?: () => void;
  // Optional filter props
  selectedStatus?: string;
  onStatusChange?: (status: string) => void;
  // When the parent (e.g. Run-live flow) wants this view to auto-select a
  // specific execution as soon as the next refresh lands. Same plumbing as
  // the Retry flow: the value is pushed into pendingSelectionRef and the
  // existing execution-list effect picks it up on the next refresh.
  pendingExecutionId?: string;
  onPendingExecutionConsumed?: () => void;
}

// Node types for the execution canvas (read-only, same as editor)
const executionNodeTypes = {
  trigger: TriggerNode,
  action: ActionNode,
  switch: SwitchNode,
};

const executionEdgeTypes = {
  smoothstep: DeletableEdge,
  conditional: ConditionalEdge,
};

const ExecutionsView: React.FC<ExecutionsViewProps> = ({
  workflowId,
  accountId,
  executions,
  loading,
  onRefresh,
  taskDefinitions,
  editorNodes,
  editorEdges,
  hasMore = false,
  hasPrevious = false,
  loadingMore = false,
  onNext,
  onPrevious,
  selectedStatus = 'All',
  onStatusChange,
  pendingExecutionId,
  onPendingExecutionConsumed,
}) => {
  const [selectedExecution, setSelectedExecution] = useState<ExecutionData | null>(null);
  const [selectedTask, setSelectedTask] = useState<string | null>(null);
  const [executionTasks, setExecutionTasks] = useState<WorkflowExecutionTaskResponse[]>([]);
  // Tracks which execution's tasks are currently in `executionTasks`. Used to gate
  // rendering so we never show one execution's tasks on another's canvas during
  // switch/refresh (prevents the appear/disappear bug without eagerly clearing).
  const [loadedExecutionId, setLoadedExecutionId] = useState<string | null>(null);
  const [tasksLoading, setTasksLoading] = useState(false);
  const [retriggerLoading, setRetriggerLoading] = useState(false);
  const [cancelLoading, setCancelLoading] = useState(false);
  const [approvalLoading, setApprovalLoading] = useState<string | null>(null);
  const pendingSelectionRef = useRef<string | null>(null);
  const [highlightedExecutionId, setHighlightedExecutionId] = useState<string | null>(null);
  const [inlineOutputViewMode, setInlineOutputViewMode] = useState<'json' | 'formatted'>('formatted');
  const [inlineInputViewMode, setInlineInputViewMode] = useState<'json' | 'formatted'>('formatted');
  const [executionData, setExecutionData] = useState<any>(null);
  const [logsExpanded, setLogsExpanded] = useState(false);
  const reactFlowInstanceRef = useRef<ReactFlowInstance | null>(null);
  const canvasContainerRef = useRef<HTMLDivElement | null>(null);
  const [rightPanelWidth, setRightPanelWidth] = useState(700);
  const isResizingRef = useRef(false);
  // Mirrors selectedExecution.id so async fetches can detect if the user switched
  // executions mid-flight and drop the stale response.
  const selectedExecutionIdRef = useRef<string | null>(null);
  const isMountedRef = useRef(true);

  useEffect(() => {
    selectedExecutionIdRef.current = selectedExecution?.id || null;
  }, [selectedExecution?.id]);

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  const RIGHT_PANEL_MIN_WIDTH = 400;
  const RIGHT_PANEL_MAX_WIDTH = 900;

  const handleResizeMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      isResizingRef.current = true;
      const startX = e.clientX;
      const startWidth = rightPanelWidth;

      const onMouseMove = (moveEvent: MouseEvent) => {
        if (!isResizingRef.current) return;
        const delta = startX - moveEvent.clientX;
        const newWidth = Math.min(RIGHT_PANEL_MAX_WIDTH, Math.max(RIGHT_PANEL_MIN_WIDTH, startWidth + delta));
        setRightPanelWidth(newWidth);
      };

      const onMouseUp = () => {
        isResizingRef.current = false;
        document.removeEventListener('mousemove', onMouseMove);
        document.removeEventListener('mouseup', onMouseUp);
        document.body.style.cursor = '';
        document.body.style.userSelect = '';
        // Auto-adjust ReactFlow viewport after resize completes
        if (reactFlowInstanceRef.current) {
          setTimeout(() => {
            reactFlowInstanceRef.current?.fitView({
              padding: 0.15,
              maxZoom: 0.9,
              minZoom: 0.75,
              duration: 300,
            });
          }, 50);
        }
      };

      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
      document.addEventListener('mousemove', onMouseMove);
      document.addEventListener('mouseup', onMouseUp);
    },
    [rightPanelWidth]
  );

  // Helper to check if execution is in a completed state (not running)
  const isExecutionCompleted = (status: string) => {
    const completedStatuses = ['COMPLETE', 'COMPLETED', 'COMPLETE_WITH_ERROR', 'FAILED', 'TERMINATED', 'TIMED_OUT', 'CANCELED', 'CONTINUED_AS_NEW'];
    return completedStatuses.includes(status.toUpperCase());
  };

  // When the parent passes a new pendingExecutionId, queue it for the next
  // refresh. The existing executions-list effect at line ~275 already drains
  // pendingSelectionRef for the Retry flow; we just feed it.
  useEffect(() => {
    if (pendingExecutionId) {
      pendingSelectionRef.current = pendingExecutionId;
    }
  }, [pendingExecutionId]);

  // Track previous execution list identity to detect actual data refreshes (not just reference changes)
  const prevExecutionIdsRef = useRef<string>('');

  // Auto-select first execution on mount or when executions change
  useEffect(() => {
    prevExecutionIdsRef.current = executions.map((e) => e.id).join(',');

    if (executions.length > 0) {
      // Check if we have a pending execution to select (from retry or Run live)
      if (pendingSelectionRef.current) {
        const targetExecution = executions.find((exec) => exec.id === pendingSelectionRef.current);
        if (targetExecution) {
          setSelectedExecution(targetExecution);
          setHighlightedExecutionId(targetExecution.id);
          setSelectedTask(null);
          pendingSelectionRef.current = null;
          onPendingExecutionConsumed?.();
          return;
        }
        // Not found (filters/pagination) - clear and fall through to default
        pendingSelectionRef.current = null;
        onPendingExecutionConsumed?.();
      }

      // Default: select first if none selected or current not in list
      if (!selectedExecution || !executions.find((exec) => exec.id === selectedExecution.id)) {
        setSelectedExecution(executions[0]);
        setSelectedTask(null);
      }
    } else {
      setSelectedExecution(null);
      setSelectedTask(null);
      setExecutionTasks([]);
      setLoadedExecutionId(null);
      pendingSelectionRef.current = null;
    }
  }, [executions]);

  // Clear highlight animation after 2.5 seconds
  useEffect(() => {
    if (highlightedExecutionId) {
      const timer = setTimeout(() => {
        setHighlightedExecutionId(null);
      }, 2500);
      return () => clearTimeout(timer);
    }
  }, [highlightedExecutionId]);

  // Fetch tasks when execution is selected
  useEffect(() => {
    if (selectedExecution && workflowId && accountId) {
      fetchExecutionTasks(selectedExecution.id);
    }
  }, [selectedExecution?.id, workflowId, accountId]);

  // Poll execution list when any execution is in a running state
  const hasRunningExecution = executions.some((e) => !isExecutionCompleted(e.status));
  const listPollingRef = useRef<ReturnType<typeof setInterval> | null>(null);
  useEffect(() => {
    if (listPollingRef.current) {
      clearInterval(listPollingRef.current);
      listPollingRef.current = null;
    }

    if (!hasRunningExecution) return;

    listPollingRef.current = setInterval(() => {
      onRefresh(false);
    }, 4000);

    return () => {
      if (listPollingRef.current) {
        clearInterval(listPollingRef.current);
        listPollingRef.current = null;
      }
    };
  }, [hasRunningExecution]);

  // Poll execution tasks when the selected execution is running
  const taskPollingRef = useRef<ReturnType<typeof setInterval> | null>(null);
  useEffect(() => {
    if (taskPollingRef.current) {
      clearInterval(taskPollingRef.current);
      taskPollingRef.current = null;
    }

    if (!selectedExecution || !workflowId || !accountId) return;
    if (isExecutionCompleted(selectedExecution.status)) return;

    taskPollingRef.current = setInterval(() => {
      fetchExecutionTasks(selectedExecution.id, true);
    }, 4000);

    return () => {
      if (taskPollingRef.current) {
        clearInterval(taskPollingRef.current);
        taskPollingRef.current = null;
      }
    };
  }, [selectedExecution?.id, selectedExecution?.status]);

  const fetchExecutionTasks = async (executionId: string, isPolling = false) => {
    try {
      if (!isPolling) setTasksLoading(true);

      const response: any = await apiWorkflow.getWorkflowExecution(accountId, workflowId, executionId);

      // Drop stale responses: the user switched execution (or unmounted) mid-flight.
      // Rendering is gated by `loadedExecutionId` so we don't need to clear state.
      if (!isMountedRef.current || selectedExecutionIdRef.current !== executionId) return;

      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        console.error('Failed to fetch execution tasks:', errorMessage);
        return;
      }

      const execution = response.data?.workflow_get_execution;
      const tasks = execution?.tasks || [];
      setExecutionData(execution);
      setExecutionTasks(tasks);
      setLoadedExecutionId(executionId);

      // Update the selected execution's status from the API response
      if (execution?.status) {
        setSelectedExecution((prev) =>
          prev && prev.id === executionId ? { ...prev, status: execution.status, close_time: execution.close_time } : prev
        );
      }
    } catch (error) {
      console.error('Error fetching execution tasks:', error);
    } finally {
      if (!isPolling && isMountedRef.current) setTasksLoading(false);
    }
  };

  // Utility functions
  const getDuration = (startTime: string, endTime?: string) => {
    if (!startTime) {
      return 'N/A';
    }
    const start = new Date(startTime.endsWith('Z') ? startTime : startTime + 'Z');
    const end = endTime ? new Date(endTime.endsWith('Z') ? endTime : endTime + 'Z') : new Date();
    const diffMs = end.getTime() - start.getTime();

    if (diffMs < 1000) {
      return `${diffMs}ms`;
    } else if (diffMs < 60000) {
      return `${(diffMs / 1000).toFixed(1)}s`;
    }
    return `${(diffMs / 60000).toFixed(1)}m`;
  };

  const getStatusColor = (status: string) => {
    switch (status.toUpperCase()) {
      case 'COMPLETED':
      case 'COMPLETE':
        return colors.success;
      case 'COMPLETE_WITH_ERROR':
      case 'CONTINUED_AS_NEW':
        return colors.yellow;
      case 'FAILED':
        return colors.error;
      case 'TERMINATED':
        return colors.highest;
      case 'TIMED_OUT':
        return colors.medium;
      case 'RUNNING':
      case 'IN_PROGRESS':
      case 'SCHEDULED':
        return colors.primary;
      case 'CANCELED':
      case 'CANCELLED':
        return colors.tertiary;
      case 'SKIPPED':
        return colors.text.secondaryDark;
      case 'UNSPECIFIED':
      default:
        return colors.border.secondary;
    }
  };

  const copyToClipboard = (text: string, label: string) => {
    navigator.clipboard.writeText(text).then(() => {
      snackbar.success(`${label} copied to clipboard`);
    });
  };

  const filteredExecutions = executions;

  const handleRefresh = () => {
    pendingSelectionRef.current = null;
    setSelectedTask(null);
    onRefresh();
  };

  const handleRetrigger = async () => {
    if (!selectedExecution || !workflowId || !accountId) {
      return;
    }

    try {
      setRetriggerLoading(true);
      const response: any = await apiWorkflow.retriggerExecution({
        account_id: accountId,
        workflow_id: workflowId,
        execution_id: selectedExecution.id,
      });

      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(`Failed to retry execution: ${errorMessage}`);
        return;
      }

      const newExecutionId = response.data?.workflow_replay_execution?.execution_id;
      if (newExecutionId) {
        snackbar.success(`Execution restarted successfully`);
        // Store the new execution ID for auto-selection after refresh
        pendingSelectionRef.current = newExecutionId;
        // Refresh the executions list to show the new execution
        onRefresh();
      } else {
        snackbar.error('Failed to retry execution');
      }
    } catch (error) {
      console.error('Error retriggering execution:', error);
      snackbar.error('Failed to retry execution');
    } finally {
      setRetriggerLoading(false);
    }
  };

  // Helper to check if execution can be cancelled (still in progress)
  const isExecutionCancellable = (status: string) => {
    const cancellableStatuses = ['RUNNING', 'IN_PROGRESS', 'SCHEDULED', 'QUEUED'];
    return cancellableStatuses.includes(status.toUpperCase());
  };

  const handleCancel = async () => {
    if (!selectedExecution || !workflowId || !accountId) return;
    try {
      setCancelLoading(true);
      const response: any = await apiWorkflow.cancelExecution({
        account_id: accountId,
        id: workflowId,
        execution_id: selectedExecution.id,
      });
      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(`Failed to cancel execution: ${errorMessage}`);
        return;
      }
      const cancelMsg = response?.data?.workflow_cancel_execution?.message;
      if (cancelMsg?.toLowerCase().includes('workflow execution canceled successfully')) {
        snackbar.success(cancelMsg);
      } else {
        snackbar.error(cancelMsg || 'Failed to cancel execution');
      }
      onRefresh();
    } catch {
      snackbar.error('Failed to cancel execution');
    } finally {
      setCancelLoading(false);
    }
  };

  const handleCompleteApproval = async (taskId: string, status: string, comments?: string) => {
    if (!selectedExecution || !workflowId || !accountId || !taskId) return;
    const loadingKey = `${taskId}:${status}`;
    try {
      setApprovalLoading(loadingKey);
      const response: any = await apiWorkflow.completeApproval({
        account_id: accountId,
        workflow_id: workflowId,
        execution_id: selectedExecution.id,
        task_id: taskId,
        status,
        comments,
      });
      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(`Failed to record approval: ${errorMessage}`);
        return;
      }
      snackbar.success(`Approval recorded as "${status}"`);
      onRefresh();
    } catch (error) {
      console.error('Error completing approval:', error);
      snackbar.error('Failed to record approval');
    } finally {
      setApprovalLoading(null);
    }
  };

  // Helper function to map execution status
  const mapExecutionStatus = (status: string | undefined) => {
    const statusUpper = status?.toUpperCase() || '';
    if (statusUpper === 'COMPLETED' || statusUpper === 'COMPLETE') {
      return 'COMPLETED';
    } else if (statusUpper === 'FAILED' || statusUpper === 'ERROR' || statusUpper === 'TIMED_OUT') {
      return 'FAILED';
    } else if (statusUpper === 'RUNNING' || statusUpper === 'IN_PROGRESS') {
      return 'RUNNING';
    } else if (statusUpper === 'SKIPPED') {
      return 'SKIPPED';
    } else if (statusUpper === 'SCHEDULED' || statusUpper === 'QUEUED') {
      return 'PENDING';
    } else if (statusUpper === 'CANCELED' || statusUpper === 'CANCELLED') {
      return 'CANCELED';
    }
    return statusUpper || undefined;
  };

  // Only use tasks when they belong to the currently selected execution.
  // Prevents stale tasks of a previous execution from overlaying the canvas
  // while a new execution's tasks are still loading.
  const effectiveTasks = useMemo(
    () => (loadedExecutionId && loadedExecutionId === selectedExecution?.id ? executionTasks : []),
    [loadedExecutionId, selectedExecution?.id, executionTasks]
  );

  // Render against the execution's own definition whenever the backend
  // returned one — never the parent's draft. `version` = exact published
  // snapshot, `draft_run` = exact draft that ran via "Run current" (recovered
  // from Temporal history), `legacy_live_snapshot` = best-effort for pre-V741
  // executions (the live snapshot at backfill time). Only fall back to
  // editorNodes when even the live snapshot is unavailable (`live_fallback`).
  const executionDefinitionGraph = useMemo<{ nodes: Node[]; edges: Edge[] }>(() => {
    const usableSnapshot =
      executionData?.definition &&
      (executionData.definition_source === 'version' ||
        executionData.definition_source === 'draft_run' ||
        executionData.definition_source === 'legacy_live_snapshot');
    if (usableSnapshot) {
      try {
        const { nodes, edges } = convertWorkflowToReactFlow(executionData.definition, {}, taskDefinitions);
        return { nodes, edges };
      } catch (err) {
        console.error('Failed to convert execution definition to ReactFlow:', err);
      }
    }
    return { nodes: editorNodes, edges: editorEdges };
  }, [executionData?.definition, executionData?.definition_source, editorNodes, editorEdges, taskDefinitions]);

  const overlayBaseNodes = executionDefinitionGraph.nodes;
  const overlayBaseEdges = executionDefinitionGraph.edges;

  // When the selected execution changes, re-fit the viewport so the (possibly
  // historical) definition layout is visible. The instance ref already exists
  // for the editor canvas. fitView with a small delay lets ReactFlow finish
  // measuring the newly-injected nodes.
  useEffect(() => {
    if (!selectedExecution?.id || !reactFlowInstanceRef.current) return;
    const tid = setTimeout(() => {
      reactFlowInstanceRef.current?.fitView({ padding: 0.15, maxZoom: 0.9, minZoom: 0.5, duration: 250 });
    }, 80);
    return () => clearTimeout(tid);
  }, [selectedExecution?.id, executionDefinitionGraph.nodes.length]);

  const executionOverlayNodes: Node[] = useMemo(() => {
    const taskMap = new Map<string, WorkflowExecutionTaskResponse>();
    effectiveTasks.forEach((task) => {
      if (task.id) {
        // Primary: use task.id (unique per task, matches editor node IDs)
        taskMap.set(task.id, task);
      } else if (task.type && !taskMap.has(task.type)) {
        // Fallback: use task.type only for the first task with this type
        // to avoid overwriting when multiple tasks share the same type
        taskMap.set(task.type, task);
      }
    });

    // If no base nodes but we have execution tasks, create ghost nodes for all tasks
    if (!overlayBaseNodes || overlayBaseNodes.length === 0) {
      if (effectiveTasks.length === 0) return [];

      // Create ghost nodes for all execution tasks when workflow is empty
      return effectiveTasks.map((task, index) => {
        const taskId = getTaskKey(task, index);
        return {
          id: taskId,
          type: 'action',
          position: { x: 400, y: 150 + index * 180 },
          data: {
            description: task.type || 'Deleted Task',
            taskConfig: { type: task.type },
            executionStatus: mapExecutionStatus(task.status),
            executionDuration: getDuration(task.start_time, task.end_time),
            isDeleted: true, // Mark as deleted for visual styling
          },
          selected: taskId === selectedTask,
        };
      });
    }

    // Track which base node IDs exist
    const baseNodeIds = new Set(overlayBaseNodes.map((n) => n.id));
    // Also track base node task types for fallback matching (e.g., core.switch → switch node)
    const baseNodeTaskTypes = new Set(overlayBaseNodes.filter((n) => n.data?.taskConfig?.type).map((n) => n.data.taskConfig.type));

    // Collect IDs of tasks that were successfully matched to a base node (direct or via switch
    // rename). These must NOT be emitted as "deleted task" ghost nodes later — that's what caused
    // switch children to appear as deleted tasks when the base node was actually present.
    const matchedExecutionTaskIds = new Set<string>();

    // Map existing base nodes with execution data.
    // Triggers are workflow definition, not tasks — they never get execution overlays.
    // When nothing about a node changes we return the original reference so ReactFlow
    // can skip re-rendering it (fixes the trigger flicker during polling).
    const mappedNodes = overlayBaseNodes.map((node) => {
      const isSelected = node.id === selectedTask;

      if (node.type === 'trigger') {
        // Editor trigger nodes from convertWorkflowToReactFlow never set `selected`, so
        // `node.selected` is `undefined` on every render. Strict-equality with a boolean
        // always fails, causing this pass-through to produce a new object reference on
        // every overlay recomputation. ReactFlow treats the new ref as a changed node,
        // wipes its measured width/height, and renders it with `visibility: hidden` until
        // the ResizeObserver re-measures. When overlay recomputes faster than measurement
        // completes (execution switch + auto-select + task click in quick succession),
        // the trigger stays hidden. Normalize with `!!` so unchanged selection returns
        // the stable ref.
        const currentSelected = !!node.selected;
        return currentSelected === isSelected ? node : { ...node, selected: isSelected };
      }

      const match = findExecutionTaskForNode(node, overlayBaseNodes, overlayBaseEdges, taskMap);
      const execTask = match?.task;
      if (match) matchedExecutionTaskIds.add(match.matchedId);
      const hasAnyTasks = effectiveTasks.length > 0;
      const nextStatus = execTask ? mapExecutionStatus(execTask.status) : hasAnyTasks ? 'PENDING' : undefined;
      const nextDuration = execTask ? getDuration(execTask.start_time, execTask.end_time) : undefined;

      // Stable-reference short-circuit: no execution data and selection unchanged.
      // `!!` normalizes undefined -> false so the compare actually short-circuits
      // (see the trigger branch above for the same rationale).
      if (!execTask && !hasAnyTasks && !!node.selected === isSelected && !node.data?.executionStatus) {
        return node;
      }

      const newData = { ...node.data };
      if (nextStatus !== undefined) {
        newData.executionStatus = nextStatus;
      } else {
        delete newData.executionStatus;
      }
      if (nextDuration !== undefined) {
        newData.executionDuration = nextDuration;
      } else {
        delete newData.executionDuration;
      }

      return {
        ...node,
        data: newData,
        selected: isSelected,
      };
    });

    // Find execution tasks that don't have a corresponding base node (deleted nodes)
    const deletedTaskNodes: Node[] = [];
    const maxY = Math.max(...overlayBaseNodes.map((n) => n.position.y), 0);

    effectiveTasks.forEach((task, index) => {
      // A task that was already matched to a base node (possibly via switch rename) is NOT deleted.
      if (task.id && matchedExecutionTaskIds.has(task.id)) return;
      // Check if this task matches any base node by id, or by task type as fallback
      const matchesBaseNode =
        (task.id && baseNodeIds.has(task.id)) || (task.type && baseNodeIds.has(task.type)) || (task.type && baseNodeTaskTypes.has(task.type));
      if (!matchesBaseNode) {
        // This task was deleted from the workflow - create a ghost node
        const taskId = getTaskKey(task, index);
        deletedTaskNodes.push({
          id: taskId,
          type: 'action',
          position: { x: 400, y: maxY + 180 + deletedTaskNodes.length * 180 },
          data: {
            description: task.type || 'Deleted Task',
            taskConfig: { type: task.type },
            executionStatus: mapExecutionStatus(task.status),
            executionDuration: getDuration(task.start_time, task.end_time),
            isDeleted: true, // Mark as deleted for visual styling
          },
          selected: taskId === selectedTask,
        });
      }
    });

    return [...mappedNodes, ...deletedTaskNodes];
  }, [overlayBaseNodes, overlayBaseEdges, effectiveTasks, selectedTask]);

  // Handle node click on canvas
  const onNodeClick = useCallback((_event: any, node: Node) => {
    setSelectedTask(node.id);
    setInlineInputViewMode('formatted');
    setInlineOutputViewMode('formatted');
    setLogsExpanded(false);
  }, []);

  const onPaneClick = useCallback(() => {
    setSelectedTask(null);
  }, []);

  // Get selected task data (cast to any for dynamic fields like input).
  // selectedTask is an editor node id when the user clicked a canvas node, so resolve through
  // findExecutionTaskForNode to pick up switch-renamed children and switch parents. For ghost
  // (deleted) nodes there is no editor node — fall back to a raw task-key lookup.
  const selectedTaskData: any = useMemo(() => {
    if (!selectedTask) return null;
    const editorNode = overlayBaseNodes.find((n) => n.id === selectedTask);
    if (editorNode) {
      const taskMap = new Map<string, WorkflowExecutionTaskResponse>();
      effectiveTasks.forEach((task) => {
        if (task.id) {
          taskMap.set(task.id, task);
        } else if (task.type && !taskMap.has(task.type)) {
          taskMap.set(task.type, task);
        }
      });
      return findExecutionTaskForNode(editorNode, overlayBaseNodes, overlayBaseEdges, taskMap)?.task ?? null;
    }
    return effectiveTasks.find((t, i) => getTaskKey(t, i) === selectedTask) || null;
  }, [selectedTask, effectiveTasks, overlayBaseNodes, overlayBaseEdges]);

  // Detect when a trigger node is selected (trigger nodes don't have execution tasks)
  const selectedTriggerNode: Node | null = useMemo(() => {
    if (!selectedTask || selectedTaskData) return null;
    const node = executionOverlayNodes.find((n) => n.id === selectedTask);
    return node?.type === 'trigger' ? node : null;
  }, [selectedTask, selectedTaskData, executionOverlayNodes]);

  // Context for the detail-panel header: when the matched task was renamed by a switch ancestor
  // (or is inherited by the switch parent), the raw task.id from the executor is opaque
  // (e.g. "core_switch-core_print-1-1"). Show the editor-node id the user actually clicked, plus
  // a small label explaining the relationship, so the Executions view reads naturally.
  const selectedTaskContext = useMemo<{ displayId: string; contextLabel?: string } | null>(() => {
    if (!selectedTask || !selectedTaskData) return null;
    const editorNode = overlayBaseNodes.find((n) => n.id === selectedTask);
    if (!editorNode) return null;

    if (editorNode.type === 'switch') {
      const taskMap = new Map<string, WorkflowExecutionTaskResponse>();
      effectiveTasks.forEach((task) => {
        if (task.id) {
          taskMap.set(task.id, task);
        } else if (task.type && !taskMap.has(task.type)) {
          taskMap.set(task.type, task);
        }
      });
      const takenChildId = getSwitchChildNodeIds(editorNode.id, overlayBaseEdges).find((cid) => {
        const childNode = overlayBaseNodes.find((n) => n.id === cid);
        return childNode
          ? findExecutionTaskForNode(childNode, overlayBaseNodes, overlayBaseEdges, taskMap)?.matchedId === selectedTaskData.id
          : false;
      });
      return { displayId: editorNode.id, contextLabel: takenChildId ? `Showing branch: ${takenChildId}` : undefined };
    }

    const ancestors = getSwitchAncestorChain(editorNode.id, overlayBaseNodes, overlayBaseEdges);
    if (ancestors.length > 0) {
      return { displayId: editorNode.id, contextLabel: `Branch of switch: ${ancestors[ancestors.length - 1]}` };
    }
    return { displayId: editorNode.id };
  }, [selectedTask, selectedTaskData, effectiveTasks, overlayBaseNodes, overlayBaseEdges]);

  // Renders formatted field content or falls back to raw JSON
  // Renders trigger panel or empty placeholder when no task is selected
  const renderDetailPanelFallback = () => {
    if (selectedTriggerNode) {
      return (
        <TriggerDetailsPanel
          triggerNode={selectedTriggerNode}
          executionData={executionData}
          selectedExecution={selectedExecution}
          getDuration={getDuration}
          copyToClipboard={copyToClipboard}
        />
      );
    }
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100%', color: colors.tertiary }}>
        <Typography sx={{ fontSize: 'var(--ds-text-body-lg)' }}>{tasksLoading ? 'Loading tasks...' : 'Select a node to view details'}</Typography>
      </Box>
    );
  };

  const renderFormattedField = (data: any, fieldType: 'input' | 'output') => {
    const schemaKey = fieldType === 'input' ? 'input_schema' : 'output_schema';
    const hasSchema = selectedTaskData?.type && taskDefinitions.find((def: any) => def.name === selectedTaskData.type)?.[schemaKey];

    if (hasSchema) {
      return (
        <FieldRenderer
          data={data}
          schema={null}
          taskType={selectedTaskData.type}
          fieldType={fieldType}
          taskDefinitions={taskDefinitions}
          copyToClipboard={copyToClipboard}
        />
      );
    }

    return (
      <Box sx={{ fontFamily: 'monospace', fontSize: 'var(--ds-text-body)', color: colors.text.secondary }}>
        {typeof data === 'string' ? data : JSON.stringify(data, null, 2)}
      </Box>
    );
  };

  // Fit the viewport when execution overlay nodes first become available.
  // ReactFlow's `fitView` prop only fires on initial mount, but nodes load
  // asynchronously (both editorNodes and executionTasks arrive after mount),
  // so the initial fitView runs against an empty canvas. This effect
  // re-fits once nodes actually appear.
  const prevOverlayCountRef = useRef(0);
  useEffect(() => {
    if (prevOverlayCountRef.current === 0 && executionOverlayNodes.length > 0 && reactFlowInstanceRef.current) {
      setTimeout(() => {
        reactFlowInstanceRef.current?.fitView({
          padding: 0.15,
          maxZoom: 0.9,
          minZoom: 0.75,
          duration: 300,
        });
      }, 50);
    }
    prevOverlayCountRef.current = executionOverlayNodes.length;
  }, [executionOverlayNodes]);

  // Re-fit viewport when the canvas container resizes. Selecting a task expands
  // the right panel (Input/Output grid), shrinking the canvas — without this,
  // nodes that were visible fall outside the new viewport (trigger/untriggered
  // tasks appear to vanish). Skipped during manual drag; the drag handler
  // fits on mouseup to avoid jitter during drag.
  useEffect(() => {
    const el = canvasContainerRef.current;
    if (!el || typeof ResizeObserver === 'undefined') return;

    let rafId: number | null = null;
    let prevWidth = el.clientWidth;
    let prevHeight = el.clientHeight;
    const observer = new ResizeObserver((entries) => {
      if (isResizingRef.current) return;
      const entry = entries[0];
      if (!entry) return;
      const { width, height } = entry.contentRect;
      if (width === 0 || height === 0) return;
      if (width === prevWidth && height === prevHeight) return;
      prevWidth = width;
      prevHeight = height;
      if (rafId !== null) cancelAnimationFrame(rafId);
      rafId = requestAnimationFrame(() => {
        reactFlowInstanceRef.current?.fitView({
          padding: 0.15,
          maxZoom: 0.9,
          minZoom: 0.75,
          duration: 300,
        });
      });
    });
    observer.observe(el);
    return () => {
      observer.disconnect();
      if (rafId !== null) cancelAnimationFrame(rafId);
    };
  }, []);

  // Auto-select first task when execution tasks load (prefer existing nodes over deleted ones).
  // Gated on effectiveTasks so we only auto-select once the tasks match the selected execution.
  // Prefer an editor node id (via findExecutionTaskForNode) so selection highlights the canvas node
  // even when the task was renamed by a switch parent; fall back to a raw task key for deleted tasks.
  useEffect(() => {
    if (effectiveTasks.length > 0 && !selectedTask) {
      const taskMap = new Map<string, WorkflowExecutionTaskResponse>();
      effectiveTasks.forEach((task) => {
        if (task.id) {
          taskMap.set(task.id, task);
        } else if (task.type && !taskMap.has(task.type)) {
          taskMap.set(task.type, task);
        }
      });

      const matchedNode = overlayBaseNodes.find(
        (node) => node.type !== 'trigger' && findExecutionTaskForNode(node, overlayBaseNodes, overlayBaseEdges, taskMap)
      );

      if (matchedNode) {
        setSelectedTask(matchedNode.id);
      } else {
        setSelectedTask(getTaskKey(effectiveTasks[0], 0));
      }
    }
  }, [effectiveTasks, overlayBaseNodes, overlayBaseEdges, selectedTask]);

  return (
    <Box
      sx={{
        width: '100%',
        height: '100%',
        backgroundColor: 'var(--ds-background-200)',
        overflow: 'hidden',
        display: 'flex',
        flexDirection: 'column',
      }}
    >
      {/* Three Column Body */}
      <Box sx={{ flex: 1, display: 'flex', overflow: 'hidden' }}>
        {/* LEFT PANEL - Execution List */}
        <Box
          sx={{
            width: '320px',
            minWidth: '320px',
            height: '100%',
            borderRight: `1px solid ${colors.border.secondaryLight}`,
            backgroundColor: colors.background.white,
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          {/* Section 1 - Panel Header */}
          <Box sx={{ padding: 'var(--ds-space-3) var(--ds-space-4)', borderBottom: `1px solid ${colors.border.secondaryLight}` }}>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: onStatusChange ? 1 : 0 }}>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                <PlaylistPlay sx={{ fontSize: 'var(--ds-text-heading)', color: colors.tertiary }} />
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-title)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    color: colors.text.secondary,
                    fontFamily: 'Poppins, sans-serif',
                    letterSpacing: '-0.2px',
                  }}
                >
                  Executions
                </Typography>
              </Box>
              <Button
                composition='icon-only'
                tone='ghost'
                size='sm'
                aria-label='Refresh executions'
                icon={<Refresh sx={{ fontSize: 'var(--ds-text-title)' }} />}
                disabled={loading}
                onClick={handleRefresh}
              />
            </Box>
            {onStatusChange && (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: colors.tertiary, fontWeight: 'var(--ds-font-weight-medium)' }}>
                  Status:
                </Typography>
                <FormControl size='small' sx={{ minWidth: 120 }}>
                  <Select
                    value={selectedStatus}
                    onChange={(e) => onStatusChange(e.target.value)}
                    sx={{
                      fontSize: 'var(--ds-text-caption)',
                      height: '30px',
                      '& .MuiSelect-select': { padding: 'var(--ds-space-1) var(--ds-space-2)' },
                    }}
                  >
                    <MenuItem value='All'>All</MenuItem>
                    <MenuItem value='Running'>Running</MenuItem>
                    <MenuItem value='Completed'>Completed</MenuItem>
                    <MenuItem value='Failed'>Failed</MenuItem>
                    <MenuItem value='Canceled'>Canceled</MenuItem>
                    <MenuItem value='Terminated'>Terminated</MenuItem>
                    <MenuItem value='Timed Out'>Timed Out</MenuItem>
                    <MenuItem value='Continued As New'>Continued As New</MenuItem>
                    <MenuItem value='Unspecified'>Unspecified</MenuItem>
                  </Select>
                </FormControl>
              </Box>
            )}
          </Box>

          {/* Section 2 - Execution List (scrollable) */}
          <Box className='custom-scrollbar' sx={{ flex: 1, overflowY: 'auto' }}>
            {loading ? (
              <Box sx={{ textAlign: 'center', padding: 'var(--ds-space-6) var(--ds-space-4)', color: colors.tertiary }}>Loading executions...</Box>
            ) : filteredExecutions.length === 0 ? (
              <Box sx={{ textAlign: 'center', padding: 'var(--ds-space-6) var(--ds-space-4)', color: colors.tertiary }}>
                <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', marginBottom: 'var(--ds-space-2)' }}>No executions found</Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-small)' }}>{"This automation hasn't been executed yet."}</Typography>
              </Box>
            ) : (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: '0px' }}>
                {filteredExecutions.map((execution: any) => {
                  const isSelected = selectedExecution?.id === execution.id;

                  return (
                    <Box
                      key={execution.id}
                      onClick={() => {
                        setSelectedExecution(execution);
                        setSelectedTask(null);
                      }}
                      sx={{
                        padding: 'var(--ds-space-2) var(--ds-space-4)',
                        cursor: 'pointer',
                        backgroundColor: isSelected ? colors.background.primaryLightest : colors.background.white,
                        borderBottom: `1px solid ${colors.border.secondaryLight}`,
                        borderLeft: isSelected ? `5px solid ${colors.primary}` : '3px solid transparent',
                        transition: 'all 0.15s ease',
                        // Highlight animation for newly retried execution
                        ...(highlightedExecutionId === execution.id && {
                          animation: 'highlightPulse 2.5s ease-out',
                          '@keyframes highlightPulse': {
                            '0%': {
                              boxShadow: '0 0 0 0 rgba(34, 197, 94, 0.7)',
                              borderColor: 'var(--ds-green-500)',
                            },
                            '30%': {
                              boxShadow: '0 0 0 8px rgba(34, 197, 94, 0)',
                              borderColor: 'var(--ds-green-500)',
                            },
                            '100%': {
                              boxShadow: '0 0 0 0 rgba(34, 197, 94, 0)',
                              borderColor: colors.primary,
                            },
                          },
                        }),
                        '&:hover': {
                          backgroundColor: isSelected ? colors.background.primaryLightest : colors.background.tertiaryLightestestest,
                        },
                      }}
                    >
                      {/* Primary row: dot + relative time + badge */}
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                        {/* Status dot */}
                        <Box
                          sx={{
                            width: '8px',
                            height: '8px',
                            borderRadius: '50%',
                            backgroundColor: getStatusColor(execution.status),
                            flexShrink: 0,
                          }}
                        />
                        {/* Relative timestamp */}
                        <Box sx={{ flex: 1 }}>
                          <Datetime
                            value={execution.start_time}
                            maxLevel={1}
                            sx={{
                              fontSize: 'var(--ds-text-body)',
                              fontWeight: isSelected ? '600' : '500',
                              color: colors.text.secondary,
                              marginRight: 'var(--ds-space-1)',
                            }}
                            sxSuffix={{ fontSize: 'var(--ds-text-small)', fontWeight: isSelected ? '500' : '400', color: colors.text.secondary }}
                            sxSecondary={false}
                            sxSuffixSecondary={false}
                          />
                        </Box>
                        {/* Status badge - using existing CustomLabels */}
                        <Box sx={{ flexShrink: 0 }}>
                          <CustomLabels text={execution.status.toLowerCase()} />
                        </Box>
                      </Box>
                      {/* Secondary row: trigger type + duration with icons */}
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-3)', mt: 'var(--ds-space-1)', ml: 'var(--ds-space-4)' }}>
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                          <SafeIcon src={PlayCircleIcon} alt='trigger' width={12} height={12} style={{ opacity: 0.6 }} />
                          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.tertiary }}>
                            {execution.trigger_type || 'manual'}
                          </Typography>
                        </Box>
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                          <Schedule sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark }} />
                          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.tertiary }}>
                            {getDuration(execution?.start_time as string, execution.close_time)}
                          </Typography>
                        </Box>
                      </Box>
                    </Box>
                  );
                })}

                {(hasMore || hasPrevious) && (
                  <Box sx={{ display: 'flex', justifyContent: 'center', gap: 1, padding: 'var(--ds-space-3) var(--ds-space-4)' }}>
                    <Button tone='secondary' size='sm' onClick={onPrevious} disabled={!hasPrevious || loadingMore}>
                      Previous
                    </Button>
                    <Button tone='secondary' size='sm' onClick={onNext} disabled={!hasMore || loadingMore} loading={loadingMore}>
                      {loadingMore ? 'Loading...' : 'Next'}
                    </Button>
                  </Box>
                )}
              </Box>
            )}
          </Box>
        </Box>

        {/* CENTER PANEL - Visual Workflow Canvas */}
        <Box
          ref={canvasContainerRef}
          sx={{
            flex: 1,
            height: '100%',
            position: 'relative',
            overflow: 'hidden',
            // ReactFlow sets `visibility: hidden` until it has measured a node's
            // width/height via ResizeObserver. In the execution view, overlay
            // recomputations (selection change, exec switch, auto-select) rebuild
            // `nodeInternals` and wipe width/height before measurement completes,
            // so trigger nodes can end up stuck hidden. Positions come from
            // `node.position` (set by convertWorkflowToReactFlow), not from
            // measurements, so nodes render at the correct spot even before
            // measurement. Force visible to short-circuit that race.
            '& .react-flow__node': {
              visibility: 'visible !important',
            },
          }}
        >
          {/* Execution Summary Strip - floating over the canvas top */}
          {selectedExecution && (
            <Box
              sx={{
                position: 'absolute',
                top: 0,
                left: 0,
                right: 0,
                zIndex: 5,
                backgroundColor: colors.background.white,
                backdropFilter: 'blur(4px)',
                borderBottom: '1px solid var(--ds-brand-150)',
                padding: 'var(--ds-space-4) var(--ds-space-4)',
              }}
            >
              <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-5)' }}>
                  <Box>
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                      Started
                    </Typography>
                    <Typography sx={{ fontSize: 'var(--ds-text-body)', color: colors.text.secondary }}>
                      <Datetime value={selectedExecution.start_time} />
                    </Typography>
                  </Box>
                  <Box>
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                      Duration
                    </Typography>
                    <Typography sx={{ fontSize: 'var(--ds-text-body)', color: colors.text.secondary }}>
                      {getDuration(selectedExecution.start_time as string, selectedExecution.close_time)}
                    </Typography>
                  </Box>
                  <Box>
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                      Trigger
                    </Typography>
                    <Typography sx={{ fontSize: 'var(--ds-text-body)', color: colors.text.secondary }}>
                      {selectedExecution.trigger_type || 'Manual'}
                    </Typography>
                  </Box>
                  {executionData?.version_number != null && (
                    <Box>
                      <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                        Version
                      </Typography>
                      <Typography sx={{ fontSize: 'var(--ds-text-body)', color: colors.text.secondary }}>
                        v{executionData.version_number}
                        {executionData.version_name ? ` · ${executionData.version_name}` : ''}
                      </Typography>
                    </Box>
                  )}
                  <Box>
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                      Tasks
                    </Typography>
                    <Typography sx={{ fontSize: 'var(--ds-text-body)', color: colors.text.secondary }}>{effectiveTasks.length}</Typography>
                  </Box>
                </Box>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-3)' }}>
                  {isExecutionCompleted(selectedExecution.status) && hasWriteAccess(accountId) && (
                    <Button
                      tone='secondary'
                      size='sm'
                      icon={<Replay sx={{ fontSize: 'var(--ds-text-body-lg)' }} />}
                      onClick={handleRetrigger}
                      disabled={retriggerLoading}
                      loading={retriggerLoading}
                    >
                      {retriggerLoading ? 'Retrying...' : 'Retry'}
                    </Button>
                  )}
                  {isExecutionCancellable(selectedExecution.status) && hasWriteAccess(accountId) && (
                    <Button
                      id='execution-cancel-btn'
                      tone='danger'
                      size='sm'
                      icon={<StopCircleOutlined sx={{ fontSize: 'var(--ds-text-body-lg)' }} />}
                      onClick={handleCancel}
                      disabled={cancelLoading}
                      loading={cancelLoading}
                    >
                      {cancelLoading ? 'Cancelling...' : 'Cancel'}
                    </Button>
                  )}
                  <CustomLabels text={selectedExecution.status.toUpperCase()} />
                </Box>
              </Box>

              {executionData?.error && (
                <Box sx={{ mt: 0.5 }}>
                  <AccordionSmall
                    summarySx={{ maxHeight: '28px', my: '0px', boxShadow: '0 6px 10px rgba(0, 0, 0, 0.07)' }}
                    header={
                      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', width: '100%' }}>
                        <Typography
                          sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-red-600)' }}
                        >
                          Execution Error
                        </Typography>
                        <Button
                          composition='icon-only'
                          tone='ghost'
                          size='xs'
                          aria-label='Copy execution error'
                          icon={<ContentCopy sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-red-600)' }} />}
                          onClick={(e) => {
                            e.stopPropagation();
                            copyToClipboard(executionData?.error || '', 'Execution Error');
                          }}
                        />
                      </Box>
                    }
                    status='FAILED'
                  >
                    <Box
                      sx={{
                        fontFamily: 'monospace',
                        fontSize: 'var(--ds-text-caption)',
                        color: 'var(--ds-red-600)',
                        wordBreak: 'break-word',
                        backgroundColor: 'var(--ds-red-100)',
                        padding: 'var(--ds-space-2)',
                        borderRadius: 'var(--ds-radius-sm)',
                        border: '1px solid var(--ds-red-200)',
                      }}
                    >
                      {executionData?.error}
                    </Box>
                  </AccordionSmall>
                </Box>
              )}

              {(executionData?.inputs || executionData?.workflow_result) && (
                <Box sx={{ mt: 1.5 }}>
                  <AccordionSmall
                    summarySx={{ maxHeight: '28px', my: '0px', boxShadow: '0 6px 10px rgba(0, 0, 0, 0.07)' }}
                    header={
                      <Typography
                        sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}
                      >
                        Execution Input/Output
                      </Typography>
                    }
                  >
                    <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)', flexDirection: 'column' }}>
                      {executionData?.inputs && (
                        <Box sx={{ flex: 1 }}>
                          <FormCard title='Inputs' description={''} icon={null} number={''} columns={1}>
                            <Box
                              sx={{
                                fontFamily: 'monospace',
                                fontSize: 'var(--ds-text-caption)',
                                color: colors.text.secondary,
                                wordBreak: 'break-word',
                                backgroundColor: 'var(--ds-background-200)',
                                padding: 'var(--ds-space-2)',
                                borderRadius: 'var(--ds-radius-sm)',
                                border: '1px solid var(--ds-brand-150)',
                                maxHeight: '200px',
                                overflowY: 'auto',
                              }}
                            >
                              <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 1 }}>
                                <Typography
                                  sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-600)' }}
                                >
                                  JSON Data
                                </Typography>
                                <Button
                                  composition='icon-only'
                                  tone='ghost'
                                  size='xs'
                                  aria-label='Copy automation inputs'
                                  icon={<ContentCopy sx={{ fontSize: 'var(--ds-text-small)' }} />}
                                  onClick={() => copyToClipboard(JSON.stringify(executionData?.inputs, null, 2), 'Automation Inputs')}
                                />
                              </Box>
                              {JSON.stringify(executionData?.inputs, null, 2)}
                            </Box>
                          </FormCard>
                        </Box>
                      )}
                      {executionData?.workflow_result && (
                        <Box sx={{ flex: 1 }}>
                          <FormCard title='Result' description={''} icon={null} number={''} columns={1}>
                            <Box
                              sx={{
                                fontFamily: 'monospace',
                                fontSize: 'var(--ds-text-caption)',
                                color: colors.text.secondary,
                                wordBreak: 'break-word',
                                backgroundColor: 'var(--ds-blue-100)',
                                padding: 'var(--ds-space-2)',
                                borderRadius: 'var(--ds-radius-sm)',
                                border: '1px solid var(--ds-teal-200)',
                                maxHeight: '200px',
                                overflowY: 'auto',
                              }}
                            >
                              <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 1 }}>
                                <Typography
                                  sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-600)' }}
                                >
                                  JSON Data
                                </Typography>
                                <Button
                                  composition='icon-only'
                                  tone='ghost'
                                  size='xs'
                                  aria-label='Copy automation result'
                                  icon={<ContentCopy sx={{ fontSize: 'var(--ds-text-small)' }} />}
                                  onClick={() => copyToClipboard(JSON.stringify(executionData?.workflow_result, null, 2), 'Automation Result')}
                                />
                              </Box>
                              {JSON.stringify(executionData?.workflow_result, null, 2)}
                            </Box>
                          </FormCard>
                        </Box>
                      )}
                    </Box>
                  </AccordionSmall>
                </Box>
              )}
            </Box>
          )}

          {/* Execution Status Bar - floating pill with approval prompt when an approval task is waiting */}
          <ExecutionStatusBar
            top={140}
            visible={!!selectedExecution && !isExecutionCompleted(selectedExecution.status)}
            completedTasks={
              effectiveTasks.filter((t) => {
                const s = String(t.status ?? '').toUpperCase();
                return s === 'COMPLETED' || s === 'COMPLETE' || s === 'COMPLETE_WITH_ERROR' || s === 'FAILED';
              }).length
            }
            totalTasks={effectiveTasks.length}
            pendingApprovals={
              hasWriteAccess(accountId)
                ? effectiveTasks
                    .filter((t) => t.type === 'core.approval' && String(t.status ?? '').toUpperCase() === 'SCHEDULED' && !!t.id)
                    .map<PendingApproval>((t) => ({
                      taskId: t.id as string,
                      options: Array.isArray((t as any).input?.approval_options)
                        ? (t as any).input.approval_options.filter((o: any) => typeof o === 'string' && o.length > 0)
                        : [],
                    }))
                : []
            }
            onApprove={handleCompleteApproval}
            approvalLoading={approvalLoading}
          />

          {/* Banner for executions whose exact ran definition can't be
              guaranteed.
                - `legacy_live_snapshot`: pre-V741 execution, no Memo keys.
                  We're showing the live-version snapshot taken at backfill,
                  which is a close proxy but not the exact ran graph.
                - `live_fallback`: even the live snapshot is missing; canvas
                  falls back to the current draft and may differ from what
                  actually ran. */}
          {executionData?.definition_source === 'draft_run' && (
            <Alert severity='info' sx={{ margin: 'var(--ds-space-2) var(--ds-space-4)' }}>
              This was a draft run (“Run current”) of an unpublished definition. The canvas shows the exact draft that ran, not the live version.
            </Alert>
          )}
          {executionData?.definition_source === 'legacy_live_snapshot' && (
            <Alert severity='info' sx={{ margin: '8px 16px' }}>
              This execution predates version tracking — showing the live version snapshot taken at backfill time, which may differ from the exact
              definition that ran.
            </Alert>
          )}
          {executionData?.definition_source === 'live_fallback' && (
            <Alert severity='warning' sx={{ margin: 'var(--ds-space-2) var(--ds-space-4)' }}>
              This execution predates version tracking and the live snapshot is unavailable — the canvas below reflects the current workflow draft and
              may differ from what actually ran.
            </Alert>
          )}

          {/* ReactFlow Canvas */}
          <ReactFlow
            nodes={executionOverlayNodes}
            edges={overlayBaseEdges}
            nodeTypes={executionNodeTypes}
            edgeTypes={executionEdgeTypes}
            onNodeClick={onNodeClick}
            onPaneClick={onPaneClick}
            onInit={(instance) => {
              reactFlowInstanceRef.current = instance;
            }}
            defaultEdgeOptions={{
              type: 'smoothstep',
              style: { strokeWidth: 1, stroke: 'rgb(175, 175, 175)' },
            }}
            connectionLineType={'smoothstep' as any}
            fitView
            fitViewOptions={{ padding: 0.15, maxZoom: 0.9, minZoom: 0.75 }}
            snapToGrid={true}
            snapGrid={[19, 15]}
            nodesDraggable={false}
            nodesConnectable={false}
            elementsSelectable={true}
            panOnDrag={true}
            panOnScroll
            panOnScrollMode={PanOnScrollMode.Vertical}
            panOnScrollSpeed={0.8}
            zoomOnScroll={false}
            zoomOnPinch
            proOptions={{ hideAttribution: true }}
          >
            <MiniMap
              nodeStrokeWidth={2}
              position='bottom-right'
              style={{
                width: 50,
                height: 100,
                backgroundColor: 'white',
                border: '1px solid rgb(187, 187, 187)',
                borderRadius: 'var(--ds-radius-sm)',
                margin: '0px var(--ds-space-3) var(--ds-space-4) 0px',
              }}
            />
            <Background color='rgba(0, 0, 0, 0.42)' />
            <Controls />
          </ReactFlow>
        </Box>

        {/* RIGHT PANEL - Input & Output (resizable) */}
        <Box sx={{ position: 'relative', display: 'flex', height: '100%' }}>
          {/* Resize handle */}
          <Box
            onMouseDown={handleResizeMouseDown}
            sx={{
              width: '14px',
              cursor: 'col-resize',
              backgroundColor: colors.background.white,
              borderLeft: '1px solid var(--ds-brand-150)',
              flexShrink: 0,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              '&:hover': { backgroundColor: 'var(--ds-brand-150)' },
              transition: 'background-color 0.15s',
            }}
          >
            <DragIndicator sx={{ fontSize: 'var(--ds-text-title)', color: 'var(--ds-gray-400)' }} />
          </Box>
          <Box
            sx={{
              width: `${rightPanelWidth}px`,
              minWidth: `${RIGHT_PANEL_MIN_WIDTH}px`,
              maxWidth: `${RIGHT_PANEL_MAX_WIDTH}px`,
              height: '100%',
              backgroundColor: 'white',
              display: 'flex',
              flexDirection: 'column',
            }}
          >
            {selectedTaskData ? (
              <>
                {/* Node Header - pinned */}
                <Box
                  sx={{
                    padding: 'var(--ds-space-3) var(--ds-space-4) var(--ds-space-3) var(--ds-space-2)',
                    borderBottom: '1px solid var(--ds-brand-150)',
                    flexShrink: 0,
                  }}
                >
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                    <Box
                      sx={{
                        width: '32px',
                        height: '32px',
                        borderRadius: 'var(--ds-radius-md)',
                        backgroundColor: 'var(--ds-blue-500)',
                        color: 'white',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                      }}
                    >
                      {(() => {
                        const icon = getTaskIcon(selectedTaskData.type);
                        if (typeof icon === 'string' && !icon.includes('/') && !icon.includes('.')) {
                          return <span style={{ fontSize: 'var(--ds-text-small)' }}>{icon}</span>;
                        }
                        const providerLogos = [newAwsLogo, ouAzure, ouGoogle, K8sIcon, RabbitmqIcon, RedisLogoIcon, GithubIcon, ArgocdIcon];
                        const shouldKeepColors = providerLogos.includes(icon);
                        return (
                          <SafeIcon
                            src={icon}
                            alt='task-icon'
                            width={20}
                            height={20}
                            style={{ filter: shouldKeepColors ? 'none' : 'brightness(0) invert(1)' }}
                          />
                        );
                      })()}
                    </Box>
                    <Box sx={{ flex: 1, minWidth: 0 }}>
                      <Typography
                        sx={{
                          fontWeight: 'bold',
                          fontSize: 'var(--ds-text-body-lg)',
                          color: colors.text.secondary,
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                        }}
                        title={
                          selectedTaskContext?.displayId && selectedTaskData.id && selectedTaskContext.displayId !== selectedTaskData.id
                            ? `Runtime task id: ${selectedTaskData.id}`
                            : undefined
                        }
                      >
                        {selectedTaskContext?.displayId || selectedTaskData.id || 'Unknown Task'}
                      </Typography>
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', flexWrap: 'wrap' }}>
                        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondaryDark }}>
                          {selectedTaskData.type || 'No type specified'}
                        </Typography>
                        {selectedTaskContext?.contextLabel && (
                          <Box
                            sx={{
                              fontSize: 'var(--ds-text-caption)',
                              fontWeight: 'var(--ds-font-weight-medium)',
                              color: colors.text.secondary,
                              backgroundColor: 'var(--ds-blue-100)',
                              border: '1px solid var(--ds-blue-300)',
                              borderRadius: 'var(--ds-radius-lg)',
                              padding: 'var(--ds-space-1) var(--ds-space-2)',
                              lineHeight: '16px',
                              whiteSpace: 'nowrap',
                            }}
                          >
                            {selectedTaskContext.contextLabel}
                          </Box>
                        )}
                      </Box>
                    </Box>
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-3)', flexShrink: 0 }}>
                      <Box
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: 'var(--ds-space-1)',
                          backgroundColor: colors.background.tertiaryLightestestest,
                          padding: 'var(--ds-space-1) var(--ds-space-2)',
                          borderRadius: 'var(--ds-radius-sm)',
                        }}
                      >
                        <AccessTime sx={{ fontSize: 'var(--ds-text-body-lg)', color: colors.text.secondaryDark }} />
                        <Typography
                          sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: colors.text.secondary }}
                        >
                          {getDuration(selectedTaskData.start_time, selectedTaskData.end_time)}
                        </Typography>
                      </Box>
                      <CustomLabels text={selectedTaskData.status.toUpperCase()} />
                    </Box>
                  </Box>
                </Box>

                {/* Input & Output side by side with independent scroll */}
                <Box sx={{ flex: 1, display: 'flex', overflow: 'hidden', marginTop: 'var(--ds-space-3)' }}>
                  {/* Input column */}
                  <Box sx={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0 }}>
                    {/* Pinned header */}
                    <Box
                      sx={{
                        flexShrink: 0,
                        padding: 'var(--ds-space-2) var(--ds-space-4)',
                        backgroundColor: '#fdf3e69a',
                        borderRadius: 'var(--ds-radius-lg) 0px 0px var(--ds-radius-lg)',
                      }}
                    >
                      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                          <InputIcon sx={{ fontSize: 'var(--ds-text-title)', color: colors.text.secondary }} />
                          <Typography
                            sx={{
                              fontSize: 'var(--ds-text-small)',
                              fontWeight: 'var(--ds-font-weight-semibold)',
                              color: colors.text.secondary,
                              fontFamily: 'Poppins, sans-serif',
                            }}
                          >
                            Input
                          </Typography>
                        </Box>
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                          <Box
                            sx={{
                              display: 'flex',
                              backgroundColor: colors.background.white,
                              borderRadius: 'var(--ds-radius-md)',
                              padding: 'var(--ds-space-1)',
                            }}
                          >
                            <Button
                              tone={inlineInputViewMode === 'formatted' ? 'secondary' : 'ghost'}
                              size='xs'
                              onClick={() => setInlineInputViewMode('formatted')}
                            >
                              Formatted
                            </Button>
                            <Button
                              tone={inlineInputViewMode === 'json' ? 'secondary' : 'ghost'}
                              size='xs'
                              onClick={() => setInlineInputViewMode('json')}
                            >
                              JSON
                            </Button>
                          </Box>
                          <Button
                            composition='icon-only'
                            tone='ghost'
                            size='xs'
                            aria-label='Copy input'
                            icon={<ContentCopy sx={{ fontSize: 'var(--ds-text-body-lg)' }} />}
                            onClick={() =>
                              copyToClipboard(
                                typeof selectedTaskData.input === 'string' ? selectedTaskData.input : JSON.stringify(selectedTaskData.input, null, 2),
                                'Input'
                              )
                            }
                          />
                        </Box>
                      </Box>
                    </Box>
                    {/* Scrollable content */}
                    <Box
                      className='custom-scrollbar'
                      sx={{
                        flex: 1,
                        overflowY: 'auto',
                        padding: 'var(--ds-space-4) var(--ds-space-2)',
                        borderRight: '1px solid var(--ds-gray-300)',
                      }}
                    >
                      <Box
                        sx={{
                          backgroundColor: colors.background.tertiaryLightestestest,
                          border: `1px solid ${colors.border.secondaryLight}`,
                          borderRadius: 'var(--ds-radius-md)',
                          padding: 'var(--ds-space-3)',
                          wordBreak: 'break-word',
                        }}
                      >
                        {inlineInputViewMode === 'formatted' ? (
                          renderFormattedField(selectedTaskData.input, 'input')
                        ) : (
                          <Box
                            sx={{ fontFamily: 'monospace', fontSize: 'var(--ds-text-body)', color: colors.text.secondary, whiteSpace: 'pre-wrap' }}
                          >
                            {typeof selectedTaskData.input === 'string' ? selectedTaskData.input : JSON.stringify(selectedTaskData.input, null, 2)}
                          </Box>
                        )}
                      </Box>
                    </Box>
                  </Box>

                  {/* Output column */}
                  <Box sx={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, marginRight: 'var(--ds-space-3)' }}>
                    {/* Pinned header */}
                    <Box
                      sx={{
                        flexShrink: 0,
                        padding: 'var(--ds-space-2) var(--ds-space-4)',
                        backgroundColor: '#fdf3e69a',
                        borderRadius: '0px var(--ds-radius-lg) var(--ds-radius-lg) 0px',
                      }}
                    >
                      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                          <OutputIcon sx={{ fontSize: 'var(--ds-text-title)', color: colors.text.secondary }} />
                          <Typography
                            sx={{
                              fontSize: 'var(--ds-text-small)',
                              fontWeight: 'var(--ds-font-weight-semibold)',
                              color: colors.text.secondary,
                              fontFamily: 'Poppins, sans-serif',
                            }}
                          >
                            Output
                          </Typography>
                        </Box>
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                          <Box
                            sx={{
                              display: 'flex',
                              backgroundColor: colors.background.white,
                              borderRadius: 'var(--ds-radius-md)',
                              padding: 'var(--ds-space-1)',
                            }}
                          >
                            <Button
                              tone={inlineOutputViewMode === 'formatted' ? 'secondary' : 'ghost'}
                              size='xs'
                              onClick={() => setInlineOutputViewMode('formatted')}
                            >
                              Formatted
                            </Button>
                            <Button
                              tone={inlineOutputViewMode === 'json' ? 'secondary' : 'ghost'}
                              size='xs'
                              onClick={() => setInlineOutputViewMode('json')}
                            >
                              JSON
                            </Button>
                          </Box>
                          <Button
                            composition='icon-only'
                            tone='ghost'
                            size='xs'
                            aria-label='Copy output'
                            icon={<ContentCopy sx={{ fontSize: 'var(--ds-text-body-lg)' }} />}
                            onClick={() =>
                              copyToClipboard(
                                typeof selectedTaskData.output === 'string'
                                  ? selectedTaskData.output
                                  : JSON.stringify(selectedTaskData.output, null, 2),
                                'Output'
                              )
                            }
                          />
                        </Box>
                      </Box>
                    </Box>
                    {/* Scrollable content */}
                    <Box
                      className='custom-scrollbar'
                      sx={{
                        flex: 1,
                        overflowY: 'auto',
                        padding: 'var(--ds-space-4) var(--ds-space-2) var(--ds-space-4) var(--ds-space-3)',
                      }}
                    >
                      {selectedTaskData.output ? (
                        <Box
                          sx={{
                            backgroundColor: colors.background.primaryLightest,
                            border: `1px solid ${colors.border.primaryLight}`,
                            borderRadius: 'var(--ds-radius-md)',
                            padding: 'var(--ds-space-3)',
                            wordBreak: 'break-word',
                          }}
                        >
                          {inlineOutputViewMode === 'formatted' ? (
                            renderFormattedField(selectedTaskData.output, 'output')
                          ) : (
                            <Box
                              sx={{ fontFamily: 'monospace', fontSize: 'var(--ds-text-body)', color: colors.text.secondary, whiteSpace: 'pre-wrap' }}
                            >
                              {typeof selectedTaskData.output === 'string'
                                ? selectedTaskData.output
                                : JSON.stringify(selectedTaskData.output, null, 2)}
                            </Box>
                          )}
                        </Box>
                      ) : (
                        <Box sx={{ textAlign: 'center', color: colors.text.secondaryDark, py: 4 }}>
                          <Typography sx={{ fontSize: 'var(--ds-text-small)' }}>No output data</Typography>
                        </Box>
                      )}

                      {/* Error section within Output column */}
                      {selectedTaskData.error && (
                        <Box sx={{ mt: 2 }}>
                          <Box
                            onClick={() => setLogsExpanded(!logsExpanded)}
                            sx={{
                              display: 'flex',
                              justifyContent: 'space-between',
                              alignItems: 'center',
                              cursor: 'pointer',
                              mb: logsExpanded ? 1 : 0,
                              '&:hover': { opacity: 0.8 },
                            }}
                          >
                            <Typography
                              sx={{
                                fontSize: 'var(--ds-text-small)',
                                fontWeight: 'var(--ds-font-weight-semibold)',
                                color: colors.error,
                                fontFamily: 'Poppins, sans-serif',
                              }}
                            >
                              Error
                            </Typography>
                            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                              <Button
                                composition='icon-only'
                                tone='ghost'
                                size='xs'
                                aria-label='Copy error'
                                icon={<ContentCopy sx={{ fontSize: 'var(--ds-text-small)', color: colors.error }} />}
                                onClick={(e) => {
                                  e.stopPropagation();
                                  copyToClipboard(selectedTaskData.error || '', 'Error');
                                }}
                              />
                              {logsExpanded ? (
                                <ExpandLess sx={{ fontSize: 'var(--ds-text-title)', color: colors.error }} />
                              ) : (
                                <ExpandMore sx={{ fontSize: 'var(--ds-text-title)', color: colors.error }} />
                              )}
                            </Box>
                          </Box>
                          {logsExpanded && (
                            <Box
                              sx={{
                                backgroundColor: colors.background.accordionSummay,
                                border: `1px solid ${colors.background.errorLight}`,
                                borderRadius: 'var(--ds-radius-md)',
                                padding: 'var(--ds-space-3)',
                                fontFamily: 'monospace',
                                fontSize: 'var(--ds-text-body)',
                                wordBreak: 'break-word',
                                whiteSpace: 'pre-wrap',
                                color: colors.error,
                              }}
                            >
                              {selectedTaskData.error}
                            </Box>
                          )}
                        </Box>
                      )}
                    </Box>
                  </Box>
                </Box>
                {/* Called Workflow Tasks — surfaces the called workflow's nested tasks
                    (populated by backend processWorkflowHistory) so users can see each
                    step's Input/Output without leaving the parent run. */}
                {selectedTaskData.type === 'core.call-workflow' &&
                  Array.isArray(selectedTaskData.children) &&
                  selectedTaskData.children.length > 0 && (
                    <CallWorkflowChildren tasks={selectedTaskData.children} copyToClipboard={copyToClipboard} />
                  )}
              </>
            ) : (
              renderDetailPanelFallback()
            )}
          </Box>
        </Box>
      </Box>
    </Box>
  );
};

export default ExecutionsView;

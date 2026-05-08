import { useState, useEffect, useCallback, useRef, useMemo, lazy, Suspense } from 'react';
import {
  ReactFlow,
  MiniMap,
  applyNodeChanges,
  applyEdgeChanges,
  addEdge,
  Background,
  Controls,
  PanOnScrollMode,
  type Node,
  type Edge,
  type ReactFlowInstance,
} from 'reactflow';
import 'reactflow/dist/style.css';
import { useRouter } from 'next/router';
import { Alert, Box, Typography, CircularProgress, TextField } from '@mui/material';
import ChevronLeftIcon from '@mui/icons-material/ChevronLeft';
import ChevronRightIcon from '@mui/icons-material/ChevronRight';
import AddIcon from '@mui/icons-material/Add';
import AutoFixHighIcon from '@mui/icons-material/AutoFixHigh';
import StopCircleOutlinedIcon from '@mui/icons-material/StopCircleOutlined';
import WorkflowHeader from './WorkflowHeader';
import NodeCategoriesSidebar from './modals/NodeCategoriesSidebar';
import TriggerSelectorPopup from './modals/TriggerSelectorPopup';
import { colors } from 'src/utils/colors';

// Extracted components (lightweight, always visible)
import { TriggerWarningMessage, ExecutionStatusBar } from './components';
import Loader from '@components1/common/Loader';
import { createWorkflowCreateRequest, createWorkflowUpdateRequest, prepareWorkflowForSave } from './utils/workflowApiUtils';
import { convertWorkflowToReactFlow } from './utils/workflowLayoutEngine';
import { validateWorkflowForSave, wouldCreateCycle } from './utils/workflowValidation';
import { parseDurationToSeconds } from './utils/taskUtils';
import { extractTriggersFromNodes, extractTasksFromWorkflowNodes, getPreviousNodesOutputSchemas } from './utils/workflowTaskExtraction';
import { disableTask, enableTask } from './utils/toggleTaskDisable';
import {
  cleanupSwitchReferencesAfterDelete,
  findExecutionTaskForNode,
  getPreviousTasksForNode,
  getSwitchDryRunEligibility,
} from './utils/templateUtils';
import { buildWorkflowFromAIResponse, type AIGenerateWorkflowResponse, sanitizeTaskId } from './utils';
import { parseConditionLabel, hasConditionalStyling } from './utils/conditionParser';

// Custom imports
import { nodeCategories, generateNodeCategories } from './constants/nodeCategories';
import type { TaskDefinitionAPIResponse, NodeCategories, WorkflowStatus, WorkflowSettings } from './types';
import { ActionNode, TriggerNode, SwitchNode } from './nodes';
import DeletableEdge from './components/DeletableEdge';
import ConditionalEdge from './components/ConditionalEdge';
import { useExecutionData } from './hooks/useExecutionData';
import { useWorkflowInteractions } from './hooks/useWorkflowInteractions';
import { validateTaskData } from './hooks/useTaskValidation';
import apiWorkflow from '@api1/workflow';
import { snackbar } from '@components1/common/snackbarService';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import CustomButton from '@components1/common/NewCustomButton';
import CustomIconButton from '@components1/CustomIconButton';
import {
  manualTriggerIcon,
  SettingOutlineIconGrey,
  PlayIconBlue,
  workflowUserIcon,
  workflowWebhookIcon,
  workflowCalendarIcon,
  SaveIconOutline,
} from '@assets';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import { useUnsavedChangesTracking } from './hooks/useUnsavedChangesTracking';
import { useJsonEditorSync } from './hooks/useJsonEditorSync';
import SafeIcon from '@components1/common/SafeIcon';
import CustomDropdown from '@components1/common/CustomDropdown';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { hasFeatureAccess } from '@lib/auth';
import { Modal } from '@components1/common/modal';
import { Text } from '@components1/common';

// Lazy-loaded heavy components — only loaded when actually needed
const ExecutionsView = lazy(() => import('./ExecutionsView'));
const ActionDetailsSidebar = lazy(() => import('./ActionDetailsSidebar'));
const JsonEditorTab = lazy(() => import('./components/JsonEditorTab'));
const WorkflowSettingsModal = lazy(() => import('./components/WorkflowSettingsModal'));
const TriggerConfigSidebar = lazy(() => import('./components/TriggerConfigSidebar'));
const NubiChatSidebar = lazy(() => import('@components1/common/NubiChatSidebar'));
const TestResponseModal = lazy(() => import('./components/TestResponseModal'));
const DryRunResultModal = lazy(() => import('./components/DryRunResultModal'));
const TriggerWorkflowModal = lazy(() => import('./components/TriggerWorkflowModal'));

interface WorkflowData {
  id: string | null;
  name: string;
  status?: WorkflowStatus;
  definition: {
    version: string;
    timeout: string;
    inputs: any[];
    tasks: any[];
    triggers: Array<{ type: string; params: any; layout?: { x: number; y: number } }>;
    output: any;
    // set_execution_tags?: string[];
    retry_policy: {
      maximum_attempts: number;
      initial_interval: string;
      maximum_interval: string;
      backoff_coefficient: number;
    };
    layout?: { viewport?: { x: number; y: number; zoom: number } };
  };
  tags: Record<string, any>;
}

/** Filter tasks to only those in the included set and clean up dangling depends_on references. */
const filterTasksForPartialRun = (tasks: any[], includedIds: Set<string>): any[] =>
  tasks
    .filter((t: any) => includedIds.has(t.id))
    .map((t: any) => {
      if (!t.depends_on || t.depends_on.length === 0) return t;
      const cleanedDeps = t.depends_on.filter((dep: string) => includedIds.has(dep));
      return { ...t, depends_on: cleanedDeps.length > 0 ? cleanedDeps : undefined };
    });

function getEdgeStrokeColor(isSwitchEdge: boolean, isSwitchDefault: boolean, hasCondition: boolean, parsedCondition: any): string {
  if (isSwitchEdge) {
    return isSwitchDefault ? '#9CA3AF' : '#8B5CF6';
  }
  if (hasCondition && parsedCondition) {
    return parsedCondition.color;
  }
  return 'rgb(192, 192, 192)';
}

function rejectConnection(sourceNodeId: string, targetNodeId: string, setNodes: any) {
  const markRejection = (rejected: boolean) => (nodesSnapshot: any[]) =>
    nodesSnapshot.map((node) =>
      node.id === targetNodeId || node.id === sourceNodeId ? { ...node, data: { ...node.data, connectionRejected: rejected } } : node
    );

  setNodes(markRejection(true));
  setTimeout(() => setNodes(markRejection(false)), 800);
}

/** Calculate horizontal offset for nodes created from switch handles so they spread out. */
function getSwitchHandleXOffset(sourceNode: any, sourceHandleId: string | undefined): number {
  if (sourceNode.type !== 'switch' || !sourceHandleId?.startsWith('switch-')) return 0;

  const cases: Array<{ value: string }> = sourceNode.data?.taskConfig?.config?.cases || [];
  const validCases = cases.filter((c: { value: string }) => c.value);
  const totalHandles = validCases.length + 1; // +1 for default
  let handleIndex = totalHandles - 1; // default is last

  if (sourceHandleId !== 'switch-default') {
    const caseValue = sourceHandleId.replace('switch-case-', '');
    const found = validCases.findIndex((c: { value: string }) => c.value === caseValue);
    handleIndex = found === -1 ? 0 : found;
  }

  const centerIndex = (totalHandles - 1) / 2;
  return (handleIndex - centerIndex) * 350;
}

function buildNewEdge(connection: any, nodes: any[]) {
  const isSwitchEdge = connection.sourceHandle?.startsWith('switch-');
  const isSwitchDefault = connection.sourceHandle === 'switch-default';

  const targetNode = nodes.find((n: any) => n.id === connection.target);
  const targetCondition = targetNode?.data?.taskConfig?.if;
  const hasCondition = !isSwitchEdge && hasConditionalStyling(targetCondition);
  const parsedCondition = hasCondition ? parseConditionLabel(targetCondition) : null;

  return {
    id: `edge_${connection.source}_${connection.target}_${Date.now()}`,
    source: connection.source,
    target: connection.target,
    sourceHandle: connection.sourceHandle,
    targetHandle: connection.targetHandle,
    type: hasCondition ? 'conditional' : 'smoothstep',
    ...(isSwitchEdge ? { label: isSwitchDefault ? 'default' : connection.sourceHandle?.replace('switch-case-', '') } : {}),
    ...(hasCondition && parsedCondition
      ? {
          data: {
            ...parsedCondition,
            condition: targetCondition,
          },
        }
      : {}),
    style: {
      strokeWidth: isSwitchEdge || hasCondition ? 2 : 1,
      stroke: getEdgeStrokeColor(isSwitchEdge, isSwitchDefault, hasCondition, parsedCondition),
      ...(isSwitchDefault ? { strokeDasharray: '5 5' } : {}),
    },
  };
}

type WorkflowBuilderMode = 'editor' | 'json' | 'executions';

interface WorkflowBuilderNotebookProps {
  mode?: 'create' | 'edit';
}

const WorkflowBuilderNoteBook: React.FC<WorkflowBuilderNotebookProps> = ({ mode }) => {
  const router = useRouter();
  const { assistantName } = useTenantBranding();
  const [currentMode, setCurrentMode] = useState<WorkflowBuilderMode>('editor');
  const [jsonPanelVisible, setJsonPanelVisible] = useState<boolean>(false); // Control JSON panel visibility

  const [jsonWindowWidth, setJsonWindowWidth] = useState(400);
  const [nubiChatWindowWidth, setNubiChatWindowWidth] = useState(500);

  // Refs for drag resize
  const isResizingRef = useRef<'nubi' | 'json' | null>(null);
  const startXRef = useRef(0);
  const startWidthRef = useRef(0);

  const handleResizeMouseDown = useCallback(
    (panel: 'nubi' | 'json') => (e: React.MouseEvent) => {
      e.preventDefault();
      e.stopPropagation();
      isResizingRef.current = panel;
      startXRef.current = e.clientX;
      startWidthRef.current = panel === 'nubi' ? nubiChatWindowWidth : jsonWindowWidth;

      const handleMouseMove = (moveEvent: MouseEvent) => {
        if (!isResizingRef.current) return;
        const delta = moveEvent.clientX - startXRef.current;
        const newWidth = isResizingRef.current === 'nubi' ? startWidthRef.current + delta : startWidthRef.current - delta;
        const clampedWidth = Math.max(300, Math.min(800, newWidth));
        if (isResizingRef.current === 'nubi') {
          setNubiChatWindowWidth(clampedWidth);
        } else {
          setJsonWindowWidth(clampedWidth);
        }
      };

      const handleMouseUp = () => {
        isResizingRef.current = null;
        document.removeEventListener('mousemove', handleMouseMove);
        document.removeEventListener('mouseup', handleMouseUp);
        document.body.style.cursor = '';
        document.body.style.userSelect = '';
        // Recenter workflow after drag resize
        reactFlowInstanceRef.current?.fitView({
          padding: 0.15,
          maxZoom: 0.9,
          minZoom: 0.75,
          duration: 300,
        });
      };

      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
      document.addEventListener('mousemove', handleMouseMove);
      document.addEventListener('mouseup', handleMouseUp);
    },
    [nubiChatWindowWidth, jsonWindowWidth]
  );

  // Get workflow ID from URL parameters
  const workflowId = router.query.workflowId as string;
  const accountId = router.query.accountId as string;

  // State flag to track workflow creation transition
  const [isTransitioningFromCreateToEdit, setIsTransitioningFromCreateToEdit] = useState(false);

  // Determine if this is a new workflow, accounting for transition state
  const isNewWorkflow = (workflowId === 'new' || mode === 'create') && !isTransitioningFromCreateToEdit;

  // Workflow-specific state
  const [workflowData, setWorkflowData] = useState<WorkflowData | null>(null);
  const workflowDataRef = useRef(workflowData);
  useEffect(() => {
    workflowDataRef.current = workflowData;
  }, [workflowData]);
  const [loading, setLoading] = useState<boolean>(true);
  // Tracks whether the workflow has been fully loaded and state is ready for unsaved changes tracking
  const [isInitialized, setIsInitialized] = useState<boolean>(false);

  const [workflowSettings, setWorkflowSettings] = useState<WorkflowSettings>({
    timeout: '5m',
    maxInterval: '1m',
    retries: 3,
    inputs: [],
    outputs: {},
    tags: [],
    status: 'DRAFT',
  });
  const workflowSettingsRef = useRef(workflowSettings);
  useEffect(() => {
    workflowSettingsRef.current = workflowSettings;
  }, [workflowSettings]);

  // Trigger modal state for Run/DryRun input collection
  const [triggerModalOpen, setTriggerModalOpen] = useState(false);
  const [triggerModalMode, setTriggerModalMode] = useState<'run' | 'dryrun'>('run');
  const [triggerModalLoading, setTriggerModalLoading] = useState(false);

  // DEPRECATED: Replaced with aiWorkflowInitializedRef (see line 162)
  // const [aiWorkflowInitialized, setAiWorkflowInitialized] = useState<boolean>(false);

  // Update workflow settings from loaded workflow data
  useEffect(() => {
    if (workflowData?.definition) {
      setWorkflowSettings((prev) => {
        return {
          timeout: workflowData.definition.timeout ?? prev.timeout,
          maxInterval: workflowData.definition.retry_policy?.maximum_interval ?? prev.maxInterval,
          retries: workflowData.definition.retry_policy?.maximum_attempts ?? prev.retries,
          inputs: workflowData.definition.inputs ?? prev.inputs,
          outputs: workflowData.definition.output ?? prev.outputs,
          tags: Object.entries(workflowData.tags || {}).map(([key, value]) => (value ? `${key}:${value}` : key)),
          status: workflowData.status ?? prev.status,
        };
      });

      // Extract ai_session_id from tags for existing AI-generated workflows
      const aiTag = workflowData.tags?.ai_session_id;
      if (aiTag && !aiSessionId) {
        setAiSessionId(aiTag);
      }
    }
  }, [workflowData]);

  // Initialize currentMode from URL hash fragment (e.g. #editor, #executions)
  useEffect(() => {
    const hash = router.asPath.split('#')[1];
    if (hash && (hash === 'editor' || hash === 'json' || hash === 'executions')) {
      setCurrentMode(hash);
    }
  }, [router.asPath]);

  // Removed actionsList state as it's no longer needed for workflow tasks
  const [nodes, setNodes] = useState<Node[]>([]);
  const nodesRef = useRef(nodes);
  useEffect(() => {
    nodesRef.current = nodes;
  }, [nodes]);
  const [edges, setEdges] = useState<Edge[]>([]);
  const [autoFitViewport, setAutoFitViewport] = useState<{ x: number; y: number; zoom: number } | null>(null);
  const [persistedViewport, setPersistedViewport] = useState<{ x: number; y: number; zoom: number } | null>(null);

  // ReactFlow instance ref for manual fitView control
  const reactFlowInstanceRef = useRef<ReactFlowInstance | null>(null);
  const hasInitiallyFit = useRef(false);

  // Dynamic categories state
  const [dynamicCategories, setDynamicCategories] = useState<NodeCategories>(nodeCategories);
  const [taskDefinitions, setTaskDefinitions] = useState<any[]>([]);

  // Template state
  const templateInitializedRef = useRef(false);

  // AI Conversation stateƒ
  const [aiSessionId, setAiSessionId] = useState<string>('');

  // NuBi Chat Sidebar state
  const [showNubiChat, setShowNubiChat] = useState(false); // Overlay mode - hidden by default
  const [nubiChatFeatureEnabled, setNubiChatFeatureEnabled] = useState(false); // Feature flag for NubiChat
  const [nubiChatContext, setNubiChatContext] = useState<{
    type: 'workflow' | 'workflowbuilder';
    data?: any;
  }>({
    type: 'workflow',
    data: {},
  });
  const [nubiChatSuffix] = useState<string>('');
  const [configWarningDismissed, setConfigWarningDismissed] = useState(false);

  // Keep nubiChatContext in sync with workflowData (id + definition)
  // so that NuBi chat always sends the current workflow context to the backend
  useEffect(() => {
    if (!workflowData) {
      return;
    }
    setNubiChatContext((prev) => {
      const newId = workflowData.id || undefined;
      const newDef = workflowData.definition || undefined;
      if (prev.data?.id === newId && prev.data?.definition === newDef) {
        return prev;
      }
      return {
        ...prev,
        data: { ...prev.data, id: newId, definition: newDef },
      };
    });
  }, [workflowData?.id, workflowData?.definition]);

  // Detect config placeholders in AI-generated workflows
  const configPlaceholders = useMemo(() => {
    if (!workflowData?.definition?.tasks || !aiSessionId) {
      return [];
    }
    const placeholders = new Set<string>();
    const configRegex = /\{\{\s*Configs[.[']+(\w+)/g;
    const scanValue = (val: unknown) => {
      if (typeof val === 'string') {
        let match;
        while ((match = configRegex.exec(val)) !== null) {
          placeholders.add(match[1]);
        }
      } else if (typeof val === 'object' && val !== null) {
        Object.values(val).forEach(scanValue);
      }
    };
    workflowData.definition.tasks.forEach((task: any) => {
      if (task.params) {
        scanValue(task.params);
      }
    });
    return Array.from(placeholders);
  }, [workflowData?.definition?.tasks, aiSessionId]);

  // Check NubiChat feature flag on mount
  useEffect(() => {
    const checkNubiChatFeatureAccess = async () => {
      try {
        const hasAccess = await hasFeatureAccess('WORKFLOWS');
        setNubiChatFeatureEnabled(hasAccess);
      } catch (error) {
        console.error('Error checking NubiChat feature access:', error);
        setNubiChatFeatureEnabled(false);
      }
    };

    checkNubiChatFeatureAccess();
  }, []);

  // Adjust viewport when JSON panel or NubiChat visibility changes
  useEffect(() => {
    if (reactFlowInstanceRef.current && nodes.length > 0 && hasInitiallyFit.current) {
      const timer = setTimeout(() => {
        // Always recenter workflow when sidebars change to keep it centered
        const rfInstance = reactFlowInstanceRef.current;
        if (rfInstance) {
          // Always call fitView to recenter the workflow when sidebars open/close
          // This ensures the workflow stays centered regardless of which sidebars are visible
          rfInstance.fitView({
            padding: 0.15,
            maxZoom: 0.9,
            minZoom: 0.75,
            duration: 300,
          });
        }
      }, 350); // Wait for CSS transitions to complete (300ms + buffer)

      return () => clearTimeout(timer);
    }
  }, [jsonPanelVisible, showNubiChat]); // Only react to sidebar changes, not node changes

  // Apply autoFitViewport when it changes (from JSON apply, workflow load, or AI generation)
  useEffect(() => {
    if (autoFitViewport && reactFlowInstanceRef.current && nodes.length > 0) {
      const timer = setTimeout(() => {
        // Use fitView to center all nodes properly
        // Must wait for all sidebar transitions and canvas resize to complete
        reactFlowInstanceRef.current?.fitView({
          padding: 0.4,
          maxZoom: 0.9,
          minZoom: 0.75,
          duration: 400,
        });
        // Clear the viewport after applying to prevent re-application
        setAutoFitViewport(null);
        hasInitiallyFit.current = true;
      }, 500); // Wait longer for all CSS transitions (300ms) + rendering + sidebar states

      return () => clearTimeout(timer);
    }
  }, [autoFitViewport, nodes.length]);

  // Restore persisted viewport (from workflow definition.layout.viewport) without re-fitting.
  useEffect(() => {
    if (persistedViewport && reactFlowInstanceRef.current && nodes.length > 0) {
      const timer = setTimeout(() => {
        reactFlowInstanceRef.current?.setViewport(persistedViewport, { duration: 0 });
        setPersistedViewport(null);
        hasInitiallyFit.current = true;
      }, 500);

      return () => clearTimeout(timer);
    }
  }, [persistedViewport, nodes.length]);

  // Use ref instead of state to track AI workflow initialization (synchronous, no re-renders)
  const aiWorkflowInitializedRef = useRef(false);

  // JSON Editor sync hook - handles all JSON editor state and synchronization
  const {
    jsonEditorText,
    jsonValid,
    jsonParseError,
    jsonHasUnsavedChanges,
    isApplyingJson,
    lastAppliedSource,
    jsonBeforeLlmApply,
    handleJsonChangeWithRevertClear,
    applyJsonToWorkflow,
    revertLastLlmApply,
  } = useJsonEditorSync({
    nodes,
    edges,
    workflowSettings,
    workflowData,
    taskDefinitions,
    currentMode,
    loading,
    setNodes,
    setEdges,
    setWorkflowData,
    setWorkflowSettings,
    setAutoFitViewport,
    setCurrentMode,
    setJsonPanelVisible,
  });

  // Load task definitions on mount
  useEffect(() => {
    const loadTaskDefinitions = async () => {
      try {
        const response = (await apiWorkflow.listTaskDefinitions()) as TaskDefinitionAPIResponse;

        if (response?.data?.workflow_list_taskdefinitions?.tasks) {
          const tasks = response.data.workflow_list_taskdefinitions.tasks;

          // Patch task definitions to ensure Python support in scripting tasks
          tasks.forEach((task) => {
            if (task.name === 'scripting.run_script' && task.input_schema?.language?.enum) {
              const languages = task.input_schema.language.enum;
              if (!languages.includes('python')) {
                languages.push('python');
              }
            }
          });

          // Store task definitions for schema viewer
          setTaskDefinitions(tasks);

          // Generate dynamic categories from task definitions
          const categories = generateNodeCategories(tasks);
          setDynamicCategories(categories);
        }
      } catch (error) {
        console.error('Failed to load task definitions:', error);
        // Keep default static categories on error
      }
    };

    loadTaskDefinitions();
  }, []);
  // Load workflow on mount or when IDs change
  useEffect(() => {
    // Skip loading if we're in transition state to prevent conflicts
    if (isTransitioningFromCreateToEdit) {
      return;
    }

    if (isNewWorkflow) {
      // Skip if AI workflow already initialized (prevent re-initialization when taskDefinitions loads)
      if (aiWorkflowInitializedRef.current) {
        setLoading(false);
        return;
      }

      setLoading(true);

      // Check if this is an AI-generated workflow
      const aiGeneratedParam = router.query.aiGenerated as string;
      const loadFromAI = router.query.loadFromAI === 'true';

      // Handle AI-generated workflows (both URL param and sessionStorage methods)
      if ((aiGeneratedParam || loadFromAI) && taskDefinitions.length > 0) {
        try {
          let workflowJson: string | null = null;

          // Method 1: sessionStorage (preferred - no URL length limit)
          if (loadFromAI) {
            workflowJson = sessionStorage.getItem('aiGeneratedWorkflow');

            if (!workflowJson) {
              console.warn('No AI workflow data found in sessionStorage - it may have been already loaded. Creating empty workflow.');
              // Don't throw error - just fall through to create empty workflow
              // This happens when user refreshes the page after loading AI workflow
              throw new Error('ALREADY_LOADED');
            }
          }
          // Method 2: URL parameter (backward compatibility)
          else if (aiGeneratedParam) {
            workflowJson = decodeURIComponent(aiGeneratedParam);
          }

          if (!workflowJson) {
            console.warn('No workflow data available');
            throw new Error('NO_DATA');
          }

          // Build the response structure expected by loadWorkflowFromAI
          // Use session_id for NuBi chat — NubiChatSidebar passes this as sessionId to
          // KubernetesLLMResponseGeneratorV2, which queries the DB via session_id field.
          // Using conversation_id (DB id column) here would cause the lookup to fail.
          const sessionId = sessionStorage.getItem('aiSessionId') || '';
          const initialQuery = sessionStorage.getItem('aiInitialQuery') || 'Generate workflow';

          const aiResponse: AIGenerateWorkflowResponse = {
            data: {
              ai_generate_workflow: {
                data: {
                  response: [workflowJson],
                  query: initialQuery,
                  chain_name: 'workflow_builder',
                  conversation_id: sessionId,
                  session_id: sessionId,
                  message_id: '',
                  agent_id: '',
                  status: 'COMPLETED',
                },
              },
            },
          };

          // Store session_id for chat continuation
          setAiSessionId(sessionId);

          // Mark AI workflow as initialized BEFORE loading to prevent re-initialization (using ref = synchronous)
          aiWorkflowInitializedRef.current = true;

          // Use the loadWorkflowFromAI function to process and load the workflow
          // Pass sessionId directly to avoid stale closure (setAiSessionId is async)
          loadWorkflowFromAI(aiResponse, sessionId);

          // Clean up sessionStorage after successful load
          if (loadFromAI) {
            sessionStorage.removeItem('aiGeneratedWorkflow');
            sessionStorage.removeItem('aiInitialQuery');
            // Keep conversation context for chat continuation
            // Will be cleared when component unmounts or user starts new workflow

            // Remove loadFromAI from URL to prevent re-load on refresh
            const newQuery = { ...router.query };
            delete newQuery.loadFromAI;
            router.replace(
              {
                pathname: router.pathname,
                query: newQuery,
              },
              undefined,
              { shallow: true }
            );
          }

          setLoading(false);
          setIsInitialized(true);
          return;
        } catch (error) {
          const errorMessage = error instanceof Error ? error.message : 'Unknown error';

          // Handle expected errors gracefully
          if (errorMessage === 'ALREADY_LOADED' || errorMessage === 'NO_DATA') {
            console.warn('AI workflow data not available, falling through to create empty workflow');
            // Don't show error to user - this is expected when refreshing page
          } else {
            console.error('Failed to load AI-generated workflow:', error);
            snackbar.error('Failed to load AI-generated automation. Creating empty automation instead.');
          }

          // Clean up failed sessionStorage data
          sessionStorage.removeItem('aiGeneratedWorkflow');

          // Remove loadFromAI from URL
          if (loadFromAI) {
            const newQuery = { ...router.query };
            delete newQuery.loadFromAI;
            router.replace(
              {
                pathname: router.pathname,
                query: newQuery,
              },
              undefined,
              { shallow: true }
            );
          }

          // Fall through to create empty workflow
        }
      }

      // Handle template-loaded workflows (sessionStorage method, similar to AI pattern)
      const loadFromTemplate = router.query.loadFromTemplate === 'true';
      if (loadFromTemplate && taskDefinitions.length > 0 && !templateInitializedRef.current) {
        try {
          const templateJson = sessionStorage.getItem('templateWorkflow');
          if (templateJson) {
            const templateData = JSON.parse(templateJson);
            const definition = templateData.definition || {};

            // Build workflow data with null id (new workflow)
            const templateWorkflowData: WorkflowData = {
              id: null,
              name: templateData.name || 'New Automation',
              definition: {
                version: definition.version || 'v1',
                timeout: definition.timeout || '',
                inputs: definition.inputs || [],
                tasks: definition.tasks || [],
                triggers: definition.triggers || [{ type: 'manual', params: {} }],
                output: definition.output || {},
                retry_policy: definition.retry_policy || {
                  maximum_attempts: 3,
                  initial_interval: '1s',
                  maximum_interval: '',
                  backoff_coefficient: 2.0,
                },
              },
              tags: templateData.tags || {},
            };

            templateInitializedRef.current = true;
            setWorkflowData(templateWorkflowData);

            // Convert to ReactFlow nodes/edges
            const {
              nodes: templateNodes,
              edges: templateEdges,
              viewport,
            } = convertWorkflowToReactFlow(
              templateWorkflowData.definition,
              {
                minHorizontalSpacing: 250,
                minVerticalSpacing: 180,
                minTriggerSpacing: 250,
                minConditionalSpacing: 120,
              },
              taskDefinitions
            );
            setNodes(templateNodes);
            setEdges(templateEdges);
            const templatePersistedViewport = templateWorkflowData.definition.layout?.viewport;
            if (templatePersistedViewport) {
              setPersistedViewport(templatePersistedViewport);
            } else if (viewport) {
              setAutoFitViewport(viewport);
            }

            // Run per-task validation to flag nodes with missing required fields
            const invalidTaskNames: string[] = [];
            templateNodes.forEach((node) => {
              if (node.type === 'action' && node.data?.taskConfig && !node.data.taskConfig.valid) {
                invalidTaskNames.push(node.data.taskConfig.id || node.data.label || node.data.taskConfig.type);
              }
            });
            if (invalidTaskNames.length > 0) {
              snackbar.error(`${invalidTaskNames.length} task(s) have missing required fields: ${invalidTaskNames.join(', ')}`);
            }

            // Clean up sessionStorage
            sessionStorage.removeItem('templateWorkflow');

            // Remove loadFromTemplate from URL
            const newQuery = { ...router.query };
            delete newQuery.loadFromTemplate;
            router.replace({ pathname: router.pathname, query: newQuery }, undefined, { shallow: true });

            snackbar.success(`Template "${templateData.name}" loaded successfully`);
            setLoading(false);
            setIsInitialized(true);
            return;
          }
        } catch (error) {
          console.error('Failed to load template workflow:', error);
          snackbar.error('Failed to load template. Creating empty automation instead.');
          sessionStorage.removeItem('templateWorkflow');
        }
      }

      // If taskDefinitions not loaded yet and we have template parameter, wait
      if (loadFromTemplate && taskDefinitions.length === 0) {
        setLoading(false);
        return;
      }

      // If taskDefinitions not loaded yet and we have AI parameter, wait
      if ((aiGeneratedParam || loadFromAI) && taskDefinitions.length === 0) {
        setLoading(false);
        return;
      }

      // Don't create empty workflow if AI workflow or template was already initialized
      if (aiWorkflowInitializedRef.current || templateInitializedRef.current) {
        setLoading(false);
        return;
      }

      // Create empty workflow (default behavior)
      const newWorkflowData: WorkflowData = {
        id: null,
        name: 'New Automation',
        definition: {
          version: 'v1',
          timeout: '',
          inputs: [],
          tasks: [],
          triggers: [{ type: 'manual', params: {} }],
          output: {},
          retry_policy: {
            maximum_attempts: 3,
            initial_interval: '1s',
            maximum_interval: '',
            backoff_coefficient: 2.0,
          },
        },
        tags: {},
      };

      setWorkflowData(newWorkflowData);
      setNodes([]);
      setEdges([]);
      setLoading(false);
      setIsInitialized(true);
    } else if (workflowId && accountId) {
      // Skip redundant reload for existing workflows that are already initialized
      // (taskDefinitions.length change triggers this effect but existing workflows don't need reloading)
      if (isInitialized && workflowData?.id) {
        return;
      }

      const loadData = async () => {
        setLoading(true);
        try {
          const response: any = await apiWorkflow.getWorkflowById(accountId, workflowId);

          const errorMessage = parseHttpResponseBodyMessage(response);
          if (errorMessage) {
            snackbar.error(errorMessage);
            console.error('Workflow load error:', response.errors);
            return;
          }

          const workflow = response.data?.workflow_get;
          if (workflow) {
            setWorkflowData(workflow);

            let taskCount = 0;
            let triggerCount = 0;

            // Convert workflow definition to nodes/edges format
            if (workflow.definition) {
              const {
                nodes,
                edges: convertedEdges,
                viewport,
              } = convertWorkflowToReactFlow(
                workflow.definition,
                {
                  minHorizontalSpacing: 250,
                  minVerticalSpacing: 180,
                  minTriggerSpacing: 250,
                  minConditionalSpacing: 120,
                },
                taskDefinitions
              );
              setNodes(nodes);
              setEdges(convertedEdges);
              const loadedPersistedViewport = workflow.definition?.layout?.viewport;
              if (loadedPersistedViewport) {
                setPersistedViewport(loadedPersistedViewport);
                setAutoFitViewport(null);
              } else {
                setAutoFitViewport(viewport || null);
              }

              // Calculate task and trigger counts
              taskCount = nodes.filter((n: any) => n.type === 'action').length;
              triggerCount = nodes.filter((n: any) => n.type === 'trigger').length;
            }

            // Open NubiChat with full workflow context for existing workflows

            // Generate initial message with workflow details
            let workflowJson = '';
            try {
              const workflowObject: any = {
                id: workflow.id,
                name: workflow.name,
                definition: workflow.definition,
                tags: workflow.tags,
              };
              if (workflow.status) {
                workflowObject.status = workflow.status;
              }
              workflowJson = JSON.stringify(workflowObject, null, 2);
            } catch (jsonError) {
              console.error('Failed to create workflow JSON:', jsonError);
            }

            setNubiChatContext({
              type: 'workflow',
              data: {
                name: workflow.name,
                id: workflow.id,
                status: workflow.status,
                taskCount,
                triggerCount,
                definition: workflow.definition,
                workflowJson,
                conversationId: workflow.tags?.ai_session_id || aiSessionId,
              },
            });
            setIsInitialized(true);
          } else {
            snackbar.error('Failed to load automation');
            router.push('/auto-pilot?accountId=' + accountId + '#workflow');

            setLoading(false);
          }
        } catch (error) {
          console.error('Error loading workflow:', error);
          snackbar.error('Failed to load automation');
          router.push('/auto-pilot?accountId=' + accountId + '#workflow');
        } finally {
          setLoading(false);
        }
      };
      loadData();
    }
  }, [
    isNewWorkflow,
    workflowId,
    accountId,
    isTransitioningFromCreateToEdit,
    router.query.aiGenerated,
    router.query.loadFromAI,
    router.query.loadFromTemplate,
    taskDefinitions.length,
    // Note: aiWorkflowInitializedRef and templateInitializedRef are refs, not in dependencies
  ]);

  // Reset AI/template workflow flags when navigating to a new non-AI/template workflow
  useEffect(() => {
    if (isNewWorkflow && !router.query.loadFromAI && !router.query.aiGenerated && !router.query.loadFromTemplate) {
      aiWorkflowInitializedRef.current = false;
      templateInitializedRef.current = false;
    }
  }, [isNewWorkflow, router.query.loadFromAI, router.query.aiGenerated, router.query.loadFromTemplate]);

  // Standard React Flow event handlers using built-in utilities.
  // Remove changes (keyboard delete, selection delete) must also scrub any switch-case
  // `next` / `default_next` refs to the removed task ids — the custom per-node delete buttons
  // (ActionNode/SwitchNode) do the same cleanup via their own handlers.
  const onNodesChange = useCallback(
    (changes: any) => {
      const removeChanges = Array.isArray(changes) ? changes.filter((c: any) => c?.type === 'remove' && c?.id) : [];
      setNodes((nds: Node[]) => {
        const applied = applyNodeChanges(changes, nds);
        if (removeChanges.length === 0) return applied;
        const deletedTaskIds = new Set<string>();
        removeChanges.forEach((c: any) => {
          const removed = nds.find((n) => n.id === c.id);
          if (!removed || (removed.type !== 'action' && removed.type !== 'switch')) return;
          const configId = removed.data?.taskConfig?.id;
          if (configId) deletedTaskIds.add(configId);
          deletedTaskIds.add(sanitizeTaskId(removed.id));
        });
        return cleanupSwitchReferencesAfterDelete(applied, deletedTaskIds);
      });
    },
    [setNodes]
  );

  const onEdgesChange = useCallback(
    (changes: any) => {
      setEdges((eds: Edge[]) => applyEdgeChanges(changes, eds));
    },
    [setEdges]
  );

  const onConnect = useCallback(
    (connection: any) => {
      const sourceNodeId = connection.source;
      const targetNodeId = connection.target;

      // Check if this connection would create a cycle
      if (wouldCreateCycle(sourceNodeId, targetNodeId, edges)) {
        rejectConnection(sourceNodeId, targetNodeId, setNodes);
        return;
      }

      const newEdge = buildNewEdge(connection, nodes);
      setEdges((eds: Edge[]) => addEdge(newEdge, eds));
    },
    [edges, nodes, setEdges, setNodes]
  );

  // Filter state for executions
  const [executionStatusFilter, setExecutionStatusFilter] = useState('All');
  const [executionTriggerTypeFilter, _setExecutionTriggerTypeFilter] = useState('All');

  // Helper functions to convert UI values to API values
  const getExecutionStatusFilter = (status: string) => {
    if (status === 'All') {
      return undefined;
    }
    // Map user-friendly names to backend values
    const statusMap: { [key: string]: string } = {
      Running: 'RUNNING',
      Completed: 'COMPLETED',
      Failed: 'FAILED',
      Canceled: 'CANCELED',
      Terminated: 'TERMINATED',
      'Timed Out': 'TIMED_OUT',
      'Continued As New': 'CONTINUED_AS_NEW',
      Unspecified: 'UNSPECIFIED',
    };
    return statusMap[status] || status.toUpperCase();
  };

  const getExecutionTriggerTypeFilter = (type: string) => {
    if (type === 'All') {
      return undefined;
    }
    return type.toLowerCase();
  };

  // Workflow execution data
  const { executionData, executionLoading, fetchExecutionData, hasMore, hasPrevious, loadingMore, goToNextPage, goToPreviousPage } = useExecutionData(
    {
      workflowId,
      accountId,
      currentMode,
      workflowDataId: workflowData?.id || undefined,
      status: getExecutionStatusFilter(executionStatusFilter),
      triggerType: getExecutionTriggerTypeFilter(executionTriggerTypeFilter),
    }
  );

  // Workflow execution state
  const runbookId = workflowId;
  const [isTestRunning, setIsTestRunning] = useState(false);
  const isTestRunningRef = useRef(false);
  const currentExecutionIdRef = useRef<string | null>(null);
  const [pollingInterval, setPollingInterval] = useState<any>(null);

  // Dry run state
  const [isDryRunning, setIsDryRunning] = useState(false);
  const dryRunPollingRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const [dryRunResult, setDryRunResult] = useState<any>(null);
  const [showDryRunModal, setShowDryRunModal] = useState(false);

  // Test task response dialog state
  const [testResponseDialog, setTestResponseDialog] = useState<{
    open: boolean;
    taskType: string;
    responseData: any;
  }>({
    open: false,
    taskType: '',
    responseData: null,
  });
  // Removed actionDataMap as it's no longer needed with WorkflowActionRenderer

  // Trigger selector popup state
  const [triggerPopupOpen, setTriggerPopupOpen] = useState(false);

  // Settings modal state (moved from WorkflowHeader to support bottom toolbar)
  const [isSettingsModalOpen, setIsSettingsModalOpen] = useState(false);

  // Track context when opening sidebar from half-edge or edge add button
  const [addNodeIntent, setAddNodeIntent] = useState<{
    type: 'half-edge' | 'edge-insert';
    sourceNodeId?: string;
    sourceHandleId?: string;
    edgeId?: string;
    edgeSource?: string;
    edgeTarget?: string;
    edgeSourceHandle?: string;
    edgeData?: any;
    edgeStyle?: any;
    edgeType?: string;
    edgeLabel?: string;
  } | null>(null);

  const {
    selectedNode,
    sidebarOpen,
    setSidebarOpen,
    expandedCategory,
    actionDetailsSidebarOpen,
    setActionDetailsSidebarOpen,
    triggerConfigSidebarOpen,
    selectedActionType,
    setSelectedActionType,
    nodeTypesConfig,
    onNodeClick,
    addNode,
    toggleCategory,
    deselectAllNodes,
    closeTriggerConfigSidebar,
    updateTriggerConfig,
  } = useWorkflowInteractions(dynamicCategories, taskDefinitions);

  // Unsaved changes tracking hook
  const {
    hasUnsavedChanges: _hasUnsavedChanges, // Exposed for future UI indicator
    showUnsavedChangesDialog,
    setShowUnsavedChangesDialog: _setShowUnsavedChangesDialog, // Exposed for manual control if needed
    handleConfirmNavigation,
    handleCancelNavigation,
    updateSavedSnapshot: _updateSavedSnapshot,
    pauseChangeDetection,
    resumeChangeDetection,
  } = useUnsavedChangesTracking({
    nodes,
    edges,
    workflowSettings,
    workflowData,
    loading,
    isInitialized,
  });

  // Function to open dedicated trigger selector popup
  const openSidebarWithTriggers = () => {
    setTriggerPopupOpen(true);
  };

  // Handle tab switching
  const handleTabChange = useCallback(
    (tab: string) => {
      const returningToEditor = tab === 'editor' && currentMode === 'executions';
      setCurrentMode(tab as 'editor' | 'json' | 'executions');
      // When the user comes back from the Executions tab, wipe the execution badges/colors that
      // were painted on the canvas by updateNodeStatusesFromTasks — the editor should present a
      // clean canvas, not a frozen view of the last run.
      if (returningToEditor) {
        setNodes((prev) =>
          prev.map((node) => {
            if (node.type !== 'action' && node.type !== 'switch') return node;
            const { executionStatus, executionDuration, lastExecutionTime, executionOutput, executionError, ...rest } = node.data || {};
            const hadAny =
              executionStatus !== undefined ||
              executionDuration !== undefined ||
              lastExecutionTime !== undefined ||
              executionOutput !== undefined ||
              executionError !== undefined;
            return hadAny ? { ...node, data: rest } : node;
          })
        );
      }
      // Update URL hash to reflect the current tab
      const basePath = router.asPath.split('#')[0];
      router.replace(`${basePath}#${tab}`, undefined, { shallow: true });
    },
    [router, currentMode, setNodes]
  );

  // Save workflow function
  const handlePrettifyLayout = useCallback(() => {
    if (nodes.length === 0) return;

    const strippedTasks = extractTasksFromWorkflowNodes(nodes, edges).map(({ layout: _layout, ...t }) => t);
    const strippedTriggers = extractTriggersFromNodes(nodes).map(({ layout: _layout, ...t }) => t);

    const definition = {
      ...(workflowData?.definition || {}),
      tasks: strippedTasks,
      triggers: strippedTriggers,
    };

    const {
      nodes: newNodes,
      edges: newEdges,
      viewport,
    } = convertWorkflowToReactFlow(
      definition,
      {
        minHorizontalSpacing: 250,
        minVerticalSpacing: 180,
        minTriggerSpacing: 250,
        minConditionalSpacing: 120,
      },
      taskDefinitions
    );

    setNodes(newNodes);
    setEdges(newEdges);
    setPersistedViewport(null);
    setAutoFitViewport(viewport || null);
  }, [nodes, edges, workflowData, taskDefinitions]);

  const handleSaveWorkflow = async () => {
    try {
      // Block if on JSON tab with invalid JSON
      if (currentMode === 'json' && !jsonValid) {
        snackbar.error('Cannot save: JSON is invalid. Please fix errors or switch to Editor tab.');
        return;
      }

      // Block if on JSON tab with unsaved changes
      if (currentMode === 'json' && jsonHasUnsavedChanges) {
        snackbar.error('Cannot save: You have unapplied JSON changes. Click "Apply" first.');
        return;
      }

      // Validate workflow
      const validationErrors = validateWorkflowForSave(workflowData, nodes, (nodes: Node[]) => extractTasksFromWorkflowNodes(nodes, edges));
      if (validationErrors.length > 0) {
        snackbar.error(`Cannot save automation: ${validationErrors.join(', ')}`);
        return;
      }

      // Validate that sum of task timeouts does not exceed workflow timeout
      const taskTimeoutsForValidation = nodes.filter((n) => n.data?.taskConfig?.timeout).map((n) => n.data.taskConfig.timeout as string);
      if (workflowSettings.timeout && taskTimeoutsForValidation.length > 0) {
        const workflowSec = parseDurationToSeconds(workflowSettings.timeout);
        if (!isNaN(workflowSec) && workflowSec > 0) {
          const totalTaskSec = taskTimeoutsForValidation.reduce((sum, t) => {
            const sec = parseDurationToSeconds(t);
            return sum + (isNaN(sec) ? 0 : sec);
          }, 0);
          if (totalTaskSec > 0 && totalTaskSec > workflowSec) {
            snackbar.error(
              'Cannot save: Sum of all task timeouts exceeds the automation timeout. Reduce task timeouts or increase the automation timeout.'
            );
            return;
          }
        }
      }

      // Prepare workflow data using utility function
      const { definition: workflowDefinition } = prepareWorkflowForSave(
        nodes,
        edges,
        (nodes: Node[]) => extractTasksFromWorkflowNodes(nodes, edges),
        extractTriggersFromNodes,
        workflowSettings,
        workflowData?.definition,
        reactFlowInstanceRef.current?.getViewport()
      );

      // Inject ai_session_id tag if this is an AI-generated workflow
      const settingsForSave = { ...workflowSettings };
      if (aiSessionId && !settingsForSave.tags.some((t) => t.startsWith('ai_session_id:'))) {
        settingsForSave.tags = [...settingsForSave.tags, `ai_session_id:${aiSessionId}`];
      }

      if (isNewWorkflow) {
        // Create new workflow using utility function
        const createRequest = createWorkflowCreateRequest(accountId, workflowData?.name || 'New Automation', workflowDefinition, settingsForSave);

        const response: any = await apiWorkflow.createWorkflow(createRequest);

        const errorMessage = parseHttpResponseBodyMessage(response);
        if (errorMessage) {
          snackbar.error(errorMessage);
          console.error('Workflow creation error:', response.errors);
          return;
        }

        const newWorkflowId = response.data?.workflow_create?.id;
        if (newWorkflowId) {
          snackbar.success(`Automation "${workflowData?.name}" created successfully`);

          // Pause change detection during save-and-redirect flow to prevent false positive unsaved changes
          pauseChangeDetection();

          // Set transition flag to prevent state conflicts
          setIsTransitioningFromCreateToEdit(true);

          // Update workflowData with the new ID immediately
          setWorkflowData((prev) => (prev ? { ...prev, id: newWorkflowId } : null));

          // Redirect to edit mode after creation
          router.replace(`/workflow/${newWorkflowId}?accountId=${accountId}`);

          // After URL change, reload the workflow data to ensure complete sync
          setTimeout(async () => {
            try {
              const reloadResponse: any = await apiWorkflow.getWorkflowById(accountId, newWorkflowId);
              const reloadedWorkflow = reloadResponse.data?.workflow_get;

              if (reloadedWorkflow) {
                setWorkflowData(reloadedWorkflow);

                // Update nodes and edges with fresh data
                if (reloadedWorkflow.definition) {
                  const { nodes: reloadedNodes, edges: reloadedEdges } = convertWorkflowToReactFlow(
                    reloadedWorkflow.definition,
                    {
                      minHorizontalSpacing: 250,
                      minVerticalSpacing: 180,
                      minTriggerSpacing: 250,
                      minConditionalSpacing: 120,
                    },
                    taskDefinitions
                  );
                  setNodes(reloadedNodes);
                  setEdges(reloadedEdges);
                }

                // Open NubiChat with workflow context after successful creation
                setNubiChatContext({
                  type: 'workflow',
                  data: {
                    name: reloadedWorkflow.name,
                    id: reloadedWorkflow.id,
                    status: reloadedWorkflow.status,
                    definition: reloadedWorkflow.definition,
                    conversationId: aiSessionId,
                  },
                });
              }

              // Resume change detection after state has stabilized
              resumeChangeDetection();

              // Clear transition flag after successful sync
              setIsTransitioningFromCreateToEdit(false);
            } catch (error) {
              console.error('Error reloading workflow after creation:', error);
              // Resume change detection even if reload fails
              resumeChangeDetection();
              // Clear transition flag even if reload fails
              setIsTransitioningFromCreateToEdit(false);
            }
          }, 500); // Small delay to ensure router update completes
        } else {
          snackbar.error('Failed to get new automation ID');
        }
      } else {
        // Update existing workflow using utility function
        const updateRequest = createWorkflowUpdateRequest(
          accountId,
          workflowData?.id || workflowId,
          workflowData?.name || 'Automation',
          workflowDefinition,
          settingsForSave
        );

        const response: any = await apiWorkflow.updateWorkflow(updateRequest);

        const errorMessage = parseHttpResponseBodyMessage(response);
        if (errorMessage) {
          snackbar.error(errorMessage);
          console.error('Workflow update error:', response.errors);
          console.error('Full error details:', JSON.stringify(response.errors, null, 2));
          console.error('Workflow definition being sent:', workflowDefinition);
          return;
        }

        const updateStatus = response.data?.workflow_update?.definition;
        if (updateStatus === 'success' || updateStatus) {
          snackbar.success(`Automation "${workflowData?.name}" updated successfully`);

          // Pause change detection during reload to prevent false positive unsaved changes
          // (updateSavedSnapshot would capture stale closure values before React re-renders)
          pauseChangeDetection();

          // Reload the workflow data after successful update
          try {
            const reloadResponse: any = await apiWorkflow.getWorkflowById(accountId, workflowId);

            const reloadErrorMessage = parseHttpResponseBodyMessage(reloadResponse);
            if (reloadErrorMessage) {
              console.error('Failed to reload workflow after save:', reloadErrorMessage);
              resumeChangeDetection();
            } else {
              const reloadedWorkflow = reloadResponse.data?.workflow_get;
              if (reloadedWorkflow) {
                setWorkflowData(reloadedWorkflow);

                // Update nodes and edges with reloaded data
                if (reloadedWorkflow.definition) {
                  const { nodes: reloadedNodes, edges: reloadedEdges } = convertWorkflowToReactFlow(
                    reloadedWorkflow.definition,
                    {
                      minHorizontalSpacing: 250,
                      minVerticalSpacing: 180,
                      minTriggerSpacing: 250,
                      minConditionalSpacing: 120,
                    },
                    taskDefinitions
                  );
                  setNodes(reloadedNodes);
                  setEdges(reloadedEdges);
                }

                // Resume change detection - snapshot will be taken on next render with updated state
                resumeChangeDetection();
              } else {
                resumeChangeDetection();
              }
            }
          } catch (reloadError) {
            console.error('Error reloading workflow after save:', reloadError);
            resumeChangeDetection();
          }
        } else {
          snackbar.error('Failed to update automation - no success status returned');
        }
      }
    } catch (error) {
      console.error('Error saving workflow:', error);
      snackbar.error('Error saving automation. Check console for details.');
    }
  };

  // Function to load workflow from AI-generated response
  // This function can be called to populate the workflow builder with an AI-generated workflow
  const loadWorkflowFromAI = useCallback(
    (aiResponse: AIGenerateWorkflowResponse, conversationSessionId?: string) => {
      try {
        // Build workflow from AI response
        const result = buildWorkflowFromAIResponse(aiResponse, taskDefinitions);

        if (!result.success) {
          snackbar.error(result.error || 'Failed to build automation from AI response');
          return;
        }

        // Update workflow data
        if (result.workflowData) {
          setWorkflowData(result.workflowData);
        }

        // Update nodes and edges
        if (result.nodes && result.edges) {
          setNodes(result.nodes);
          setEdges(result.edges);

          // Update viewport if provided
          if (result.viewport) {
            setAutoFitViewport(result.viewport);
          }
        }

        // Extract workflow JSON for NubiChat context
        let workflowJson = '';
        try {
          const workflowObject: any = {
            name: result.workflowData?.name,
            definition: result.workflowData?.definition,
            tags: result.workflowData?.tags,
          };
          if ((result.workflowData as any)?.status) {
            workflowObject.status = (result.workflowData as any).status;
          }
          workflowJson = JSON.stringify(workflowObject, null, 2);
        } catch (jsonError) {
          console.error('Failed to parse workflow JSON for NubiChat:', jsonError);
          // Skip JSON if parsing fails
        }

        // Open NubiChat with workflowbuilder context
        setNubiChatContext({
          type: 'workflowbuilder',
          data: {
            workflowJson,
            conversationId: conversationSessionId || aiSessionId,
          },
        });
        setShowNubiChat(true);

        snackbar.success(`Automation "${result.workflowData?.name}" loaded successfully from AI response`);
      } catch (error) {
        console.error('Error loading workflow from AI response:', error);
        snackbar.error('Failed to load automation from AI response');
      }
    },
    [accountId, taskDefinitions]
  );

  // Helper: get default trigger inputs from manual trigger nodes
  const getDefaultTriggerInputs = useCallback(() => {
    const currentNodes = nodesRef.current;
    const triggerNodes = currentNodes.filter((node) => node.type === 'trigger' && node.data.trigger?.type === 'manual');
    return triggerNodes.length > 0 ? triggerNodes[0].data.trigger?.params?.inputs || {} : {};
  }, []);

  // Helper: get input schema from workflow settings
  const getInputSchema = useCallback(() => {
    const settings = workflowSettingsRef.current;
    if (!settings?.inputs || settings.inputs.length === 0) return [];
    return settings.inputs.map((input) => ({
      id: input.id,
      type: input.type,
      description: input.description || `Input parameter: ${input.id}`,
      default: input.default,
    }));
  }, []);

  // Helper: get primary trigger type
  const getTriggerType = useCallback(() => {
    const currentNodes = nodesRef.current;
    const triggerNodes = currentNodes.filter((node) => node.type === 'trigger');
    if (triggerNodes.length === 0) return 'manual';
    const manualTrigger = triggerNodes.find((n) => n.data.trigger?.type === 'manual');
    return manualTrigger ? 'manual' : triggerNodes[0].data.trigger?.type || 'manual';
  }, []);

  // Validate workflow state before run/dry-run, returns error message or null if valid
  const validateBeforeExecution = useCallback(
    (mode: 'run' | 'dryrun'): string | null => {
      if (mode === 'run' && isTestRunning) return 'Manual run already in progress';
      if (mode === 'dryrun' && (isDryRunning || isTestRunning)) return 'A run is already in progress';
      if (currentMode === 'json' && !jsonValid) return 'JSON is invalid. Please fix errors or switch to Editor tab.';
      if (currentMode === 'json' && jsonHasUnsavedChanges) return 'You have unapplied JSON changes. Click "Apply" first.';
      if (mode === 'run' && workflowSettingsRef.current?.status === 'DRAFT') {
        return 'Automation is in Draft status and cannot be executed. Set the status to Active to run it, or use Dry Run to test.';
      }
      const validationErrors = validateWorkflowForSave(workflowDataRef.current, nodesRef.current, (nodes: Node[]) =>
        extractTasksFromWorkflowNodes(nodes, edges)
      );
      if (validationErrors.length > 0) return `Cannot execute: ${validationErrors.join(', ')}`;
      return null;
    },
    [isTestRunning, isDryRunning, edges]
  );

  // Run button click: validate, save, then open trigger input modal
  const handleRunButtonClick = useCallback(async () => {
    try {
      const validationError = validateBeforeExecution('run');
      if (validationError) {
        snackbar.error(validationError);
        return;
      }

      // Get the latest workflow data from ref
      const currentWorkflowData = workflowDataRef.current;
      const currentNodes = nodesRef.current;

      // Get current workflow settings from ref to avoid stale closure
      const currentWorkflowSettings = workflowSettingsRef.current;

      // Prepare workflow data using utility function with latest nodes from ref
      const { definition: workflowDefinition } = prepareWorkflowForSave(
        currentNodes,
        edges,
        (nodes: Node[]) => extractTasksFromWorkflowNodes(nodes, edges),
        extractTriggersFromNodes,
        currentWorkflowSettings,
        currentWorkflowData?.definition,
        reactFlowInstanceRef.current?.getViewport()
      );

      // Use workflow ID from workflowData if it exists
      let currentWorkflowId = currentWorkflowData?.id || workflowId;

      if (!currentWorkflowData?.id) {
        // Create new workflow first
        const createRequest = createWorkflowCreateRequest(
          accountId,
          currentWorkflowData?.name || 'New Automation',
          workflowDefinition,
          currentWorkflowSettings
        );

        const createResponse: any = await apiWorkflow.createWorkflow(createRequest);

        if (createResponse.errors) {
          snackbar.error(`Failed to create automation: ${createResponse.errors.join(', ')}`);
          console.error('Workflow creation error:', createResponse.errors);
          return;
        }

        currentWorkflowId = createResponse.data?.workflow_create?.id;
        if (!currentWorkflowId) {
          snackbar.error('Failed to get new automation ID');
          return;
        }

        snackbar.success('Automation created and saved successfully');

        // Set transition flag and update workflowData with new ID
        setIsTransitioningFromCreateToEdit(true);
        setWorkflowData((prev) => (prev ? { ...prev, id: currentWorkflowId } : null));

        // Update URL to reflect the new workflow ID
        router.replace(`/workflow/${currentWorkflowId}?accountId=${accountId}`);
      } else {
        // Update existing workflow first
        const updateRequest = createWorkflowUpdateRequest(
          accountId,
          currentWorkflowId,
          currentWorkflowData?.name || 'Automation',
          workflowDefinition,
          currentWorkflowSettings
        );

        const updateResponse: any = await apiWorkflow.updateWorkflow(updateRequest);

        if (updateResponse.errors) {
          snackbar.error('Failed to update automation before manual run');
          console.error('Workflow update error:', updateResponse.errors);
          return;
        }

        snackbar.success('Automation saved successfully');
      }

      // Open the trigger input modal instead of immediately executing
      setTriggerModalMode('run');
      setTriggerModalOpen(true);
    } catch (error) {
      console.error('Error during save before run:', error);
      snackbar.error('Failed to save automation');
    }
  }, [validateBeforeExecution, edges, workflowId, accountId, isNewWorkflow, router, workflowSettings]);

  // Dry Run button click: validate, then open trigger input modal
  const handleDryRunButtonClick = useCallback(async () => {
    const validationError = validateBeforeExecution('dryrun');
    if (validationError) {
      snackbar.error(validationError);
      return;
    }

    // Open the trigger input modal for dry run
    setTriggerModalMode('dryrun');
    setTriggerModalOpen(true);
  }, [validateBeforeExecution]);

  // Helper function to prepare dry-run request with optional task filtering
  const prepareDryRunRequest = useCallback(
    (taskIdsToInclude?: string[], inputsOverride?: any) => {
      const currentNodes = nodesRef.current;
      const currentWorkflowData = workflowDataRef.current;

      // Prepare workflow definition, optionally filtering tasks
      // When filtering, also clean up depends_on references to excluded tasks
      // to avoid sending a broken dependency graph to the server
      const taskExtractor = taskIdsToInclude
        ? (nodes: Node[]) => filterTasksForPartialRun(extractTasksFromWorkflowNodes(nodes, edges), new Set(taskIdsToInclude))
        : (nodes: Node[]) => extractTasksFromWorkflowNodes(nodes, edges);

      const { definition } = prepareWorkflowForSave(
        currentNodes,
        edges,
        taskExtractor,
        extractTriggersFromNodes,
        workflowSettings,
        currentWorkflowData?.definition
      );

      // Use provided inputs override, or extract from manual trigger nodes
      const manualTriggerInputs =
        inputsOverride !== undefined
          ? inputsOverride
          : (() => {
              const triggerNodes = currentNodes.filter((node) => node.type === 'trigger' && node.data.trigger?.type === 'manual');
              return triggerNodes.length > 0 ? triggerNodes[0].data.trigger?.params?.inputs || {} : {};
            })();

      return {
        account_id: accountId,
        definition,
        inputs: manualTriggerInputs,
      };
    },
    [edges, workflowSettings, accountId]
  );

  // Execute a workflow run with user-provided inputs
  const executeRun = useCallback(
    async (inputs: any) => {
      const currentWorkflowId = workflowDataRef.current?.id || workflowId;
      if (!currentWorkflowId) {
        snackbar.error('Cannot trigger automation: No automation ID');
        return;
      }

      // Clear execution states from previous run
      setNodes((prevNodes) =>
        prevNodes.map((node) => {
          if (!(node.type === 'action' || node.type === 'switch') || !node.data.taskConfig) return node;
          const needsIdSync = !node.data.taskConfig.id;
          const needsStatusClear = !!node.data.executionStatus;
          if (!needsIdSync && !needsStatusClear) return node;
          return {
            ...node,
            data: {
              ...node.data,
              taskConfig: needsIdSync ? { ...node.data.taskConfig, id: sanitizeTaskId(node.id) } : node.data.taskConfig,
              ...(needsStatusClear
                ? { executionStatus: undefined, lastExecutionTime: undefined, executionOutput: undefined, executionError: undefined }
                : {}),
            },
          };
        })
      );

      setIsTestRunning(true);
      isTestRunningRef.current = true;

      const response: any = await apiWorkflow.triggerWorkflow({ account_id: accountId, id: currentWorkflowId, inputs });

      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        console.error('Workflow trigger failed with errors:', response.errors);
        snackbar.error(errorMessage);
        setIsTestRunning(false);
        isTestRunningRef.current = false;
        return;
      }

      const triggerData = response?.data?.workflow_trigger;
      if (triggerData?.execution_id) {
        snackbar.success('Automation execution started successfully!');
        fetchExecutionData();
        setTimeout(() => startPollingWorkflowExecution(triggerData.execution_id), 1000);
      } else {
        console.error('No execution ID returned from trigger:', response);
        snackbar.error('Failed to get execution ID from automation trigger');
        setIsTestRunning(false);
        isTestRunningRef.current = false;
      }
    },
    [workflowId, accountId, fetchExecutionData]
  );

  // Poll for dry run completion
  const startPollingDryRun = useCallback(
    (dryrunId: string, executionId: string) => {
      // Clear any existing dry run polling
      if (dryRunPollingRef.current !== null) {
        clearInterval(dryRunPollingRef.current);
        dryRunPollingRef.current = null;
      }
      const startTime = Date.now();
      const terminalStatuses = ['COMPLETED', 'COMPLETE', 'FAILED', 'COMPLETE_WITH_ERROR', 'CANCELED', 'TERMINATED', 'TIMED_OUT'];
      const poll = async () => {
        if (Date.now() - startTime > 600000) {
          if (dryRunPollingRef.current !== null) {
            clearInterval(dryRunPollingRef.current);
            dryRunPollingRef.current = null;
          }
          setIsDryRunning(false);
          snackbar.error('Dry run timed out');
          return;
        }
        try {
          const response: any = await apiWorkflow.getWorkflowExecution(accountId, dryrunId, executionId);
          const execution = response?.data?.workflow_get_execution;
          if (execution === null || execution === undefined) {
            return;
          }
          if (terminalStatuses.includes(execution.status)) {
            if (dryRunPollingRef.current !== null) {
              clearInterval(dryRunPollingRef.current);
              dryRunPollingRef.current = null;
            }
            const dryRunResponse = {
              status: execution.status,
              output: execution.workflow_result,
              error: execution.error,
              tasks: execution.tasks,
            };
            setDryRunResult(dryRunResponse);
            setShowDryRunModal(true);
            setIsDryRunning(false);
            if (execution.status === 'COMPLETED' || execution.status === 'COMPLETE') {
              snackbar.success('Dry run completed successfully');
            } else if (execution.status === 'FAILED') {
              snackbar.error('Dry run failed: ' + (execution.error || 'Unknown error'));
            }
          }
        } catch (error) {
          console.error('Dry run polling error:', error);
        }
      };
      poll();
      dryRunPollingRef.current = setInterval(poll, 3000);
    },
    [accountId]
  );

  // Execute a dry run with user-provided inputs
  const executeDryRun = useCallback(
    async (inputs: any) => {
      setIsDryRunning(true);

      const request = prepareDryRunRequest(undefined, inputs);
      const response: any = await apiWorkflow.triggerWorkflowDryRun(request);

      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        console.error('Dry-run failed with errors:', response.errors);
        snackbar.error(errorMessage);
        setIsDryRunning(false);
        return;
      }

      const result = response?.data?.workflow_trigger_dryrun;

      // If already terminal (fast failure during validation), show immediately
      if (result?.status === 'COMPLETED' || result?.status === 'FAILED') {
        setDryRunResult(result);
        setShowDryRunModal(true);
        if (result?.status === 'COMPLETED') {
          snackbar.success('Dry run completed successfully');
        } else {
          snackbar.error('Dry run failed: ' + (result?.error || 'Unknown error'));
        }
        setIsDryRunning(false);
        return;
      }

      // Async: start polling
      if (result?.dryrun_id && result?.execution_id) {
        startPollingDryRun(result.dryrun_id, result.execution_id);
      } else {
        snackbar.error('Unexpected dry run response');
        setIsDryRunning(false);
      }
    },
    [prepareDryRunRequest, startPollingDryRun]
  );

  // Handle trigger from modal: execute with user-provided inputs
  const handleTriggerFromModal = useCallback(
    async (inputs: any) => {
      setTriggerModalLoading(true);
      try {
        if (triggerModalMode === 'run') {
          await executeRun(inputs);
        } else {
          await executeDryRun(inputs);
        }
      } catch (error) {
        console.error('Error during trigger from modal:', error);
        snackbar.error(triggerModalMode === 'run' ? 'Failed to run automation' : 'Failed to execute dry run');
        if (triggerModalMode === 'run') {
          setIsTestRunning(false);
          isTestRunningRef.current = false;
        } else {
          setIsDryRunning(false);
        }
      } finally {
        setTriggerModalLoading(false);
        setTriggerModalOpen(false);
      }
    },
    [triggerModalMode, executeRun, executeDryRun]
  );

  // Legacy handleTestRun kept for backward compatibility (trigger node play button)
  const handleTestRun = useCallback(async () => {
    await handleRunButtonClick();
  }, [handleRunButtonClick]);

  // Poll for dry run and resolve promise when complete
  const pollDryRunUntilDone = useCallback(
    (dryrunId: string, executionId: string): Promise<any> => {
      return new Promise((resolve) => {
        const startTime = Date.now();
        const terminalStatuses = ['COMPLETED', 'COMPLETE', 'FAILED', 'COMPLETE_WITH_ERROR', 'CANCELED', 'TERMINATED', 'TIMED_OUT'];
        let interval: ReturnType<typeof setInterval> | null = null;
        const poll = async () => {
          if (Date.now() - startTime > 600000) {
            if (interval !== null) {
              clearInterval(interval);
            }
            snackbar.error('Dry run timed out');
            resolve(null);
            return;
          }
          try {
            const response: any = await apiWorkflow.getWorkflowExecution(accountId, dryrunId, executionId);
            const execution = response?.data?.workflow_get_execution;
            if (execution === null || execution === undefined) {
              return;
            }
            if (terminalStatuses.includes(execution.status)) {
              if (interval !== null) {
                clearInterval(interval);
              }
              resolve({
                status: execution.status,
                output: execution.workflow_result,
                error: execution.error,
                tasks: execution.tasks,
              });
            }
          } catch (error) {
            console.error('Dry run polling error:', error);
          }
        };
        poll();
        interval = setInterval(poll, 3000);
      });
    },
    [accountId]
  );

  // Trigger dry run and wait for result (handles both sync failure and async polling)
  const triggerAndAwaitDryRun = useCallback(
    async (request: any): Promise<any> => {
      const response: any = await apiWorkflow.triggerWorkflowDryRun(request);

      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(errorMessage);
        return null;
      }

      const result = response?.data?.workflow_trigger_dryrun;

      // If already terminal (fast failure during validation), return immediately
      if (result?.status === 'COMPLETED' || result?.status === 'FAILED') {
        return result;
      }

      // Async: poll until complete
      if (result?.dryrun_id && result?.execution_id) {
        return await pollDryRunUntilDone(result.dryrun_id, result.execution_id);
      }

      snackbar.error('Unexpected dry run response');
      return null;
    },
    [pollDryRunUntilDone]
  );

  // Run all previous steps for a given task (dry-run up to but not including the task)
  const handleRunPreviousSteps = useCallback(
    async (currentTaskId: string): Promise<Record<string, any>> => {
      const currentNodes = nodesRef.current;

      // Get all tasks that are predecessors of the current task
      // Sanitize task IDs to match the format used in extractTasksFromWorkflowNodes
      const previousTasks = getPreviousTasksForNode(currentTaskId, currentNodes, edges, taskDefinitions);
      const previousTaskIds = previousTasks.map((t: any) => sanitizeTaskId(t.id));

      if (previousTaskIds.length === 0) {
        snackbar.info('No previous tasks to run');
        return {};
      }

      const request = prepareDryRunRequest(previousTaskIds);
      const result = await triggerAndAwaitDryRun(request);
      if (!result) {
        return {};
      }

      // Extract per-task outputs from response
      const taskOutputs: Record<string, any> = {};
      if (result?.tasks && Array.isArray(result.tasks)) {
        result.tasks.forEach((task: any) => {
          taskOutputs[task.id] = task.output;
        });
      }

      if (result?.status === 'COMPLETED') {
        snackbar.success('Previous steps completed successfully');
      } else if (result?.status === 'FAILED') {
        snackbar.error('Previous steps failed: ' + (result?.error || 'Unknown error'));
      }

      return taskOutputs;
    },
    [edges, taskDefinitions, prepareDryRunRequest, triggerAndAwaitDryRun]
  );

  // Dry-run from start up to and including a specific task. For switch targets, also include
  // direct children so the chosen branch actually runs (the switch alone produces no useful trace).
  const handleDryRunToTask = useCallback(
    async (targetTaskId: string): Promise<any> => {
      const currentNodes = nodesRef.current;

      const previousTasks = getPreviousTasksForNode(targetTaskId, currentNodes, edges, taskDefinitions);
      const targetNode = currentNodes.find((n) => n.id === targetTaskId);
      const switchChildIds =
        targetNode?.type === 'switch' ? getSwitchDryRunEligibility(targetTaskId, currentNodes, edges).childNodeIds.map(sanitizeTaskId) : [];

      // Sanitize task IDs to match the format used in extractTasksFromWorkflowNodes
      const taskIds = [...previousTasks.map((t: any) => sanitizeTaskId(t.id)), sanitizeTaskId(targetTaskId), ...switchChildIds];

      const request = prepareDryRunRequest(taskIds);
      return await triggerAndAwaitDryRun(request);
    },
    [edges, taskDefinitions, prepareDryRunRequest, triggerAndAwaitDryRun]
  );

  // Run a single task in isolation (real execution)
  const handleRunTask = useCallback(
    async (taskType: string, params: any): Promise<any> => {
      try {
        const response: any = await apiWorkflow.triggerTask(accountId, taskType, params);

        // Check for GraphQL-level errors (network, auth, etc)
        const errorMessage = parseHttpResponseBodyMessage(response);
        if (errorMessage) {
          return { error: errorMessage };
        }

        const taskResult = response?.data?.workflow_trigger_task;
        if (taskResult?.status === 'FAILED') {
          return { error: taskResult.error || 'Task execution failed' };
        }
        return taskResult?.result ?? taskResult;
      } catch (error: any) {
        console.error('Failed to run task:', error);
        return { error: error?.message || 'Failed to run task' };
      }
    },
    [accountId]
  );

  // Memoize sidebar derived values keyed off nodes/edges so a `{}` literal
  // isn't returned with a fresh reference every render (which previously
  // re-fired the sidebar's taskData sync effect and the cascading API fetches).
  const sidebarSelectedNode = useMemo(() => nodes.find((node) => node.selected && (node.type === 'action' || node.type === 'switch')), [nodes]);
  const sidebarTaskData = useMemo(() => sidebarSelectedNode?.data?.taskConfig?.config ?? {}, [sidebarSelectedNode]);
  const sidebarValidationErrors = useMemo(() => sidebarSelectedNode?.data?.taskConfig?.errors ?? {}, [sidebarSelectedNode]);
  const sidebarPreviousNodeOutputSchema = useMemo(
    () => (sidebarSelectedNode ? getPreviousNodesOutputSchemas(sidebarSelectedNode.id, edges, nodes, taskDefinitions) : {}),
    [sidebarSelectedNode, edges, nodes, taskDefinitions]
  );

  // Track the sidebar's target node id in a ref so the change handlers stay
  // stable across renders. Without this, including sidebarSelectedNode in
  // the useCallback deps would re-create the handler every time nodes
  // changes, defeating the render-loop fix below.
  const sidebarSelectedNodeIdRef = useRef<string | undefined>(undefined);
  sidebarSelectedNodeIdRef.current = sidebarSelectedNode?.id;

  // Stable handlers for ActionDetailsSidebar so its child effects (which list
  // these in deps) don't re-fire on every parent render. Without useCallback,
  // every WorkflowBuilderNotebook render created new arrow refs, cascading
  // into infinite-render loops in the sidebar's data fetchers. Updates are
  // scoped to the sidebar's specific node id so multi-select doesn't
  // accidentally overwrite siblings' configs.
  const handleSidebarTaskDataChange = useCallback(
    (taskData: any) => {
      const targetId = sidebarSelectedNodeIdRef.current;
      if (!targetId) return;
      setNodes((prevNodes) =>
        prevNodes.map((node) => {
          if (node.id === targetId && (node.type === 'action' || node.type === 'switch') && node.data.taskConfig) {
            const validation = validateTaskData(node.data.taskConfig.type, taskData, taskDefinitions);
            return {
              ...node,
              data: {
                ...node.data,
                taskConfig: {
                  ...node.data.taskConfig,
                  config: taskData,
                  valid: validation.isValid,
                  errors: validation.errors,
                },
              },
            };
          }
          return node;
        })
      );
    },
    [setNodes, taskDefinitions]
  );

  const handleSidebarTaskConfigChange = useCallback(
    (field: string, value: any) => {
      const targetId = sidebarSelectedNodeIdRef.current;
      if (!targetId) return;
      setNodes((prevNodes) =>
        prevNodes.map((node) => {
          if (node.id === targetId && (node.type === 'action' || node.type === 'switch') && node.data.taskConfig) {
            return {
              ...node,
              data: {
                ...node.data,
                taskConfig: {
                  ...node.data.taskConfig,
                  [field]: value ?? undefined,
                },
              },
            };
          }
          return node;
        })
      );
    },
    [setNodes]
  );

  // Start polling for workflow execution status
  const startPollingWorkflowExecution = (executionId: string) => {
    // Store the current execution ID so the cancel handler can access it
    currentExecutionIdRef.current = executionId;

    // Clear any existing polling
    if (pollingInterval) {
      clearInterval(pollingInterval);
    }

    // Refresh executions listing before starting polling to ensure the new execution is visible
    fetchExecutionData();

    // Set poll start time for timeout safety
    const startTime = Date.now();

    const poll = async () => {
      try {
        // Safety timeout - stop polling after 10 minutes
        if (Date.now() - startTime > 600000) {
          stopPolling();
          setIsTestRunning(false);
          isTestRunningRef.current = false;
          return;
        }

        // Check if component is still running test
        if (!isTestRunningRef.current) {
          stopPolling();
          return;
        }

        const response: any = await apiWorkflow.getWorkflowExecution(accountId, workflowId!, executionId);

        if (response?.errors) {
          console.error('Error fetching workflow execution:', response.errors);
          return;
        }

        const executionData = response?.data?.workflow_get_execution;
        if (executionData) {
          // Update node statuses based on task execution data
          if (executionData.tasks && Array.isArray(executionData.tasks)) {
            updateNodeStatusesFromTasks(executionData.tasks);
          }

          // Check if execution is complete (handle all terminal statuses)
          const terminalStatuses = [
            'COMPLETED',
            'COMPLETE',
            'FAILED',
            'COMPLETE_WITH_ERROR',
            'CANCELED',
            'TERMINATED',
            'TIMED_OUT',
            'CONTINUED_AS_NEW',
          ];

          if (terminalStatuses.includes(executionData.status)) {
            stopPolling();
            setIsTestRunning(false);
            isTestRunningRef.current = false;
            currentExecutionIdRef.current = null;
            updateAllNodeStatesToFinalState(executionData.status);

            // Clear transition flag if it was set during test run
            setIsTransitioningFromCreateToEdit(false);

            switch (executionData.status) {
              case 'COMPLETED':
              case 'COMPLETE':
                snackbar.success('Automation execution completed successfully!');
                break;
              case 'COMPLETE_WITH_ERROR':
                snackbar.warning('Automation execution completed with errors');
                break;
              case 'FAILED':
                snackbar.error('Automation execution failed');
                break;
              case 'CANCELED':
                snackbar.info('Automation execution was canceled');
                break;
              case 'TERMINATED':
                snackbar.error('Automation execution was terminated');
                break;
              case 'TIMED_OUT':
                snackbar.error('Automation execution timed out');
                break;
              case 'CONTINUED_AS_NEW':
                snackbar.info('Automation execution continued as new instance');
                break;
              default:
                snackbar.info(`Automation execution finished with status: ${executionData.status}`);
                break;
            }
          }
        }
      } catch (error) {
        console.error('Polling error:', error);
        // Continue polling even on error
      }
    };

    // Poll immediately and then every 4 seconds
    poll();
    const interval = setInterval(poll, 4000);
    setPollingInterval(interval);
  };

  // Cancel the currently running execution
  const handleCancelExecution = useCallback(async () => {
    const execId = currentExecutionIdRef.current;
    if (!execId || !workflowId) return;

    try {
      const response: any = await apiWorkflow.cancelExecution({
        account_id: accountId,
        id: workflowId,
        execution_id: execId,
      });
      const cancelMsg = response?.data?.workflow_cancel_execution?.message;
      if (cancelMsg?.toLowerCase().includes('workflow execution canceled successfully')) {
        snackbar.success(cancelMsg);
      } else {
        snackbar.error(cancelMsg || 'Failed to cancel execution');
      }
      // Keep polling; it will detect the CANCELED terminal status and clean up
    } catch {
      snackbar.error('Failed to cancel execution');
    }
  }, [accountId, workflowId]);

  // Handle individual task testing
  const handleTestTask = useCallback(
    async (taskType: string, params: any) => {
      try {
        snackbar.info(`Starting manual run for task: ${taskType}`);

        const response: any = await apiWorkflow.triggerTask(accountId, taskType, params);

        const errorMessage = parseHttpResponseBodyMessage(response);
        if (errorMessage) {
          console.error('Task trigger failed with errors:', response.errors);
          snackbar.error(errorMessage);
          return;
        }

        const taskResult = response?.data?.workflow_trigger_task;

        // Refresh executions list to show the new task execution
        fetchExecutionData();

        if (taskResult?.status === 'FAILED') {
          snackbar.error(taskResult.error || 'Task execution failed');
          setTestResponseDialog({ open: true, taskType, responseData: taskResult });
          return;
        }

        snackbar.success('Task test completed successfully!');

        if (taskResult) {
          setTestResponseDialog({
            open: true,
            taskType,
            responseData: taskResult,
          });
        }
      } catch (error) {
        console.error('Error testing task:', error);
        snackbar.error('Failed to test task');
      }
    },
    [accountId]
  );

  // Handle trigger run from trigger node
  const handleTriggerRun = useCallback(async () => {
    // Use the same logic as handleTestRun but specifically for trigger nodes
    await handleTestRun();
  }, [handleTestRun]);

  // Handle add node from a half-edge (unconnected source handle)
  const handleAddFromHandle = useCallback(
    (nodeId: string, handleId: string) => {
      setAddNodeIntent({ type: 'half-edge', sourceNodeId: nodeId, sourceHandleId: handleId });
      setSidebarOpen(true);
    },
    [setSidebarOpen]
  );

  // Handle add node on an existing edge (insert between two nodes)
  const handleAddOnEdgeRef = useRef<(edgeId: string) => void>(() => {});
  handleAddOnEdgeRef.current = useCallback(
    (edgeId: string) => {
      const edge = edges.find((e) => e.id === edgeId);
      if (edge) {
        setAddNodeIntent({
          type: 'edge-insert',
          edgeId: edge.id,
          edgeSource: edge.source,
          edgeTarget: edge.target,
          edgeSourceHandle: edge.sourceHandle || undefined,
          edgeData: edge.data,
          edgeStyle: edge.style,
          edgeType: edge.type,
          edgeLabel: typeof edge.label === 'string' ? edge.label : undefined,
        });
        setSidebarOpen(true);
      }
    },
    [edges, setSidebarOpen]
  );
  const stableHandleAddOnEdge = useCallback((edgeId: string) => {
    handleAddOnEdgeRef.current(edgeId);
  }, []);

  // Stable refs for node type callbacks — prevents customNodeTypes from invalidating (React Flow error #002)
  const handleTestTaskRef = useRef(handleTestTask);
  handleTestTaskRef.current = handleTestTask;
  const handleTriggerRunRef = useRef(handleTriggerRun);
  handleTriggerRunRef.current = handleTriggerRun;
  const handleAddFromHandleRef = useRef(handleAddFromHandle);
  handleAddFromHandleRef.current = handleAddFromHandle;
  const accountIdRef = useRef(accountId);
  accountIdRef.current = accountId;

  // Create custom node types — MUST be stable to prevent React Flow error #002 (unmount/remount all nodes)
  const customNodeTypes = useMemo(
    () => ({
      ...nodeTypesConfig,
      action: (props: any) => (
        <ActionNode
          {...props}
          onTestTask={handleTestTaskRef.current}
          accountId={accountIdRef.current}
          onAddFromHandle={handleAddFromHandleRef.current}
        />
      ),
      trigger: (props: any) => <TriggerNode {...props} onTriggerRun={handleTriggerRunRef.current} onAddFromHandle={handleAddFromHandleRef.current} />,
      switch: (props: any) => <SwitchNode {...props} onAddFromHandle={handleAddFromHandleRef.current} />,
    }),
    [nodeTypesConfig]
  );

  // Custom edge types — MUST be stable to prevent React Flow error #002
  const customEdgeTypes = useMemo(
    () => ({
      smoothstep: (props: any) => <DeletableEdge {...props} onAddOnEdge={stableHandleAddOnEdge} />,
      conditional: (props: any) => <ConditionalEdge {...props} onAddOnEdge={stableHandleAddOnEdge} />,
    }),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- stableHandleAddOnEdge is stable (empty-deps useCallback)
    []
  );

  // Update node statuses from workflow task execution data
  const updateNodeStatusesFromTasks = (tasks: any[]) => {
    const taskStatusMap = new Map<string, any>();

    // Create a map of task ID to status (match by ID only, not type)
    // Type-based matching is unreliable when multiple tasks share the same type
    tasks.forEach((task) => {
      taskStatusMap.set(task.id, {
        status: task.status,
        startTime: task.start_time,
        endTime: task.end_time,
        output: task.output,
        error: task.error,
      });
    });

    // Update nodes with execution status. findExecutionTaskForNode handles the switch-renaming
    // quirk (see executor.go:1142) and infers switch-node status from the child that actually ran.
    setNodes((prevNodes) =>
      prevNodes.map((node) => {
        if ((node.type === 'action' || node.type === 'switch') && node.data.taskConfig) {
          const match = findExecutionTaskForNode(node, prevNodes, edges, taskStatusMap);
          if (match) {
            const { task: taskStatus } = match;
            return {
              ...node,
              data: {
                ...node.data,
                executionStatus: taskStatus.status,
                lastExecutionTime: taskStatus.startTime || new Date().toISOString(),
                executionOutput: taskStatus.output,
                executionError: taskStatus.error,
              },
            };
          }
        }
        return node;
      })
    );
  };

  // Stop polling
  const stopPolling = () => {
    if (pollingInterval) {
      clearInterval(pollingInterval);
      setPollingInterval(null);
    }

    fetchExecutionData();
  };

  // Update all nodes to final execution state
  const updateAllNodeStatesToFinalState = (_executionStatus: string) => {
    setNodes((prevNodes) =>
      prevNodes.map((node) => {
        if ((node.type === 'action' || node.type === 'switch') && node.data.taskConfig) {
          // Preserve task-specific status if already set by polling (e.g. COMPLETED, FAILED)
          if (node.data.executionStatus) {
            return node;
          }
          // Tasks that were never reached get SKIPPED status
          return {
            ...node,
            data: {
              ...node.data,
              executionStatus: 'SKIPPED',
              lastExecutionTime: new Date().toISOString(),
            },
          };
        }
        return node;
      })
    );

    // Also update edges to final state
    setEdges((prevEdges) =>
      prevEdges.map((edge) => ({
        ...edge,
        animated: false, // Stop all animations
        style: {
          stroke: 'rgb(175, 175, 175)',
          strokeWidth: 1,
        },
      }))
    );
  };

  // Cleanup polling on unmount or when isTestRunning changes
  useEffect(() => {
    return () => {
      if (pollingInterval) {
        clearInterval(pollingInterval);
      }
    };
  }, [pollingInterval]);

  // Cleanup dry run polling on unmount
  useEffect(() => {
    return () => {
      if (dryRunPollingRef.current) {
        clearInterval(dryRunPollingRef.current);
      }
    };
  }, []);

  // Sync ref with state changes
  useEffect(() => {
    isTestRunningRef.current = isTestRunning;
  }, [isTestRunning]);

  // Safety mechanism: Clear transition flag after a reasonable timeout
  useEffect(() => {
    if (isTransitioningFromCreateToEdit) {
      const timeoutId = setTimeout(() => {
        console.warn('Clearing transition flag due to timeout - this should not normally happen');
        setIsTransitioningFromCreateToEdit(false);
      }, 5000); // 5 second timeout

      return () => clearTimeout(timeoutId);
    }
  }, [isTransitioningFromCreateToEdit]);

  // Stop polling when isTestRunning becomes false (but not on initial render)
  useEffect(() => {
    // Only stop polling if test was running and is now stopped
    // Don't stop on initial mount when isTestRunning is false by default
    if (!isTestRunning && pollingInterval) {
      stopPolling();
    }
  }, [isTestRunning, pollingInterval]);

  // Show loading state while router is not ready or runbookId is not available
  if (!router.isReady || !runbookId) {
    return (
      <Box
        sx={{
          width: '100%',
          height: '100vh',
          display: 'flex',
          justifyContent: 'center',
          alignItems: 'center',
          backgroundColor: 'rgb(243, 243, 243)',
        }}
      >
        <Typography sx={{ color: '#6b7280' }}>Unable to get workflowId from URL params</Typography>
      </Box>
    );
  }

  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        width: '100%',
        height: '100vh',
        overflow: 'hidden',
      }}
    >
      {/* Header Component - Spans entire width */}
      <WorkflowHeader
        workflowTitle={workflowData?.name || (isNewWorkflow ? 'New Automation' : 'Loading Automation...')}
        onTabChange={isNewWorkflow ? undefined : handleTabChange}
        activeTab={currentMode === 'json' ? 'editor' : currentMode}
        allowTitleEdit
        onTitleChange={(newTitle) => {
          setWorkflowData((prev) =>
            prev
              ? {
                  ...prev,
                  name: newTitle,
                }
              : null
          );
        }}
        accountId={accountId}
      />

      {/* Config placeholder warning for AI-generated workflows */}
      {configPlaceholders.length > 0 && !configWarningDismissed && (
        <Alert severity='info' onClose={() => setConfigWarningDismissed(true)} sx={{ borderRadius: 0 }}>
          This workflow uses configuration placeholders: <strong>{configPlaceholders.join(', ')}</strong>. Please ensure these are set in the Config
          Manager before running.
        </Alert>
      )}

      {/* Content Area - Sidebar + Main Content */}
      <Box
        sx={{
          display: 'flex',
          flex: 1,
          overflow: 'hidden',
          height: 'calc(100vh - 60px)', // Subtract header height
          position: 'relative',
        }}
      >
        {/* Main Workflow Content */}
        <Box
          sx={{
            flex: 1,
            height: '100%',
            position: 'relative',
            backgroundColor: 'rgb(246, 246, 246)',
            overflow: 'auto',
          }}
        >
          {/* Render different views based on current mode */}
          {currentMode === 'executions' ? (
            /* Executions View - Full screen dedicated view */
            <Box sx={{ height: '100%', width: '100%' }}>
              <Suspense fallback={<Loader style={{ height: '100%', width: '100%' }} />}>
                <ExecutionsView
                  workflowId={workflowId}
                  accountId={accountId}
                  executions={executionData}
                  loading={executionLoading}
                  onRefresh={fetchExecutionData}
                  taskDefinitions={taskDefinitions}
                  editorNodes={nodes}
                  editorEdges={edges}
                  hasMore={hasMore}
                  hasPrevious={hasPrevious}
                  loadingMore={loadingMore}
                  onNext={goToNextPage}
                  onPrevious={goToPreviousPage}
                  selectedStatus={executionStatusFilter}
                  // selectedTriggerType={executionTriggerTypeFilter}
                  onStatusChange={setExecutionStatusFilter}
                  // onTriggerTypeChange={setExecutionTriggerTypeFilter}
                />
              </Suspense>
            </Box>
          ) : (
            /* Editor View - Can be split view (editor + JSON) or just editor */
            <>
              {/* Sidebar Component - Only in Editor mode */}
              <NodeCategoriesSidebar
                open={sidebarOpen}
                onClose={() => {
                  setSidebarOpen(false);
                  setAddNodeIntent(null);
                }}
                categories={dynamicCategories}
                expandedCategory={expandedCategory}
                onToggleCategory={toggleCategory}
                onAddNode={(categoryKey, subcategoryKey) => {
                  if (addNodeIntent?.type === 'half-edge' && addNodeIntent.sourceNodeId) {
                    // Auto-place below source node and auto-connect
                    const sourceNode = nodes.find((n) => n.id === addNodeIntent.sourceNodeId);
                    if (sourceNode) {
                      const xOffset = getSwitchHandleXOffset(sourceNode, addNodeIntent.sourceHandleId);
                      const newPosition = { x: sourceNode.position.x + xOffset, y: sourceNode.position.y + 180 };
                      setNodes((nds) => {
                        const newNode = addNode(categoryKey, subcategoryKey, nds, newPosition);
                        const connection = {
                          source: addNodeIntent.sourceNodeId,
                          target: newNode.id,
                          sourceHandle: addNodeIntent.sourceHandleId,
                          targetHandle: 'action-input',
                        };
                        const newEdge = buildNewEdge(connection, [...nds, newNode]);
                        setEdges((eds) => [...eds, newEdge]);
                        return [...nds, newNode];
                      });
                    }
                  } else if (addNodeIntent?.type === 'edge-insert' && addNodeIntent.edgeSource && addNodeIntent.edgeTarget) {
                    // Insert node between two connected nodes
                    const sourceNode = nodes.find((n) => n.id === addNodeIntent.edgeSource);
                    const targetNode = nodes.find((n) => n.id === addNodeIntent.edgeTarget);
                    if (sourceNode && targetNode) {
                      const LAYER_HEIGHT = 180;
                      const newPosition = {
                        x: sourceNode.position.x,
                        y: sourceNode.position.y + LAYER_HEIGHT,
                      };
                      setNodes((nds) => {
                        // Push all nodes at or below the target's Y down to make room for the inserted node
                        const targetY = targetNode.position.y;
                        const shiftedNodes = nds.map((n) => {
                          if (n.position.y >= targetY) {
                            return { ...n, position: { ...n.position, y: n.position.y + LAYER_HEIGHT } };
                          }
                          return n;
                        });
                        const newNode = addNode(categoryKey, subcategoryKey, shiftedNodes, newPosition);
                        setEdges((eds) => {
                          // Remove original edge
                          const filteredEdges = eds.filter((e) => e.id !== addNodeIntent.edgeId);
                          // Edge: source -> newNode (preserve original edge's style/data)
                          const connectionAtoNew = {
                            source: addNodeIntent.edgeSource,
                            target: newNode.id,
                            sourceHandle: addNodeIntent.edgeSourceHandle,
                            targetHandle: 'action-input',
                          };
                          const edgeAtoNew = buildNewEdge(connectionAtoNew, [...shiftedNodes, newNode]);
                          // Preserve original edge styling for switch/conditional edges
                          if (addNodeIntent.edgeType === 'conditional' && addNodeIntent.edgeData) {
                            edgeAtoNew.type = 'conditional';
                            edgeAtoNew.data = addNodeIntent.edgeData;
                            if (addNodeIntent.edgeStyle) edgeAtoNew.style = addNodeIntent.edgeStyle;
                          }
                          if (addNodeIntent.edgeLabel) {
                            (edgeAtoNew as any).label = addNodeIntent.edgeLabel;
                          }
                          // Edge: newNode -> target (plain edge)
                          const sourceHandle = newNode.type === 'switch' ? 'switch-default' : 'action-output';
                          const connectionNewToB = {
                            source: newNode.id,
                            target: addNodeIntent.edgeTarget,
                            sourceHandle: newNode.type === 'trigger' ? 'trigger-output' : sourceHandle,
                            targetHandle: 'action-input',
                          };
                          const edgeNewToB = buildNewEdge(connectionNewToB, [...shiftedNodes, newNode]);
                          return [...filteredEdges, edgeAtoNew, edgeNewToB];
                        });
                        return [...shiftedNodes, newNode];
                      });
                    }
                  } else {
                    // Default: random placement (toolbar "Add Action" button)
                    setNodes((nds) => {
                      const newNode = addNode(categoryKey, subcategoryKey, nds);
                      return [...nds, newNode];
                    });
                  }
                  setSidebarOpen(false);
                  setAddNodeIntent(null);
                }}
              />
              {/* Trigger Selector Popup - Dedicated popup for trigger nodes */}
              <TriggerSelectorPopup
                open={triggerPopupOpen}
                onClose={() => setTriggerPopupOpen(false)}
                onSelectTrigger={(triggerKey) => {
                  setNodes((nds) => {
                    const newNode = addNode('triggers', triggerKey, nds);
                    return [...nds, newNode];
                  });
                  setTriggerPopupOpen(false);
                }}
              />
              {/* Split View Container - NubiChat + Editor + JSON */}
              <Box sx={{ height: '100%', width: '100%', display: 'flex', flexDirection: 'row', position: 'relative', overflow: 'hidden' }}>
                {/* NubiChat Sidebar - Sliding Panel on Left (Feature Flag Controlled) */}
                {nubiChatFeatureEnabled && (
                  <Box
                    sx={{
                      position: 'absolute',
                      left: showNubiChat ? '0' : `-${nubiChatWindowWidth}px`,
                      top: 0,
                      height: '100%',
                      width: `${nubiChatWindowWidth}px`,
                      borderRight: '1px solid rgb(226, 226, 227)',
                      backgroundColor: colors.background.white,
                      overflow: 'hidden',
                      transition: isResizingRef.current ? 'none' : 'left 0.3s cubic-bezier(0.4, 0, 0.2, 1)',
                      zIndex: 20,
                    }}
                  >
                    <Suspense
                      fallback={
                        <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100%' }}>
                          <CircularProgress size={24} />
                        </Box>
                      }
                    >
                      {showNubiChat && (
                        <NubiChatSidebar
                          isVisible={true}
                          onClose={() => setShowNubiChat(false)}
                          accountId={accountId}
                          position='left'
                          mode='fixed'
                          width={`${nubiChatWindowWidth}px`}
                          showHeader={true}
                          context={nubiChatContext}
                          querySuffix={nubiChatSuffix}
                        />
                      )}
                      {/* Drag resize handle */}
                      <Box
                        onMouseDown={handleResizeMouseDown('nubi')}
                        sx={{
                          position: 'absolute',
                          right: -3,
                          top: 0,
                          width: '8px',
                          height: '100%',
                          cursor: 'col-resize',
                          zIndex: 25,
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'center',
                          '&::after': {
                            content: '""',
                            width: '2px',
                            height: '32px',
                            borderRadius: '2px',
                            backgroundColor: 'rgba(0, 0, 0, 0.15)',
                            transition: 'background-color 0.15s, height 0.15s',
                          },
                          '&:hover::after': {
                            backgroundColor: 'rgba(59, 130, 246, 0.7)',
                            height: '48px',
                          },
                          '&:hover': {
                            backgroundColor: 'rgba(59, 130, 246, 0.08)',
                          },
                        }}
                      />
                    </Suspense>
                  </Box>
                )}
                {/* NubiChat Toggle Button (Feature Flag Controlled) */}
                {nubiChatFeatureEnabled && (
                  <Box
                    id='workflow-nubi-chat-toggle'
                    onClick={() => setShowNubiChat(!showNubiChat)}
                    sx={{
                      position: 'absolute',
                      left: showNubiChat ? `${nubiChatWindowWidth}px` : '0',
                      top: '50%',
                      ml: '-25px',
                      transform: 'translateY(-50%) rotate(-90deg)',
                      transformOrigin: 'center',
                      zIndex: 30,
                      backgroundColor: colors.background.white,
                      border: `1px solid ${colors.border.secondary}`,
                      borderLeft: showNubiChat ? `1px solid ${colors.border.secondary}` : 'none',
                      borderTopLeftRadius: 0,
                      borderBottomLeftRadius: '8px',
                      borderTopRightRadius: 0,
                      borderBottomRightRadius: '8px',
                      padding: '8px 12px',
                      transition: isResizingRef.current ? 'none' : 'left 0.3s cubic-bezier(0.4, 0, 0.2, 1)',
                      cursor: 'pointer',
                      display: 'flex',
                      alignItems: 'center',
                      gap: '4px',
                      '&:hover': {
                        backgroundColor: 'rgb(249, 249, 249)',
                      },
                      boxShadow: '2px 0 8px rgba(0, 0, 0, 0.1)',
                    }}
                  >
                    {showNubiChat ? (
                      <ChevronLeftIcon fontSize='small' sx={{ color: colors.text.secondary, transform: 'rotate(90deg)' }} />
                    ) : (
                      <ChevronRightIcon fontSize='small' sx={{ color: colors.text.secondary, transform: 'rotate(90deg)' }} />
                    )}
                    <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.secondary, whiteSpace: 'nowrap' }}>AI Chat</Typography>
                  </Box>
                )}
                {/* Middle: Workflow Editor */}
                <Box
                  sx={{
                    height: '100%',
                    flex: 1,
                    marginLeft: nubiChatFeatureEnabled && showNubiChat ? `${nubiChatWindowWidth}px` : '0',
                    marginRight: jsonPanelVisible ? `${jsonWindowWidth}px` : '0',
                    position: 'relative',
                    transition: isResizingRef.current
                      ? 'none'
                      : 'margin-left 0.3s cubic-bezier(0.4, 0, 0.2, 1), margin-right 0.3s cubic-bezier(0.4, 0, 0.2, 1)',
                  }}
                >
                  {loading ? (
                    <Loader style={{ height: '100%', width: '100%' }} />
                  ) : (
                    <>
                      {/* Workflow Canvas - Editor Mode Only */}
                      <Box
                        sx={{
                          width: '100%',
                          height: '100%',
                        }}
                      >
                        <ReactFlow
                          nodes={nodes}
                          edges={edges}
                          onNodesChange={onNodesChange}
                          onEdgesChange={onEdgesChange}
                          onConnect={onConnect}
                          onInit={(instance) => {
                            reactFlowInstanceRef.current = instance;
                            // Perform initial fitView after instance is ready
                            if (nodes.length > 0) {
                              setTimeout(() => {
                                instance.fitView({
                                  padding: 0.15,
                                  maxZoom: 0.9,
                                  minZoom: 0.75,
                                  duration: 0,
                                });
                                hasInitiallyFit.current = true;
                              }, 50);
                            }
                          }}
                          defaultEdgeOptions={{
                            type: 'smoothstep',
                            style: {
                              strokeWidth: 1,
                              stroke: 'rgb(175, 175, 175)',
                            },
                          }}
                          connectionLineType={'smoothstep' as any}
                          className='editor-mode'
                          style={{ width: '100%', height: '100%' }}
                          onNodeClick={(event: any, node: any) =>
                            onNodeClick(event, node, currentMode, setNodes, setSelectedActionType, setActionDetailsSidebarOpen, setSidebarOpen)
                          }
                          onPaneClick={() => deselectAllNodes(setNodes)}
                          nodeTypes={customNodeTypes}
                          edgeTypes={customEdgeTypes}
                          fitView={false}
                          fitViewOptions={{
                            padding: 0.15,
                            maxZoom: 0.9,
                            minZoom: 0.75,
                          }}
                          snapToGrid={true}
                          snapGrid={[19, 15]}
                          nodesDraggable={true}
                          nodesConnectable={true}
                          elementsSelectable={true}
                          panOnScroll
                          panOnScrollMode={PanOnScrollMode.Vertical}
                          panOnScrollSpeed={0.8}
                          zoomOnScroll={false}
                          zoomOnPinch
                          proOptions={{
                            hideAttribution: true,
                          }}
                        >
                          <MiniMap
                            nodeStrokeWidth={2}
                            position='bottom-right'
                            style={{
                              width: 50,
                              height: 100,
                              backgroundColor: 'white',
                              border: '1px solid rgb(187, 187, 187)',
                              borderRadius: '4px',
                              margin: '0px 12px 80px 0px',
                            }}
                          />
                          {/* Soften the canvas dots to be less distracting */}
                          <Background color='rgba(0, 0, 0, 0.42)' />
                          <Controls style={{ marginBottom: '60px' }} />
                        </ReactFlow>
                      </Box>

                      {/* Loading message for AI workflows */}
                      {isNewWorkflow && (router.query.loadFromAI || router.query.aiGenerated) && nodes.length === 0 && (
                        <Box
                          sx={{
                            position: 'absolute',
                            top: '50%',
                            left: '50%',
                            transform: 'translate(-50%, -50%)',
                            textAlign: 'center',
                            zIndex: 15,
                          }}
                        >
                          <Typography variant='h6' sx={{ color: colors.text.secondary, mb: 2 }}>
                            Loading AI-generated workflow...
                          </Typography>
                          <Box sx={{ display: 'flex', justifyContent: 'center' }}>
                            <CircularProgress />
                          </Box>
                        </Box>
                      )}

                      {/* Empty State - Consolidated trigger selection for new workflows */}
                      {isNewWorkflow &&
                        nodes.length === 0 &&
                        !router.query.loadFromAI &&
                        !router.query.aiGenerated &&
                        !router.query.loadFromTemplate &&
                        !aiWorkflowInitializedRef.current &&
                        !templateInitializedRef.current && (
                          <Box
                            sx={{
                              position: 'absolute',
                              top: '50%',
                              left: '50%',
                              transform: 'translate(-50%, -50%)',
                              border: '1px solid rgb(226, 226, 227)',
                              backgroundColor: 'white',
                              borderRadius: '12px',
                              padding: '32px 28px',
                              textAlign: 'center',
                              zIndex: 15,
                            }}
                          >
                            <Box
                              sx={{
                                display: 'flex',
                                justifyContent: 'center',
                                mb: 2,
                              }}
                            >
                              <Box
                                sx={{
                                  width: '36px',
                                  height: '36px',
                                  borderRadius: '16px',
                                  background: 'linear-gradient(135deg, #22c55e 0%, #16a34a 100%)',
                                  display: 'flex',
                                  alignItems: 'center',
                                  justifyContent: 'center',
                                  padding: '16px',
                                  boxShadow: '0 4px 12px rgba(34, 197, 94, 0.3)',
                                  border: '2px solid rgba(255, 255, 255, 0.2)',
                                }}
                              >
                                <SafeIcon
                                  src={manualTriggerIcon?.default || manualTriggerIcon}
                                  alt='trigger-icon'
                                  width={36}
                                  height={36}
                                  style={{ filter: 'brightness(0) invert(1)' }}
                                />
                              </Box>
                            </Box>
                            <Typography
                              variant='h5'
                              sx={{
                                color: colors.text.secondary,
                                fontWeight: 600,
                                fontSize: '22px',
                                fontFamily: 'poppins',
                                letterSpacing: '-0.010em',
                              }}
                            >
                              How should your Automation begin?
                            </Typography>
                            <Typography variant='body1' sx={{ color: colors.text.secondaryDark, mb: 3, fontSize: '14px', mt: '4px' }}>
                              Pick a trigger to kick things off. You can always change it later
                            </Typography>
                            <Box sx={{ display: 'flex', gap: '12px', justifyContent: 'center' }}>
                              {(
                                [
                                  {
                                    key: 'manual',
                                    label: 'Manual Trigger',
                                    description: 'Start automation manually',
                                    icon: workflowUserIcon?.default || workflowUserIcon,
                                    color: '#10b981',
                                  },
                                  {
                                    key: 'webhook',
                                    label: 'Webhook',
                                    description: 'HTTP endpoint trigger',
                                    icon: workflowWebhookIcon?.default || workflowWebhookIcon,
                                    color: '#f97316',
                                  },
                                  {
                                    key: 'schedule',
                                    label: 'Schedule',
                                    description: 'Time-based trigger',
                                    icon: workflowCalendarIcon?.default || workflowCalendarIcon,
                                    color: '#3b82f6',
                                  },
                                  {
                                    key: 'event',
                                    label: 'Event Trigger',
                                    description: 'Event-based trigger',
                                    icon: null,
                                    emoji: '\u26A1',
                                    color: '#f59e0b',
                                  },
                                  {
                                    key: 'optimization',
                                    label: 'Optimization',
                                    description: 'Triggered by new recommendations',
                                    icon: null,
                                    emoji: '\uD83D\uDCA1',
                                    color: '#8b5cf6',
                                  },
                                ] as { key: string; label: string; description: string; icon: any; emoji?: string; color: string }[]
                              ).map((trigger) => (
                                <Box
                                  key={trigger.key}
                                  data-testid={`trigger-option-${trigger.key}`}
                                  onClick={() => {
                                    setNodes((nds) => {
                                      const newNode = addNode('triggers', trigger.key, nds);
                                      return [...nds, newNode];
                                    });
                                  }}
                                  sx={{
                                    display: 'flex',
                                    flexDirection: 'column',
                                    alignItems: 'center',
                                    padding: '16px 12px',
                                    borderRadius: '12px',
                                    border: `1px solid ${trigger.color}30`,
                                    backgroundColor: `${trigger.color}08`,
                                    cursor: 'pointer',
                                    transition: 'all 0.2s',
                                    width: '120px',
                                    '&:hover': {
                                      backgroundColor: `${trigger.color}15`,
                                      borderColor: `${trigger.color}50`,
                                      transform: 'translateY(-2px)',
                                      boxShadow: `0 4px 12px ${trigger.color}20`,
                                    },
                                  }}
                                >
                                  <Box
                                    sx={{
                                      display: 'flex',
                                      alignItems: 'center',
                                      justifyContent: 'center',
                                      width: '44px',
                                      height: '44px',
                                      borderRadius: '10px',
                                      backgroundColor: `${trigger.color}15`,
                                      border: `1px solid ${trigger.color}30`,
                                      mb: 1,
                                    }}
                                  >
                                    {trigger.icon ? (
                                      <SafeIcon src={trigger.icon} alt={trigger.label} width={24} height={24} />
                                    ) : (
                                      <span style={{ fontSize: '20px' }}>{trigger.emoji}</span>
                                    )}
                                  </Box>
                                  <Typography
                                    sx={{
                                      fontSize: '13px',
                                      fontWeight: 600,
                                      color: colors.text.secondary,
                                      fontFamily: 'poppins',
                                      lineHeight: 1.3,
                                    }}
                                  >
                                    {trigger.label}
                                  </Typography>
                                  <Typography
                                    sx={{
                                      fontSize: '11px',
                                      color: '#6b7280',
                                      mt: 0.5,
                                      lineHeight: 1.3,
                                    }}
                                  >
                                    {trigger.description}
                                  </Typography>
                                </Box>
                              ))}
                            </Box>
                          </Box>
                        )}

                      {/* Guidance Message for workflows without triggers */}
                      {!isNewWorkflow && nodes.length > 0 && !nodes.some((node) => node.type === 'trigger') && (
                        <TriggerWarningMessage onAddTrigger={openSidebarWithTriggers} />
                      )}

                      {/* Execution Status Bar - Show during manual run */}
                      <ExecutionStatusBar isTestRunning={isTestRunning} nodes={nodes} />

                      {/* Bottom Action Toolbar - Positioned within canvas - Only show when nodes exist */}
                      {nodes.length > 0 && (
                        <Box
                          sx={{
                            position: 'absolute',
                            bottom: '24px',
                            left: '50%',
                            transform: 'translateX(-50%)',
                            zIndex: 100,
                            backgroundColor: colors.background.white,
                            border: `1px solid ${colors.border.secondaryLight}`,
                            borderRadius: '12px',
                            padding: '8px 10px',
                            boxShadow: '0 -2px 16px rgba(0, 0, 0, 0.08)',
                            display: 'flex',
                            alignItems: 'center',
                            gap: '10px',
                            whiteSpace: 'nowrap',
                            width: 'max-content',
                          }}
                        >
                          {/* Add Action Button */}
                          <CustomButton
                            id='workflow-add-action-btn'
                            onClick={() => setSidebarOpen(true)}
                            variant='secondary'
                            size='Small'
                            startIcon={<AddIcon sx={{ fontSize: '18px' }} />}
                            text='Add Action'
                          />

                          {/* Divider */}
                          <Box sx={{ width: '1px', height: '28px', backgroundColor: colors.border.secondary }} />

                          {/* Save Button - Primary CTA */}
                          <CustomButton
                            id='workflow-save-btn'
                            onClick={handleSaveWorkflow}
                            text='Save'
                            variant='secondary'
                            size='Small'
                            startIcon={<SafeIcon src={SaveIconOutline} alt='Save' width={16} height={16} />}
                          />

                          {/* Divider */}
                          <Box sx={{ width: '1px', height: '28px', backgroundColor: colors.border.secondary }} />

                          {/* Run Button */}
                          {!isNewWorkflow && (
                            <CustomButton
                              id='workflow-run-btn'
                              onClick={handleRunButtonClick}
                              text={isTestRunning ? 'Running...' : 'Run'}
                              variant='tertiary'
                              size='Small'
                              startIcon={<SafeIcon src={PlayIconBlue} alt='Run' width={16} height={16} />}
                              disabled={isTestRunning || isDryRunning}
                            />
                          )}

                          {/* Stop Button — visible only during an active run */}
                          {isTestRunning && !isNewWorkflow && (
                            <CustomButton
                              id='workflow-stop-btn'
                              onClick={handleCancelExecution}
                              text='Cancel'
                              variant='secondary'
                              size='Small'
                              startIcon={<StopCircleOutlinedIcon sx={{ fontSize: 16, color: '#dc2626' }} />}
                              sx={{ color: '#dc2626', borderColor: '#dc2626' }}
                            />
                          )}

                          {/* Dry Run Button */}
                          <CustomButton
                            id='workflow-dry-run-btn'
                            onClick={handleDryRunButtonClick}
                            text={isDryRunning ? 'Running...' : 'Dry Run'}
                            variant='secondary'
                            size='Small'
                            startIcon={<SafeIcon src={PlayIconBlue} alt='Dry Run' width={16} height={16} />}
                            disabled={isDryRunning || isTestRunning}
                            showTooltip
                            tooltipPlacement='top'
                          />

                          {/* Divider */}
                          <Box sx={{ width: '1px', height: '28px', backgroundColor: colors.border.secondary }} />

                          {/* NuBi AI Chat Button */}
                          {nubiChatFeatureEnabled && (
                            <CustomIconButton
                              id='workflow-nubi-chat-btn'
                              onClick={() => setShowNubiChat(!showNubiChat)}
                              variant='no-border-white'
                              tooltip={`Ask ${assistantName} AI`}
                            >
                              <SafeIcon src={getNubiIconUrl()} alt='AI Chat' width={22} height={22} />
                            </CustomIconButton>
                          )}

                          {/* Settings Button */}
                          <CustomIconButton
                            id='workflow-settings-btn'
                            onClick={() => setIsSettingsModalOpen(true)}
                            variant='no-border-white'
                            tooltip='Automation Settings'
                          >
                            <SafeIcon src={SettingOutlineIconGrey} alt='Settings' width={20} height={20} />
                          </CustomIconButton>

                          {/* Prettify Button - re-runs auto layout */}
                          <CustomIconButton
                            id='workflow-prettify-btn'
                            onClick={handlePrettifyLayout}
                            variant='no-border-white'
                            tooltip='Prettify Layout'
                            tooltipPlacement='top'
                            isDisabled={nodes.length === 0}
                          >
                            <AutoFixHighIcon sx={{ fontSize: '20px' }} />
                          </CustomIconButton>

                          {!isNewWorkflow && workflowSettings?.status && (
                            <CustomDropdown
                              value={workflowSettings.status}
                              onChange={(event: any) => {
                                if (setWorkflowSettings && workflowSettings) {
                                  setWorkflowSettings({
                                    ...workflowSettings,
                                    status: event.target.value,
                                  });
                                }
                              }}
                              options={[
                                { label: 'ACTIVE', value: 'ACTIVE' },
                                { label: 'INACTIVE', value: 'INACTIVE' },
                                { label: 'PAUSED', value: 'PAUSED' },
                                { label: 'DRAFT', value: 'DRAFT' },
                              ]}
                              minWidth='120px'
                              disableClearable
                              label=''
                              noBorder
                              customStyle={{
                                minHeight: '36px',
                                '& .MuiOutlinedInput-root': {
                                  height: '36px',
                                  fontSize: '13px',
                                  fontWeight: 500,
                                  padding: '4px 12px',
                                  backgroundColor: '#ffffff',
                                  color: '#374151',
                                  transition: 'all 0.15s ease-in-out',
                                  '&:hover': {
                                    backgroundColor: '#f9fafb',
                                  },
                                  '& .MuiSelect-select': {
                                    padding: '0px',
                                  },
                                },
                                '& .MuiOutlinedInput-notchedOutline': {
                                  border: 'none',
                                },
                              }}
                              additionalAutoCompleteProps={{
                                renderInput: (params: any) => (
                                  <Box sx={{ position: 'relative', width: '100%' }}>
                                    <TextField
                                      {...params}
                                      margin='none'
                                      sx={{
                                        margin: '0px',
                                        '& .MuiInputBase-input': {
                                          opacity: 0,
                                          position: 'absolute',
                                        },
                                      }}
                                    />
                                    <Box
                                      sx={{
                                        position: 'absolute',
                                        top: '50%',
                                        left: '12px',
                                        transform: 'translateY(-50%)',
                                        pointerEvents: 'none',
                                        zIndex: 1,
                                      }}
                                    >
                                      <CustomLabels
                                        text={workflowSettings.status}
                                        variant={
                                          workflowSettings.status === 'ACTIVE'
                                            ? 'green'
                                            : workflowSettings.status === 'PAUSED'
                                            ? 'orange'
                                            : workflowSettings.status === 'INACTIVE'
                                            ? 'red'
                                            : 'grey'
                                        }
                                        textTransform='none'
                                      />
                                    </Box>
                                  </Box>
                                ),
                                renderOption: (props: any, option: any) => (
                                  <Box component='li' {...props} sx={{ padding: '8px 12px' }}>
                                    <CustomLabels
                                      text={option.label}
                                      variant={
                                        option.value === 'ACTIVE'
                                          ? 'green'
                                          : option.value === 'PAUSED'
                                          ? 'orange'
                                          : option.value === 'INACTIVE'
                                          ? 'red'
                                          : 'grey'
                                      }
                                      textTransform='none'
                                    />
                                  </Box>
                                ),
                              }}
                            />
                          )}
                        </Box>
                      )}
                    </>
                  )}
                </Box>
                {/* Toggle Button for JSON Panel */}
                <Box
                  id='workflow-json-panel-toggle'
                  onClick={() => setJsonPanelVisible(!jsonPanelVisible)}
                  sx={{
                    position: 'absolute',
                    right: jsonPanelVisible ? `${jsonWindowWidth}px` : '0',
                    top: '50%',
                    mr: '-22px',
                    transform: 'translateY(-50%) rotate(90deg)',
                    transformOrigin: 'center',
                    zIndex: 30,
                    backgroundColor: colors.background.white,
                    border: `1px solid ${colors.border.secondary}`,
                    borderRight: jsonPanelVisible ? `1px solid ${colors.border.secondary}` : 'none',
                    borderBottomLeftRadius: '8px',
                    borderTopRightRadius: 0,
                    borderBottomRightRadius: '8px',
                    padding: '8px 12px',
                    transition: isResizingRef.current ? 'none' : 'right 0.3s cubic-bezier(0.4, 0, 0.2, 1)',
                    cursor: 'pointer',
                    display: 'flex',
                    alignItems: 'center',
                    gap: '4px',
                    '&:hover': {
                      backgroundColor: 'rgb(249, 249, 249)',
                    },
                    boxShadow: '-2px 0 8px rgba(0, 0, 0, 0.1)',
                  }}
                >
                  <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.secondary, whiteSpace: 'nowrap' }}>JSON</Typography>
                  {jsonPanelVisible ? (
                    <ChevronRightIcon fontSize='small' sx={{ color: colors.text.secondary, transform: 'rotate(-90deg)' }} />
                  ) : (
                    <ChevronLeftIcon fontSize='small' sx={{ color: colors.text.secondary, transform: 'rotate(-90deg)' }} />
                  )}
                </Box>
                {/* Right Side: JSON Editor (sliding panel) */}
                <Box
                  sx={{
                    position: 'absolute',
                    right: jsonPanelVisible ? '0' : `-${jsonWindowWidth}px`,
                    top: 0,
                    height: 'calc(100% - 8px)',
                    width: `${jsonWindowWidth}px`,
                    borderLeft: '1px solid rgb(226, 226, 227)',
                    backgroundColor: 'rgb(30, 30, 30)',
                    overflow: 'hidden',
                    transition: isResizingRef.current ? 'none' : 'right 0.3s cubic-bezier(0.4, 0, 0.2, 1)',
                    zIndex: 20,
                  }}
                >
                  {/* Drag resize handle */}
                  <Suspense
                    fallback={
                      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100%' }}>
                        <CircularProgress size={24} />
                      </Box>
                    }
                  >
                    <Box
                      onMouseDown={handleResizeMouseDown('json')}
                      sx={{
                        position: 'absolute',
                        left: -3,
                        top: 0,
                        width: '8px',
                        height: '100%',
                        cursor: 'col-resize',
                        zIndex: 25,
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        '&::after': {
                          content: '""',
                          width: '2px',
                          height: '32px',
                          borderRadius: '2px',
                          backgroundColor: 'rgba(0, 0, 0, 0.15)',
                          transition: 'background-color 0.15s, height 0.15s',
                        },
                        '&:hover::after': {
                          backgroundColor: 'rgba(59, 130, 246, 0.7)',
                          height: '48px',
                        },
                        '&:hover': {
                          backgroundColor: 'rgba(59, 130, 246, 0.08)',
                        },
                      }}
                    />
                    <JsonEditorTab
                      jsonText={jsonEditorText}
                      onChange={handleJsonChangeWithRevertClear}
                      onApply={applyJsonToWorkflow}
                      isValid={jsonValid}
                      parseError={jsonParseError}
                      hasUnsavedChanges={jsonHasUnsavedChanges}
                      disabled={loading || isApplyingJson}
                      canRevert={lastAppliedSource === 'llm' && jsonBeforeLlmApply !== ''}
                      onRevert={revertLastLlmApply}
                      isLoading={isApplyingJson}
                    />
                  </Suspense>
                </Box>
              </Box>
              {/* Action Details Sidebar - Only in Editor mode (lazy-loaded) */}
              {actionDetailsSidebarOpen && (
                <Suspense fallback={null}>
                  <ActionDetailsSidebar
                    open={actionDetailsSidebarOpen}
                    onClose={() => {
                      setActionDetailsSidebarOpen(false);
                      deselectAllNodes(setNodes);
                    }}
                    selectedActionType={selectedActionType}
                    nodes={nodes}
                    edges={edges}
                    onTaskDataChange={handleSidebarTaskDataChange}
                    onTaskConfigChange={handleSidebarTaskConfigChange}
                    taskDefinitions={taskDefinitions}
                    taskData={sidebarTaskData}
                    validationErrors={sidebarValidationErrors}
                    viewOnlyMode={false}
                    previousNodeOutputSchema={sidebarPreviousNodeOutputSchema}
                    accountId={accountId}
                    onRunPreviousSteps={handleRunPreviousSteps}
                    onDryRunToTask={handleDryRunToTask}
                    onRunTask={handleRunTask}
                    workflowInputs={workflowSettings.inputs}
                    workflowTimeout={workflowSettings.timeout}
                    onToggleDisable={(disable: boolean) => {
                      const selectedNode = nodes.find((node) => node.selected && (node.type === 'action' || node.type === 'switch'));
                      if (!selectedNode) return;
                      const result = disable ? disableTask(selectedNode.id, nodes, edges) : enableTask(selectedNode.id, nodes, edges);
                      setNodes(result.nodes);
                      setEdges(result.edges);
                    }}
                  />
                </Suspense>
              )}
              {/* Trigger Configuration Sidebar (lazy-loaded) */}
              {triggerConfigSidebarOpen && (
                <Suspense fallback={null}>
                  <TriggerConfigSidebar
                    open={triggerConfigSidebarOpen}
                    selectedNode={selectedNode}
                    onClose={() => closeTriggerConfigSidebar(setNodes)}
                    onSave={(nodeId, triggerConfig) => {
                      updateTriggerConfig(nodeId, triggerConfig, setNodes);
                    }}
                    accountId={accountId}
                    workflowData={workflowDataRef.current}
                  />
                </Suspense>
              )}
            </>
          )}

          {/* Test Task Response Modal (lazy-loaded) */}
          {testResponseDialog.open && (
            <Suspense fallback={null}>
              <TestResponseModal
                open={testResponseDialog.open}
                onClose={() => setTestResponseDialog({ open: false, taskType: '', responseData: null })}
                taskType={testResponseDialog.taskType}
                responseData={testResponseDialog.responseData}
              />
            </Suspense>
          )}

          {/* Dry Run Result Modal (lazy-loaded) */}
          {showDryRunModal && (
            <Suspense fallback={null}>
              <DryRunResultModal open={showDryRunModal} onClose={() => setShowDryRunModal(false)} result={dryRunResult} />
            </Suspense>
          )}

          {/* Trigger Input Modal for Run/Dry Run (lazy-loaded) */}
          {triggerModalOpen && (
            <Suspense fallback={null}>
              <TriggerWorkflowModal
                open={triggerModalOpen}
                onClose={() => {
                  setTriggerModalOpen(false);
                  setTriggerModalLoading(false);
                }}
                workflowName={workflowData?.name || 'Automation'}
                triggerType={getTriggerType()}
                defaultInputs={getDefaultTriggerInputs()}
                inputSchema={getInputSchema()}
                onTrigger={handleTriggerFromModal}
                loading={triggerModalLoading}
              />
            </Suspense>
          )}

          {/* Workflow Settings Modal (lazy-loaded) */}
          {isSettingsModalOpen && (
            <Suspense fallback={null}>
              <WorkflowSettingsModal
                open={isSettingsModalOpen}
                onClose={() => setIsSettingsModalOpen(false)}
                onSave={setWorkflowSettings}
                initialSettings={workflowSettings}
                taskTimeouts={nodes.filter((n) => n.data?.taskConfig?.timeout).map((n) => n.data.taskConfig.timeout as string)}
              />
            </Suspense>
          )}

          {/* Unsaved Changes Confirmation Dialog */}
          <Modal
            open={showUnsavedChangesDialog}
            title='Unsaved Changes'
            actionButtons={
              <Box sx={{ display: 'flex', gap: 2, justifyContent: 'flex-end', p: 2 }}>
                <CustomButton
                  id='workflow-unsaved-cancel-btn'
                  variant='secondary'
                  size='Medium'
                  text='Cancel'
                  onClick={handleCancelNavigation}
                  disabled={loading}
                />
                <CustomButton
                  id='workflow-unsaved-leave-btn'
                  size='Medium'
                  text={'Leave page'}
                  onClick={handleConfirmNavigation}
                  disabled={loading}
                />
              </Box>
            }
            handleClose={handleCancelNavigation}
          >
            <Box padding={'24px'}>
              <Text value={'You have unsaved changes. Are you sure you want to leave? Your changes will be lost.'} />
            </Box>
          </Modal>
        </Box>
      </Box>
    </Box>
  );
};

export default WorkflowBuilderNoteBook;

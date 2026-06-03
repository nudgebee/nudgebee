import { useState, useEffect, useCallback, useRef } from 'react';
import type { Node, Edge } from 'reactflow';
import { prepareWorkflowForSave } from '@components1/workflow/utils/workflowApiUtils';
import { convertWorkflowToReactFlow } from '@components1/workflow/utils/workflowLayoutEngine';
import { extractTriggersFromNodes, extractTasksFromWorkflowNodes } from '@components1/workflow/utils/workflowTaskExtraction';
import { isWorkflowJson } from '@components1/workflow/utils/workflowDetection';
import { snackbar } from '@components1/common/snackbarService';
import type { WorkflowSettings, WorkflowStatus } from '@components1/workflow/types';

interface WorkflowData {
  id: string | null;
  name: string;
  status?: WorkflowStatus;
  definition: {
    version: string;
    timeout: string;
    inputs: any[];
    tasks: any[];
    triggers: Array<{ type: string; params: any }>;
    output: any;
    retry_policy: {
      maximum_attempts: number;
      initial_interval: string;
      maximum_interval: string;
      backoff_coefficient: number;
    };
  };
  tags: Record<string, any>;
}

interface UseJsonEditorSyncProps {
  nodes: Node[];
  edges: Edge[];
  workflowSettings: WorkflowSettings;
  workflowData: WorkflowData | null;
  taskDefinitions: any[];
  currentMode: 'editor' | 'json' | 'executions';
  loading: boolean;
  setNodes: React.Dispatch<React.SetStateAction<Node[]>>;
  setEdges: React.Dispatch<React.SetStateAction<Edge[]>>;
  setWorkflowData: React.Dispatch<React.SetStateAction<WorkflowData | null>>;
  setWorkflowSettings: React.Dispatch<React.SetStateAction<WorkflowSettings>>;
  setAutoFitViewport: React.Dispatch<React.SetStateAction<{ x: number; y: number; zoom: number } | null>>;
  setCurrentMode: React.Dispatch<React.SetStateAction<'editor' | 'json' | 'executions'>>;
  setJsonPanelVisible: React.Dispatch<React.SetStateAction<boolean>>;
}

interface UseJsonEditorSyncReturn {
  jsonEditorText: string;
  setJsonEditorText: React.Dispatch<React.SetStateAction<string>>;
  jsonValid: boolean;
  jsonParseError: string;
  jsonHasUnsavedChanges: boolean;
  isApplyingJson: boolean;
  lastAppliedSource: 'llm' | 'visual' | null;
  jsonBeforeLlmApply: string;
  generateJsonFromWorkflowState: () => string;
  handleJsonChange: (newJsonText: string) => void;
  handleJsonChangeWithRevertClear: (newJsonText: string) => void;
  applyJsonToWorkflow: () => Promise<void>;
  revertLastLlmApply: () => void;
}

export function useJsonEditorSync({
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
}: UseJsonEditorSyncProps): UseJsonEditorSyncReturn {
  // JSON Editor state
  const [jsonEditorText, setJsonEditorText] = useState<string>('');
  const [jsonValid, setJsonValid] = useState<boolean>(true);
  const [jsonParseError, setJsonParseError] = useState<string>('');
  const [jsonHasUnsavedChanges, setJsonHasUnsavedChanges] = useState<boolean>(false);
  const [lastVisualEditorSnapshot, setLastVisualEditorSnapshot] = useState<string>('');

  // Loading state for JSON apply operation
  const [isApplyingJson, setIsApplyingJson] = useState<boolean>(false);

  // LLM-to-Editor sync state
  const [jsonBeforeLlmApply, setJsonBeforeLlmApply] = useState<string>('');
  const [lastAppliedSource, setLastAppliedSource] = useState<'llm' | 'visual' | null>(null);

  // Ref to track applying state for event handler (avoids stale closure issues)
  const applyingLlmIdRef = useRef<string | null>(null);
  // Ref to access current jsonEditorText in event handler without causing re-subscriptions
  const jsonEditorTextRef = useRef<string>(jsonEditorText);

  // Keep jsonEditorTextRef in sync with state
  useEffect(() => {
    jsonEditorTextRef.current = jsonEditorText;
  }, [jsonEditorText]);

  // Generate JSON from current workflow state (workflow object only)
  const generateJsonFromWorkflowState = useCallback((): string => {
    try {
      // Prepare workflow definition from current visual state
      const { definition } = prepareWorkflowForSave(
        nodes,
        edges,
        (nodes: Node[]) => extractTasksFromWorkflowNodes(nodes, edges),
        extractTriggersFromNodes,
        workflowSettings,
        workflowData?.definition
      );

      // Build workflow object (not the full request)
      const workflowObject = {
        name: workflowData?.name || 'New Automation',
        definition: definition,
        tags: workflowSettings.tags.reduce((acc, tag) => {
          // Parse tags that contain colon to separate key and value
          const colonIndex = tag.indexOf(':');
          if (colonIndex > 0) {
            const key = tag.substring(0, colonIndex).trim();
            const value = tag.substring(colonIndex + 1).trim();
            return { ...acc, [key]: value };
          }
          return { ...acc, [tag]: '' };
        }, {} as Record<string, any>),
        status: workflowSettings.status,
      };

      return JSON.stringify(workflowObject, null, 2);
    } catch (error) {
      console.error('Failed to generate JSON:', error);
      // Return a minimal valid workflow structure instead of empty object
      return JSON.stringify(
        {
          name: 'Error generating automation',
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
              maximum_interval: '60s',
              backoff_coefficient: 2.0,
            },
          },
          tags: {},
          status: 'ACTIVE',
        },
        null,
        2
      );
    }
  }, [nodes, edges, workflowSettings, workflowData]);

  // Validate JSON as user types
  const handleJsonChange = useCallback((newJsonText: string) => {
    setJsonEditorText(newJsonText);
    setJsonHasUnsavedChanges(true);

    try {
      const parsed = JSON.parse(newJsonText);

      // Validate workflow object structure - top level
      if (!parsed.name) {
        setJsonValid(false);
        setJsonParseError('Invalid structure: missing name');
        return;
      }

      if (!parsed.definition) {
        setJsonValid(false);
        setJsonParseError('Invalid structure: missing definition');
        return;
      }

      // Validate definition structure - required fields
      if (!parsed.definition.version) {
        setJsonValid(false);
        setJsonParseError('Invalid structure: missing definition.version');
        return;
      }

      if (!Array.isArray(parsed.definition.tasks)) {
        setJsonValid(false);
        setJsonParseError('Invalid structure: definition.tasks must be an array');
        return;
      }

      if (!Array.isArray(parsed.definition.triggers)) {
        setJsonValid(false);
        setJsonParseError('Invalid structure: definition.triggers must be an array');
        return;
      }

      if (parsed.definition.triggers.length === 0) {
        setJsonValid(false);
        setJsonParseError('Invalid structure: automation must have at least one trigger');
        return;
      }

      // Valid JSON
      setJsonValid(true);
      setJsonParseError('');
    } catch (error) {
      setJsonValid(false);
      setJsonParseError(error instanceof Error ? error.message : 'Invalid JSON syntax');
    }
  }, []);

  // Apply JSON changes to visual workflow
  const applyJsonToWorkflow = useCallback(async () => {
    // Prevent concurrent apply operations
    if (isApplyingJson) {
      return;
    }

    setIsApplyingJson(true);
    try {
      // Parse JSON
      const parsed = JSON.parse(jsonEditorText);

      // Validate workflow object structure
      if (!parsed.name || !parsed.definition) {
        throw new Error('Invalid automation structure: missing required fields (name or definition)');
      }

      // Update workflow data with the full parsed payload — name, definition,
      // tags and status. Updating only `name` left `workflowData.definition`
      // stale and caused the WorkflowBuilderNotebook effect that mirrors
      // `workflowData.definition` -> `workflowSettings` to overwrite the
      // setWorkflowSettings call below on the very next render, so the JSON
      // changes appeared to apply successfully but were silently reverted.
      const def = parsed.definition;
      setWorkflowData((prev) =>
        prev
          ? {
              ...prev,
              name: parsed.name,
              definition: def,
              tags: parsed.tags ?? prev.tags,
              status: parsed.status ?? prev.status,
            }
          : null
      );

      setWorkflowSettings((prev) => ({
        timeout: def.timeout ?? prev.timeout,
        maxInterval: def.retry_policy?.maximum_interval ?? prev.maxInterval,
        retries: def.retry_policy?.maximum_attempts ?? prev.retries,
        inputs: def.inputs || prev.inputs,
        outputs: def.output || prev.outputs,
        tags: Object.entries(parsed.tags || {}).map(([key, value]) => (value ? `${key}:${value}` : key)),
        status: parsed.status || prev.status,
      }));

      // Convert definition back to nodes/edges
      const {
        nodes: convertedNodes,
        edges: convertedEdges,
        viewport,
      } = convertWorkflowToReactFlow(
        def,
        {
          minHorizontalSpacing: 250,
          minVerticalSpacing: 180,
          minTriggerSpacing: 50,
          minConditionalSpacing: 120,
        },
        taskDefinitions
      );

      setNodes(convertedNodes);
      setEdges(convertedEdges);

      if (viewport) {
        setAutoFitViewport(viewport);
      }

      // Mark as applied
      setJsonHasUnsavedChanges(false);
      setLastVisualEditorSnapshot(jsonEditorText);

      snackbar.success('JSON applied to automation successfully');
    } catch (error) {
      const errorMessage = error instanceof Error ? error.message : 'Invalid JSON';
      snackbar.error(`Failed to apply JSON: ${errorMessage}`);
      setJsonParseError(errorMessage);
    } finally {
      setIsApplyingJson(false);
    }
  }, [jsonEditorText, taskDefinitions, isApplyingJson, setNodes, setEdges, setWorkflowData, setWorkflowSettings, setAutoFitViewport]);

  // Revert to state before LLM apply (single undo)
  const revertLastLlmApply = useCallback(() => {
    if (jsonBeforeLlmApply && lastAppliedSource === 'llm') {
      setJsonEditorText(jsonBeforeLlmApply);
      setJsonHasUnsavedChanges(true);
      setJsonBeforeLlmApply('');
      setLastAppliedSource(null);
      snackbar.info('Reverted to JSON before LLM changes.');
    }
  }, [jsonBeforeLlmApply, lastAppliedSource]);

  // Clear revert capability when user manually edits JSON
  const handleJsonChangeWithRevertClear = useCallback(
    (newJsonText: string) => {
      handleJsonChange(newJsonText);
      // Clear revert if user manually edits after LLM apply
      if (lastAppliedSource === 'llm') {
        setLastAppliedSource(null);
        setJsonBeforeLlmApply('');
      }
    },
    [handleJsonChange, lastAppliedSource]
  );

  // Listen for LLM workflow generation events
  useEffect(() => {
    const handleLlmWorkflowGenerated = (event: Event) => {
      if (!(event instanceof CustomEvent)) {
        return;
      }
      const { json } = event.detail;

      // Prevent concurrent applies
      if (applyingLlmIdRef.current) {
        return;
      }

      try {
        // Validate JSON structure
        const parsed = JSON.parse(json);

        if (!isWorkflowJson(parsed)) {
          snackbar.error('Invalid automation structure from LLM');
          return;
        }

        // 1. Switch to JSON editor tab and show panel
        setCurrentMode('json');
        setJsonPanelVisible(true);

        // 2. Show loading state
        setIsApplyingJson(true);
        applyingLlmIdRef.current = 'applying';

        // 3. Apply JSON after a brief delay to allow UI to update
        setTimeout(() => {
          setJsonBeforeLlmApply(jsonEditorTextRef.current); // Store for potential revert
          setJsonEditorText(json);
          setJsonHasUnsavedChanges(true);
          setLastAppliedSource('llm');
          setIsApplyingJson(false);
          applyingLlmIdRef.current = null;
        }, 100);
      } catch {
        snackbar.error('Invalid JSON from LLM');
      }
    };

    window.addEventListener('llm-workflow-generated', handleLlmWorkflowGenerated);
    return () => {
      window.removeEventListener('llm-workflow-generated', handleLlmWorkflowGenerated);
    };
  }, [setCurrentMode, setJsonPanelVisible]);

  // Auto-update JSON when visual editor changes
  useEffect(() => {
    // Skip if on JSON tab, loading, or no workflow data
    if (currentMode === 'json' || loading || !workflowData) {
      return;
    }

    // Generate fresh JSON from current visual state
    const freshJson = generateJsonFromWorkflowState();

    // Only update if actually changed
    if (freshJson !== lastVisualEditorSnapshot) {
      setJsonEditorText(freshJson);
      setLastVisualEditorSnapshot(freshJson);
      setJsonHasUnsavedChanges(false);
      setJsonValid(true);
      setJsonParseError('');
    }
  }, [nodes, edges, workflowSettings, workflowData?.name, currentMode, loading, generateJsonFromWorkflowState, lastVisualEditorSnapshot]);

  // Initialize JSON when switching to JSON tab
  useEffect(() => {
    if (currentMode === 'json' && !jsonEditorText && workflowData) {
      const initialJson = generateJsonFromWorkflowState();
      setJsonEditorText(initialJson);
      setLastVisualEditorSnapshot(initialJson);
      setJsonValid(true);
      setJsonParseError('');
    }
  }, [currentMode, jsonEditorText, workflowData, generateJsonFromWorkflowState]);

  return {
    jsonEditorText,
    setJsonEditorText,
    jsonValid,
    jsonParseError,
    jsonHasUnsavedChanges,
    isApplyingJson,
    lastAppliedSource,
    jsonBeforeLlmApply,
    generateJsonFromWorkflowState,
    handleJsonChange,
    handleJsonChangeWithRevertClear,
    applyJsonToWorkflow,
    revertLastLlmApply,
  };
}

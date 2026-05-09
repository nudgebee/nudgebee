import { useState, useCallback, useMemo } from 'react';
import { applyNodeChanges, applyEdgeChanges, addEdge, type Node } from 'reactflow';
import { nodeTypes } from '@components1/workflow/nodes';
import type { NodeCategories } from '@components1/workflow/types';
import DeletableEdge from '@components1/workflow/components/DeletableEdge';
import ConditionalEdge from '@components1/workflow/components/ConditionalEdge';
import { validateTaskData, validateTriggerData } from './useTaskValidation';
import { sanitizeTaskId } from '@components1/workflow/utils';

export const useWorkflowInteractions = (categories: NodeCategories, taskDefinitions: any) => {
  const [selectedNode, setSelectedNode] = useState<Node | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [expandedCategory, setExpandedCategory] = useState<string | null>(null);
  const [actionDetailsSidebarOpen, setActionDetailsSidebarOpen] = useState(false);
  const [triggerConfigSidebarOpen, setTriggerConfigSidebarOpen] = useState(false);
  const [selectedActionType, setSelectedActionType] = useState<string | null>(null);
  const [selectedNodeForTask, setSelectedNodeForTask] = useState<Node | null>(null);

  const nodeTypesConfig = useMemo(() => nodeTypes, []);
  const edgeTypesConfig = useMemo(
    () => ({
      smoothstep: DeletableEdge,
      conditional: ConditionalEdge,
    }),
    []
  );

  const onNodesChange = useCallback(
    (changes: any, setNodes: React.Dispatch<React.SetStateAction<any[]>>) => setNodes((nodesSnapshot) => applyNodeChanges(changes, nodesSnapshot)),
    []
  );

  const onEdgesChange = useCallback(
    (changes: any, setEdges: React.Dispatch<React.SetStateAction<any[]>>) => setEdges((edgesSnapshot) => applyEdgeChanges(changes, edgesSnapshot)),
    []
  );

  const onConnect = useCallback((params: any, setEdges: React.Dispatch<React.SetStateAction<any[]>>) => {
    setEdges((edgesSnapshot) => addEdge(params, edgesSnapshot));
  }, []);

  const onNodeClick = useCallback(
    (
      event: any,
      node: any,
      currentMode: 'editor' | 'json' | 'executions',
      setNodes: React.Dispatch<React.SetStateAction<any[]>>,
      setSelectedActionType: React.Dispatch<React.SetStateAction<string | null>>,
      setActionDetailsSidebarOpen: React.Dispatch<React.SetStateAction<boolean>>,
      setSidebarOpen: React.Dispatch<React.SetStateAction<boolean>>
    ) => {
      // Check if the click was on a toolbar button or its children
      const clickedElement = event.target as HTMLElement;

      const isToolbarClick =
        clickedElement.closest('.nodrag') ||
        clickedElement.classList.contains('nodrag') ||
        clickedElement.classList.contains('nopan') ||
        clickedElement.id === 'customButton' ||
        clickedElement.id == 'change-id'; // Add specific check for delete button

      if (isToolbarClick) {
        return; // Don't process the node click if it was on a toolbar button
      }
      setSelectedNode(node);

      // Update nodes array to mark the clicked node as selected
      setNodes((nds) =>
        nds.map((n) => ({
          ...n,
          selected: n.id === node.id,
        }))
      );

      // Different behavior based on mode
      if (currentMode === 'editor' || currentMode === 'json') {
        if (node.type === 'action' || node.type === 'switch') {
          // Check if node has taskConfig (for workflow nodes) or subcategory (for new nodes)
          const taskType = node.data.taskConfig?.type || node.data.subcategory;
          if (taskType) {
            setSelectedActionType(taskType);
            setActionDetailsSidebarOpen(true);
            setTriggerConfigSidebarOpen(false);
            setSidebarOpen(false);
          } else {
            console.warn('Node has no taskConfig or subcategory:', node.data);
          }
        } else if (node.type === 'trigger') {
          // Open trigger configuration sidebar
          setTriggerConfigSidebarOpen(true);
          setActionDetailsSidebarOpen(false);
          setSidebarOpen(false);
        }
      }
    },
    []
  );

  const addNode = useCallback(
    (categoryKey: string, subcategoryKey: string, existingNodes?: Node[], customPosition?: { x: number; y: number }) => {
      const category = categories[categoryKey as keyof typeof categories];
      const subcategory = category.subcategories[subcategoryKey as keyof typeof category.subcategories];

      let nodeType = 'action';
      if (categoryKey === 'triggers') {
        nodeType = 'trigger';
      } else if (subcategoryKey === 'core.switch') {
        nodeType = 'switch';
      }

      // Helper function to generate unique ID
      const generateUniqueId = (baseName: string, existingNodes: Node[] = []): string => {
        const existingIds = new Set(existingNodes.map((node) => node.id));

        if (!existingIds.has(baseName)) {
          return baseName;
        }

        let counter = 1;
        let uniqueId = `${baseName}-${counter}`;
        while (existingIds.has(uniqueId)) {
          counter++;
          uniqueId = `${baseName}-${counter}`;
        }
        return uniqueId;
      };

      // Generate unique ID early - sanitize first to match what will be saved
      // This must happen before taskConfig initialization so we can set taskConfig.id
      const uniqueId = generateUniqueId(sanitizeTaskId(subcategoryKey), existingNodes);

      // Initialize task configuration for action nodes or trigger data for trigger nodes
      let taskConfig = null;
      let triggerData = null;

      if (nodeType === 'switch') {
        taskConfig = {
          type: 'core.switch',
          id: uniqueId,
          config: {
            expression: '',
            cases: [{ value: '' }],
            default_next: '',
          },
          valid: false,
          errors: { expression: 'Expression is required' },
        };
      } else if (nodeType === 'action') {
        // For workflow actions, use subcategoryKey directly as the API type
        const apiType = subcategoryKey;

        // Validate with empty config to get initial validation state
        const initialValidation = validateTaskData(apiType, {}, taskDefinitions);

        taskConfig = {
          type: apiType,
          id: uniqueId,
          config: {},
          valid: initialValidation.isValid,
          errors: initialValidation.errors,
        };
      } else if (nodeType === 'trigger') {
        // For trigger nodes, create trigger data with validation
        const initialValidation = validateTriggerData(subcategoryKey, {});
        triggerData = {
          type: subcategoryKey, // e.g., 'schedule', 'manual', 'webhook', 'event'
          params: {}, // Empty params initially - user will configure these
          valid: initialValidation.isValid,
          errors: initialValidation.errors,
        };
      }

      // Calculate position based on node type with proper spacing
      const getNodePosition = () => {
        if (nodeType === 'trigger') {
          // Position triggers at the top with minimal vertical spread
          return {
            x: Math.random() * 500 + 50, // Spread horizontally: 50-550px
            y: 100, // Fixed at top: 100px from top
          };
        }
        // Position actions well below triggers with significant gap
        return {
          x: Math.random() * 500 + 250, // Same horizontal spread: 50-550px
          y: Math.random() * 300 + 100, // Well below triggers: 400-700px from top (300px gap minimum)
        };
      };

      const newNode = {
        id: uniqueId,
        type: nodeType,
        position: customPosition || getNodePosition(),
        data: {
          label: subcategory.label,
          description: subcategory.description,
          category: categoryKey,
          subcategory: subcategoryKey,
          ...(taskConfig && { taskConfig }),
          ...(triggerData && {
            trigger: triggerData,
            triggerType: triggerData.type,
            triggerParams: triggerData.params,
          }),
        },
      };

      return newNode;
    },
    [categories]
  );

  const toggleCategory = (categoryKey: string) => {
    setExpandedCategory(expandedCategory === categoryKey ? null : categoryKey);
  };

  const deselectAllNodes = useCallback((setNodes: React.Dispatch<React.SetStateAction<any[]>>) => {
    setNodes((nds) => nds.map((n) => ({ ...n, selected: false })));
  }, []);

  const handleCloseNodeTaskDetails = useCallback(
    (setNodes: React.Dispatch<React.SetStateAction<any[]>>) => {
      setSelectedNodeForTask(null);
      deselectAllNodes(setNodes);
    },
    [deselectAllNodes]
  );

  const closeActionDetailsSidebar = useCallback(
    (setNodes: React.Dispatch<React.SetStateAction<any[]>>) => {
      setActionDetailsSidebarOpen(false);
      deselectAllNodes(setNodes);
    },
    [deselectAllNodes]
  );

  const closeTriggerConfigSidebar = useCallback(
    (setNodes: React.Dispatch<React.SetStateAction<any[]>>) => {
      setTriggerConfigSidebarOpen(false);
      deselectAllNodes(setNodes);
    },
    [deselectAllNodes]
  );

  const updateTriggerConfig = useCallback(
    (nodeId: string, triggerConfig: { type: string; params: any }, setNodes: React.Dispatch<React.SetStateAction<any[]>>) => {
      // Validate the trigger config
      const validation = validateTriggerData(triggerConfig.type, triggerConfig.params);

      setNodes((nds) =>
        nds.map((n) =>
          n.id === nodeId
            ? {
                ...n,
                data: {
                  ...n.data,
                  trigger: {
                    ...triggerConfig,
                    valid: validation.isValid,
                    errors: validation.errors,
                  },
                  triggerType: triggerConfig.type,
                  triggerParams: triggerConfig.params,
                },
              }
            : n
        )
      );
    },
    []
  );

  return {
    selectedNode,
    setSelectedNode,
    sidebarOpen,
    setSidebarOpen,
    expandedCategory,
    actionDetailsSidebarOpen,
    setActionDetailsSidebarOpen,
    triggerConfigSidebarOpen,
    setTriggerConfigSidebarOpen,
    selectedActionType,
    setSelectedActionType,
    selectedNodeForTask,
    setSelectedNodeForTask,
    nodeTypesConfig,
    edgeTypesConfig,
    onNodesChange,
    onEdgesChange,
    onConnect,
    onNodeClick,
    addNode,
    toggleCategory,
    deselectAllNodes,
    handleCloseNodeTaskDetails,
    closeActionDetailsSidebar,
    closeTriggerConfigSidebar,
    updateTriggerConfig,
  };
};

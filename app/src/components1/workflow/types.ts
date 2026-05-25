export interface SubCategory {
  label: string;
  description: string;
  icon: string;
  aliases?: string[];
  deprecated?: boolean;
  deprecationMessage?: string;
}

export interface Category {
  label: string;
  description: string;
  icon: string | any;
  color: string; // Category color for consistent theming
  subcategories: Record<string, SubCategory>;
}

export type NodeCategories = Record<string, Category>;

export interface TaskConfig {
  type: string; // Maps to API task type (e.g., 'ticket_create', 'notification')
  config: any; // Action-specific configuration
  valid: boolean; // Validation state
  errors: Record<string, string>; // Field-specific errors
  if?: string; // Conditional execution (Go template expression)
  timeout?: string; // Task-specific timeout
  set_state?: any; // Persistent state configuration
  set_vars?: any; // Workflow variables configuration
  matrix?: any; // Matrix execution configuration
  hooks?: any; // Task hooks configuration
  failure_policy?: any; // Failure policy (action + retry)
  disabled?: boolean; // Task is muted — splice predecessors → successors so the chain keeps flowing past it
  // Stash for restoring connections on enable. New shape: { originals, splices? }.
  // Old shape (from before splice): a flat StashedEdge[]. Both are accepted on read.
  _prev_edges?:
    | Array<{
        source: string;
        target: string;
        sourceHandle?: string;
        targetHandle?: string;
        type?: string;
      }>
    | {
        originals: Array<{
          source: string;
          target: string;
          sourceHandle?: string;
          targetHandle?: string;
          type?: string;
        }>;
        splices?: Array<{
          source: string;
          target: string;
          sourceHandle?: string;
          targetHandle?: string;
          type?: string;
        }>;
      };
}

export type NodeData = {
  label: string;
  description: string;
  category?: string;
  subcategory?: string;
  taskConfig?: TaskConfig; // NEW: Task configuration data
  [key: string]: unknown;
};

export interface NodeProps {
  data: NodeData;
  isConnectable: boolean;
  selected?: boolean;
  onTestTask?: (taskType: string, params: any) => void;
  onTriggerRun?: () => void;
  accountId?: string;
}

export interface ExecutionPagination {
  currentPage: number;
  recordsPerPage: number;
  totalCount: number;
}

// Task Definition API Types
export interface TaskDefinitionField {
  default: any;
  description: string;
  required: boolean;
  type: string;
  order?: number;
  title?: string;
  enum?: string[];
  options?: string[];
  sub_type?: string;
  is_encrypted?: boolean;
}

export interface TaskDefinitionSchema {
  [fieldName: string]: TaskDefinitionField;
}

export interface TaskDefinition {
  name: string;
  description: string;
  input_schema: TaskDefinitionSchema;
  output_schema: TaskDefinitionSchema | null;
  display_name?: string;
  aliases?: string[];
}

export interface TaskDefinitionAPIResponse {
  data: {
    workflow_list_taskdefinitions: {
      tasks: TaskDefinition[];
    };
  };
}

export type WorkflowStatus = 'ACTIVE' | 'INACTIVE' | 'PAUSED' | 'DRAFT';

/**
 * WorkflowData represents the structure of a workflow JSON
 * Used for validating LLM-generated workflow responses
 */
export interface WorkflowData {
  name: string;
  definition: {
    tasks: any[];
    triggers: any[];
  };
}

export interface WorkflowInput {
  id: string;
  type: 'string' | 'int' | 'bool' | 'json' | 'array';
  description: string;
  default: any;
  customType?: string; // For custom type specification
  validation?: {
    isValid: boolean;
    error?: string;
  };
}

export interface WorkflowSettings {
  timeout: string;
  maxInterval: string;
  retries: number;
  inputs: WorkflowInput[];
  outputs: Record<string, string>;
  tags: string[];
  status: WorkflowStatus;
}

import type { Node, Edge } from 'reactflow';
import { convertWorkflowToReactFlow } from './workflowLayoutEngine';

/**
 * AI Generate Workflow Response structure
 */
export interface AIGenerateWorkflowResponse {
  data: {
    ai_generate_workflow: {
      data: {
        response: string[];
        query: string;
        chain_name: string;
        conversation_id: string;
        session_id: string;
        message_id: string;
        agent_id: string;
        status: string;
      };
    };
  };
}

/**
 * Parsed workflow structure from AI response
 */
export interface ParsedAIWorkflow {
  name: string;
  definition: {
    version: string;
    triggers: Array<{ type: string; params?: any }>;
    tasks: Array<{
      id: string;
      type: string;
      params?: any;
      depends_on?: string[];
      if?: string;
      matrix?: string;
      outputs?: any;
      timeout?: string;
      hooks?: any;
    }>;
    inputs?: any[];
    output?: any;
    timeout?: string;
    retry_policy?: {
      maximum_attempts: number;
      initial_interval: string;
      maximum_interval: string;
      backoff_coefficient: number;
    };
  };
}

/**
 * Result of building a workflow from AI response
 */
export interface BuildWorkflowResult {
  success: boolean;
  workflowData?: {
    id: string | null;
    name: string;
    definition: any;
    tags: Record<string, any>;
  };
  nodes?: Node[];
  edges?: Edge[];
  viewport?: { x: number; y: number; zoom: number };
  error?: string;
}

const removeCommentsFromJSON = (jsonString: string): string => {
  let result = '';
  let inString = false;
  let stringDelimiter = '';
  let i = 0;

  while (i < jsonString.length) {
    const char = jsonString[i];
    const nextChar = jsonString[i + 1];

    // Handle string boundaries - improved escaped character detection
    if (char === '"' || char === "'") {
      // Count consecutive backslashes before this quote
      let backslashCount = 0;
      let j = i - 1;
      while (j >= 0 && jsonString[j] === '\\') {
        backslashCount++;
        j--;
      }

      // If even number of backslashes (or zero), the quote is not escaped
      const isEscaped = backslashCount % 2 === 1;

      if (!isEscaped) {
        if (!inString) {
          inString = true;
          stringDelimiter = char;
        } else if (char === stringDelimiter) {
          inString = false;
          stringDelimiter = '';
        }
      }
      result += char;
      i++;
      continue;
    }

    // If we're inside a string, keep everything as-is
    if (inString) {
      result += char;
      i++;
      continue;
    }

    // Handle single-line comments: // comment
    if (char === '/' && nextChar === '/') {
      // Skip until end of line
      i += 2;
      while (i < jsonString.length && jsonString[i] !== '\n' && jsonString[i] !== '\r') {
        i++;
      }
      continue;
    }

    // Handle multi-line comments: /* comment */
    if (char === '/' && nextChar === '*') {
      // Skip until */
      i += 2;
      while (i < jsonString.length - 1) {
        if (jsonString[i] === '*' && jsonString[i + 1] === '/') {
          i += 2;
          break;
        }
        i++;
      }
      continue;
    }

    // Keep the character
    result += char;
    i++;
  }

  return result;
};

/**
 * Extracts the first complete JSON object from a string that may contain multiple JSON objects
 * Uses depth tracking to properly handle nested objects
 * @param jsonString - String that may contain one or more JSON objects
 * @returns First complete JSON object as string, or original string if only one object found
 */
const extractFirstCompleteJSON = (jsonString: string): string => {
  let depth = 0;
  let inString = false;
  let stringDelimiter = '';
  let startIdx = -1;

  for (let i = 0; i < jsonString.length; i++) {
    const char = jsonString[i];

    // Handle string boundaries to avoid counting braces inside strings
    if (char === '"' || char === "'") {
      // Count consecutive backslashes before this quote
      let backslashCount = 0;
      let j = i - 1;
      while (j >= 0 && jsonString[j] === '\\') {
        backslashCount++;
        j--;
      }

      // If even number of backslashes (or zero), the quote is not escaped
      const isEscaped = backslashCount % 2 === 1;

      if (!isEscaped) {
        if (!inString) {
          inString = true;
          stringDelimiter = char;
        } else if (char === stringDelimiter) {
          inString = false;
          stringDelimiter = '';
        }
      }
      continue;
    }

    // Skip if inside a string
    if (inString) {
      continue;
    }

    // Track depth of braces
    if (char === '{') {
      if (depth === 0) {
        startIdx = i; // Mark start of first JSON object
      }
      depth++;
    } else if (char === '}') {
      depth--;
      if (depth === 0 && startIdx !== -1) {
        // Found complete first JSON object
        const firstObject = jsonString.substring(startIdx, i + 1);

        // Check if there's more content after this object (excluding whitespace)
        const remainingContent = jsonString.substring(i + 1).trim();
        if (remainingContent.length > 0 && remainingContent[0] === '{') {
          console.warn(
            `Multiple JSON objects detected. Extracted first object (${firstObject.length} chars). Remaining: ${remainingContent.length} chars`
          );
        }

        return firstObject;
      }
    }
  }

  // No complete object found or only one object - return original
  return jsonString;
};

/**
 * Parses the AI-generated workflow response
 * @param response - The AI generate workflow API response
 * @returns Parsed workflow object or null if parsing fails
 */
export const parseAIWorkflowResponse = (response: AIGenerateWorkflowResponse): ParsedAIWorkflow | null => {
  try {
    // Extract the response array from the nested structure
    const responseArray = response?.data?.ai_generate_workflow?.data?.response;

    if (!responseArray || !Array.isArray(responseArray) || responseArray.length === 0) {
      console.error('Invalid AI workflow response: response array is missing or empty');
      return null;
    }

    // The response is a JSON string in the first element of the array
    let workflowJsonString = responseArray[0];

    if (typeof workflowJsonString !== 'string') {
      console.error('Invalid AI workflow response: response is not a string');
      return null;
    }

    // Strip markdown code fences if present (common in LLM output)
    workflowJsonString = workflowJsonString
      .trim()
      .replace(/^```(?:json)?\s*\n?/i, '')
      .replace(/\n?```\s*$/i, '')
      .trim();

    // Handle multiple workflows concatenated together - take only the first one
    // Use depth tracking to properly extract first complete JSON object
    workflowJsonString = extractFirstCompleteJSON(workflowJsonString.trim());

    // Remove JSON comments and clean up (AI sometimes adds explanatory comments)
    // This function respects string boundaries to avoid breaking URLs and other string content
    workflowJsonString = removeCommentsFromJSON(workflowJsonString);

    // Remove ellipsis placeholders: ... or […]
    workflowJsonString = workflowJsonString.replace(/\[\s*\.\.\.\s*\]/g, '[]');
    workflowJsonString = workflowJsonString.replace(/\{\s*\.\.\.\s*\}/g, '{}');
    workflowJsonString = workflowJsonString.replace(/:\s*\.\.\.\s*([,}\]])/g, ': null$1');
    // Clean up any trailing commas before closing braces/brackets (common in commented JSON)
    workflowJsonString = workflowJsonString.replace(/,(\s*[}\]])/g, '$1');

    // Parse the JSON string directly (don't use parseJSONSafely which replaces ' with "
    // and corrupts template expressions like Tasks['task-id'])
    let parsedWorkflow: any;
    try {
      parsedWorkflow = JSON.parse(workflowJsonString);
    } catch (parseError) {
      console.error('Failed to parse workflow JSON:', workflowJsonString.substring(0, 200), parseError);
      return null;
    }

    if (!parsedWorkflow || typeof parsedWorkflow !== 'object') {
      console.error('Invalid workflow structure after parsing');
      return null;
    }

    // Normalize structure - AI sometimes returns flat structure without definition wrapper
    if (!parsedWorkflow.definition) {
      const { name, ...definitionFields } = parsedWorkflow;
      parsedWorkflow = {
        name: name,
        definition: definitionFields,
      };
    }

    // Normalize structure - AI sometimes returns version at wrong level
    if (parsedWorkflow.version && !parsedWorkflow.definition.version) {
      parsedWorkflow.definition.version = parsedWorkflow.version;
      delete parsedWorkflow.version;
    }

    // Remove nested name in definition if it exists
    if (parsedWorkflow.definition.name) {
      delete parsedWorkflow.definition.name;
    }

    // Validate required fields
    if (!parsedWorkflow.name || !parsedWorkflow.definition) {
      console.error('Invalid AI workflow: missing name or definition');
      return null;
    }

    if (!parsedWorkflow.definition.triggers || !Array.isArray(parsedWorkflow.definition.triggers)) {
      console.error('Invalid AI workflow: missing or invalid triggers');
      return null;
    }

    if (!parsedWorkflow.definition.tasks || !Array.isArray(parsedWorkflow.definition.tasks)) {
      console.error('Invalid AI workflow: missing or invalid tasks');
      return null;
    }

    // Ensure all triggers have params field
    parsedWorkflow.definition.triggers = parsedWorkflow.definition.triggers.map((trigger: any) => {
      return {
        type: trigger.type,
        params: trigger.params || {}, // Default to empty object if missing
      };
    });

    // Ensure all tasks have default fields
    parsedWorkflow.definition.tasks = parsedWorkflow.definition.tasks.map((task: any) => {
      const normalizedTask: any = {
        id: task.id,
        type: task.type,
        params: task.params || {}, // Default to empty object
        depends_on: task.depends_on || [], // Default to empty array
      };

      // Add optional fields only if they exist (preserve even if falsy values like empty strings)
      if (task.if !== undefined && task.if !== null) {
        normalizedTask.if = task.if;
      }
      if (task.matrix !== undefined && task.matrix !== null) {
        normalizedTask.matrix = task.matrix;
      }
      if (task.outputs !== undefined && task.outputs !== null) {
        normalizedTask.outputs = task.outputs;
      }
      if (task.timeout !== undefined && task.timeout !== null) {
        normalizedTask.timeout = task.timeout;
      }
      if (task.hooks !== undefined && task.hooks !== null) {
        normalizedTask.hooks = task.hooks;
      }

      return normalizedTask;
    });

    // Ensure definition has all expected fields with defaults
    parsedWorkflow.definition = {
      version: parsedWorkflow.definition.version || 'v1',
      timeout: parsedWorkflow.definition.timeout || '',
      inputs: parsedWorkflow.definition.inputs || [],
      tasks: parsedWorkflow.definition.tasks, // Already normalized above
      triggers: parsedWorkflow.definition.triggers, // Already normalized above
      output: parsedWorkflow.definition.output || {},
      retry_policy: parsedWorkflow.definition.retry_policy || {
        maximum_attempts: 3,
        initial_interval: '1s',
        maximum_interval: '60s',
        backoff_coefficient: 2.0,
      },
    };

    return parsedWorkflow;
  } catch (error) {
    console.error('Exception during AI workflow parsing:', error);
    return null;
  }
};

/**
 * Builds a workflow in the WorkflowBuilderNotebook from an AI-generated response
 * @param aiResponse - The AI generate workflow API response
 * @param taskDefinitions - Task definitions for validation and layout
 * @returns BuildWorkflowResult containing workflow data, nodes, edges, and viewport
 */
export const buildWorkflowFromAIResponse = (aiResponse: AIGenerateWorkflowResponse, taskDefinitions: any[]): BuildWorkflowResult => {
  try {
    const parsedWorkflow = parseAIWorkflowResponse(aiResponse);

    if (!parsedWorkflow) {
      return {
        success: false,
        error: 'Failed to parse AI workflow response',
      };
    }

    // Create workflow data structure compatible with WorkflowBuilderNotebook
    const workflowData = {
      id: null,
      name: parsedWorkflow.name,
      definition: parsedWorkflow.definition,
      tags: {},
    };

    // Convert workflow definition to ReactFlow nodes and edges
    const { nodes, edges, viewport } = convertWorkflowToReactFlow(
      workflowData.definition,
      {
        minHorizontalSpacing: 250,
        minVerticalSpacing: 180,
        minTriggerSpacing: 250,
        minConditionalSpacing: 120,
      },
      taskDefinitions
    );

    return {
      success: true,
      workflowData,
      nodes,
      edges,
      viewport,
    };
  } catch (error) {
    console.error('Exception during workflow build:', error);
    return {
      success: false,
      error: error instanceof Error ? error.message : 'Unknown error occurred',
    };
  }
};

/**
 * Validates if an AI workflow response is complete and successful
 * @param response - The AI generate workflow API response
 * @returns True if the response is valid and complete
 */
export const isValidAIWorkflowResponse = (response: AIGenerateWorkflowResponse): boolean => {
  try {
    const status = response?.data?.ai_generate_workflow?.data?.status;
    const responseArray = response?.data?.ai_generate_workflow?.data?.response;

    return status === 'COMPLETED' && Array.isArray(responseArray) && responseArray.length > 0;
  } catch (error) {
    console.error(error);
    return false;
  }
};

/**
 * Conversation message structure for the LLM response generator
 */
export interface ConversationMessage {
  id?: string;
  messageId?: string;
  text: string;
  type: 'question' | 'response' | 'tool_call' | 'acknowledgment' | 'followup-question';
  created_at: string;
  updated_at: string;
  user?: string;
  agentName?: string;
  parentAgents?: string[];
  references?: any[];
}

/**
 * Workflow API response data structure (inner data object)
 */
export interface WorkflowResponseData {
  response?: string | string[];
  query?: string;
  chain_name?: string;
  conversation_id?: string;
  session_id?: string;
  message_id?: string;
  agent_id?: string;
  status?: string;
}

/**
 * Builds conversation messages from workflow API response
 * @param response - The workflow API response data
 * @param userName - The user's display name
 * @param fallbackQuery - Fallback query text if response.query is not available
 * @returns Array of conversation messages
 */
export const buildWorkflowConversationMessages = (response: WorkflowResponseData, userName: string, fallbackQuery: string): ConversationMessage[] => {
  const messages: ConversationMessage[] = [];
  const timestamp = new Date().toISOString();

  // Add the question message
  messages.push({
    messageId: response.message_id,
    text: response.query || fallbackQuery,
    type: 'question',
    created_at: timestamp,
    updated_at: timestamp,
    user: userName || 'User',
  });

  // Add the response message(s)
  if (response.response) {
    let responseText: string;

    if (Array.isArray(response.response)) {
      // Join all response items if it's an array
      responseText = response.response.join('\n\n');
    } else {
      // Handle case where response is a string
      responseText = response.response;
    }

    messages.push({
      id: response.message_id,
      text: responseText,
      type: 'response',
      created_at: timestamp,
      updated_at: timestamp,
      agentName: response.chain_name || 'workflow',
      parentAgents: [],
      references: [],
    });
  }

  return messages;
};

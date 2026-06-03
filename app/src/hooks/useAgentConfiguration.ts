import { useEffect, useCallback, useReducer } from 'react';
import apiAskNudgebee from '@api1/ask-nudgebee';

export interface RawAgent {
  name: string;
  status: string;
  aliases?: string[];
  [key: string]: any;
}

export interface FormattedAgent {
  name: string;
  display_name: string;
}

export interface LLMFunction {
  id: string;
  name: string;
  description: string;
  prompt: string;
  variables: any;
  variable_defaults: any;
  status: string;
  version: string;
  account_id: string;
  tenant_id: string;
  created_by: string;
  updated_by: string;
  created_at: string;
  updated_at: string;
}

interface AgentState {
  allAgents: RawAgent[];
  enabledAgents: FormattedAgent[];
  loadingAgents: boolean;
  allFunctions: LLMFunction[];
}

type AgentAction =
  | { type: 'FETCH_AGENTS_START' }
  | { type: 'FETCH_AGENTS_SUCCESS'; allAgents: RawAgent[]; enabledAgents: FormattedAgent[] }
  | { type: 'FETCH_AGENTS_EMPTY' }
  | { type: 'FETCH_AGENTS_END' }
  | { type: 'SET_FUNCTIONS'; functions: LLMFunction[] };

interface UseAgentConfigurationReturn {
  allAgents: RawAgent[];
  enabledAgents: FormattedAgent[];
  loadingAgents: boolean;
  allFunctions: LLMFunction[];
  refreshAgents: () => Promise<void>;
}

const initialState: AgentState = {
  allAgents: [],
  enabledAgents: [],
  loadingAgents: false,
  allFunctions: [],
};

function agentReducer(state: AgentState, action: AgentAction): AgentState {
  switch (action.type) {
    case 'FETCH_AGENTS_START':
      return { ...state, loadingAgents: true };
    case 'FETCH_AGENTS_SUCCESS':
      return { ...state, loadingAgents: false, allAgents: action.allAgents, enabledAgents: action.enabledAgents };
    case 'FETCH_AGENTS_EMPTY':
      return { ...state, loadingAgents: false, allAgents: [], enabledAgents: [] };
    case 'FETCH_AGENTS_END':
      return { ...state, loadingAgents: false };
    case 'SET_FUNCTIONS':
      return { ...state, allFunctions: action.functions };
    default:
      return state;
  }
}

export const useAgentConfiguration = (accountId: string | number): UseAgentConfigurationReturn => {
  const [state, dispatch] = useReducer(agentReducer, initialState);

  // --- Logic: List Agents ---
  const listAgents = useCallback(async () => {
    if (!accountId) {
      return;
    }

    dispatch({ type: 'FETCH_AGENTS_START' });
    try {
      const res: any = await apiAskNudgebee.listAgents({ accountId });
      const listAgentResponse: RawAgent[] = res?.data?.data?.ai_list_agents?.data ?? [];

      if (listAgentResponse.length > 0) {
        const agents = listAgentResponse
          .filter((agent) => agent.status === 'enabled')
          .map((agent) => ({
            name: agent.name,
            display_name: agent.aliases?.[0] ?? agent.name,
          }));
        agents.sort((a, b) => a.name.localeCompare(b.name));

        dispatch({
          type: 'FETCH_AGENTS_SUCCESS',
          allAgents: listAgentResponse,
          enabledAgents: agents,
        });
      } else {
        dispatch({ type: 'FETCH_AGENTS_EMPTY' });
      }
    } catch (error) {
      console.error('Failed to list agents', error);
      dispatch({ type: 'FETCH_AGENTS_END' });
    }
  }, [accountId]);

  // --- Logic: List Functions ---
  const listFunctions = useCallback(async () => {
    if (!accountId) {
      return;
    }
    try {
      const res: any = await apiAskNudgebee.listFunctions({ accountId });
      dispatch({ type: 'SET_FUNCTIONS', functions: res?.res?.llm_functions || [] });
    } catch (error) {
      console.error('Failed to list functions', error);
    }
  }, [accountId]);

  // --- Effects ---

  // 1. Fetch Functions on mount/account change
  useEffect(() => {
    listFunctions();
  }, [accountId]);

  // 2. Fetch Agents on account change
  useEffect(() => {
    listAgents();
  }, [accountId]);

  return {
    allAgents: state.allAgents,
    enabledAgents: state.enabledAgents,
    loadingAgents: state.loadingAgents,
    allFunctions: state.allFunctions,
    refreshAgents: listAgents,
  };
};

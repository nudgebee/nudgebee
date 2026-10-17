import { useEffect, useCallback, useReducer, useRef } from 'react';
import apiAskNudgebee from '@api1/ask-nudgebee';
import apiKnowledgeBase from '@api1/knowledge-base';

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

export interface SkillCommand {
  name: string;
  description: string;
}

interface AgentState {
  allAgents: RawAgent[];
  enabledAgents: FormattedAgent[];
  loadingAgents: boolean;
  allSkills: SkillCommand[];
}

type AgentAction =
  | { type: 'FETCH_AGENTS_START' }
  | { type: 'FETCH_AGENTS_SUCCESS'; allAgents: RawAgent[]; enabledAgents: FormattedAgent[] }
  | { type: 'FETCH_AGENTS_EMPTY' }
  | { type: 'FETCH_AGENTS_END' }
  | { type: 'SET_SKILLS'; skills: SkillCommand[] };

interface UseAgentConfigurationReturn {
  allAgents: RawAgent[];
  enabledAgents: FormattedAgent[];
  loadingAgents: boolean;
  allSkills: SkillCommand[];
  refreshAgents: () => Promise<void>;
}

const initialState: AgentState = {
  allAgents: [],
  enabledAgents: [],
  loadingAgents: false,
  allSkills: [],
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
    case 'SET_SKILLS':
      return { ...state, allSkills: action.skills };
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

  // --- Logic: List Skills (offered as /skill-name commands) ---
  // The account the skill list was last requested for: a reply for an account the user
  // has since switched away from is dropped, so the list never shows another account's skills.
  const skillsAccountRef = useRef<string | number>(accountId);
  const listSkills = useCallback(async () => {
    skillsAccountRef.current = accountId;
    dispatch({ type: 'SET_SKILLS', skills: [] });
    if (!accountId) {
      return;
    }
    try {
      const skills = await apiKnowledgeBase.listSkillCommands(String(accountId));
      if (skillsAccountRef.current === accountId) {
        dispatch({ type: 'SET_SKILLS', skills });
      }
    } catch (error) {
      console.error('Failed to list skills', error);
    }
  }, [accountId]);

  // --- Effects ---

  // 1. Fetch Skills on mount/account change
  useEffect(() => {
    listSkills();
  }, [accountId]);

  // 2. Fetch Agents on account change
  useEffect(() => {
    listAgents();
  }, [accountId]);

  return {
    allAgents: state.allAgents,
    enabledAgents: state.enabledAgents,
    loadingAgents: state.loadingAgents,
    allSkills: state.allSkills,
    refreshAgents: listAgents,
  };
};

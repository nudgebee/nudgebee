import { renderHook, act, waitFor } from '@testing-library/react';
import { useAgentConfiguration } from '@hooks/useAgentConfiguration';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    listAgents: jest.fn(),
    listFunctions: jest.fn(),
  },
}));

import apiAskNudgebee from '@api1/ask-nudgebee';
const mockListAgents = apiAskNudgebee.listAgents as jest.Mock;
const mockListFunctions = apiAskNudgebee.listFunctions as jest.Mock;

const mockAgents = [
  { name: 'k8s_debug', status: 'enabled', aliases: ['K8s Debug'] },
  { name: 'aws_debug', status: 'enabled', aliases: ['AWS Debug'] },
  { name: 'disabled_agent', status: 'disabled', aliases: [] },
];

const mockFunctions = [{ id: 'fn-1', name: 'get_pods', description: 'Get pods', status: 'active' }];

describe('useAgentConfiguration', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockListFunctions.mockResolvedValue({ res: { llm_functions: mockFunctions } });
    mockListAgents.mockResolvedValue({ data: { data: { ai_list_agents: { data: mockAgents } } } });
  });

  it('initialises with empty state and loadingAgents=false', () => {
    const { result } = renderHook(() => useAgentConfiguration(''));
    expect(result.current.allAgents).toEqual([]);
    expect(result.current.enabledAgents).toEqual([]);
    expect(result.current.loadingAgents).toBe(false);
  });

  it('does not fetch when accountId is falsy', () => {
    renderHook(() => useAgentConfiguration(''));
    expect(mockListAgents).not.toHaveBeenCalled();
    expect(mockListFunctions).not.toHaveBeenCalled();
  });

  it('fetches functions on mount', async () => {
    renderHook(() => useAgentConfiguration('acc-1'));
    await waitFor(() => expect(mockListFunctions).toHaveBeenCalledWith({ accountId: 'acc-1' }));
  });

  it('fetches both agents and functions when accountId is provided', async () => {
    renderHook(() => useAgentConfiguration('acc-1'));
    await waitFor(() => {
      expect(mockListFunctions).toHaveBeenCalled();
      expect(mockListAgents).toHaveBeenCalled();
    });
  });

  it('filters out disabled agents and returns only enabled ones', async () => {
    const { result } = renderHook(() => useAgentConfiguration('acc-1'));
    await waitFor(() => expect(result.current.enabledAgents).toHaveLength(2));
    expect(result.current.enabledAgents.every((a) => !['disabled_agent'].includes(a.name))).toBe(true);
  });

  it('uses agent alias as display_name when available', async () => {
    const { result } = renderHook(() => useAgentConfiguration('acc-1'));
    await waitFor(() => expect(result.current.enabledAgents).toHaveLength(2));
    const k8sAgent = result.current.enabledAgents.find((a) => a.name === 'k8s_debug');
    expect(k8sAgent?.display_name).toBe('K8s Debug');
  });

  it('stores all agents (including disabled) in allAgents', async () => {
    const { result } = renderHook(() => useAgentConfiguration('acc-1'));
    await waitFor(() => expect(result.current.allAgents).toHaveLength(3));
  });

  it('refreshAgents re-fetches agents', async () => {
    const { result } = renderHook(() => useAgentConfiguration('acc-1'));
    await waitFor(() => expect(mockListAgents).toHaveBeenCalledTimes(1));

    await act(async () => {
      await result.current.refreshAgents();
    });
    expect(mockListAgents).toHaveBeenCalledTimes(2);
  });

  it('sets loadingAgents=false after fetch completes', async () => {
    const { result } = renderHook(() => useAgentConfiguration('acc-1'));
    await waitFor(() => expect(result.current.loadingAgents).toBe(false));
  });

  it('handles empty agent list gracefully', async () => {
    mockListAgents.mockResolvedValue({ data: { data: { ai_list_agents: { data: [] } } } });
    const { result } = renderHook(() => useAgentConfiguration('acc-1'));
    await waitFor(() => expect(result.current.loadingAgents).toBe(false));
    expect(result.current.allAgents).toEqual([]);
    expect(result.current.enabledAgents).toEqual([]);
  });
});

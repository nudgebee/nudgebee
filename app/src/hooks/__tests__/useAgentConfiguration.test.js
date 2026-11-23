import { act, renderHook } from '@testing-library/react';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { useAgentConfiguration } from '../useAgentConfiguration';

jest.mock('@api1/ask-nudgebee', () => ({ __esModule: true, default: { listAgents: jest.fn() } }));
jest.mock('@api1/knowledge-base', () => ({ __esModule: true, default: { listSkillCommands: jest.fn(() => Promise.resolve([])) } }));

const deferred = () => {
  let resolve;
  const promise = new Promise((r) => {
    resolve = r;
  });
  return { promise, resolve };
};
const reply = (names) => ({ data: { data: { ai_list_agents: { data: names.map((name) => ({ name, status: 'enabled' })) } } } });
const names = (result) => result.current.enabledAgents.map((a) => a.name);

describe('useAgentConfiguration', () => {
  it('drops the previous account agents on a switch, even when its reply lands late (#42736)', async () => {
    const reads = {};
    apiAskNudgebee.listAgents.mockImplementation(({ accountId }) => {
      reads[accountId] = deferred();
      return reads[accountId].promise;
    });
    const { result, rerender } = renderHook(({ accountId }) => useAgentConfiguration(accountId), { initialProps: { accountId: 'A' } });
    await act(async () => reads.A.resolve(reply(['help', 'test_agent_qa'])));
    expect(names(result)).toEqual(['help', 'test_agent_qa']);

    // Switch to B: A's agents are gone while B loads.
    reads.A = null;
    rerender({ accountId: 'B' });
    const pendingB = reads.B;
    expect(names(result)).toEqual([]);

    // Back to A and over to B again; an A reply that lands after the switch is dropped.
    rerender({ accountId: 'A' });
    const lateA = reads.A;
    rerender({ accountId: 'B' });
    await act(async () => lateA.resolve(reply(['help', 'test_agent_qa'])));
    expect(names(result)).toEqual([]);
    await act(async () => reads.B.resolve(reply(['help'])));
    await act(async () => pendingB.resolve(reply(['help'])));
    expect(names(result)).toEqual(['help']);
  });
});

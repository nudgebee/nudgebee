// listAgents drops agents that belong to an extension (a Worker Bee's agent carries
// config.requires_extension), so no agent list or picker offers them.

const mockQueryGraphQL = jest.fn();
jest.mock('@lib/HttpService', () => ({
  queryGraphQL: (...args: any[]) => mockQueryGraphQL(...args),
  gqlStringify: (v: any) => JSON.stringify(v),
}));
jest.mock('@lib/auth', () => ({ getUserSession: jest.fn() }));

import api from '@api1/ask-nudgebee';

describe('listAgents — extension agents are hidden', () => {
  beforeEach(() => mockQueryGraphQL.mockReset());

  it('keeps ordinary agents and drops a bee agent', async () => {
    mockQueryGraphQL.mockResolvedValue({
      data: {
        data: {
          ai_list_agents: {
            data: [
              { id: 'a1', name: 'k8s_debugger', config: {} },
              { id: 'a2', name: 'oom_fix', config: { requires_extension: 'worker_bees', bee: 'oom_fix' } },
              { id: 'a3', name: 'no_config' },
            ],
          },
        },
      },
    });
    const response: any = await api.listAgents({ accountId: 'acct-1' } as any);
    expect(response.data.data.ai_list_agents.data.map((a: any) => a.name)).toEqual(['k8s_debugger', 'no_config']);
  });

  it('passes an answer without an agent list through unchanged', async () => {
    const answer = { data: { errors: [{ message: 'boom' }] } };
    mockQueryGraphQL.mockResolvedValue(answer);
    await expect(api.listAgents({ accountId: 'acct-1' } as any)).resolves.toBe(answer);
  });
});

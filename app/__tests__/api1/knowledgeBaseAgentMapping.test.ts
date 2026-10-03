import apiKnowledgeBase, { KB_AGENT_WILDCARD } from '@api1/knowledge-base';
import { queryGraphQL } from '@lib/HttpService';

jest.mock('@lib/HttpService', () => ({
  queryGraphQL: jest.fn(),
}));

const mockQuery = queryGraphQL as jest.Mock;

// An unmapped knowledge base is invisible to every agent — the skill list,
// load_skills and skill narrowing all INNER JOIN llm_kb_agent_mappings. So the
// contract worth pinning is the request this layer actually puts on the wire and
// how it folds the reply, with the transport stubbed.
describe('knowledge base <-> agent mapping', () => {
  beforeEach(() => mockQuery.mockReset());

  it('sends the wildcard agent id when a KB is mapped to all agents', async () => {
    mockQuery.mockResolvedValue({ data: { data: { ai_create_kb_mapping: { data: 'ok', errors: [] } } } });

    const result = await apiKnowledgeBase.mapKnowledgeBaseToAgent('acct-1', 'kb-9', KB_AGENT_WILDCARD);

    expect(mockQuery).toHaveBeenCalledTimes(1);
    const [, operationName, variables] = mockQuery.mock.calls[0];
    expect(operationName).toBe('MapKBToAgent');
    expect(variables).toEqual({ request: { account_id: 'acct-1', kb_id: 'kb-9', agent_id: '*' } });
    expect(result).toEqual({ data: 'ok', errors: [] });
  });

  it('returns the wildcard so the edit form reopens on All agents', async () => {
    mockQuery.mockResolvedValue({ data: { data: { ai_list_kb_agents: { data: [KB_AGENT_WILDCARD], errors: [] } } } });

    const result = await apiKnowledgeBase.getKBAgents('acct-1', 'kb-9');

    expect(result.data).toEqual(['*']);
  });

  it('surfaces a nested RPC error instead of a silent success', async () => {
    mockQuery.mockResolvedValue({
      data: {
        errors: [
          {
            message: 'internal error',
            extensions: { internal: { response: { body: { errors: [{ message: 'agent not found: sre-agent' }] } } } },
          },
        ],
      },
    });

    const result = await apiKnowledgeBase.mapKnowledgeBaseToAgent('acct-1', 'kb-9', 'sre-agent');

    expect(result.data).toBeNull();
    expect(result.errors[0].message).toBe('agent not found: sre-agent');
  });

  it('never reaches the network for the demo account', async () => {
    const result = await apiKnowledgeBase.mapKnowledgeBaseToAgent('demo', 'kb-9', KB_AGENT_WILDCARD);

    expect(mockQuery).not.toHaveBeenCalled();
    expect(result.errors[0].message).toBe('Demo account does not have access.');
  });
});

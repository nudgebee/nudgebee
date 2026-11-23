import apiKnowledgeBase from '@api1/knowledge-base';
import { queryGraphQL } from '@lib/HttpService';

jest.mock('@lib/HttpService', () => ({
  queryGraphQL: jest.fn(),
}));

const mockQuery = queryGraphQL as jest.Mock;

const source = { id: 'src-1', repo: 'acme/skills', folder: 'skills', branch: '', visibility: 'organisation', status: 'pending', skill_count: 0 };

// Skill sources: the request each call puts on the wire and how it folds the reply.
describe('knowledge base skill sources', () => {
  beforeEach(() => mockQuery.mockReset());

  it('lists the sources with no request input', async () => {
    mockQuery.mockResolvedValue({ data: { data: { ai_list_kb_skill_sources: { data: [source], errors: [] } } } });

    const result = await apiKnowledgeBase.listSkillSources();

    const [query, operationName, variables] = mockQuery.mock.calls[0];
    expect(operationName).toBe('ListKBSkillSources');
    expect(query).toContain('ai_list_kb_skill_sources {');
    expect(variables).toBeUndefined();
    expect(result).toEqual({ data: [source], errors: [] });
  });

  it('creates an organisation source and drops account ids that only apply to selected accounts', async () => {
    mockQuery.mockResolvedValue({ data: { data: { ai_create_kb_skill_source: { data: source, errors: [] } } } });

    const result = await apiKnowledgeBase.createSkillSource({ repo: 'acme/skills', folder: 'skills', accountIds: ['acct-1'] });

    const [, operationName, variables] = mockQuery.mock.calls[0];
    expect(operationName).toBe('CreateKBSkillSource');
    expect(variables).toEqual({ request: { repo: 'acme/skills', folder: 'skills', branch: '', visibility: 'organisation', account_ids: [] } });
    expect(result).toEqual({ data: source, errors: [] });
  });

  it('creates a selected-accounts source with its accounts', async () => {
    mockQuery.mockResolvedValue({ data: { data: { ai_create_kb_skill_source: { data: source, errors: [] } } } });

    await apiKnowledgeBase.createSkillSource({ repo: 'acme/skills', branch: 'main', visibility: 'selected_accounts', accountIds: ['acct-1'] });

    expect(mockQuery.mock.calls[0][2]).toEqual({
      request: { repo: 'acme/skills', folder: '', branch: 'main', visibility: 'selected_accounts', account_ids: ['acct-1'] },
    });
  });

  it('updates who can use a source', async () => {
    mockQuery.mockResolvedValue({ data: { data: { ai_update_kb_skill_source: { data: source, errors: [] } } } });

    await apiKnowledgeBase.updateSkillSource('src-1', 'selected_accounts', ['acct-1', 'acct-2']);

    expect(mockQuery.mock.calls[0][1]).toBe('UpdateKBSkillSource');
    expect(mockQuery.mock.calls[0][2]).toEqual({ request: { id: 'src-1', visibility: 'selected_accounts', account_ids: ['acct-1', 'acct-2'] } });
  });

  it('returns the sync report', async () => {
    const report = { commit: 'abc', found: 3, added: 2, updated: 1, archived: 0, skipped: [{ source_path: 'x', reason: 'no SKILL.md' }] };
    mockQuery.mockResolvedValue({ data: { data: { ai_sync_kb_skill_source: { data: report, errors: [] } } } });

    const result = await apiKnowledgeBase.syncSkillSource('src-1');

    expect(mockQuery.mock.calls[0][1]).toBe('SyncKBSkillSource');
    expect(mockQuery.mock.calls[0][2]).toEqual({ request: { id: 'src-1' } });
    expect(result).toEqual({ data: report, errors: [] });
  });

  it('deletes a source', async () => {
    mockQuery.mockResolvedValue({ data: { data: { ai_delete_kb_skill_source: { data: { status: 'ok', id: 'src-1' }, errors: [] } } } });

    const result = await apiKnowledgeBase.deleteSkillSource('src-1');

    expect(mockQuery.mock.calls[0][1]).toBe('DeleteKBSkillSource');
    expect(result).toEqual({ data: { status: 'ok', id: 'src-1' }, errors: [] });
  });

  it("surfaces the action's error and a nested transport error", async () => {
    mockQuery.mockResolvedValueOnce({
      data: { data: { ai_sync_kb_skill_source: { data: null, errors: [{ message: 'a sync of this source is already running' }] } } },
    });
    expect(await apiKnowledgeBase.syncSkillSource('src-1')).toEqual({
      data: null,
      errors: [{ message: 'a sync of this source is already running' }],
    });

    mockQuery.mockResolvedValueOnce({
      data: {
        errors: [{ message: 'internal error', extensions: { internal: { response: { body: { errors: [{ message: 'repo not found' }] } } } } }],
      },
    });
    expect(await apiKnowledgeBase.createSkillSource({ repo: 'acme/missing' })).toEqual({ data: null, errors: [{ message: 'repo not found' }] });
  });

  it('carries source_id and skill_meta through the knowledge-base list', async () => {
    const meta = { source_path: 'skills/pdf', commit: 'abc' };
    mockQuery.mockResolvedValue({
      data: {
        data: {
          ai_list_kb: {
            data: [
              { id: 'kb-1', name: 'pdf', source_id: 'src-1', skill_meta: meta },
              { id: 'kb-2', name: 'b' },
            ],
            errors: [],
          },
        },
      },
    });

    const result = await apiKnowledgeBase.getKnowledgeBases('acct-1');

    expect(mockQuery.mock.calls[0][0]).toContain('skill_meta');
    expect(result.data[0]).toMatchObject({ source_id: 'src-1', skill_meta: meta });
    expect(result.data[1]).toMatchObject({ source_id: null, skill_meta: null });
  });
});

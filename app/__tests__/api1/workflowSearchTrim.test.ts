import apiWorkflow from '@api1/workflow';
import { queryGraphQL } from '@lib/HttpService';

jest.mock('@lib/HttpService', () => ({
  queryGraphQL: jest.fn(),
}));

const mockQuery = queryGraphQL as jest.Mock;

const sentVariables = () => mockQuery.mock.calls.at(-1)[2];

const search = (name?: string, tags?: string) => apiWorkflow.listWorkflows(['acc-1'], undefined, undefined, undefined, 10, '', name, tags);

// The Automations name and tag boxes pass the text as typed, and the name becomes a
// substring match, so a space around a pasted name used to be matched literally.
describe('apiWorkflow.listWorkflows search text', () => {
  beforeEach(() => {
    mockQuery.mockReset();
    mockQuery.mockResolvedValue({ data: { data: { workflow_list: { workflows: [] } } } });
  });

  it('drops spaces around the name', async () => {
    await search('  print  ');
    expect(sentVariables().name).toBe('print');

    await search(' print');
    expect(sentVariables().name).toBe('print');

    await search('print ');
    expect(sentVariables().name).toBe('print');
  });

  it('keeps a space inside the name', async () => {
    await search(' daily report ');

    expect(sentVariables().name).toBe('daily report');
  });

  it('applies no name filter when the name is only spaces', async () => {
    await search('   ');

    expect(sentVariables()).not.toHaveProperty('name');
  });

  it('drops spaces around the tags and sends none when they are only spaces', async () => {
    await search(undefined, '  team:sre  ');
    expect(sentVariables().tags).toBe('team:sre');

    await search(undefined, '   ');
    expect(sentVariables()).not.toHaveProperty('tags');
  });
});

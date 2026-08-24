import { buildWorkflowExportJson, buildWorkflowShareUrl, sanitizeWorkflowDefinitionForExport } from '@components/workflow/workflowExport';

describe('buildWorkflowShareUrl', () => {
  it('builds an absolute automation URL carrying the account id', () => {
    expect(buildWorkflowShareUrl('https://app.nudgebee.com', 'wf-1', 'acc-1')).toBe('https://app.nudgebee.com/automation/wf-1?accountId=acc-1');
  });
});

describe('sanitizeWorkflowDefinitionForExport', () => {
  const definition = () => ({
    version: '1',
    tasks: [
      {
        id: 'a',
        outputs: { some: 'artifact' },
        params: {
          tasks: [{ id: 'nested', outputs: { more: 'artifact' }, params: {} }],
        },
      },
    ],
    triggers: [
      { type: 'webhook', internal: { name: 'wf-1-hook' }, params: { secret: 'super-secret', path: '/hook' } },
      { type: 'schedule', internal: { name: 'keep-me' }, params: { cron: '* * * * *' } },
    ],
  });

  it('strips task outputs at every nesting level', () => {
    const result = sanitizeWorkflowDefinitionForExport(definition());
    expect(result.tasks[0].outputs).toBeUndefined();
    expect(result.tasks[0].params.tasks[0].outputs).toBeUndefined();
  });

  it('strips the secret and internal binding from webhook triggers only', () => {
    const result = sanitizeWorkflowDefinitionForExport(definition());
    expect(result.triggers[0].params.secret).toBeUndefined();
    expect(result.triggers[0].params.path).toBe('/hook');
    expect(result.triggers[0].internal).toBeUndefined();
    expect(result.triggers[1].internal).toEqual({ name: 'keep-me' });
    expect(result.triggers[1].params.cron).toBe('* * * * *');
  });

  it('does not mutate the input definition', () => {
    const input = definition();
    sanitizeWorkflowDefinitionForExport(input);
    expect(input.tasks[0].outputs).toEqual({ some: 'artifact' });
    expect(input.triggers[0].params.secret).toBe('super-secret');
  });

  it('tolerates a definition without tasks or triggers', () => {
    expect(sanitizeWorkflowDefinitionForExport({ version: '1' })).toEqual({ version: '1' });
  });

  it('passes a missing definition through instead of throwing', () => {
    expect(sanitizeWorkflowDefinitionForExport(undefined)).toBeUndefined();
    expect(sanitizeWorkflowDefinitionForExport(null)).toBeNull();
  });
});

describe('buildWorkflowExportJson', () => {
  const workflow = {
    id: 'wf-1',
    account_id: 'acc-1',
    name: 'My automation',
    status: 'ACTIVE',
    tags: { env: 'prod' },
    definition: { version: '1', tasks: [{ id: 'a', outputs: { x: 1 }, params: {} }], triggers: [{ type: 'manual', params: {} }] },
  };

  it('emits the same four keys, in the same order, as the builder JSON tab', () => {
    const parsed = JSON.parse(buildWorkflowExportJson(workflow));
    expect(Object.keys(parsed)).toEqual(['name', 'definition', 'tags', 'status']);
    expect(parsed.name).toBe('My automation');
    expect(parsed.status).toBe('ACTIVE');
    expect(parsed.tags).toEqual({ env: 'prod' });
  });

  it('sanitizes the definition it embeds', () => {
    const parsed = JSON.parse(buildWorkflowExportJson(workflow));
    expect(parsed.definition.tasks[0].outputs).toBeUndefined();
  });

  it('stringifies with a 2-space indent', () => {
    expect(buildWorkflowExportJson(workflow).split('\n')[1]).toMatch(/^ {2}"name"/);
  });

  it('falls back to an empty tag map when the workflow has no tags', () => {
    const parsed = JSON.parse(buildWorkflowExportJson({ ...workflow, tags: null }));
    expect(parsed.tags).toEqual({});
  });
});

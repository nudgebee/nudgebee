import { isCompleteWorkflowDefinition } from '@components1/llm/utils/isCompleteWorkflowDefinition';

describe('isCompleteWorkflowDefinition', () => {
  it('accepts a definition wrapper', () => {
    expect(isCompleteWorkflowDefinition({ definition: { tasks: [{ id: 't1' }] } })).toBe(true);
  });

  it('accepts a flat workflow with tasks and a non-empty triggers array', () => {
    expect(isCompleteWorkflowDefinition({ name: 'wf', tasks: [{ id: 't1' }], triggers: [{ type: 'manual' }] })).toBe(true);
  });

  it('accepts a flat workflow with tasks and a singular trigger', () => {
    expect(isCompleteWorkflowDefinition({ tasks: [{ id: 't1' }], trigger: { type: 'manual' } })).toBe(true);
  });

  it('rejects tasks with an empty triggers array (empty arrays are truthy)', () => {
    expect(isCompleteWorkflowDefinition({ tasks: [{ id: 't1' }], triggers: [] })).toBe(false);
  });

  it('rejects tasks with no trigger', () => {
    expect(isCompleteWorkflowDefinition({ tasks: [{ id: 't1' }] })).toBe(false);
  });

  it('rejects an empty tasks array', () => {
    expect(isCompleteWorkflowDefinition({ tasks: [], triggers: [{ type: 'manual' }] })).toBe(false);
  });

  it('rejects a partial fragment quoted in a read-only answer', () => {
    expect(isCompleteWorkflowDefinition({ tasks: 'see the get-pods task' })).toBe(false);
  });

  it('rejects null / non-object input', () => {
    expect(isCompleteWorkflowDefinition(null)).toBe(false);
    expect(isCompleteWorkflowDefinition(undefined)).toBe(false);
    expect(isCompleteWorkflowDefinition('string')).toBe(false);
  });
});

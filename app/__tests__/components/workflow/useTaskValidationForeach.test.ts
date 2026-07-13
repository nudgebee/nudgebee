import { validateTaskData } from '@components/workflow/hooks/useTaskValidation';

// Minimal task definitions mirroring the runbook-server schemas the validator
// consumes (input_schema is a flat field → schema-property map).
const TASK_DEFINITIONS = [
  {
    name: 'core.foreach',
    input_schema: {
      items: { type: 'any', required: true },
      tasks: { type: 'array', required: true },
      item: { type: 'string', required: false, default: 'item' },
      concurrency: { type: 'integer', required: false, default: 1 },
      output: { type: 'object', required: false },
    },
  },
  {
    name: 'core.print',
    input_schema: {
      message: { type: 'string', required: true },
    },
  },
];

const baseParams = { items: '{{ Inputs.items }}' };

describe('validateTaskData core.foreach branch', () => {
  it('requires at least one sub-task (empty array passes the generic required check)', () => {
    const res = validateTaskData('core.foreach', { ...baseParams, tasks: [] }, TASK_DEFINITIONS);
    expect(res.isValid).toBe(false);
    expect(res.errors['tasks']).toBe('At least one sub-task is required');
  });

  it('accepts a valid sub-task list', () => {
    const res = validateTaskData(
      'core.foreach',
      { ...baseParams, tasks: [{ id: 'print_item', type: 'core.print', params: { message: '{{ LoopItem.item }}' } }] },
      TASK_DEFINITIONS
    );
    expect(res.isValid).toBe(true);
    expect(res.errors).toEqual({});
  });

  it('flags missing id, missing type and duplicate ids with indexed keys', () => {
    const res = validateTaskData(
      'core.foreach',
      {
        ...baseParams,
        tasks: [
          { id: '', type: 'core.print', params: { message: 'x' } },
          { id: 'a', params: {} },
          { id: 'a', type: 'core.print', params: { message: 'x' } },
        ],
      },
      TASK_DEFINITIONS
    );
    expect(res.isValid).toBe(false);
    expect(res.errors['tasks[0].id']).toBe('Sub-task name is required');
    expect(res.errors['tasks[1].type']).toBe('Sub-task action is required');
    // index 1 claimed "a" first; index 2 is the duplicate
    expect(res.errors['tasks[2].id']).toBe('Duplicate sub-task name "a"');
  });

  it('rejects blocked container types inside the loop', () => {
    const res = validateTaskData('core.foreach', { ...baseParams, tasks: [{ id: 'nested', type: 'core.foreach', params: {} }] }, TASK_DEFINITIONS);
    expect(res.isValid).toBe(false);
    expect(res.errors['tasks[0].type']).toContain('cannot run inside a foreach loop');
  });

  it('recurses into sub-task params and prefixes nested errors', () => {
    const res = validateTaskData('core.foreach', { ...baseParams, tasks: [{ id: 'print_item', type: 'core.print', params: {} }] }, TASK_DEFINITIONS);
    expect(res.isValid).toBe(false);
    expect(res.errors['tasks[0].params.message']).toContain('is required');
  });
});

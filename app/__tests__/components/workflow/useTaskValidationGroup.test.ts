import { validateTaskData } from '@components/workflow/hooks/useTaskValidation';

// Minimal task definitions mirroring the runbook-server schemas the validator
// consumes (input_schema is a flat field → schema-property map).
const TASK_DEFINITIONS = [
  {
    name: 'core.group',
    input_schema: {
      tasks: { type: 'array', required: true },
    },
  },
  {
    name: 'core.print',
    input_schema: {
      message: { type: 'string', required: true },
    },
  },
];

describe('validateTaskData core.group branch', () => {
  it('requires at least one sub-task', () => {
    const res = validateTaskData('core.group', { tasks: [] }, TASK_DEFINITIONS);
    expect(res.isValid).toBe(false);
    expect(res.errors['tasks']).toBe('At least one sub-task is required');
  });

  it('accepts a valid sub-task list', () => {
    const res = validateTaskData('core.group', { tasks: [{ id: 'print_step', type: 'core.print', params: { message: 'hello' } }] }, TASK_DEFINITIONS);
    expect(res.isValid).toBe(true);
    expect(res.errors).toEqual({});
  });

  it('flags missing id, missing type and duplicate ids with indexed keys', () => {
    const res = validateTaskData(
      'core.group',
      {
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
    expect(res.errors['tasks[2].id']).toBe('Duplicate sub-task name "a"');
  });

  it('rejects blocked container types inside the group', () => {
    const res = validateTaskData('core.group', { tasks: [{ id: 'nested', type: 'core.group', params: {} }] }, TASK_DEFINITIONS);
    expect(res.isValid).toBe(false);
    expect(res.errors['tasks[0].type']).toContain('cannot run inside a group');
  });

  it('recurses into sub-task params and prefixes nested errors', () => {
    const res = validateTaskData('core.group', { tasks: [{ id: 'print_step', type: 'core.print', params: {} }] }, TASK_DEFINITIONS);
    expect(res.isValid).toBe(false);
    expect(res.errors['tasks[0].params.message']).toContain('is required');
  });
});

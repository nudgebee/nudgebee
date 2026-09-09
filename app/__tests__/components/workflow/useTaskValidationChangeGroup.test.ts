import { validateTaskData } from '@components/workflow/hooks/useTaskValidation';

// Minimal task definitions mirroring the runbook-server schemas the validator
// consumes. k8s.pv_rightsize exposes change_by + change_to with no discriminator
// field; k8s.horizontal_rightsize gates the same pair behind its own
// scaling_mode selector.
const TASK_DEFINITIONS = [
  {
    name: 'k8s.pv_rightsize',
    input_schema: {
      namespace: { type: 'string', required: true },
      kind: { type: 'string', required: true, default: 'PersistentVolumeClaim', read_only: true },
      name: { type: 'string', required: true },
      change_by: { type: 'string' },
      change_to: { type: 'string' },
      max: { type: 'string' },
    },
  },
  {
    name: 'k8s.horizontal_rightsize',
    input_schema: {
      namespace: { type: 'string', required: true },
      name: { type: 'string', required: true },
      scaling_mode: { type: 'string', enum: ['change_by', 'change_to'] },
      change_by: { type: 'number', visible_when: { field: 'scaling_mode', value: ['change_by'] } },
      change_to: { type: 'number', visible_when: { field: 'scaling_mode', value: ['change_to'] } },
    },
  },
];

const pvcParams = { namespace: 'default', kind: 'PersistentVolumeClaim', name: 'storage-nudgebee-0' };

describe('validateTaskData resize pair (change_by / change_to)', () => {
  it('requires a resize value when neither change_by nor change_to is set', () => {
    const res = validateTaskData('k8s.pv_rightsize', pvcParams, TASK_DEFINITIONS);
    expect(res.isValid).toBe(false);
    expect(res.errors['change_by']).toBe('Resize value is required');
    expect(res.errors['change_to']).toBe('Resize value is required');
  });

  it('treats an empty string as missing', () => {
    const res = validateTaskData('k8s.pv_rightsize', { ...pvcParams, change_by: '' }, TASK_DEFINITIONS);
    expect(res.isValid).toBe(false);
    expect(res.errors['change_by']).toBe('Resize value is required');
  });

  it('treats a whitespace-only value as missing', () => {
    const res = validateTaskData('k8s.pv_rightsize', { ...pvcParams, change_to: '   ' }, TASK_DEFINITIONS);
    expect(res.isValid).toBe(false);
    expect(res.errors['change_to']).toBe('Resize value is required');
  });

  it('accepts a percentage in change_by', () => {
    const res = validateTaskData('k8s.pv_rightsize', { ...pvcParams, change_by: '10%' }, TASK_DEFINITIONS);
    expect(res.isValid).toBe(true);
    expect(res.errors).toEqual({});
  });

  it('accepts an absolute size in change_to', () => {
    const res = validateTaskData('k8s.pv_rightsize', { ...pvcParams, change_to: '20Gi' }, TASK_DEFINITIONS);
    expect(res.isValid).toBe(true);
    expect(res.errors).toEqual({});
  });

  it('leaves tasks with their own scaling_mode selector untouched', () => {
    const res = validateTaskData('k8s.horizontal_rightsize', { namespace: 'default', name: 'web' }, TASK_DEFINITIONS);
    expect(res.isValid).toBe(true);
    expect(res.errors).toEqual({});
  });
});

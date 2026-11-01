import { describeRollbackWrites } from '../changeRollback';

// The row states what a rollback writes, as "running now → after rollback".
describe('describeRollbackWrites', () => {
  it('states each field with the value running now and the value the rollback restores', () => {
    const fields = [
      { path: 'spec.template.spec.containers[0].resources.limits.memory', old: '128Mi', new: '512Mi' },
      { path: 'spec.template.spec.containers[0].resources.requests.memory', old: '64Mi', new: '256Mi' },
    ];
    expect(describeRollbackWrites(fields)).toBe('resources.limits.memory 512Mi → 128Mi, resources.requests.memory 256Mi → 64Mi');
  });

  it('counts the fields it does not state', () => {
    const fields = [
      { path: 'Deployment.spec.replicas', old: 2, new: 3 },
      { path: 'spec.template.spec.containers[app].image', old: 'registry.example.com/team/app:1.4.1', new: 'registry.example.com/team/app:1.4.2' },
      { path: 'spec.template.spec.containers[app].env[2]', old: null, new: { name: 'MODE', value: 'fast' } },
      { path: 'spec.template.spec.containers[app].resources.limits.cpu', old: '500m', new: '1' },
      { path: 'spec.template.spec.containers[app].resources.requests.cpu', old: '100m', new: '200m' },
    ];
    expect(describeRollbackWrites(fields)).toBe('replicas 3 → 2, resources.limits.cpu 1 → 500m and 3 more');
  });

  it('keeps the container in the label when the change touched more than one', () => {
    const fields = [
      { path: 'spec.template.spec.containers[0].resources.limits.memory', old: '128Mi', new: '512Mi' },
      { path: 'spec.template.spec.containers[1].resources.limits.memory', old: '256Mi', new: '1Gi' },
    ];
    expect(describeRollbackWrites(fields)).toBe(
      'containers[0].resources.limits.memory 512Mi → 128Mi, containers[1].resources.limits.memory 1Gi → 256Mi'
    );
  });

  it('says when the rollback removes a field the change added', () => {
    expect(describeRollbackWrites([{ path: 'spec.template.spec.containers[0].resources.limits.memory', old: null, new: '1Gi' }])).toBe(
      'resources.limits.memory 1Gi → (not set)'
    );
  });

  it('ignores rollout bookkeeping and returns nothing when no field can be stated', () => {
    expect(describeRollbackWrites([{ path: 'spec.template.metadata.annotations.rollme', old: 'a', new: 'b' }])).toBe('');
    expect(describeRollbackWrites([{ path: 'spec.template.spec.containers[0].env', old: [{ name: 'A' }], new: [{ name: 'B' }] }])).toBe('');
    expect(describeRollbackWrites(undefined)).toBe('');
  });
});

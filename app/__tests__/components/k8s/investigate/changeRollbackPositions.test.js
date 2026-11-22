import { nameListPositions } from '@components/k8s/investigate/changeRollback';

// With several containers, "containers[1]" says nothing about which one a rollback touches.
describe('nameListPositions', () => {
  const after = {
    spec: {
      template: {
        spec: {
          containers: [
            { name: 'app', env: [{ name: 'A' }, { name: 'B' }] },
            { name: 'sidecar', env: [] },
          ],
          init_containers: [{ name: 'migrate' }],
        },
      },
    },
  };
  const before = { spec: { template: { spec: { containers: [{ name: 'app', env: [{ name: 'A' }, { name: 'B' }, { name: 'GONE' }] }] } } } };

  it('names containers and env entries from the manifest, reading snake_case keys', () => {
    expect(nameListPositions('spec.template.spec.containers[1].image', [after])).toBe('spec.template.spec.containers[sidecar].image');
    expect(nameListPositions('spec.template.spec.containers[0].env[1].value', [after])).toBe('spec.template.spec.containers[app].env[B].value');
    expect(nameListPositions('spec.template.spec.initContainers[0].image', [after])).toBe('spec.template.spec.initContainers[migrate].image');
  });

  it('falls back to the manifest before the change, then to the recorded item', () => {
    expect(nameListPositions('spec.template.spec.containers[0].env[2]', [after, before])).toBe('spec.template.spec.containers[app].env[GONE]');
    expect(nameListPositions('spec.template.spec.containers[0].env[5]', [after], { name: 'NEW' })).toBe(
      'spec.template.spec.containers[app].env[NEW]'
    );
  });

  it('keeps the position when nothing names the item', () => {
    expect(nameListPositions('spec.template.spec.containers[0].args[6]', [after])).toBe('spec.template.spec.containers[app].args[6]');
    expect(nameListPositions('spec.template.spec.containers[3].image', [])).toBe('spec.template.spec.containers[3].image');
  });
});

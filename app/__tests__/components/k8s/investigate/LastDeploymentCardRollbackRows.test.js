import LastDeploymentCard, { isScaleOnlyChange, summarizeChangedFields } from '@components/k8s/investigate/cards/LastDeploymentCard';
import { describeChangeTiming, revertTargetLabel } from '@components/k8s/investigate/changeRollback';
import { detectGitOpsManager } from '@utils/workloadManager';

const image = (from, to) => ({ path: 'spec.template.spec.containers[0].image', old: from, new: to });
const rollme = (from, to) => ({ path: 'spec.template.metadata.annotations.rollme', old: from, new: to });
const restartedAt = { path: 'spec.template.metadata.annotations.kubectl.kubernetes.io/restartedAt', old: 'a', new: 'b' };
const replicas = (from, to) => ({ path: 'spec.replicas', old: from, new: to });

const historyEvidence = (deployments) => ({
  type: 'json',
  additional_info: { action_name: 'deployment_history' },
  data: JSON.stringify({ namespace: 'ns', rollout_name: 'app', service_name: 'app', time_range_hours: 168, deployments }),
});
const change = (id, timestamp, updated_values) => ({ event_id: id, timestamp, updated_values });

const cardFor = async (deployments, starts_at = '2026-09-18T00:14:11Z') => {
  const evidence = historyEvidence(deployments);
  const card = new LastDeploymentCard(evidence, { starts_at, evidences: [evidence] }, 0);
  await card.canRenderContent();
  return card;
};

// A scale is the work of an HPA, KEDA or a cron parking an environment — not a deploy. Writing the old
// replica count back took the workload to zero on a quarter of dev's revert rows, offered as the fix.
describe('isScaleOnlyChange', () => {
  it('recognises a change to the replica count alone', () => {
    expect(isScaleOnlyChange([replicas(0, 2)])).toBe(true);
    expect(isScaleOnlyChange([{ path: 'Deployment.spec.replicas', old: 1, new: 0 }])).toBe(true);
  });

  it('keeps a scale that came with a real change revertible', () => {
    expect(isScaleOnlyChange([replicas(1, 2), image('a', 'b')])).toBe(false);
  });

  it('is not a restart', () => {
    expect(isScaleOnlyChange([rollme('a', 'b')])).toBe(false);
    expect(isScaleOnlyChange([])).toBe(false);
  });
});

describe('rollback choices', () => {
  // Whether a later change stands in the way of a choice is the server's to say, when it is picked:
  // this history ends at the alert, so the page cannot know what changed after it, nor that a later
  // change (c3 here, over c1's image) has itself been undone since. It used to decide anyway, and hid
  // c1 for good.
  it('offers every change that is a deploy to put back', async () => {
    const card = await cardFor([
      change('c3', '2026-09-16T17:12:28Z', [rollme('F', 'U'), image('v2', 'v3')]),
      change('c2', '2026-09-16T12:00:00Z', [{ path: 'spec.template.spec.containers[0].env[1].value', old: 'a', new: 'b' }]),
      change('c1', '2026-09-15T08:27:09Z', [rollme('m', 'F'), image('v1', 'v2')]),
    ]);
    const choices = card.rollbackChoices;
    expect(choices.map((c) => c.changeEventId)).toEqual(['c3', 'c2', 'c1']);
    expect(choices[0]).toMatchObject({ timing: '31h before the alert', summary: 'image' });
    expect(card.resolveButton).toBe(true);
  });

  // The row stays one line: several changes are counted there and picked in the dialog.
  it('summarises several choices in the row and names a single one', async () => {
    const several = await cardFor([
      change('c2', '2026-09-16T17:12:28Z', [image('v2', 'v3')]),
      change('c1', '2026-09-16T12:00:00Z', [{ path: 'spec.template.spec.containers[0].env[1].value', old: 'a', new: 'b' }]),
    ]);
    expect(several.choosesTarget).toBe(true);
    expect(several.actionSubject).toBe('');
    // It counts the changes; which of them can still be rolled back is asked when one is picked.
    expect(several.actionDetail).toBe('2 recent changes to choose from · newest 31h before the alert');

    const single = await cardFor([change('c1', '2026-09-16T17:12:28Z', [image('v1', 'v2')])]);
    expect(single.choosesTarget).toBe(false);
    expect(single.actionSubject).toBeTruthy();
    // A single change states what the rollback writes back: running now → after rollback.
    expect(single.actionDetail).toBe('31h before the alert · rolls back image v2 → v1');
    // "Running now" is the change's recorded value, so the row that states it asks the server whether
    // the workload was changed again since, and has this to say when it was. A row that only counts
    // changes makes no such claim; the dialog checks the one that is picked.
    expect(single.checksRollback).toBe(true);
    expect(single.blockedActionDetail).toBe('31h before the alert · changed again since, so this change cannot be rolled back on its own');
    expect(several.checksRollback).toBe(false);
    expect(several.blockedActionDetail).toBe('');
  });

  // app-test: the newest change was a cron scaling 0 → 2, and the row offered to "fix" it by scaling
  // back to zero.
  it('never offers a scale or a restart as a rollback', async () => {
    const card = await cardFor([
      change('scale-up', '2026-09-18T02:30:08Z', [replicas(0, 2)]),
      change('scale-down', '2026-09-17T17:00:00Z', [replicas(2, 0)]),
      change('restart', '2026-09-17T12:00:00Z', [restartedAt]),
    ]);
    expect(card.rollbackChoices).toEqual([]);
    expect(card.resolveButton).toBe(false);
    expect(card.getResolveComponent()).toBeNull();
  });

  // cubeapm: the history listed a ConfigMap change linked to the StatefulSet's resource, so the server's
  // same-resource check let it through and the revert would write data.* onto the StatefulSet.
  it('never offers a change to another object the workload uses', async () => {
    const card = await cardFor([
      { event_id: 'sts', title: 'StatefulSet cubeapm/cubeapm was changed', timestamp: '2026-09-17T10:55:31Z', updated_values: [image('a', 'b')] },
      {
        event_id: 'cm',
        title: 'ConfigMap cubeapm/cubeapm was changed',
        timestamp: '2026-09-15T11:04:00Z',
        updated_values: [{ path: 'data.config.properties', old: 'x', new: 'y' }],
      },
      { event_id: 'cm-old', timestamp: '2026-09-14T11:04:00Z', updated_values: [{ path: 'data.other', old: 'x', new: 'y' }] },
    ]);
    expect(card.rollbackChoices.map((c) => c.changeEventId)).toEqual(['sts']);
  });

  // The page asks for the dialog component on every render; a fresh one each time remounts the dialog
  // and drops the change it had loaded.
  it('keeps the dialog component stable across calls', async () => {
    const card = await cardFor([change('c1', '2026-09-16T17:12:28Z', [image('v1', 'v2')])]);
    expect(card.getResolveComponent()).toBe(card.getResolveComponent());
  });

  it('does not offer a single-diff scale as a rollback', async () => {
    const evidence = {
      type: 'diff',
      start_at: '2026-09-14T02:30:18Z',
      data: { resource_name: 'deployment/ns/app.yaml', old: 'a', new: 'b', updated_values: [replicas(0, 1)] },
    };
    const card = new LastDeploymentCard(evidence, { starts_at: '2026-09-14T02:48:23Z', evidences: [evidence] }, 0);
    await card.canRenderContent();
    expect(card.resolveButton).toBe(false);
    expect(card.getResolveComponent()).toBeNull();
  });
});

describe('describeChangeTiming', () => {
  it('states the gap to the alert in the largest sensible unit', () => {
    expect(describeChangeTiming('2026-09-18T00:00:00Z', '2026-09-18T00:18:00Z')).toBe('18 min before the alert');
    expect(describeChangeTiming('2026-09-16T17:12:28Z', '2026-09-18T00:14:11Z')).toBe('31h before the alert');
    expect(describeChangeTiming('2026-08-26T11:21:30Z', '2026-09-17T00:11:09Z')).toBe('22 days before the alert');
    expect(describeChangeTiming('2026-09-18T01:00:00Z', '2026-09-18T00:00:00Z')).toBe('1h after the alert started');
  });

  it('says nothing when either time is missing', () => {
    expect(describeChangeTiming('', '2026-09-18T00:00:00Z')).toBe('');
    expect(describeChangeTiming('2026-09-18T00:00:00Z', undefined)).toBe('');
  });
});

describe('summarizeChangedFields', () => {
  it('names fields without the pod-template prefix and ignores rollout markers', () => {
    expect(summarizeChangedFields([rollme('a', 'b'), image('v1', 'v2')])).toBe('image');
    expect(
      summarizeChangedFields([
        image('a', 'b'),
        { path: 'spec.template.spec.containers[0].resources.limits.cpu' },
        { path: 'spec.template.spec.containers[0].resources.limits.memory' },
      ])
    ).toBe('image, resources');
    expect(
      summarizeChangedFields([
        image('a', 'b'),
        { path: 'spec.template.spec.containers[0].env[1].value' },
        { path: 'spec.template.spec.containers[0].command' },
        { path: 'spec.strategy.rollingUpdate.maxSurge' },
      ])
    ).toBe('image, env and 2 more');
  });
});

describe('revertTargetLabel', () => {
  it('names the real kind', () => {
    expect(revertTargetLabel('daemonset/agent/node-agent.yaml')).toBe('DaemonSet agent/node-agent');
    expect(revertTargetLabel('deployment/nudgebee/notifications.yaml')).toBe('Deployment nudgebee/notifications');
    expect(revertTargetLabel('configmap/ns/x.yaml')).toBe('');
  });
});

describe('detectGitOpsManager', () => {
  // vmagent's Deployment is controller-owned by a VMAgent: the operator rewrites any manual edit.
  it('recognises an operator from a non-builtin controller owner', () => {
    const m = detectGitOpsManager({ 'meta.helm.sh/release-name': 'victoria' }, { 'app.kubernetes.io/managed-by': 'Helm' }, [
      { kind: 'VMAgent', name: 'victoria-vmagent', controller: true },
    ]);
    expect(m.operator).toEqual({ kind: 'VMAgent', name: 'victoria-vmagent' });
    expect(m.manager).toBe('VMAgent');
  });

  it('does not treat Kubernetes’ own ownership as an operator', () => {
    expect(detectGitOpsManager({}, {}, [{ kind: 'ReplicaSet', name: 'x', controller: true }]).operator).toBeNull();
  });

  it('reads the Helm release', () => {
    const m = detectGitOpsManager({ 'meta.helm.sh/release-name': 'notifications' }, { 'app.kubernetes.io/managed-by': 'Helm' });
    expect(m).toMatchObject({ manager: 'Helm', helmRelease: 'notifications', operator: null });
  });

  it('prefers Argo CD over the Helm labels its charts carry', () => {
    expect(detectGitOpsManager({ 'argocd.argoproj.io/tracking-id': 'x' }, { 'app.kubernetes.io/managed-by': 'Helm' }).manager).toBe('Argo CD');
  });
});

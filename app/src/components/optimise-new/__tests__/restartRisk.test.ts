import { singleReplicaOutage, podRightSizingRisk } from '../interpretation/buildInterpretation';

describe('singleReplicaOutage', () => {
  it('claims an outage only for a single-pod StatefulSet', () => {
    expect(singleReplicaOutage({ kind: 'StatefulSet', pods: 1 })).toBe(true);
  });

  it('makes no outage claim for a single-pod Deployment — it surges before terminating', () => {
    expect(singleReplicaOutage({ kind: 'Deployment', pods: 1 })).toBe(false);
  });

  it('treats a zero or unknown live pod count as unknown, never as single', () => {
    expect(singleReplicaOutage({ kind: 'StatefulSet', pods: 0 })).toBe(false);
    expect(singleReplicaOutage({ kind: 'StatefulSet', pods: null })).toBe(false);
    expect(singleReplicaOutage({ kind: 'StatefulSet' })).toBe(false);
    expect(singleReplicaOutage(undefined)).toBe(false);
  });
});

describe('podRightSizingRisk', () => {
  it('prepends the outage warning only when it applies, and never claims a restart is unconditional', () => {
    const plain = podRightSizingRisk({ kind: 'Deployment', pods: 1 });
    expect(plain).toMatch(/unless the no-restart \(in-place\) apply is used/);
    expect(plain).not.toMatch(/single pod/);
    expect(podRightSizingRisk({ kind: 'StatefulSet', pods: 1 })).toMatch(/^This StatefulSet runs a single pod/);
  });
});

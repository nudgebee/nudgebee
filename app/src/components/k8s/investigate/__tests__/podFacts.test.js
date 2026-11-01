import { describeContainerMemory, podDetailsFromEvidence } from '../podFacts';

const podEvidence = (pod) => ({ type: 'json', data: JSON.stringify([pod]), additional_info: { action_name: 'pod_enricher' } });

// The shape the server stores: the pod itself, snake_case.
const crashLoopingPod = {
  kind: 'Pod',
  spec: { containers: [{ name: 'app', image: 'registry.example.com/demo/app:1.0', resources: {} }] },
  status: {
    qos_class: 'Burstable',
    container_statuses: [
      {
        name: 'app',
        image: 'registry.example.com/demo/app:1.0',
        ready: false,
        restart_count: 695,
        state: { waiting: { reason: 'CrashLoopBackOff', message: 'back-off 5m0s restarting failed container' } },
        last_state: { terminated: { reason: 'OOMKilled', exit_code: 137 } },
        resources: { limits: { cpu: '200m', memory: '96Mi' }, requests: { cpu: '10m', memory: '64Mi' } },
      },
    ],
  },
};

describe('podDetailsFromEvidence', () => {
  it('reads restarts, readiness, the waiting reason and the last exit from the stored pod', () => {
    const details = podDetailsFromEvidence([{ type: 'table', data: {} }, podEvidence(crashLoopingPod)]);
    expect(details.data.restarts).toBe(695);
    expect(details.data.qosClass).toBe('Burstable');
    expect(details.data.containersReady).toEqual({ ready: 0, total: 1 });
    expect(details.data.containers).toEqual([
      {
        name: 'app',
        imageName: 'registry.example.com/demo/app:1.0',
        ready: false,
        status: { state: 'Waiting', reason: 'CrashLoopBackOff' },
        lastStatus: { state: 'Terminated', reason: 'OOMKilled', exitCode: 137 },
        memoryLimit: '96Mi',
        memoryRequest: '64Mi',
        memoryPeakBytes: undefined,
      },
    ]);
  });

  it('reads the same facts when the collector wrote camelCase keys', () => {
    const pod = {
      spec: { containers: [{ name: 'api', image: 'registry.example.com/demo/api:2.0', resources: { limits: { memory: '512Mi' } } }] },
      status: {
        qosClass: 'Guaranteed',
        containerStatuses: [
          {
            name: 'api',
            ready: true,
            restartCount: 2,
            state: { running: { startedAt: '2026-01-01T00:00:00Z' } },
            lastState: { terminated: { exitCode: 0 } },
          },
        ],
      },
    };
    const details = podDetailsFromEvidence([podEvidence(pod)]);
    expect(details.data.restarts).toBe(2);
    expect(details.data.qosClass).toBe('Guaranteed');
    expect(details.data.containersReady).toEqual({ ready: 1, total: 1 });
    expect(details.data.containers[0].status).toEqual({ state: 'Running' });
    expect(details.data.containers[0].lastStatus).toEqual({ state: 'Terminated', exitCode: 0 });
    expect(details.data.containers[0].memoryLimit).toBe('512Mi');
    expect(details.data.containers[0].imageName).toBe('registry.example.com/demo/api:2.0');
  });

  it('has no previous run for a container that never ran before', () => {
    const pod = {
      status: {
        container_statuses: [{ name: 'api', ready: false, restart_count: 0, state: { waiting: { reason: 'ErrImagePull' } }, last_state: {} }],
      },
    };
    const container = podDetailsFromEvidence([podEvidence(pod)]).data.containers[0];
    expect(container.status).toEqual({ state: 'Waiting', reason: 'ErrImagePull' });
    expect(container.lastStatus).toBeUndefined();
  });

  it('adds the sampled memory peak from the metric evidence of the same event', () => {
    const metric = {
      type: 'json',
      data: JSON.stringify({
        name: 'pod_metric',
        resource_type: 'Memory',
        data: [
          { metric: { container: 'app' }, values: ['104857600', '524288000', '209715200'] },
          { metric: { container: 'sidecar' }, values: [1048576] },
        ],
      }),
    };
    const cpu = {
      type: 'json',
      data: JSON.stringify({ name: 'pod_metric', resource_type: 'cpu', data: [{ metric: { container: 'app' }, values: [9e12] }] }),
    };
    const details = podDetailsFromEvidence([metric, cpu, podEvidence(crashLoopingPod)]);
    expect(details.data.containers[0].memoryPeakBytes).toBe(524288000);
  });

  it('returns an older pod_details block as stored when the event has no stored pod', () => {
    const stored = { name: 'pod_details', data: { restarts: 3, containers: [{ name: 'web', status: { reason: 'Error', exitCode: 1 } }] } };
    expect(podDetailsFromEvidence([{ type: 'json', data: JSON.stringify(stored) }])).toEqual(stored);
    // With both, the stored pod is the fuller record.
    expect(podDetailsFromEvidence([{ type: 'json', data: JSON.stringify(stored) }, podEvidence(crashLoopingPod)]).data.restarts).toBe(695);
  });

  it('is null when the event has no pod, or the pod was already gone', () => {
    expect(podDetailsFromEvidence(undefined)).toBeNull();
    expect(podDetailsFromEvidence([{ type: 'json', data: 'not json' }])).toBeNull();
    expect(
      podDetailsFromEvidence([{ type: 'json', data: JSON.stringify({ marker: 'pod_not_found' }), additional_info: { action_name: 'pod_enricher' } }])
    ).toBeNull();
  });
});

describe('describeContainerMemory', () => {
  it('states the sampled peak and the limit', () => {
    expect(describeContainerMemory({ memoryPeakBytes: 518000000, memoryLimit: '512Mi' })).toBe('sampled peak 494 MiB · limit 512Mi');
  });

  it('states only what is known', () => {
    expect(describeContainerMemory({ memoryLimit: '512Mi', memoryRequest: '256Mi' })).toBe('limit 512Mi');
    expect(describeContainerMemory({ memoryRequest: '256Mi' })).toBe('no limit, request 256Mi');
    expect(describeContainerMemory({})).toBe('');
    expect(describeContainerMemory(undefined)).toBe('');
  });
});

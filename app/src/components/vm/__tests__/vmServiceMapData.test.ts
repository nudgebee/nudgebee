import { listHosts, scopeToHost } from '../vmServiceMapData';

const svc = (kind: string, name: string, namespace: string, upstreams: any[] = [], downstreams: any[] = []) => ({
  Id: { kind, name, namespace },
  Upstreams: upstreams,
  Downstreams: downstreams,
});
const link = (namespace: string, kind: string, name: string) => ({ Id: `${namespace}:${kind}:${name}`, RequestCount: 10 });
const down = (kind: string, name: string, namespace: string) => ({ Id: { kind, name, namespace }, RequestCount: 10 });

// host-a runs web (calls api on host-b), a quiet unit and a container; host-b runs api and a
// quiet unit of its own; cron on host-b calls nothing.
const apps = [
  svc('SystemdService', 'web.service', 'host-a', [
    link('host-b', 'SystemdService', 'api.service'),
    link('external', 'ExternalService', 'api.stripe.com'),
  ]),
  svc('SystemdService', 'chronyd.service', 'host-a'),
  svc('DockerContainer', 'cache', 'host-a', [], [down('SystemdService', 'api.service', 'host-b')]),
  svc('SystemdService', 'api.service', 'host-b', [link('host-a', 'DockerContainer', 'cache')], [down('SystemdService', 'web.service', 'host-a')]),
  svc('SystemdService', 'cron.service', 'host-b'),
  svc('ExternalService', 'api.stripe.com', 'external', [], [down('SystemdService', 'web.service', 'host-a')]),
  svc('ManagedDatabase', 'orders-db', 'managed-db'),
];

describe('listHosts', () => {
  it('counts the systemd units and containers per VM and ignores everything that is not running on one', () => {
    expect(listHosts(apps)).toEqual([
      { name: 'host-a', systemd: 2, docker: 1, other: 0 },
      { name: 'host-b', systemd: 2, docker: 0, other: 0 },
    ]);
  });
});

describe('scopeToHost', () => {
  const scoped = scopeToHost(apps, 'host-a');
  const names = scoped.map((a) => a.Id.name).sort();

  it('keeps every service on the VM, including the ones nobody calls', () => {
    expect(names).toEqual(expect.arrayContaining(['web.service', 'chronyd.service', 'cache']));
  });

  it('adds what the VM talks to, one hop wide, and nothing else', () => {
    expect(names).toEqual(expect.arrayContaining(['api.service', 'api.stripe.com']));
    expect(names).not.toContain('cron.service');
    expect(names).not.toContain('orders-db');
  });

  it('keeps only the links that touch the VM', () => {
    const api = scoped.find((a) => a.Id.name === 'api.service')!;
    // api -> cache touches host-a (cache lives there); api's other links, if any, would not.
    expect(api.Upstreams.map((l: any) => l.Id)).toEqual(['host-a:DockerContainer:cache']);
    expect(api.Downstreams.map((l: any) => l.Id.name)).toEqual(['web.service']);
  });

  it('shows a VM with no calls as just its services', () => {
    const only = scopeToHost(apps, 'host-b');
    expect(only.map((a) => a.Id.name).sort()).toEqual(['api.service', 'cache', 'cron.service', 'web.service']);
  });
});

describe('scopeToHost with a same-named systemd unit and container', () => {
  // A host can run a systemd unit and a Docker container that happen to share
  // a name (e.g. both called "agent") — kind must be part of the key or one
  // collapses into the other.
  const collidingApps = [
    svc('SystemdService', 'agent', 'host-a', [link('host-a', 'DockerContainer', 'agent')]),
    svc('DockerContainer', 'agent', 'host-a'),
  ];

  it('keeps the systemd unit and the container as distinct services', () => {
    const scoped = scopeToHost(collidingApps, 'host-a');
    expect(scoped).toHaveLength(2);
    expect(scoped.map((a) => a.Id.kind).sort()).toEqual(['DockerContainer', 'SystemdService']);
  });
});

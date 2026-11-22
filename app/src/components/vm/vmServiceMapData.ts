/**
 * Pure helpers over the applications the VM service map returns: which VMs there are,
 * and what a single VM looks like — its systemd units and containers, plus whatever
 * they call or are called by.
 */

/** Node kinds that are something running on a VM: a systemd unit, a container, or another process. */
export const SERVICE_KINDS = ['SystemdService', 'DockerContainer', 'Process'];

export interface VmHost {
  /** The host's name as the nodes carry it (their namespace). */
  name: string;
  systemd: number;
  docker: number;
  other: number;
}

type AppId = { name: string; namespace: string; kind: string };

// kind is part of the key: a host can run a systemd unit and a Docker
// container of the same name, and the server keeps them as distinct
// applications (vmservicemap/build.go keys on namespace+kind+name too).
const keyOf = (id: { name: string; namespace: string; kind: string }) => `${id.name}|${id.namespace}|${id.kind}`;

/** A link id is "namespace:kind:name"; names carry no ":" (the server strips them). */
const linkKey = (link: { Id: unknown }) => {
  const [namespace, kind, name] = String(link.Id).split(':');
  return `${name}|${namespace}|${kind}`;
};

const isService = (app: { Id: AppId }) => SERVICE_KINDS.includes(app?.Id?.kind);

/** The VMs that have at least one service in the map, sorted by name, with what runs on each. */
export const listHosts = (applications: any[]): VmHost[] => {
  const hosts = new Map<string, VmHost>();
  applications.filter(isService).forEach((app) => {
    const name = app.Id.namespace;
    const host = hosts.get(name) || { name, systemd: 0, docker: 0, other: 0 };
    if (app.Id.kind === 'SystemdService') host.systemd += 1;
    else if (app.Id.kind === 'DockerContainer') host.docker += 1;
    else host.other += 1;
    hosts.set(name, host);
  });
  return Array.from(hosts.values()).sort((a, b) => a.name.localeCompare(b.name));
};

/**
 * Narrows the map to one VM: every service on it (called or not, so a quiet unit is
 * still listed), and the services on other hosts, databases and external hosts it
 * talks to. Only links that touch the VM are kept, so the picture is one hop wide.
 */
export const scopeToHost = (applications: any[], host: string): any[] => {
  const inHost = new Set(applications.filter((app) => isService(app) && app.Id.namespace === host).map((app) => keyOf(app.Id)));
  const keep = new Set(inHost);

  applications.forEach((app) => {
    const key = keyOf(app.Id);
    if (inHost.has(key)) {
      (app.Upstreams || []).forEach((link: any) => keep.add(linkKey(link)));
      (app.Downstreams || []).forEach((link: any) => keep.add(keyOf(link.Id)));
      return;
    }
    const callsHost = (app.Upstreams || []).some((link: any) => inHost.has(linkKey(link)));
    const calledByHost = (app.Downstreams || []).some((link: any) => inHost.has(keyOf(link.Id)));
    if (callsHost || calledByHost) keep.add(key);
  });

  return applications
    .filter((app) => keep.has(keyOf(app.Id)))
    .map((app) => {
      const own = inHost.has(keyOf(app.Id));
      return {
        ...app,
        Upstreams: (app.Upstreams || []).filter((link: any) => keep.has(linkKey(link)) && (own || inHost.has(linkKey(link)))),
        Downstreams: (app.Downstreams || []).filter((link: any) => keep.has(keyOf(link.Id)) && (own || inHost.has(keyOf(link.Id)))),
      };
    });
};

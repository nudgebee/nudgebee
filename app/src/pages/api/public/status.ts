import fs from 'fs';
import net from 'net';
import type { NextApiRequest, NextApiResponse } from 'next';

// Public status aggregator (GET /api/public/status). Unauthenticated, like the
// rest of this folder — handlers here bypass auth simply by never calling
// getServerSession. Two properties below are therefore load-bearing:
//
//   - Responses are cached process-wide and single-flighted. Without that, an
//     anonymous caller amplifies one cheap request into a fan-out across every
//     backend service.
//   - Only a coarse status escapes. Upstream URLs, versions and error text stay
//     server-side.
//
// Accuracy ceiling, deliberate and worth knowing before trusting this page:
// every upstream below is a liveness probe that returns 200 without checking
// its own dependencies (api-server's says so explicitly — see
// api-server/services/api/health.go). A component reads "operational" whenever
// its process answers; that is not evidence its database or queue is healthy.
// Deepening those probes is what would make this signal trustworthy.
//
// This endpoint also runs inside the cluster it reports on, so it cannot
// observe its own total outage. A prober outside the cluster should treat this
// as one input, not as the source of truth.

export type ComponentStatus = 'operational' | 'degraded' | 'outage' | 'maintenance' | 'unknown';

/**
 * Human-authored incident lifecycle. No probe can produce these — "we are
 * working on a fix" has no sensor. Operators post it; see readNotice below.
 */
export type NoticeState = 'investigating' | 'identified' | 'monitoring' | 'maintenance';

export interface StatusNotice {
  state: NoticeState;
  message: string;
  /** Component ids this applies to. Absent/empty = the whole system. */
  components?: string[];
  started_at?: string;
}

export interface StatusComponent {
  id: string;
  name: string;
  status: ComponentStatus;
  /**
   * Why the component is not operational, sanitized for a public page: the
   * failure class only (which sub-probe, timed out / refused / HTTP 5xx / not
   * configured). Never the upstream URL, host, port, or raw error string.
   * Absent when operational.
   */
  reason?: string;
}

export interface StatusPayload {
  status: ComponentStatus;
  components: StatusComponent[];
  checked_at: string;
  /** Present only while an operator has posted one. */
  notice?: StatusNotice;
}

interface Probe {
  /** `http` reads a health path; `tcp` only opens a socket (see Temporal below). */
  kind: 'http' | 'tcp';
  /** Absent env var => that probe reports `unknown`. All keys live in the shared nudgebee secret. */
  envVar: string;
  /** http only. Path confirmed against the service's Helm livenessProbe. */
  path?: string;
  /**
   * Friendly name of what this probe checks. Surfaced in the reason ONLY when a
   * component has more than one probe, to say which part is unhealthy (e.g.
   * "Temporal: connection refused"). Never a URL/host.
   */
  name: string;
}

/** One probe's outcome. `reason` is a sanitized failure class, absent when operational. */
interface ProbeResult {
  status: ComponentStatus;
  reason?: string;
}

interface ComponentDef {
  id: string;
  name: string;
  /** Component status is the worst of its probes — a component is only as healthy as its weakest dependency. */
  probes: Probe[];
}

const COMPONENTS: ComponentDef[] = [
  { id: 'api', name: 'API', probes: [{ kind: 'http', name: 'API', envVar: 'SERVICE_API_SERVER_URL', path: '/health' }] },
  { id: 'ingest', name: 'Data Ingest', probes: [{ kind: 'http', name: 'Data Ingest', envVar: 'RELAY_SERVER_ENDPOINT', path: '/status' }] },
  { id: 'ai', name: 'Nubi AI', probes: [{ kind: 'http', name: 'Nubi AI', envVar: 'LLM_SERVER_URL', path: '/health' }] },
  {
    id: 'automation',
    name: 'Automation',
    probes: [
      { kind: 'http', name: 'Workflow engine', envVar: 'WORKFLOW_SERVER_URL', path: '/health' },
      // Temporal speaks gRPC on temporal-frontend:7233 and its Web UI is
      // deliberately disabled (CVE reasons — see nudgebee/values.yaml), so there
      // is no HTTP surface to GET. A TCP connect is the honest floor: it proves
      // the frontend accepts connections, NOT that Temporal is serving
      // workflows. The real fix is workflow-server's own /health checking its
      // Temporal client, since it already holds one.
      { kind: 'tcp', name: 'Temporal', envVar: 'TEMPORAL_GRPC_ENDPOINT' },
    ],
  },
  {
    id: 'notifications',
    name: 'Notifications',
    probes: [{ kind: 'http', name: 'Notifications', envVar: 'NOTIFICATION_SERVICE_URL', path: '/health' }],
  },
];

/**
 * Worst-wins ordering when a component has several probes. `maintenance` is
 * never produced by a probe — it only arrives via a notice — so it sits at 0
 * and never participates in this comparison.
 */
const SEVERITY: Record<ComponentStatus, number> = { operational: 0, maintenance: 0, unknown: 1, degraded: 2, outage: 3 };

const NOTICE_STATES: NoticeState[] = ['investigating', 'identified', 'monitoring', 'maintenance'];

const PROBE_TIMEOUT_MS = 2500;
// Bounds both staleness and fan-out: at most one round of upstream probes per
// TTL per pod, no matter how many people are watching. Lower means fresher and
// more upstream traffic; the page polls on roughly the same interval.
const CACHE_TTL_MS = 10_000;

let cache: { at: number; payload: StatusPayload } | null = null;
let inFlight: Promise<StatusPayload> | null = null;

async function probeHttp(base: string, path: string): Promise<ProbeResult> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), PROBE_TIMEOUT_MS);
  try {
    const res = await fetch(`${base.replace(/\/$/, '')}${path}`, {
      signal: controller.signal,
      headers: { accept: 'application/json' },
    });
    // 5xx means the process answered but reports itself unwell; anything else
    // non-2xx is treated as an outage rather than guessed at. The HTTP code is
    // safe to surface; the URL is not.
    if (res.ok) return { status: 'operational' };
    if (res.status >= 500) return { status: 'degraded', reason: `reported unhealthy (HTTP ${res.status})` };
    return { status: 'outage', reason: `unexpected response (HTTP ${res.status})` };
  } catch (err) {
    // Classify without ever including the raw message (it may carry the host).
    const timedOut = err instanceof Error && err.name === 'AbortError';
    return { status: 'outage', reason: timedOut ? 'health check timed out' : 'no response (unreachable)' };
  } finally {
    clearTimeout(timer);
  }
}

function probeTcp(endpoint: string): Promise<ProbeResult> {
  // Endpoint is host:port (no scheme), e.g. temporal-frontend:7233.
  const [host, portText] = endpoint.replace(/^[a-z]+:\/\//i, '').split(':');
  const port = Number(portText);
  // Range-check, not just integer-check: net.connect throws ERR_SOCKET_BAD_PORT
  // *synchronously* for a port outside 1-65535, which would reject this promise
  // and 500 the whole endpoint rather than degrade one component.
  if (!host || !Number.isInteger(port) || port < 1 || port > 65535) {
    return Promise.resolve({ status: 'unknown', reason: 'misconfigured endpoint' });
  }

  return new Promise((resolve) => {
    const socket = new net.Socket();
    const settle = (result: ProbeResult) => {
      socket.destroy();
      resolve(result);
    };
    socket.setTimeout(PROBE_TIMEOUT_MS);
    socket.once('connect', () => settle({ status: 'operational' }));
    socket.once('timeout', () => settle({ status: 'outage', reason: 'connection timed out' }));
    socket.once('error', () => settle({ status: 'outage', reason: 'connection refused / unreachable' }));
    socket.connect(port, host);
  });
}

async function runProbe(p: Probe): Promise<ProbeResult> {
  const value = process.env[p.envVar];
  if (!value) return { status: 'unknown', reason: 'not configured in this environment' };
  return p.kind === 'tcp' ? probeTcp(value) : probeHttp(value, p.path ?? '/health');
}

async function probeComponent(def: ComponentDef): Promise<StatusComponent> {
  const results = await Promise.all(def.probes.map(async (p) => ({ probe: p, ...(await runProbe(p)) })));
  const status = results.reduce<ComponentStatus>((worst, r) => (SEVERITY[r.status] > SEVERITY[worst] ? r.status : worst), 'operational');

  // Build the reason from every non-operational probe. Prefix with the probe's
  // friendly name only when the component has more than one probe, so the
  // viewer can tell which part failed (e.g. "Temporal: connection refused").
  const multi = def.probes.length > 1;
  const reason = results
    .filter((r) => r.reason)
    .map((r) => (multi ? `${r.probe.name}: ${r.reason}` : r.reason))
    .join('; ');

  return { id: def.id, name: def.name, status, ...(reason ? { reason } : {}) };
}

/**
 * Read the operator-posted notice, if any. Sourced from a file so a ConfigMap
 * edit publishes it — no rebuild, no redeploy, no pod restart.
 *
 * Anything malformed yields null rather than throwing: a typo in the notice
 * must never take the status endpoint down with it. The cost is that a
 * malformed notice fails silently, so validate before you commit it.
 */
async function readNotice(): Promise<StatusNotice | null> {
  const path = process.env.STATUS_NOTICE_FILE;
  if (!path) return null;

  try {
    const raw = (await fs.promises.readFile(path, 'utf8')).trim();
    if (!raw) return null;

    const parsed = JSON.parse(raw);
    const state = parsed?.state;
    const message = parsed?.message;
    if (!NOTICE_STATES.includes(state)) return null;
    if (typeof message !== 'string' || !message.trim()) return null;

    // An unparseable date would otherwise render as "(since Invalid Date)".
    const startedAt = typeof parsed.started_at === 'string' && !Number.isNaN(Date.parse(parsed.started_at)) ? parsed.started_at : undefined;

    return {
      state,
      message: message.trim(),
      components: Array.isArray(parsed.components) ? parsed.components.filter((c: unknown) => typeof c === 'string') : undefined,
      started_at: startedAt,
    };
  } catch {
    // Missing file, bad JSON, unreadable mount.
    return null;
  }
}

/**
 * A notice annotates; it does not overrule the probes. `investigating`,
 * `identified` and `monitoring` leave every component status untouched — they
 * ride alongside as narrative, so an operator can never turn a red dot green by
 * writing prose about it.
 *
 * `maintenance` is the one deliberate exception: planned downtime is not an
 * outage, and reporting it as one trains people to ignore red.
 */
function applyNotice(components: StatusComponent[], notice: StatusNotice | null): StatusComponent[] {
  if (!notice || notice.state !== 'maintenance') return components;

  const affected = new Set(notice.components ?? []);
  const appliesToAll = affected.size === 0;
  return components.map((c) =>
    (appliesToAll || affected.has(c.id)) && c.status !== 'unknown' ? { ...c, status: 'maintenance' as ComponentStatus } : c
  );
}

function rollup(components: StatusComponent[]): ComponentStatus {
  // `unknown` components are not deployed here and must not drag the rollup.
  const known = components.filter((c) => c.status !== 'unknown');
  if (known.length === 0) return 'unknown';

  const problems = known.filter((c) => c.status === 'outage' || c.status === 'degraded');
  if (problems.length > 0) {
    // A real failure during a maintenance window is still a failure.
    return problems.length === known.length && problems.every((c) => c.status === 'outage') ? 'outage' : 'degraded';
  }
  if (known.some((c) => c.status === 'maintenance')) return 'maintenance';
  return 'operational';
}

async function collect(): Promise<StatusPayload> {
  const probed = await Promise.all(COMPONENTS.map(probeComponent));
  // This handler responding is itself proof the dashboard is serving.
  const probeResult: StatusComponent[] = [{ id: 'dashboard', name: 'Dashboard', status: 'operational' }, ...probed];

  const notice = await readNotice();
  const components = applyNotice(probeResult, notice);

  return {
    status: rollup(components),
    components,
    checked_at: new Date().toISOString(),
    ...(notice ? { notice } : {}),
  };
}

function getStatus(): Promise<StatusPayload> {
  if (cache && Date.now() - cache.at < CACHE_TTL_MS) {
    return Promise.resolve(cache.payload);
  }
  if (!inFlight) {
    inFlight = collect()
      .then((payload) => {
        cache = { at: Date.now(), payload };
        return payload;
      })
      .finally(() => {
        inFlight = null;
      });
  }
  return inFlight;
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== 'GET') {
    res.setHeader('Allow', 'GET');
    res.status(405).json({ error: 'Method Not Allowed' });
    return;
  }

  const payload = await getStatus();
  res.setHeader('Cache-Control', `public, max-age=${CACHE_TTL_MS / 1000}, s-maxage=${CACHE_TTL_MS / 1000}`);
  res.status(200).json(payload);
}

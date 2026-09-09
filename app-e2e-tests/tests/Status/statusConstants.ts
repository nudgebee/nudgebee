// Not for OSS
import { expect } from "@playwright/test";

// The public status contract, mirrored from the app so the specs assert against a
// pinned expectation rather than re-deriving it from whatever the page happens to say.
// Source: app/src/pages/api/public/status.ts and app/src/pages/status.tsx.
export const STATUS_API_PATH = "/api/public/status";
export const STATUS_PAGE_PATH = "/status";

// app/src/pages/status.tsx POLL_INTERVAL_MS, matched to the endpoint's CACHE_TTL_MS.
export const POLL_INTERVAL_MS = 10000;

export type ComponentStatus = "operational" | "degraded" | "outage" | "maintenance" | "unknown";

export interface StatusComponent {
  id: string;
  name: string;
  status: ComponentStatus;
  reason?: string;
}

export interface StatusPayload {
  status: ComponentStatus;
  components: StatusComponent[];
  checked_at: string;
  notice?: { state: string; message: string; components?: string[]; started_at?: string };
}

// SUMMARY_LABEL in app/src/pages/status.tsx — the words on the saturated status bar.
export const SUMMARY_LABEL: Record<ComponentStatus, string> = {
  operational: "All Systems Operational",
  degraded: "Partially Degraded Service",
  outage: "Major Service Outage",
  maintenance: "Under Maintenance",
  unknown: "Status Unavailable",
};

// COMPONENT_LABEL in app/src/pages/status.tsx — the per-component status word.
export const COMPONENT_LABEL: Record<ComponentStatus, string> = {
  operational: "Operational",
  degraded: "Degraded Performance",
  outage: "Major Outage",
  maintenance: "Under Maintenance",
  unknown: "Not Deployed",
};

// The endpoint prepends a synthetic component before the probed ones: the handler
// answering at all is its own proof the dashboard is serving, so it is always
// operational and is the reason a real deployment never rolls up to `unknown`.
export const DASHBOARD_COMPONENT_ID = "dashboard";

// rollup() from app/src/pages/api/public/status.ts, reproduced exactly.
//
// Deliberately not a max-severity fold: one component down among healthy ones is
// `degraded`, and only an all-outage fleet is `outage`. A naive worst-wins version
// of this would assert the wrong overall status for the single most likely real
// incident, and would do it while looking correct.
export function expectedRollup(components: StatusComponent[]): ComponentStatus {
  const known = components.filter((c) => c.status !== "unknown");
  if (known.length === 0) return "unknown";

  const problems = known.filter((c) => c.status === "outage" || c.status === "degraded");
  if (problems.length > 0) {
    return problems.length === known.length && problems.every((c) => c.status === "outage") ? "outage" : "degraded";
  }
  if (known.some((c) => c.status === "maintenance")) return "maintenance";
  return "operational";
}

// The endpoint promises a coarse status and nothing else: upstream URLs, hosts, ports
// and raw error strings stay server-side. These are the shapes that would mean that
// promise had been broken, checked against the component payload the page renders.
export const LEAK_PATTERNS: { name: string; pattern: RegExp }[] = [
  { name: "a URL scheme", pattern: /[a-z][a-z0-9+.-]*:\/\//i },
  { name: "a host:port pair", pattern: /[a-z0-9][a-z0-9.-]*:\d{2,5}\b/i },
  { name: "an in-cluster service address", pattern: /\.svc\b|\.cluster\.local\b/i },
  { name: "an IPv4 address", pattern: /\b\d{1,3}(\.\d{1,3}){3}\b/ },
];

export function assertStatusPayload(payload: unknown): asserts payload is StatusPayload {
  const candidate = payload as StatusPayload;
  expect(candidate, "the status endpoint returned no JSON body").toBeTruthy();
  expect(Array.isArray(candidate.components), "the status payload has no components array").toBe(true);
  expect(Object.keys(SUMMARY_LABEL), "the status payload carries an overall status outside the contract").toContain(
    candidate.status
  );
}

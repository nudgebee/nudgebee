// Not for OSS
import { Page, Locator, Response, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { readGlobalClusterValue } from "../utils/helpers";
import { HomeLocators } from "./homeLocators";

const ADOPTION_HINT =
  "/home never gained an ?accountId=. The header cluster dropdown writes it there " +
  "(ClusterDropDown.jsx), so an empty URL means no account was selected — check CLUSTER " +
  "in .env.dev and that global-setup selected it.";

const NO_QUICK_LINKS_HINT =
  "The Quick Links grid never rendered. HomeWidgets shows a skeleton until it resolves a " +
  "cloud provider for the selected account, so this means the account never resolved.";

const NOT_K8S_HINT =
  "Quick Links are not pointing at /kubernetes/details, so the selected account is not a K8s " +
  "cluster. This area's link set and its Security & Compliance section are K8s-only — set " +
  "CLUSTER to a K8s cluster.";

// Cluster comes from the environment, never from the spec: the same suite runs against dev
// and test, which hold different clusters. CLUSTER_NAME is the older of the two keys and is
// still what tests/ClusterDetails reads, so both are accepted.
export function requireCluster(): string {
  const cluster = process.env.CLUSTER_NAME || process.env.CLUSTER;
  if (!cluster) throw new Error("CLUSTER (or CLUSTER_NAME) is not set — add it to .env / .env.dev");
  return cluster;
}

// Held for the worker's lifetime. The account behind a cluster does not change mid-run, so
// this is worth one navigation rather than one per test — and the cluster check below guards
// the storageState global-setup saved, which is equally a per-run property.
let cachedAccountId = "";

// /home takes the account it fetches for from ?accountId= and fetches nothing without it,
// but the header cluster dropdown pushes the selected account into whatever route is open
// (shallow, in ClusterDropDown.jsx). Landing on the bare path and reading back what the app
// wrote is therefore the account the page itself considers in scope — no second page, and no
// id assembled by the test.
//
// Deliberately NOT via /kubernetes, which is the obvious source and is wrong: that route is a
// redirector whose effect runs once on mount, and reached by page.goto it can execute before
// DataContext has restored the saved selection — it then adopts the FIRST k8s account and
// persists it, switching the run's cluster. Proved in CI on PR #36608.
export async function resolveAccountId(page: Page): Promise<string> {
  if (cachedAccountId) return cachedAccountId;

  const cluster = requireCluster();

  await page.goto("/home");
  await expect(page, ADOPTION_HINT).toHaveURL(/[?&]accountId=[^&#]+/, { timeout: 60000 });

  // Guards the selection global-setup saved rather than the page: an account adopted from a
  // stale dropdown would silently point the whole spec at another cluster's insights.
  const active = await readGlobalClusterValue(page, 30000, cluster);
  if (active !== cluster) {
    throw new Error(`Opened the wrong cluster: the dropdown shows "${active || "(empty)"}", expected the configured one`);
  }

  const match = /[?&]accountId=([^&#]+)/.exec(page.url());
  if (!match) throw new Error("Could not read the adopted account id from the Home URL");

  cachedAccountId = match[1];
  return cachedAccountId;
}

export interface HomeSession {
  locators: HomeLocators;
  accountId: string;
}

// GraphQL operations Home fires from its account effect. They are listed per concern
// because the page clears ONE loading flag from FOUR independent `.finally` blocks:
// getTroubleShootData, getImageScan, getCertificate and getWorkflowData all set
// `k8sOps: false`, so the first to land takes the skeletons down while the rest are
// still open. Waiting only on the insight query would let a test read a half-filled
// section and pass on it. Each test waits for exactly the fetches its assertions read.
export const HOME_INSIGHT_OPS = ["InsightSummary"];
// getImageScan + getCertificate — both feed Security & Compliance's subtitle and its
// hasExternalData flag, which is what decides whether the footer renders at all.
export const HOME_SECURITY_OPS = ["ImageScanData", "CertificateIssue"];
// getWorkflowData awaits all three before it calls setWorkflowData, so all three have
// to land before the Automations card's render decision is final. WorkflowCount is
// listed twice because the page issues it twice (active, then active+event).
export const HOME_WORKFLOW_OPS = ["WorkflowCount", "WorkflowCount", "WorkflowExecutionCount"];

// Resolves once every listed operation has come back, counting repeats. Attached before
// the navigation that triggers them — a listener added afterwards can miss a response
// that already landed, which is the same race in a new place.
function watchHomeData(page: Page, operations: string[], timeout = 120000): Promise<void> {
  const outstanding = new Map<string, number>();
  for (const op of operations) outstanding.set(op, (outstanding.get(op) ?? 0) + 1);
  if (outstanding.size === 0) return Promise.resolve();

  return new Promise<void>((resolve, reject) => {
    const onResponse = (response: Response) => {
      if (!response.url().includes("/api/graphql")) return;
      const body = response.request().postData() || "";
      // First match wins and the loop stops, so a WorkflowExecutionCount body cannot be
      // counted against WorkflowCount — neither name is a substring of the other.
      for (const [op, left] of outstanding) {
        if (!body.includes(op)) continue;
        if (left <= 1) outstanding.delete(op);
        else outstanding.set(op, left - 1);
        break;
      }
      if (outstanding.size === 0) {
        cleanup();
        resolve();
      }
    };
    const timer = setTimeout(() => {
      cleanup();
      reject(new Error(`Home never answered these operations: ${[...outstanding.keys()].join(", ")}`));
    }, timeout);
    const cleanup = () => {
      clearTimeout(timer);
      page.off("response", onResponse);
    };
    page.on("response", onResponse);
  });
}

// Lands on Home with the account already named, the way the sidebar's Home entry does once
// anything else in the app has put an account in scope (getDynamicPath in
// app/src/components/common/layout/index.jsx). Naming it up front means the dropdown has
// nothing left to push, so the URL stays put for the duration of the test.
export async function openHome(page: Page, alsoWaitFor: string[] = []): Promise<HomeSession> {
  const locators = new HomeLocators(page);
  await new LoginPage(page).doFullLogin();

  const accountId = await resolveAccountId(page);

  // Home fires these from an effect on the account id, and CardsBlock shows skeleton rows
  // until they land. Callers add the fetches their own assertions read — see the operation
  // lists above for why the insight query alone is not enough.
  const settled = watchHomeData(page, [...HOME_INSIGHT_OPS, ...alsoWaitFor]);
  await page.goto(`/home?accountId=${accountId}`);
  await settled;

  await expect(locators.quickLinksGrid, NO_QUICK_LINKS_HINT).toBeVisible({ timeout: 60000 });
  await expect(locators.quickLinkAnchors.first(), NOT_K8S_HINT).toHaveAttribute("href", /^\/kubernetes\/details\//, { timeout: 30000 });

  return { locators, accountId };
}

// The bare path, with no account named — what a fresh sign-in and the root redirect both
// hand a user. Left exactly as navigated so a test can observe the dropdown's adoption
// itself; every other entry point goes through openHome above.
export async function openHomeUnscoped(page: Page): Promise<HomeLocators> {
  const locators = new HomeLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto("/home");
  await expect(locators.quickLinksGrid, NO_QUICK_LINKS_HINT).toBeVisible({ timeout: 60000 });

  return locators;
}

// A section has settled once its insight fetch is no longer painting skeleton rows. The
// fetch itself is already awaited in openHome, so this only covers the render that follows.
export async function expectSectionSettled(locators: HomeLocators, title: string): Promise<void> {
  await expect(locators.sectionHeader(title)).toBeVisible({ timeout: 60000 });
  await expect(locators.sectionSkeletons(title)).toHaveCount(0, { timeout: 60000 });
}

export async function expectExpanded(header: Locator, open: boolean): Promise<void> {
  await expect(header).toHaveAttribute("aria-expanded", String(open), { timeout: 15000 });
}

// The subtitle sits in the header button alongside the title, so the header's own text is
// what carries it. Returns the whole string; callers match it against the section's
// documented fallback copy or the counted form.
export async function readSectionHeaderText(locators: HomeLocators, title: string): Promise<string> {
  const header = locators.sectionHeader(title);
  await expect(header).toBeVisible({ timeout: 60000 });
  return ((await header.textContent()) ?? "").replace(/\s+/g, " ").trim();
}

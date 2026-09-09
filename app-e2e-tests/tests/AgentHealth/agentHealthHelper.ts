// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { readGlobalClusterValue } from "../utils/helpers";
import { AgentHealthLocators } from "./agentHealthLocators";

export type AgentHealthTab = "agent" | "proxy-agent";

// Precondition for the whole area: the environment's selected account is a K8s cluster.
// /agentHealth drops the Agent tab entirely for a self-hosted fleet (SELF_HOSTED in
// app/src/pages/agentHealth.jsx), so the module has a different shape there.
const NO_TABS_HINT =
  "The Agent Health tab strip did not render. /agentHealth needs the global cluster dropdown " +
  "to hold a K8s account — check CLUSTER in .env.dev and that global-setup selected it.";

const ADOPTION_HINT =
  "/agentHealth never adopted an account into its URL. The page takes it from the header cluster " +
  "dropdown, so this means no account was selected — check CLUSTER in .env.dev and global-setup.";

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

// /agentHealth reads its account from ?accountId= and fetches nothing without it, but it also
// adopts the header dropdown's account and writes that id into its own URL. Landing on the
// bare path and reading back what the app chose is therefore the account the module itself
// considers in scope — no second page, and no id assembled by the test.
//
// Deliberately NOT via /kubernetes, which is the obvious source and is wrong. That route is a
// redirector whose effect runs once on mount: reached by `page.goto` it can execute before
// DataContext has restored the saved selection, and the `selectedCluster?.value` branch then
// misses, so it falls through to `setSelectedCluster(clusters[0])` — adopting the FIRST k8s
// account and persisting it. CI proved it: every test that went that way opened "test-again"
// instead of the configured cluster, because the redirect had switched the run's account.
export async function resolveAccountId(page: Page): Promise<string> {
  if (cachedAccountId) return cachedAccountId;

  const cluster = requireCluster();

  await page.goto("/agentHealth");
  await expect(page, ADOPTION_HINT).toHaveURL(/[?&]accountId=[^&#]+/, { timeout: 60000 });

  // Guards the selection global-setup saved rather than the page: an account adopted from a
  // stale dropdown would silently point the whole spec at another cluster's agent.
  const active = await readGlobalClusterValue(page, 30000, cluster);
  if (active !== cluster) {
    throw new Error(`Opened the wrong cluster: the dropdown shows "${active || "(empty)"}", expected the configured one`);
  }

  const match = /[?&]accountId=([^&#]+)/.exec(page.url());
  if (!match) throw new Error("Could not read the adopted account id from the Agent Health URL");

  cachedAccountId = match[1];
  return cachedAccountId;
}

// Lands on the module the way a deep link from the cluster dropdown does
// (app/src/components/common/CustomDropdown.jsx renders /agentHealth?accountId=…#agent).
// The account is named in the URL, so the page's own adoption has nothing left to write and
// the fragment survives the load — the tab it names is the one that opens.
export async function openAgentHealth(page: Page, fragment: AgentHealthTab = "agent"): Promise<AgentHealthLocators> {
  const locators = new AgentHealthLocators(page);
  await new LoginPage(page).doFullLogin();

  const accountId = await resolveAccountId(page);
  await page.goto(`/agentHealth?accountId=${accountId}#${fragment}`);

  await expect(locators.agentTab, NO_TABS_HINT).toBeVisible({ timeout: 60000 });
  await expectSelectedTab(fragment === "agent" ? locators.agentTab : locators.proxyAgentTab);

  return locators;
}

// The bare path, with no account named and no fragment — what the sidebar and global search
// hand a user. Left exactly as navigated so a test can observe the adoption itself; every
// other entry point goes through openAgentHealth above.
export async function openAgentHealthUnscoped(page: Page): Promise<AgentHealthLocators> {
  const locators = new AgentHealthLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto("/agentHealth");

  await expect(locators.agentTab, NO_TABS_HINT).toBeVisible({ timeout: 60000 });

  return locators;
}

// MUI marks the open tab with aria-selected, and unlike the URL it is always in step with
// which panel is mounted — agentHealth.jsx derives its tab from the hash, so the attribute
// is what proves the derivation happened rather than just that the link changed the URL.
export async function expectSelectedTab(tab: Locator): Promise<void> {
  await expect(tab).toHaveAttribute("aria-selected", "true", { timeout: 15000 });
}

export async function expectFragment(page: Page, fragment: AgentHealthTab): Promise<void> {
  await expect(page).toHaveURL(new RegExp(`#${fragment}$`), { timeout: 15000 });
}

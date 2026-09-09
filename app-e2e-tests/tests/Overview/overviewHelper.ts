// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { readGlobalClusterValue } from "../utils/helpers";
import { OverviewLocators } from "./overviewLocators";

const NO_SECTIONS_HINT =
  "/overview never rendered any of its three sections. AccountOverview only draws a " +
  "section when the tenant has accounts of that kind, so this means the loaded tenant " +
  "has no K8s cluster, cloud account or self-hosted VM — check CLUSTER / SWITCH_TENANT.";

// Cluster comes from the environment, never from the spec: the same suite runs against
// dev and test, which hold different clusters. CLUSTER_NAME is the older of the two
// keys and is still what tests/ClusterDetails reads, so both are accepted.
export function requireCluster(): string {
  const cluster = process.env.CLUSTER_NAME || process.env.CLUSTER;
  if (!cluster) throw new Error("CLUSTER (or CLUSTER_NAME) is not set — add it to .env / .env.dev");
  return cluster;
}

export interface OverviewSession {
  locators: OverviewLocators;
  cluster: string;
}

// Lands on /overview after the shared login has restored the configured cluster in the
// header dropdown. Overview itself sets the selected cluster to {} in a mount effect,
// so we probe the dropdown BEFORE navigating — that's the only moment the dropdown
// carries the storageState selection global-setup saved.
export async function openOverview(page: Page): Promise<OverviewSession> {
  const locators = new OverviewLocators(page);
  const cluster = requireCluster();

  await new LoginPage(page).doFullLogin();

  // Guards the selection global-setup saved before Overview clears it. A stale
  // storageState would silently open the wrong tenant, and the empty-state test
  // below would then read as a real pass instead of a bad env.
  const active = await readGlobalClusterValue(page, 30000, cluster);
  if (active !== cluster) {
    throw new Error(`Opened the wrong cluster: the dropdown shows "${active || "(empty)"}", expected the configured one`);
  }

  await page.goto("/overview");
  // AccountOverview kicks two effects on mount — the K8s cluster list and the batched
  // account/summary rollup. Waiting on the loaded page (via a landmark that only appears
  // after the fetches land) is what stops a stale "no sections" read below.
  await expect(page).toHaveURL(/\/overview/, { timeout: 60000 });

  return { locators, cluster };
}

// Resolves once at least one of the three sections has finished loading its heading,
// which is the same moment its data-driven guard flipped true. This is the only signal
// AccountOverview offers that its two `.finally`-guarded loading flags have both landed;
// waiting on one section alone would let the test read a half-populated page and pass.
export async function waitForAnySection(locators: OverviewLocators, timeout = 90000): Promise<void> {
  // Alternation rather than polling counts: one locator, one native auto-wait, and it
  // resolves the moment the first of the four lands. The empty-state heading is in the
  // set because an onboarding tenant is a settled page too, not a timeout — the specs
  // that require accounts assert that separately via readRenderedSections.
  //
  // .first() is load-bearing: a tenant with both clusters and cloud accounts matches two
  // of these at once, and an un-narrowed alternation would fail strict mode rather than pass.
  const anySection = locators.clustersHeading
    .or(locators.cloudHeading)
    .or(locators.vmHeading)
    .or(locators.emptyStateHeading)
    .first();

  await expect(anySection, NO_SECTIONS_HINT).toBeVisible({ timeout });
}

// Which section kinds the tenant has, read from what actually rendered on the page.
// This is the ground truth the anchor-nav test cross-checks — the page's own
// filterOptions memo derives from the same source, so the two must agree.
export interface RenderedSections {
  hasK8s: boolean;
  hasCloud: boolean;
  hasVm: boolean;
}

export async function readRenderedSections(locators: OverviewLocators): Promise<RenderedSections> {
  return {
    hasK8s: (await locators.clustersHeading.count()) > 0,
    hasCloud: (await locators.cloudHeading.count()) > 0,
    hasVm: (await locators.vmHeading.count()) > 0,
  };
}

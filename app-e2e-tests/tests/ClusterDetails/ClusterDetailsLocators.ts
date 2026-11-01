import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";
import { readGlobalClusterValue } from "../utils/helpers";

export class ClusterDetailsLocators extends CommonLocators {
  // all anchor tabs 
  readonly SummaryTab: Locator;
  readonly OptimizeTab: Locator;
  readonly AnchorTabTroubleshoot: Locator;
  readonly AnchorTabAppsAndInfra: Locator;
  readonly AnchorTabMonitoring: Locator;
  readonly AnchorTabSecurityAndTools: Locator;

  readonly AutoScalerTab: Locator;
  readonly UnusedVolumesTab: Locator;
  readonly BestPracticesTab: Locator;
  readonly AbandonedAppTab: Locator;
  readonly PVCRIghtSizingTab: Locator;
  readonly ReplicaRightSizingTab: Locator;
  readonly SpotRecommendationTab: Locator;
  readonly RecommendationResolution: Locator;
  readonly namespacedropdown: Locator;

  constructor(page: Page) {
    super(page);
    // Anchor Tab
    this.SummaryTab = page.locator("#anchor-tab-Summary");
    this.OptimizeTab = page.locator("#anchor-tab-Optimize");
    this.AnchorTabTroubleshoot = page.locator('#anchor-tab-Troubleshoot')
    this.AnchorTabAppsAndInfra = page.locator('[id="anchor-tab-Apps & Infra"]');
    this.AnchorTabMonitoring = page.locator('#anchor-tab-Monitoring')
    this.AnchorTabSecurityAndTools = page.locator('[id="anchor-tab-Security & Tools"]')

    this.AutoScalerTab = page.locator("#auto-scaler");
    this.UnusedVolumesTab = page.locator("#unused-volume");
    this.BestPracticesTab = page.locator("#best-practices");
    this.AbandonedAppTab = page.locator("#abandoned-resources");
    this.PVCRIghtSizingTab = page.locator("#pv-rightsizing");
    this.ReplicaRightSizingTab = page.locator("#replica-rightsizing");
    this.SpotRecommendationTab = page.locator("#spot-recommendation");
    this.RecommendationResolution = page.locator("#recommendation-resolution-status");
    this.namespacedropdown = page.locator("#auto-complete-namespace");
  }

  // `/kubernetes` no longer lists clusters — it redirects to the dropdown cluster's detail page (list is behind `#overview`).
  async openClusterFromConfig() {
    const clusterName = process.env.CLUSTER_NAME || process.env.CLUSTER;
    if (!clusterName) throw new Error("CLUSTER_NAME or CLUSTER env variable is not set");
    console.log(`Opening cluster: ${clusterName}`);

    // The dropdown writes its account into the URL once it resolves, so this is the configured cluster's id.
    const dropdownValue = await readGlobalClusterValue(this.page, 10000, clusterName);
    const clusterId = dropdownValue === clusterName ? new URL(this.page.url()).searchParams.get("accountId") : null;

    await this.openClustersFromSidenav();
    console.log("Navigated to K8s clusters via Infra sidenav");

    // The /kubernetes redirect can hang or open the first K8s account on a slow app — reopen by id when it does.
    const reached = await this.page
      .waitForURL(/\/kubernetes\/details\/[^/?#]+/, { timeout: 20000 })
      .then(() => true)
      .catch(() => false);
    const active = reached ? await readGlobalClusterValue(this.page, 30000, clusterName) : "";
    if (active === clusterName) {
      console.log(`Opened cluster: ${clusterName}`);
      return;
    }
    if (!clusterId) {
      throw new Error(`Opened the wrong cluster: dropdown shows "${active || "(empty)"}", expected "${clusterName}"`);
    }

    console.warn(
      reached
        ? `/kubernetes redirect opened "${active || "(empty)"}" instead of "${clusterName}" — opening ${clusterName} by id`
        : `/kubernetes redirect never reached a cluster page (still on ${this.page.url()}) — opening ${clusterName} by id`
    );
    await this.page.goto(`${process.env.BASE_URL}/kubernetes/details/${clusterId}#summary`);
    const reopened = await readGlobalClusterValue(this.page, 30000, clusterName);
    if (reopened !== clusterName) {
      throw new Error(`Opened the wrong cluster: dropdown shows "${reopened || "(empty)"}", expected "${clusterName}"`);
    }
    console.log(`Opened cluster: ${clusterName}`);
  }
}

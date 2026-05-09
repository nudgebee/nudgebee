import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";
import fs from "fs";
import path from "path";

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
  }

  async openClusterFromConfig() {
    const configPath = path.resolve(__dirname, "../../e2e-config.json");

    console.log(`Looking for config at: ${configPath}`); // Debug log

    if (!fs.existsSync(configPath)) {
      throw new Error(`Config file not found at: ${configPath}`);
    }

    const config = JSON.parse(fs.readFileSync(configPath, "utf-8"));
    const clusterName = config.cluster_name;
    console.log(`Opening cluster: ${clusterName}`); 

    await expect(this.ClusterBtn).toBeVisible();
    await this.ClusterBtn.click();
    console.log("Clicked on Cluster button");

    const clusterLocator = this.page.getByText(clusterName, { exact: true });
    await expect(clusterLocator).toBeVisible();
    await clusterLocator.click();
    // Move mouse away so the cursor doesn't land on an AnchorComponent tab
    // when the cluster details page renders, which would trigger onMouseOver
    // and open a popover whose backdrop intercepts subsequent clicks.
    await this.page.mouse.move(0, 0);
    console.log(`Opened cluster: ${clusterName}`);
  }
}

import { Page } from "@playwright/test";
import { ClusterDetailsLocators } from "../ClusterDetailsLocators";

export class AppsAndInfraLocators extends ClusterDetailsLocators {
  // Child sub-tab ids under the "Apps & Infra" section. Each tab renders in two
  // places with the same id family: the horizontal sub-tab strip — GAMMA — (`#<id>`)
  // and the anchor dropdown menu — BETA — (`#dropdown-<id>`), so clickTab can fall
  // back from GAMMA to BETA.
  readonly NodesTab = "nodes";
  readonly ApplicationsTab = "applications";
  readonly Pods = "pods";
  readonly Namespaces = "namespaces";
  readonly Services = "services";
  readonly PVC = "pvc";
  readonly PV = "pv";
  readonly Databases = "dbms";
  readonly Queues = "queue";

  constructor(page: Page) {
    super(page);
  }

  // Step 1 — open the "Apps & Infra" section.
  // Click the ALPHA anchor tab and verify the url hash became #kubernetes.
  // Retry the whole click up to 3 times — the redirect must succeed.
  async navigateToCluster(maxRetries = 3): Promise<void> {
    // Wait for navigation to the cluster details page after openClusterFromConfig().
    await this.page.waitForURL(/\/kubernetes\/details\/[^/?#]+/, { timeout: 30000 });

    for (let attempt = 1; attempt <= maxRetries; attempt++) {
      await this.AnchorTabAppsAndInfra.waitFor({ state: "visible", timeout: 15000 });
      await this.AnchorTabAppsAndInfra.click();
      // Move mouse away so the hover-opened dropdown backdrop doesn't intercept clicks.
      await this.page.mouse.move(0, 0);

      await this.page.waitForURL(/#kubernetes/, { timeout: 5000 }).catch(() => {});
      if (/#kubernetes/.test(this.page.url())) return;

      console.warn(`[AppsAndInfraLocators] navigateToCluster attempt ${attempt}/${maxRetries} failed — URL: ${this.page.url()}`);
      await this.page.waitForTimeout(1500);
    }
    throw new Error(
      `[AppsAndInfraLocators] Failed to open Apps & Infra section after ${maxRetries} attempts. Current URL: ${this.page.url()}`
    );
  }

  // Step 2 — click a child sub-tab by id.
  // Plan A: click the GAMMA tab in the horizontal strip (`#<id>`).
  // Plan B (fallback): if GAMMA isn't visible, hover ALPHA to open the BETA
  // dropdown and click its item (`#dropdown-<id>`).
  async clickTab(sectionId: string): Promise<void> {
    const gammaTab = this.page.locator(`[id="${sectionId}"]`);
    const betaItem = this.page.locator(`[id="dropdown-${sectionId}"]`);

    try {
      await gammaTab.waitFor({ state: "visible", timeout: 5000 });
      await gammaTab.click();
      await this.page.mouse.move(0, 0);
      return;
    } catch {
      // Fall back to the BETA dropdown below.
    }

    await this.AnchorTabAppsAndInfra.hover();
    await betaItem.waitFor({ state: "visible", timeout: 5000 });
    await betaItem.click();
    await this.page.mouse.move(0, 0);
  }
}

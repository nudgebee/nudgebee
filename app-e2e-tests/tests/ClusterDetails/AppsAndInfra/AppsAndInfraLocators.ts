import { Page, Locator } from "@playwright/test";
import { ClusterDetailsLocators } from "../ClusterDetailsLocators";

export class AppsAndInfraLocators extends ClusterDetailsLocators {
  // all anchor tabs
  readonly NodesTab: Locator;
  readonly ApplicationsTab: Locator;
  readonly Pods: Locator;
  readonly Namespaces: Locator;
  readonly Services: Locator;
  readonly PVC: Locator;
  readonly PV: Locator;
  readonly Databases: Locator;
  readonly Queues: Locator;

  constructor(page: Page) {
    super(page);
    // Anchor Tab
    this.NodesTab = page.locator("#nodes");
    this.ApplicationsTab = page.locator("#applications");
    this.Pods = page.locator("#pods");
    this.Namespaces = page.locator("#namespaces");
    this.Services = page.locator("#services");
    this.PVC = page.locator("#pvc");
    this.PV = page.locator("#pv");
    this.Databases = page.locator("#dbms");
    this.Queues = page.locator("#queue");
  }

  async clickTab(locator: Locator): Promise<void> {
    await locator.click();
    await this.page.mouse.move(0, 0);
  }

  async navigateToCluster(): Promise<void> {
    // Wait for navigation to cluster details page after openClusterFromConfig() click
    // URL pattern: /kubernetes/details/<cluster-id>
    await this.page.waitForURL(/\/kubernetes\/details\/[^/?#]+/, { timeout: 30000 });
    const currentUrl = this.page.url();
    const match = currentUrl.match(/\/kubernetes\/details\/([^/?#]+)/);
    if (!match) {
      throw new Error(`Could not extract cluster ID from URL: ${currentUrl}`);
    }
    const clusterId = match[1];
    const baseUrl = new URL(currentUrl).origin;
    await this.page.goto(`${baseUrl}/kubernetes/details/${clusterId}#kubernetes/nodes`);
    // Move mouse away to prevent hover-triggered dropdowns (e.g. Security & Tools) from opening
    await this.page.mouse.move(0, 0);
  }
}

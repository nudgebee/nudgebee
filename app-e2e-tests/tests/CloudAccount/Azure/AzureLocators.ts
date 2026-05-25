import { Page, Locator, test } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";
import axios from "axios";

export class AzureLocators extends CommonLocators {
  // Cloud Account Details - common anchor tabs
  readonly AnchorTabSummary: Locator;
  readonly AnchorTabOptimize: Locator;
  readonly AnchorTabServices: Locator;
  readonly AnchorTabTroubleshoot: Locator;
  readonly AnchorTabMonitoring: Locator;

  // Azure-specific service tabs
  readonly AnchorTabVM: Locator;
  readonly AnchorTabSQL: Locator;
  readonly AnchorTabBlobContainer: Locator;

  // Optimize subtabs
  readonly OptimizeRightSizing: Locator;
  readonly OptimizeConfiguration: Locator;
  readonly OptimizeSecurityTab: Locator;
  readonly OptimizeInfraUpgrade: Locator;
  readonly OptimizeRecommendationResolution: Locator;

  // Troubleshoot subtabs
  readonly TroubleshootEvents: Locator;
  readonly TroubleshootTriageRules: Locator;

  // Monitoring subtabs
  readonly MonitoringAlertManager: Locator;
  readonly MonitoringCloudLogs: Locator;
  readonly MonitoringCloudMetrics: Locator;

  // VM sub-tabs
  readonly VMSummary: Locator;
  readonly VMOptimize: Locator;
  readonly VMInstances: Locator;
  readonly VMEvents: Locator;

  // SQL sub-tabs
  readonly SQLSummary: Locator;
  readonly SQLOptimize: Locator;
  readonly SQLInstances: Locator;
  readonly SQLEvents: Locator;

  // Blob Container sub-tabs
  readonly BlobContainerSummary: Locator;
  readonly BlobContainerOptimize: Locator;
  readonly BlobContainerInstances: Locator;
  readonly BlobContainerEvents: Locator;

  // Services drilldown tabs (opened when a service row's expand arrow is clicked)
  readonly ServicesRowExpandButton: Locator;
  readonly ServicesDrilldownTabResources: Locator;
  readonly ServicesDrilldownTabCostTrend: Locator;
  readonly ServicesDrilldownTabRecommendations: Locator;

  constructor(page: Page) {
    super(page);

    // Common anchor tabs
    this.AnchorTabSummary = page.locator("#anchor-tab-Summary");
    this.AnchorTabOptimize = page.locator("#anchor-tab-Optimize");
    this.AnchorTabServices = page.locator("#anchor-tab-Services");
    this.AnchorTabTroubleshoot = page.locator("#anchor-tab-Troubleshoot");
    this.AnchorTabMonitoring = page.locator("#anchor-tab-Monitoring");

    // Azure-specific service tabs
    this.AnchorTabVM = page.locator("#anchor-tab-VM");
    this.AnchorTabSQL = page.locator('[id="anchor-tab-SQL Databases"]');
    this.AnchorTabBlobContainer = page.locator('[id="anchor-tab-Blob Container"]');

    // Optimize subtabs
    this.OptimizeRightSizing = page.locator("#dropdown-optimize-right-sizing");
    this.OptimizeConfiguration = page.locator("#dropdown-optimize-configuration");
    this.OptimizeSecurityTab = page.locator("#dropdown-optimize-security");
    this.OptimizeInfraUpgrade = page.locator("#dropdown-optimize-infra-upgrade");
    this.OptimizeRecommendationResolution = page.locator("#dropdown-recommendation-resolution-status");

    // Troubleshoot subtabs
    this.TroubleshootEvents = page.locator("#dropdown-events");
    this.TroubleshootTriageRules = page.locator("#dropdown-triage-rules");

    // Monitoring subtabs
    this.MonitoringAlertManager = page.locator("#dropdown-alert-manager");
    this.MonitoringCloudLogs = page.locator("#dropdown-logs");
    this.MonitoringCloudMetrics = page.locator("#dropdown-metrics");

    // VM sub-tabs
    this.VMSummary = page.locator("#dropdown-summary");
    this.VMOptimize = page.locator("#dropdown-optimize");
    this.VMInstances = page.locator("#dropdown-instances");
    this.VMEvents = page.locator("#dropdown-events");

    // SQL sub-tabs
    this.SQLSummary = page.locator("#dropdown-summary");
    this.SQLOptimize = page.locator("#dropdown-optimize");
    this.SQLInstances = page.locator("#dropdown-instances");
    this.SQLEvents = page.locator("#dropdown-events");

    // Blob Container sub-tabs
    this.BlobContainerSummary = page.locator("#dropdown-summary");
    this.BlobContainerOptimize = page.locator("#dropdown-optimize");
    this.BlobContainerInstances = page.locator("#dropdown-instances");
    this.BlobContainerEvents = page.locator("#dropdown-events");

    // Services drilldown tabs (expand arrow + tab buttons inside the collapsed row)
    this.ServicesRowExpandButton = page.locator('img[alt="arrow"]').first();
    this.ServicesDrilldownTabResources = page.getByRole("tab", { name: "Resources" });
    this.ServicesDrilldownTabCostTrend = page.getByRole("tab", { name: "Cost Trend" });
    this.ServicesDrilldownTabRecommendations = page.getByRole("tab", { name: "Recommendations" });
  }

  // Returns the expand arrow for the first row inside the Resources drilldown table.
  getResourceRowExpandButton() {
    return this.page
      .locator("#service-resource-listing-table")
      .locator('img[alt="arrow"]')
      .first();
  }

  async checkAzureIntegration(): Promise<boolean> {
    console.log("Checking Azure integration status via Admin > Integrations...");

    await this.adminBtn.click();

    const navigated = await this.page.waitForURL(/user-management/, { timeout: 15000 })
      .then(() => true)
      .catch(() => false);

    if (!navigated) {
      console.log("Admin nav click did not navigate — falling back to direct URL");
      await this.page.goto(`${process.env.BASE_URL}/user-management`);
    }

    await this.page.waitForLoadState("networkidle");

    const integrationsTab = this.page.locator("#anchor-tab-Integrations");
    await integrationsTab.waitFor({ state: "visible", timeout: 20000 });
    await integrationsTab.click();
    await this.page.waitForLoadState("networkidle");

    const azureCard = this.page.locator("#Azure-section-card");
    await azureCard.waitFor({ state: "visible", timeout: 10000 });

    const isActive = await azureCard
      .getByText("Active", { exact: true })
      .isVisible({ timeout: 5000 })
      .catch(() => false);

    console.log(`Azure integration status: ${isActive ? "Active ✅" : "Not Active ❌"}`);
    return isActive;
  }

  async sendSlackNotification(message: string): Promise<void> {
    const webhookUrl = process.env.SLACK_WEBHOOK_URL;
    if (!webhookUrl) {
      console.warn("[AzureLocators] SLACK_WEBHOOK_URL not set, skipping notification");
      return;
    }
    try {
      await axios.post(webhookUrl, { text: message });
      console.log(`[AzureLocators] Slack notification sent: ${message}`);
    } catch (error) {
      console.warn(`[AzureLocators] Failed to send Slack notification: ${error}`);
    }
  }

  async openAzureCloudAccountFromConfig() {
    const isActive = await this.checkAzureIntegration();

    if (!isActive) {
      await this.sendSlackNotification(
        "Please integrate Azure first, then I will start the testing."
      );
      test.skip(true, "Azure integration is not Active — Slack notification sent");
      return;
    }

    const cloudSidenavBtn = this.page.locator("#cloud-sidenavbutton");
    await cloudSidenavBtn.click();
    await this.page.waitForURL(/cloud-account/, { timeout: 15000 });
    await this.page.waitForLoadState("networkidle");
    await this.page.mouse.move(0, 0);
    console.log("Navigated to cloud account via sidenav");

    const azureSearchTerm = process.env.AZURE_CLUSTER_NAME || "iteration-azure";
    await this.page.waitForTimeout(500);
    const clusterInput = this.page.locator("#auto-complete-global-cluster");

    const typeInCluster = async () => {
      await clusterInput.click({ clickCount: 3 });
      await clusterInput.press("Control+a");
      await clusterInput.press("Delete");
      await clusterInput.fill("");
      await clusterInput.pressSequentially(azureSearchTerm, { delay: 50 });
    };

    await typeInCluster();
    console.log(`Typed '${azureSearchTerm}' in global cluster autocomplete`);

    const azureOption = this.page
      .locator("[role='option']")
      .filter({ hasText: azureSearchTerm })
      .first();

    const optionVisible = await azureOption.isVisible().catch(() => false);
    if (!optionVisible) {
      console.log(`No option found for '${azureSearchTerm}', retrying...`);
      await typeInCluster();
      await this.page.waitForTimeout(500);
    }

    await azureOption.waitFor({ state: "visible", timeout: 10000 });

    await this.page.keyboard.press("ArrowDown");
    await this.page.keyboard.press("Enter");
    console.log("Selected Azure cloud account option via keyboard");

    await this.page.mouse.move(0, 0);
    await this.page.waitForURL(/cloud-account/, { timeout: 15000 });
    await this.page.waitForLoadState("networkidle");
    console.log("Azure cloud account detail page loaded");
  }
}

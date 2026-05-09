import { Page, Locator, test } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";
import axios from "axios";

export class CloudAccountLocators extends CommonLocators {
  // Sidenav
  readonly CloudBtn: Locator;

  // Cloud Account Details - Anchor tabs
  readonly AnchorTabSummary: Locator;
  readonly AnchorTabOptimize: Locator;
  readonly AnchorTabServices: Locator;
  readonly AnchorTabTroubleshoot: Locator;
  readonly AnchorTabMonitoring: Locator;

  // GCP-specific service tabs
  readonly AnchorTabComputeEngine: Locator;
  readonly AnchorTabCloudSQL: Locator;
  readonly AnchorTabCloudStorage: Locator;

  // Optimize tab subtabs
  readonly OptimizeRightSizing: Locator;
  readonly OptimizeConfiguration: Locator;
  readonly OptimizeSecurityTab: Locator;
  readonly OptimizeInfraUpgrade: Locator;
  readonly OptimizeRecommendationResolution: Locator;

  // Troubleshoot tab subtabs
  readonly TroubleshootEvents: Locator;
  readonly TroubleshootTriageRules: Locator;

  // Monitoring tab subtabs
  readonly MonitoringAlertManager: Locator;
  readonly MonitoringCloudLogs: Locator;
  readonly MonitoringCloudMetrics: Locator;

  // GCP Compute Engine sub-tabs (dropdown items)
  readonly ComputeEngineSummary: Locator;
  readonly ComputeEngineOptimize: Locator;
  readonly ComputeEngineInstances: Locator;
  readonly ComputeEngineEvents: Locator;

  // GCP Cloud SQL sub-tabs (same dropdown IDs as Compute Engine)
  readonly CloudSQLSummary: Locator;
  readonly CloudSQLOptimize: Locator;
  readonly CloudSQLInstances: Locator;
  readonly CloudSQLEvents: Locator;

  // GCP Cloud Storage sub-tabs (same dropdown IDs)
  readonly CloudStorageSummary: Locator;
  readonly CloudStorageOptimize: Locator;
  readonly CloudStorageInstances: Locator;
  readonly CloudStorageEvents: Locator;

  // Services drilldown tabs (opened when a service row's expand arrow is clicked)
  readonly ServicesRowExpandButton: Locator;
  readonly ServicesDrilldownTabResources: Locator;
  readonly ServicesDrilldownTabCostTrend: Locator;
  readonly ServicesDrilldownTabRecommendations: Locator;

  constructor(page: Page) {
    super(page);

    // Sidenav
    this.CloudBtn = page.locator("#cloud-sidenavbutton");

    // Cloud Account Details - Anchor tabs (using AnchorComponent IDs)
    this.AnchorTabSummary = page.locator("#anchor-tab-Summary");
    this.AnchorTabOptimize = page.locator("#anchor-tab-Optimize");
    this.AnchorTabServices = page.locator("#anchor-tab-Services");
    this.AnchorTabTroubleshoot = page.locator("#anchor-tab-Troubleshoot");
    this.AnchorTabMonitoring = page.locator("#anchor-tab-Monitoring");

    // GCP-specific service tabs
    this.AnchorTabComputeEngine = page.locator('[id="anchor-tab-Compute Engine"]');
    this.AnchorTabCloudSQL = page.locator('[id="anchor-tab-Cloud SQL"]');
    this.AnchorTabCloudStorage = page.locator('[id="anchor-tab-Cloud Storage"]');

    // Optimize tab subtabs
    this.OptimizeRightSizing = page.locator("#dropdown-optimize-right-sizing");
    this.OptimizeConfiguration = page.locator("#dropdown-optimize-configuration");
    this.OptimizeSecurityTab = page.locator("#dropdown-optimize-security");
    this.OptimizeInfraUpgrade = page.locator("#dropdown-optimize-infra-upgrade");
    this.OptimizeRecommendationResolution = page.locator("#dropdown-recommendation-resolution-status");

    // Troubleshoot tab subtabs
    this.TroubleshootEvents = page.locator("#dropdown-events");
    this.TroubleshootTriageRules = page.locator("#dropdown-triage-rules");

    // Monitoring tab subtabs
    this.MonitoringAlertManager = page.locator("#dropdown-alert-manager");
    this.MonitoringCloudLogs = page.locator("#dropdown-logs");
    this.MonitoringCloudMetrics = page.locator("#dropdown-metrics");

    // GCP Compute Engine sub-tabs (dropdown items)
    this.ComputeEngineSummary = page.locator("#dropdown-summary");
    this.ComputeEngineOptimize = page.locator("#dropdown-optimize");
    this.ComputeEngineInstances = page.locator("#dropdown-instances");
    this.ComputeEngineEvents = page.locator("#dropdown-events");

    // GCP Cloud SQL sub-tabs (reuse same dropdown IDs)
    this.CloudSQLSummary = page.locator("#dropdown-summary");
    this.CloudSQLOptimize = page.locator("#dropdown-optimize");
    this.CloudSQLInstances = page.locator("#dropdown-instances");
    this.CloudSQLEvents = page.locator("#dropdown-events");

    // GCP Cloud Storage sub-tabs (reuse same dropdown IDs)
    this.CloudStorageSummary = page.locator("#dropdown-summary");
    this.CloudStorageOptimize = page.locator("#dropdown-optimize");
    this.CloudStorageInstances = page.locator("#dropdown-instances");
    this.CloudStorageEvents = page.locator("#dropdown-events");

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

  // ── GCP Integration Check ─────────────────────────────────────────────────
  // Navigates to Admin → Integrations and checks whether the GCP card
  // (id="Gcp-section-card") is in the Connected section by looking for the
  // "Active" text the app renders inside the card when active > 0.
  // This mirrors how integration.jsx and AccountCard.jsx display onboarded status.
  async checkGCPIntegration(): Promise<boolean> {
    console.log("Checking GCP integration status via Admin > Integrations...");

    // Open Admin section
    await this.adminBtn.click();
    await this.page.waitForLoadState("networkidle");

    // Click the Integrations anchor tab
    const integrationsTab = this.page.locator("#anchor-tab-Integrations");
    await integrationsTab.waitFor({ state: "visible", timeout: 10000 });
    await integrationsTab.click();
    await this.page.waitForLoadState("networkidle");

    // The GCP AccountCard (id="Gcp-section-card") shows "Active" text only
    // when at least one GCP cloud account has status="active" — exactly what
    // integration.jsx's generateSectionData / AccountCard renders.
    const gcpCard = this.page.locator("#Gcp-section-card");
    await gcpCard.waitFor({ state: "visible", timeout: 10000 });

    const isActive = await gcpCard
      .getByText("Active", { exact: true })
      .isVisible({ timeout: 5000 })
      .catch(() => false);

    console.log(`GCP integration status: ${isActive ? "Active ✅" : "Not Active ❌"}`);
    return isActive;
  }

  // ── Slack Notification ────────────────────────────────────────────────────
  async sendSlackNotification(message: string): Promise<void> {
    const webhookUrl = process.env.SLACK_WEBHOOK_URL;
    if (!webhookUrl) {
      console.warn("[CloudAccountLocators] SLACK_WEBHOOK_URL not set, skipping notification");
      return;
    }
    try {
      await axios.post(webhookUrl, { text: message });
      console.log(`[CloudAccountLocators] Slack notification sent: ${message}`);
    } catch (error) {
      console.warn(`[CloudAccountLocators] Failed to send Slack notification: ${error}`);
    }
  }

  // ── GCP Cloud Account Navigation (with integration pre-check) ─────────────
  // 1. Checks GCP integration via Admin → Integrations UI.
  // 2. If not Active: sends Slack message and skips the test.
  // 3. If Active: clicks Cloud sidenav — /cloud-account auto-redirects to
  //    /cloud-account/details/{first active cloud account}.
  // 4. Types "iteration-gcp" in autocomplete to select the GCP cloud account.
  //    - If already on GCP account (same ID): Header Case 4 is a no-op but
  //      waitForURL resolves immediately since the URL already matches.
  //    - If on a different cloud account: Header Case 4 navigates to GCP.
  async openGCPCloudAccountFromConfig() {
    // 1. UI-based GCP integration check via Admin → Integrations
    const isActive = await this.checkGCPIntegration();

    if (!isActive) {
      await this.sendSlackNotification(
        "Please integrate GCP first, then I will start the testing."
      );
      test.skip(true, "GCP integration is not Active — Slack notification sent");
      return;
    }

    // 2. Click Cloud sidenav button.
    //    /cloud-account/index.jsx auto-redirects to /cloud-account/details/{id}.
    const cloudSidenavBtn = this.page.locator("#cloud-sidenavbutton");
    await cloudSidenavBtn.click();
    await this.page.waitForURL(/cloud-account/, { timeout: 15000 });
    await this.page.waitForLoadState("networkidle");
    // Move mouse away to prevent AnchorComponent tab hover side-effects.
    await this.page.mouse.move(0, 0);
    console.log("Navigated to cloud account via sidenav");

    // 3. Type in the global cluster autocomplete to select the GCP cloud account.
    //    Uses the same [role='option'] + hasText pattern as LoginPage.selectCluster()
    //    because MUI renders a "No options available" div (no [role='listbox']) when
    //    the filter returns zero matches, causing waitForSelector('[role=listbox]') to
    //    time out. A brief wait after navigation lets the dropdown data stabilise.
    const gcpSearchTerm = process.env.GCP_CLUSTER_NAME || "iteration-gcp";
    await this.page.waitForTimeout(500);
    const clusterInput = this.page.locator("#auto-complete-global-cluster");
    // Triple-click selects all existing text so typing replaces it entirely.
    await clusterInput.click({ clickCount: 3 });
    await clusterInput.pressSequentially(gcpSearchTerm, { delay: 50 });
    console.log(`Typed '${gcpSearchTerm}' in global cluster autocomplete`);

    // Wait for the matching option to appear (confirms the filter returned results).
    await this.page
      .locator("[role='option']")
      .filter({ hasText: gcpSearchTerm })
      .first()
      .waitFor({ state: "visible", timeout: 10000 });

    // Select via keyboard so the mouse cursor never moves into the dropdown area.
    // A direct .click() on the option leaves the cursor where the dropdown was,
    // which overlaps with the AnchorComponent tabs after re-render and triggers them.
    await this.page.keyboard.press("ArrowDown");
    await this.page.keyboard.press("Enter");
    console.log("Selected GCP cloud account option via keyboard");

    await this.page.mouse.move(0, 0);
    await this.page.waitForURL(/cloud-account/, { timeout: 15000 });
    await this.page.waitForLoadState("networkidle");
    console.log("GCP cloud account detail page loaded");
  }
}

import { Page, Locator, test, expect } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";
import { checkIntegrationWithCache } from "../../utils/IntegrationStatusCache";

export class AWSLocators extends CommonLocators {
  // Cloud Account Details - common anchor tabs
  readonly AnchorTabSummary: Locator;
  readonly AnchorTabOptimize: Locator;
  readonly AnchorTabServices: Locator;
  readonly AnchorTabTroubleshoot: Locator;
  readonly AnchorTabMonitoring: Locator;

  // AWS-specific service tabs
  readonly AnchorTabEC2: Locator;
  readonly AnchorTabRDS: Locator;
  readonly AnchorTabS3: Locator;
  readonly AnchorTabECS: Locator;

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

  // EC2 sub-tabs
  readonly EC2Summary: Locator;
  readonly EC2Optimize: Locator;
  readonly EC2Instances: Locator;
  readonly EC2Events: Locator;

  // RDS sub-tabs
  readonly RDSSummary: Locator;
  readonly RDSOptimize: Locator;
  readonly RDSInstances: Locator;
  readonly RDSEvents: Locator;

  // S3 sub-tabs
  readonly S3Summary: Locator;
  readonly S3Optimize: Locator;
  readonly S3Instances: Locator;
  readonly S3Events: Locator;

  // ECS sub-tabs
  readonly ECSSummary: Locator;
  readonly ECSOptimize: Locator;
  readonly ECSInstances: Locator;
  readonly ECSEvents: Locator;

  // Services drilldown tabs (opened when a service row's expand arrow is clicked)
  readonly ServicesRowExpandButton: Locator;
  readonly ServicesDrilldownTabResources: Locator;
  readonly ServicesDrilldownTabCostTrend: Locator;
  readonly ServicesDrilldownTabRecommendations: Locator;

  // Expected URL hash after navigating to each sub-tab
  readonly OptimizeRightSizingUrl: RegExp;
  readonly OptimizeConfigurationUrl: RegExp;
  readonly OptimizeSecurityTabUrl: RegExp;
  readonly OptimizeInfraUpgradeUrl: RegExp;
  readonly OptimizeRecommendationResolutionUrl: RegExp;
  readonly TroubleshootEventsUrl: RegExp;
  readonly TroubleshootTriageRulesUrl: RegExp;
  readonly MonitoringAlertManagerUrl: RegExp;
  readonly MonitoringCloudLogsUrl: RegExp;
  readonly MonitoringCloudMetricsUrl: RegExp;
  readonly EC2SummaryUrl: RegExp;
  readonly EC2OptimizeUrl: RegExp;
  readonly EC2InstancesUrl: RegExp;
  readonly EC2EventsUrl: RegExp;
  readonly RDSSummaryUrl: RegExp;
  readonly RDSOptimizeUrl: RegExp;
  readonly RDSInstancesUrl: RegExp;
  readonly RDSEventsUrl: RegExp;
  readonly S3SummaryUrl: RegExp;
  readonly S3OptimizeUrl: RegExp;
  readonly S3InstancesUrl: RegExp;
  readonly S3EventsUrl: RegExp;
  readonly ECSSummaryUrl: RegExp;
  readonly ECSOptimizeUrl: RegExp;
  readonly ECSInstancesUrl: RegExp;
  readonly ECSEventsUrl: RegExp;

  constructor(page: Page) {
    super(page);

    // Common anchor tabs
    this.AnchorTabSummary = page.locator("#anchor-tab-Summary");
    this.AnchorTabOptimize = page.locator("#anchor-tab-Optimize");
    this.AnchorTabServices = page.locator("#anchor-tab-Services");
    this.AnchorTabTroubleshoot = page.locator("#anchor-tab-Troubleshoot");
    this.AnchorTabMonitoring = page.locator("#anchor-tab-Monitoring");

    // AWS-specific service tabs
    this.AnchorTabEC2 = page.locator("#anchor-tab-EC2");
    this.AnchorTabRDS = page.locator("#anchor-tab-RDS");
    this.AnchorTabS3 = page.locator("#anchor-tab-S3");
    this.AnchorTabECS = page.locator("#anchor-tab-ECS");

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

    // EC2 sub-tabs
    this.EC2Summary = page.locator("#dropdown-summary");
    this.EC2Optimize = page.locator("#dropdown-optimize");
    this.EC2Instances = page.locator("#dropdown-instances");
    this.EC2Events = page.locator("#dropdown-events");

    // RDS sub-tabs
    this.RDSSummary = page.locator("#dropdown-summary");
    this.RDSOptimize = page.locator("#dropdown-optimize");
    this.RDSInstances = page.locator("#dropdown-instances");
    this.RDSEvents = page.locator("#dropdown-events");

    // S3 sub-tabs
    this.S3Summary = page.locator("#dropdown-summary");
    this.S3Optimize = page.locator("#dropdown-optimize");
    this.S3Instances = page.locator("#dropdown-instances");
    this.S3Events = page.locator("#dropdown-events");

    // ECS sub-tabs
    this.ECSSummary = page.locator("#dropdown-summary");
    this.ECSOptimize = page.locator("#dropdown-optimize");
    this.ECSInstances = page.locator("#dropdown-instances");
    this.ECSEvents = page.locator("#dropdown-events");

    // Services drilldown tabs (expand arrow + tab buttons inside the collapsed row)
    this.ServicesRowExpandButton = page.getByRole("button", { name: "Expand row" }).first();
    this.ServicesDrilldownTabResources = page.getByRole("tab", { name: "Resources" });
    this.ServicesDrilldownTabCostTrend = page.getByRole("tab", { name: "Cost Trend" });
    this.ServicesDrilldownTabRecommendations = page.getByRole("tab", { name: "Recommendations" });

    // Expected URL hash after navigating to each sub-tab
    this.OptimizeRightSizingUrl = /#optimize\/right-sizing/;
    this.OptimizeConfigurationUrl = /#optimize\/configuration/;
    this.OptimizeSecurityTabUrl = /#optimize\/security/;
    this.OptimizeInfraUpgradeUrl = /#optimize\/infra-upgrade/;
    this.OptimizeRecommendationResolutionUrl = /#optimize\/recommendation-resolution/;
    this.TroubleshootEventsUrl = /#events\/events/;
    this.TroubleshootTriageRulesUrl = /#events\/triage-rules/;
    this.MonitoringAlertManagerUrl = /#monitoring\/alert-manager/;
    this.MonitoringCloudLogsUrl = /#monitoring\/cloud-logs/;
    this.MonitoringCloudMetricsUrl = /#monitoring\/metrics/;
    this.EC2SummaryUrl = /#ec2\/summary/;
    this.EC2OptimizeUrl = /#ec2\/optimize/;
    this.EC2InstancesUrl = /#ec2\/instances/;
    this.EC2EventsUrl = /#ec2\/events/;
    this.RDSSummaryUrl = /#rds\/summary/;
    this.RDSOptimizeUrl = /#rds\/optimize/;
    this.RDSInstancesUrl = /#rds\/instances/;
    this.RDSEventsUrl = /#rds\/events/;
    this.S3SummaryUrl = /#s3\/summary/;
    this.S3OptimizeUrl = /#s3\/optimize/;
    this.S3InstancesUrl = /#s3\/instances/;
    this.S3EventsUrl = /#s3\/events/;
    this.ECSSummaryUrl = /#ecs\/summary/;
    this.ECSOptimizeUrl = /#ecs\/optimize/;
    this.ECSInstancesUrl = /#ecs\/instances/;
    this.ECSEventsUrl = /#ecs\/events/;
  }

  // Returns the expand arrow for the first row inside the Resources drilldown table.
  // Uses a scoped locator so it never picks up the parent service row's arrow.
  getResourceRowExpandButton() {
    return this.page
      .locator("#service-resource-listing-table")
      .getByRole("button", { name: "Expand row" })
      .first();
  }

  async checkAWSIntegration(): Promise<boolean> {
    console.log("Checking AWS integration status via Admin > Integrations...");

    await this.adminBtn.click();

    const navigated = await this.page.waitForURL(/user-management/, { timeout: 15000 })
      .then(() => true)
      .catch(() => false);

    if (!navigated) {
      console.log("Admin nav click did not navigate — falling back to direct URL");
      await this.page.goto("/user-management");
    }

    await this.page.waitForLoadState("networkidle");

    const integrationsTab = this.page.locator("#anchor-tab-Integrations");
    await integrationsTab.waitFor({ state: "visible", timeout: 20000 });
    await integrationsTab.click();
    await this.page.waitForLoadState("networkidle");

    const awsCard = this.page.locator("#Aws-section-card");
    await awsCard.waitFor({ state: "visible", timeout: 10000 });

    const cardText = await awsCard.innerText().catch(() => "");
    const isActive = /\bActive\s+[1-9]\d*/i.test(cardText);

    console.log(`AWS integration status: ${isActive ? "Active ✅" : "Not Active ❌"}`);
    return isActive;
  }

  async openAWSCloudAccountFromConfig() {
    const isActive = await checkIntegrationWithCache("aws", () => this.checkAWSIntegration());

    if (!isActive) {
      test.skip(true, "AWS integration is not Active — Slack notification sent");
      return;
    }

    const cloudSidenavBtn = this.page.locator("#cloud-sidenavbutton");
    await cloudSidenavBtn.click();
    await this.page.waitForURL(/cloud-account/, { timeout: 15000 });
    await this.page.waitForLoadState("networkidle");
    await this.page.mouse.move(0, 0);
    console.log("Navigated to cloud account via sidenav");

    const awsSearchTerm = process.env.AWS_CLUSTER_NAME || "iteration-aws";
    await this.page.waitForTimeout(500);
    const clusterInput = this.page.locator("#auto-complete-global-cluster");
    await clusterInput.click({ clickCount: 3 });
    await clusterInput.pressSequentially(awsSearchTerm, { delay: 50 });
    console.log(`Typed '${awsSearchTerm}' in global cluster autocomplete`);

    await this.page
      .locator("[role='option']")
      .filter({ hasText: awsSearchTerm })
      .first()
      .waitFor({ state: "visible", timeout: 10000 });

    await this.page.keyboard.press("ArrowDown");
    await this.page.keyboard.press("Enter");
    console.log("Selected AWS cloud account option via keyboard");

    await this.page.mouse.move(0, 0);
    await this.page.waitForURL(/cloud-account/, { timeout: 15000 });
    await this.page.waitForLoadState("networkidle");
    console.log("AWS cloud account detail page loaded");
  }

  async navigateToSubTab(anchorTab: Locator, subTab: Locator, expectedUrl: RegExp) {
    await expect(anchorTab).toBeVisible();

    await expect(async () => {
      await anchorTab.hover();
      await subTab.waitFor({ state: "visible", timeout: 3000 });
      await subTab.click();
      await this.page.waitForURL(expectedUrl, { timeout: 5000 });
    }).toPass({ timeout: 30000, intervals: [500, 1000, 2000] });
  }
}

import { Page, Locator } from "@playwright/test";
import { ClusterDetailsLocators } from "../ClusterDetailsLocators";
import { LoginPage } from "../../../pages/LoginPage";

export class MonitoringTabLocator extends ClusterDetailsLocators {

    // Child sub-tab ids under the "Monitoring" section. For each tab the
    // horizontal strip — GAMMA — uses `#<id>` and the anchor dropdown — BETA —
    // uses `#dropdown-<id>`, so clickTab can fall back from GAMMA to BETA.
    readonly MonitoringDropdownQueryLogs = "query-log";
    readonly MonitoringDropdownLogGroups = "log-groups";
    readonly MonitoringDropdownServicemap = "service-map";
    readonly MonitoringDropdownTraces = "Traces";
    readonly MonitoringDropdownTraceGroup = "trace-grouping";
    readonly MonitoringDropdownCrossZone = "trace-cross-zon";
    readonly MonitoringDropdownQueryMertics = "prom-query";
    readonly MonitoringDropdownSLo = "slo";
    readonly MonitoringDropdownGrafana = "grafana";

    // Kept as Locators: the AlertManager helper drives these directly with its
    // own hover/retry logic.
    readonly MonitoringDropdownAlertManager: Locator;
    readonly MonitoringDropdownAlertSilence: Locator;

    // SLO configuration locators
    // Dialog-scoped to avoid clashing with the namespace filter on the SLO list page.
    readonly AddSLOConfigBtn: Locator;
    readonly SloNamespaceDropdownBtn: Locator;
    readonly SloWorkloadDropdownBtn: Locator;
    readonly SloAvailabilityObjectiveInput: Locator;
    readonly SloLatencyObjectiveInput: Locator;
    readonly SloLatencyThresholdBtn: Locator;
    readonly SloDialogSubmitBtn: Locator;
    readonly SloDialogCancelBtn: Locator;

    readonly RunQueryButton: Locator;

    constructor(page: Page) {
        super(page);

        this.MonitoringDropdownAlertManager = page.locator('#dropdown-alert-manager');
        this.MonitoringDropdownAlertSilence = page.locator('#dropdown-silence-alert-manager');

        this.RunQueryButton = page.getByRole('button', { name: 'Run Query' });

        this.AddSLOConfigBtn = page.locator('#add-slo-config-btn');
        this.SloNamespaceDropdownBtn = page.locator('[role="dialog"] #slo-namespace');
        this.SloWorkloadDropdownBtn = page.locator('[role="dialog"] #slo-workload');
        this.SloAvailabilityObjectiveInput = page.locator('[role="dialog"] #slo-availability-objective');
        this.SloLatencyObjectiveInput = page.locator('[role="dialog"] #slo-latency-objective');
        this.SloLatencyThresholdBtn = page.locator('[role="dialog"] #duration');
        this.SloDialogSubmitBtn = page.locator('[role="dialog"] #slo-submit-btn');
        this.SloDialogCancelBtn = page.locator('[role="dialog"] #slo-cancel-btn');
    }

    // Step 1 — open the "Monitoring" section.
    // Click the ALPHA anchor tab and verify the url hash became #monitoring.
    // Retry the whole click up to 3 times — the redirect must succeed.
    async navigateToMonitoringTab(maxRetries = 3): Promise<void> {
        await this.openClusterFromConfig();
        await this.page.waitForURL(/\/kubernetes\/details\/[^/?#]+/, { timeout: 30000 });

        for (let attempt = 1; attempt <= maxRetries; attempt++) {
            await this.AnchorTabMonitoring.waitFor({ state: "visible", timeout: 15000 });
            await this.AnchorTabMonitoring.click();
            // Move mouse away so the hover-opened dropdown backdrop doesn't intercept clicks.
            await this.page.mouse.move(0, 0);

            await this.page.waitForURL(/#monitoring/, { timeout: 5000 }).catch(() => {});
            if (/#monitoring/.test(this.page.url())) return;

            console.warn(`[MonitoringTabLocator] navigateToMonitoringTab attempt ${attempt}/${maxRetries} failed — URL: ${this.page.url()}`);
            await this.page.waitForTimeout(1500);
        }
        throw new Error(
            `[MonitoringTabLocator] Failed to open Monitoring section after ${maxRetries} attempts. Current URL: ${this.page.url()}`
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

        await this.AnchorTabMonitoring.hover();
        await betaItem.waitFor({ state: "visible", timeout: 5000 });
        await betaItem.click();
        await this.page.mouse.move(0, 0);
    }

    async setupMonitoringPage() {
        const loginPage = new LoginPage(this.page);
        await loginPage.doFullLogin();
        await this.navigateToMonitoringTab();
    }

    async selectSLONamespace(namespace: string) {
        await this.SloNamespaceDropdownBtn.click();
        await this.page.locator('[role="option"]').first().waitFor({ state: 'visible', timeout: 10000 });
        await this.page.locator('[role="option"]').filter({ hasText: namespace }).first().click();
    }

    /** Throws if the workload is not present in the current namespace's list. */
    async selectSLOWorkload(workload: string) {
        await this.SloWorkloadDropdownBtn.click();
        await this.page.locator('[role="option"]').first().waitFor({ state: 'visible', timeout: 15000 });

        const option = this.page.locator('[role="option"]').filter({ hasText: workload });
        if (await option.count() === 0) {
            await this.page.keyboard.press('Escape');
            throw new Error(`Workload "${workload}" not found in the selected namespace`);
        }

        // Set up listener before clicking so the getSLOConfig response isn't missed.
        // Awaited after the click so existing config is loaded before we type form values.
        const configFetched = this.page.waitForResponse(
            r => r.url().includes('api/graphql') && r.request().method() === 'POST',
            { timeout: 10000 }
        ).catch(() => null);

        await option.first().click();
        await configFetched;
    }

    async fillSLOForm(availabilityObjective = 99, latencyObjective = 99, latencyThresholdMs = '5') {
        await this.typeIntoNumberInput(this.SloAvailabilityObjectiveInput, availabilityObjective);
        await this.typeIntoNumberInput(this.SloLatencyObjectiveInput, latencyObjective);

        await this.SloLatencyThresholdBtn.click();
        await this.page.locator('[role="option"]').first().waitFor({ state: 'visible', timeout: 10000 });
        await this.page.locator('[role="option"]').filter({ hasText: latencyThresholdMs }).first().click();
    }

    async closeSLODialog() {
        await this.SloDialogCancelBtn.click();
    }

    // Triple-click to select all existing content, then type the new value character
    // by character — required to reliably trigger React's onChange on number inputs.
    private async typeIntoNumberInput(input: Locator, value: number) {
        await input.click({ clickCount: 3 });
        await input.pressSequentially(String(value), { delay: 10 });
    }
}

import { Page, Locator } from "@playwright/test";
import { ClusterDetailsLocators } from "../ClusterDetailsLocators";
import { LoginPage } from "../../../pages/LoginPage";

export class MonitoringTabLocator extends ClusterDetailsLocators {

    // Monitoring dropdown id's
    readonly OptimizedropdownSummary: Locator;
    readonly MonitoringDropdownQueryLogs: Locator;
    readonly MonitoringDropdownLogGroups: Locator;
    readonly MonitoringDropdownServicemap: Locator;
    readonly MonitoringDropdownTraces: Locator;
    readonly MonitoringDropdownTraceGroup: Locator;
    readonly MonitoringDropdownCrossZone: Locator;
    readonly MonitoringDropdownQueryMertics: Locator;
    readonly MonitoringDropdownAlertManager: Locator;
    readonly MonitoringDropdownAlertSilence: Locator;
    readonly MonitoringDropdownSLo: Locator;
    readonly MonitoringDropdownGrafana: Locator;

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

        // Monitoring dropdown id's
        this.OptimizedropdownSummary = page.locator('#dropdown-summary');
        this.MonitoringDropdownQueryLogs = page.locator('#dropdown-query-log');
        this.MonitoringDropdownLogGroups = page.locator('#dropdown-log-groups');
        this.MonitoringDropdownServicemap = page.locator('#dropdown-service-map');
        this.MonitoringDropdownTraces = page.locator('#dropdown-Traces');
        this.MonitoringDropdownTraceGroup = page.locator('#dropdown-trace-grouping');
        this.MonitoringDropdownCrossZone = page.locator('#dropdown-trace-cross-zon');
        this.MonitoringDropdownQueryMertics = page.locator('#dropdown-prom-query');
        this.MonitoringDropdownAlertManager = page.locator('#dropdown-alert-manager');
        this.MonitoringDropdownAlertSilence = page.locator('#dropdown-silence-alert-manager');
        this.MonitoringDropdownSLo = page.locator('#dropdown-slo');
        this.MonitoringDropdownGrafana = page.locator('#dropdown-grafana');

        this.RunQueryButton = page.getByRole('button', { name: 'Run Query' });

        this.AddSLOConfigBtn = page.locator('#add-slo-config-btn');
        this.SloNamespaceDropdownBtn = page.locator('[role="dialog"] #auto-complete-namespace');
        this.SloWorkloadDropdownBtn = page.locator('[role="dialog"] #auto-complete-workload');
        this.SloAvailabilityObjectiveInput = page.locator('[role="dialog"] #slo-availability-objective');
        this.SloLatencyObjectiveInput = page.locator('[role="dialog"] #slo-latency-objective');
        this.SloLatencyThresholdBtn = page.locator('[role="dialog"] #auto-complete-duration');
        this.SloDialogSubmitBtn = page.locator('[role="dialog"] #submit');
        this.SloDialogCancelBtn = page.locator('[role="dialog"] #cancel');
    }

    async navigateToMonitoringTab() {
        await this.openClusterFromConfig();
        await this.page.waitForURL(/\/kubernetes\/details\/.*#summary/);
        await this.AnchorTabMonitoring.waitFor({ state: "visible" });
        await this.AnchorTabMonitoring.click();
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

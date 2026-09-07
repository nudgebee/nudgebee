import { Page, Locator, expect } from "@playwright/test";
import { ClusterDetailsLocators } from "../ClusterDetailsLocators";
import { LoginPage } from "../../../pages/LoginPage";

// KubernetesLogs.tsx: id={k8sLogs} = 'k8sLogs'. CustomTable puts this on the
// <table> and `${id}-body` on the <tbody>.
export const K8S_LOGS_TABLE = "k8sLogs";

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

    // Query Logs builder. The field carries no id, but its placeholder names the
    // step it is on (LogQueryBuilderAutocomplete.getPlaceholder), and the block is
    // active from mount, so one of the three "Type ..." forms is always the one on
    // screen. Suggestions are portalled into a Popper, not a Modal, so nothing
    // behind them is aria-hidden.
    readonly LogQueryBuilderInput: Locator;
    readonly LogQuerySuggestions: Locator;
    readonly LogQueryAddOperationBtn: Locator;

    // Query Logs toolbar. KubernetesLogs, QueryModeSwitcher, ds/ToggleGroup and
    // ds/DropdownMenu render zero data-testid between them, so an id primary and a
    // role primary are the highest rungs available on this screen.
    readonly QueryLogsRoot: Locator;
    readonly LogProviderBadge: Locator;
    readonly LogProviderSwitcher: Locator;
    readonly LogQueryFilterWarning: Locator;
    readonly ExecutedQueryLabel: Locator;
    readonly QueryCodeEditor: Locator;
    readonly LogLimitDropdown: Locator;
    readonly AutoRefreshDropdown: Locator;
    readonly DownloadLogsBtn: Locator;
    readonly LogFilterRequiredToast: Locator;

    // Query Logs results table. CustomTable puts the id on the <table> and
    // `${id}-body` on the <tbody> (KubernetesLogs.tsx: id={k8sLogs} = 'k8sLogs'),
    // and — because showExpandable is true — emits a second <tr> per data row to
    // hold the collapsed drill-down (same shape as PodDetailsLocators' DATA_ROW).
    // Requiring a second cell is what keeps the drill-down row from doubling the count.
    readonly LogResultRows: Locator;
    readonly LogResultsNoData: Locator;

    // CustomDateTimeRangePicker's trigger carries no id/testid (confirmed: no
    // fallback exists for it anywhere in the component). The proven workaround
    // already used elsewhere in this suite (userFeedbackLocators.dateRangeTrigger)
    // is matching its displayed text shape instead — always "Last ...", "Current
    // ...", or a formatted "Mon DD - Mon DD" range.
    readonly LogDateRangeTrigger: Locator;

    constructor(page: Page) {
        super(page);

        this.MonitoringDropdownAlertManager = page.locator('#dropdown-alert-manager');
        this.MonitoringDropdownAlertSilence = page.locator('#dropdown-silence-alert-manager');

        this.RunQueryButton = page.getByRole('button', { name: 'Run Query' });

        this.LogQueryBuilderInput = page.getByPlaceholder(/^Type (label name|operator for|value for)/).first();
        this.LogQuerySuggestions = page.locator('.MuiPopper-root .MuiListItemButton-root');
        // Disabled by LogQueryBuilderAutocomplete exactly while `queryItems.length === 0`.
        // Unlike the "Select at least 1 label filter" hint, this button is in the DOM in
        // BOTH states - the hint is simply absent while the builder is still mounting, so
        // asking whether it is hidden answers "yes" for a builder that holds no chips at all.
        this.LogQueryAddOperationBtn = page.getByRole('button', { name: '+ Add Operation' });

        // ListingLayout puts this id on its root, so it scopes every toolbar lookup below
        // away from the Nubi panel and the sidenav, which carry their own radios.
        this.QueryLogsRoot = page.locator('#query-logs');
        // Two levels up, not one: shared/format/Text wraps its Typography in a Box, so the
        // caption's own parent holds the caption alone. The badge that carries both the
        // caption and the provider name is that wrapper's parent.
        this.LogProviderBadge = this.QueryLogsRoot.getByText('Log Provider:', { exact: true }).locator('xpath=../..');
        // Rendered ONLY when the account has more than one configured log provider -
        // KubernetesLogs falls back to a static badge with no id otherwise, so absence
        // means "cannot switch", not "not loaded yet".
        this.LogProviderSwitcher = page.locator('#log-provider-switcher');
        this.LogQueryFilterWarning = this.QueryLogsRoot.getByText('Select at least 1 label filter (label and value)');
        // Present only once a query has actually run - KubernetesLogs sets executedQuery
        // from the logs response, and clears it on reset. That makes it a post-run signal,
        // never a preview of what the builder is about to send.
        this.ExecutedQueryLabel = this.QueryLogsRoot.getByText(/^\S+ query:$/).first();
        this.QueryCodeEditor = this.QueryLogsRoot.locator('.cm-content').first();
        // By accessible name, not the id KubernetesLogs passes: ds/FilterDropdown never puts
        // that id on its root - it kebab-cases it onto the inner input as
        // `auto-complete-log-limit`. The trigger reads "Limit <n>", so the name carries the
        // current value and only its prefix is stable.
        this.LogLimitDropdown = this.QueryLogsRoot.getByRole('button', { name: /^Limit\b/ });
        this.AutoRefreshDropdown = this.QueryLogsRoot.getByRole('button', { name: 'Auto Refresh interval' });
        this.DownloadLogsBtn = this.QueryLogsRoot.getByRole('button', { name: 'Download' });
        // KubernetesLogs.handleSubmit raises this instead of building a request when the
        // Builder holds no chips, so it is the visible half of "Run Query did nothing".
        this.LogFilterRequiredToast = page.getByText('Please select at least one label filter', { exact: true });

        this.LogResultRows = page.locator(`#${K8S_LOGS_TABLE}-body tr:has(td:nth-child(2))`);
        this.LogResultsNoData = page.locator(`#${K8S_LOGS_TABLE}-no-data`);

        this.LogDateRangeTrigger = this.QueryLogsRoot
            .locator("button")
            .filter({ hasText: /^(Last\s|Current\s|\w{3}\s\d{1,2}\s-\s\w{3}\s\d{1,2})/ })
            .first();

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

    // The provider name the toolbar currently shows. The badge is two spans in one box,
    // so its text carries the "Log Provider:" caption as well.
    async currentLogProvider(): Promise<string> {
        const text = await this.LogProviderBadge.innerText();
        return text.replace(/Log Provider:/i, "").replace(/\s+/g, " ").trim();
    }

    // One option of the provider dropdown, keyed by the raw provider name rather than
    // the title-cased label the item displays.
    logProviderOption(provider: string): Locator {
        return this.page.locator(`#log-provider-option-${provider}`);
    }

    // Selects a log provider, reporting whether the account offers it at all.
    //
    // True when already on it: switching is not free. QueryModeSwitcher is keyed on
    // logProvider, so changing it remounts the builder and discards any chips, and its
    // default-mode effect re-picks Builder or Code for the new provider.
    async selectLogProvider(provider: string): Promise<boolean> {
        await this.LogProviderBadge.waitFor({ state: "visible", timeout: 30000 });
        if ((await this.currentLogProvider()).toLowerCase() === provider.toLowerCase()) return true;

        // A single-provider account renders a static badge with no id in place of the
        // trigger, so absence here means "cannot switch", not "not mounted yet".
        if (!(await this.LogProviderSwitcher.isVisible().catch(() => false))) return false;

        await this.LogProviderSwitcher.click();
        const option = this.logProviderOption(provider);
        // A probe: an account that configured other providers but not this one is a normal
        // state the caller skips on, so absence must come back as false rather than throw.
        const offered = await option
            .waitFor({ state: "visible", timeout: 5000 })
            .then(() => true)
            .catch(() => false);
        if (!offered) {
            // Leave no open menu behind - its backdrop would swallow the caller's next click.
            await this.page.keyboard.press("Escape");
            return false;
        }

        await option.click();
        await expect.poll(() => this.currentLogProvider(), { timeout: 20000 }).toMatch(new RegExp(provider, "i"));
        return true;
    }

    // One option of the Builder / Code / AI switcher. ds/ToggleGroup in single-selection
    // mode renders each option as <button role="radio"> carrying its visible label.
    queryModeOption(label: string): Locator {
        return this.QueryLogsRoot.getByRole("radio", { name: label, exact: true });
    }

    // Switches editor mode, skipping the click when that mode is already the selected one.
    async switchQueryMode(label: string): Promise<void> {
        const option = this.queryModeOption(label);
        await option.waitFor({ state: "visible", timeout: 20000 });
        if ((await option.getAttribute("aria-checked")) === "true") return;
        await option.click();
        await expect(option).toHaveAttribute("aria-checked", "true", { timeout: 10000 });
    }

    // Adds the first label filter the account's log provider offers, and reports
    // whether it managed to.
    //
    // Run Query does nothing at all without one. For signoz, loggly and loki the
    // Query Logs tab opens in Builder mode (QueryModeSwitcher picks the default
    // from the provider), and KubernetesLogs.handleSubmit returns before it builds
    // any request when logQueryItems is empty - it only raises a "Please select at
    // least one label filter" warning. So a click on Run Query with a bare builder
    // issues no GraphQL, which is not a state worth asserting an API result on.
    //
    // The builder is a three-step autocomplete: label, then operator, then value.
    async addFirstLogLabelFilter(): Promise<boolean> {
        const input = this.LogQueryBuilderInput;
        // Code mode - the default for every provider but signoz/loggly/loki - needs no
        // filter: handleSubmit falls straight through to the fetch.
        const builderShown = await input
            .waitFor({ state: "visible", timeout: 20000 })
            .then(() => true)
            .catch(() => false);
        if (!builderShown) return true;

        // Let the tab's own GetDefaultProvider / FetchLogLabels land first. The builder
        // throws its chips away when the resolved provider or account changes
        // (QueryModeSwitcher is keyed on logProvider and its resetStates runs on
        // accountId), so a filter added before that settles is silently wiped and Run
        // Query goes back to issuing nothing.
        await this.page.waitForLoadState("networkidle").catch(() => {});
        await this.LogQueryAddOperationBtn.waitFor({ state: "visible", timeout: 20000 }).catch(() => {});

        await input.click();

        // Suggestions are CLICKED, not driven from the keyboard. ArrowDown only moves a
        // highlight, and handleKeyDown's Enter branch returns immediately while the input
        // is empty - so an ArrowDown+Enter walk selects nothing and leaves the builder
        // bare. Each click advances one step: label, then operator, then value. A
        // value-less operator (exists / is null) commits the chip at step two, which the
        // cleared input reports.
        for (let step = 0; step < 3; step++) {
            const first = this.LogQuerySuggestions.first();
            const visible = await first
                .waitFor({ state: "visible", timeout: 15000 })
                .then(() => true)
                .catch(() => false);
            if (!visible) break;
            await first.click();
            if ((await input.inputValue()) === "") break;
        }

        // Positive check, not an absence: Add Operation is disabled exactly while the
        // block holds no chips, so an enabled one is proof a filter really landed.
        return await this.LogQueryAddOperationBtn.isEnabled().catch(() => false);
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

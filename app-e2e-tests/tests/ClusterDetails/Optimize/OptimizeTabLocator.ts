import { Page, Locator } from "@playwright/test";
import { ClusterDetailsLocators } from "../ClusterDetailsLocators";

export const OptimizeSections = {
    summary:                  'summary',
    rightSizing:              'right-sizing',
    autoScaler:               'auto-scaler',
    unusedVolume:             'unused-volume',
    bestPractices:            'best-practices',
    abandonedApps:            'abandoned-resources',
    pvcRightsizing:           'pv-rightsizing',
    replicaRightsizing:       'replica-rightsizing',
    spotRecommendation:       'spot-recommendation',
    recommendationResolution: 'recommendation-resolution',
} as const;

export type OptimizeSection = typeof OptimizeSections[keyof typeof OptimizeSections];


export class OptimizeTabLocator extends ClusterDetailsLocators {
    //Optimize dropdown id's
    readonly OptimizedropdownSummary: Locator;
    readonly OptimizedropdownRightSizeButton: Locator;
    readonly OptimizedropdownAutoScaler: Locator;
    readonly OptimizedropdownUnUsedVolume: Locator;
    readonly OptimizedropdownBestPractices: Locator;
    readonly OptimizedropdownAbandonedResources: Locator;
    readonly OptimizedropdownPvRightsizing: Locator;
    readonly OptimizedropdownReplicaRightsizing: Locator;
    readonly OptimizedropdownSpotRecommendation: Locator;
    readonly OptimizedropdownRecommendationResolution: Locator;
    override readonly namespacedropdown: Locator;

    // Monitoring id's
    readonly MonitoringDropdownQueryLogs: Locator;
    readonly RunQueryButton: Locator;

    readonly RightSizingTab: Locator;
    readonly OptimizeTabDropdown: Locator;
    readonly OptimizeTabSummary: Locator;
    readonly DownlaodBtn: Locator;
    readonly DownloadCSVBtn: Locator;
    readonly DownloadCSVSuccessMaggage: Locator;
    readonly DownloadExcelBtn: Locator;
    readonly DownloadExcelSuccessMaggage: Locator;
    readonly AutoScalerTab: Locator;
    readonly Summary: Locator;
    readonly Logs: Locator;

    constructor(page: Page) {
        super(page);
        this.namespacedropdown = page.locator("#auto-complete-rs-filter-namespace");
        //Optimize dropdown Id's
        this.OptimizedropdownSummary = page.locator('#dropdown-summary')
        this.OptimizedropdownRightSizeButton = page.locator('#dropdown-right-sizing');
        this.OptimizedropdownAutoScaler = page.locator('#dropdown-auto-scaler')
        this.OptimizedropdownUnUsedVolume = page.locator('#dropdown-unused-volume')
        this.OptimizedropdownBestPractices = page.locator('#dropdown-best-practices')
        this.OptimizedropdownAbandonedResources = page.locator('#dropdown-abandoned-resources')
        this.OptimizedropdownPvRightsizing = page.locator('#dropdown-pv-rightsizing')
        this.OptimizedropdownReplicaRightsizing = page.locator('#dropdown-replica-rightsizing')
        this.OptimizedropdownSpotRecommendation = page.locator('#dropdown-spot-recommendation')
        this.OptimizedropdownRecommendationResolution = page.locator('#dropdown-recommendation-resolution-status')

        //Monitoring id's
        this.MonitoringDropdownQueryLogs = page.locator('#dropdown-query-log')
        this.RunQueryButton = page.getByRole('button', { name: 'Run Query' })

        this.RightSizingTab = page.getByRole('tab', { name: 'Right Sizing' });
        this.OptimizeTabDropdown = page.locator('div').filter({ hasText: 'SummaryRight SizingAuto' }).nth(1)
        this.OptimizeTabSummary = page.locator("#summary");
        this.DownlaodBtn = page.getByRole('button', { name: 'Download' });
        this.DownloadCSVBtn = page.getByText('Download CSV', { exact: true })
        this.DownloadExcelBtn = page.getByText('Download Excel (XLSX)', { exact: true })
        this.DownloadCSVSuccessMaggage = page.getByText('Export downloaded successfully');
        this.DownloadExcelSuccessMaggage = page.getByText('Export downloaded successfully');
        this.AutoScalerTab = page.locator("#auto-scaler");
        this.Summary = page.locator('button').filter({ hasText: /^Summary$/ })
        this.Logs = page.locator('button').filter({ hasText: /^Logs$/ })
    }

    async navigateToClusterDetails(): Promise<void> {
        await this.page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 30000 }).catch(() => {});
        await this.page.waitForTimeout(1500);
        await this.openClusterFromConfig();
        await this.page.waitForURL('**/kubernetes/details/**', { timeout: 30000 }).catch(() => {});
        await this.page.waitForLoadState('domcontentloaded', { timeout: 15000 }).catch(() => {});
        await this.page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 30000 }).catch(() => {});
        await this.page.waitForTimeout(3000);
    }

    async gotoOptimizeSection(section: OptimizeSection, maxRetries = 3): Promise<void> {
        const sectionDropdownId: Record<OptimizeSection, string> = {
            'summary':                    '#dropdown-summary',
            'right-sizing':               '#dropdown-right-sizing',
            'auto-scaler':                '#dropdown-auto-scaler',
            'unused-volume':              '#dropdown-unused-volume',
            'best-practices':             '#dropdown-best-practices',
            'abandoned-resources':        '#dropdown-abandoned-resources',
            'pv-rightsizing':             '#dropdown-pv-rightsizing',
            'replica-rightsizing':        '#dropdown-replica-rightsizing',
            'spot-recommendation':        '#dropdown-spot-recommendation',
            'recommendation-resolution':  '#dropdown-recommendation-resolution-status',
        };

        for (let attempt = 1; attempt <= maxRetries; attempt++) {
            await this.OptimizeTab.waitFor({ state: 'visible', timeout: 15000 });
            await this.OptimizeTab.hover();

            const dropdownItem = this.page.locator(sectionDropdownId[section]);
            await dropdownItem.waitFor({ state: 'visible', timeout: 10000 });
            await dropdownItem.click();

            await this.page.waitForURL(`**#optimize/${section}`, { timeout: 15000 }).catch(() => {});
            await this.page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 30000 }).catch(() => {});

            const currentUrl = this.page.url();
            if (currentUrl.includes(`#optimize/${section}`)) return;

            console.warn(`[OptimizeTabLocator] gotoOptimizeSection attempt ${attempt}/${maxRetries} failed — URL: ${currentUrl}`);
            await this.page.waitForTimeout(1500);
        }
        throw new Error(`[OptimizeTabLocator] Failed to navigate to #optimize/${section} after ${maxRetries} attempts. Current URL: ${this.page.url()}`);
    }
}

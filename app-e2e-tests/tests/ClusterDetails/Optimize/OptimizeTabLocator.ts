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

    // Open the Optimize section, then select a child sub-tab. Mirrors the other
    // ClusterDetails section locators: ALPHA anchor (retry + verify) then a
    // GAMMA-strip / BETA-dropdown child click.
    async gotoOptimizeSection(section: OptimizeSection, maxRetries = 3): Promise<void> {
        await this.navigateToOptimizeTab(maxRetries);
        // Most section fragments equal their tab dom id; recommendation-resolution
        // is the exception — its dom id carries a "-status" suffix.
        const elementId = section === OptimizeSections.recommendationResolution
            ? 'recommendation-resolution-status'
            : section;
        await this.clickTab(elementId);
    }

    // Step 1 — open the "Optimize" section.
    // Click the ALPHA anchor tab and verify the url hash became #optimize.
    // Retry the whole click up to 3 times — the redirect must succeed.
    async navigateToOptimizeTab(maxRetries = 3): Promise<void> {
        await this.page.waitForURL(/\/kubernetes\/details\/[^/?#]+/, { timeout: 30000 }).catch(() => {});

        for (let attempt = 1; attempt <= maxRetries; attempt++) {
            await this.OptimizeTab.waitFor({ state: 'visible', timeout: 15000 });
            await this.OptimizeTab.click();
            // Move mouse away so the hover-opened dropdown backdrop doesn't intercept clicks.
            await this.page.mouse.move(0, 0);

            await this.page.waitForURL(/#optimize/, { timeout: 5000 }).catch(() => {});
            if (/#optimize/.test(this.page.url())) return;

            console.warn(`[OptimizeTabLocator] navigateToOptimizeTab attempt ${attempt}/${maxRetries} failed — URL: ${this.page.url()}`);
            await this.page.waitForTimeout(1500);
        }
        throw new Error(`[OptimizeTabLocator] Failed to open Optimize section after ${maxRetries} attempts. Current URL: ${this.page.url()}`);
    }

    // Step 2 — click a child sub-tab by id.
    // Plan A: click the GAMMA tab in the horizontal strip (id `<id>`).
    // Plan B (fallback): if GAMMA isn't visible, hover ALPHA to open the BETA
    // dropdown and click its item (id `dropdown-<id>`).
    async clickTab(sectionId: string): Promise<void> {
        const gammaTab = this.page.locator(`[id="${sectionId}"]`);
        const betaItem = this.page.locator(`[id="dropdown-${sectionId}"]`);

        try {
            await gammaTab.waitFor({ state: 'visible', timeout: 5000 });
            await gammaTab.click();
            await this.page.mouse.move(0, 0);
            return;
        } catch {
            // Fall back to the BETA dropdown below.
        }

        await this.OptimizeTab.hover();
        await betaItem.waitFor({ state: 'visible', timeout: 5000 });
        await betaItem.click();
        await this.page.mouse.move(0, 0);
    }
}

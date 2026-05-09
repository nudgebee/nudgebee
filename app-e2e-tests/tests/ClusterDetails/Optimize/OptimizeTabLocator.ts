import { Page, Locator } from "@playwright/test";
import { ClusterDetailsLocators } from "../ClusterDetailsLocators";

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
        this.DownlaodBtn = page.locator("#buttonmenu-button");
        this.DownloadCSVBtn = page.getByRole('menuitem', { name: 'Download CSV' })
        this.DownloadExcelBtn = page.getByRole('menuitem', { name: 'Download Excel (XLSX)' })
        this.DownloadCSVSuccessMaggage = page.getByText('Export downloaded successfully');
        this.DownloadExcelSuccessMaggage = page.getByText('Export downloaded successfully');
        this.AutoScalerTab = page.locator("#auto-scaler");
        this.Summary = page.getByRole('radiogroup').getByText('Summary')
        this.Logs = page.getByRole('radiogroup').getByText('Logs')
    }
}

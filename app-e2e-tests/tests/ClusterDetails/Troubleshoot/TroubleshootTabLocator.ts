import { Page, Locator } from "@playwright/test";
import { ClusterDetailsLocators } from "../ClusterDetailsLocators";

export class TroubleshootTabLocator extends ClusterDetailsLocators {
    // Troubleshoot Dropdown ID's
    readonly TroubleshootdropdownSummary: Locator;


    // Troubleshoot child tab Id's
    readonly TroubleshootTriageInbox: Locator;
    readonly TroubleubleshootEventsByType: Locator;
    readonly TroubleshootPodErrors: Locator;
    readonly TroubleshootNodeErrors: Locator;
    readonly TroubleshootApplicationErrors: Locator;
    readonly TroubleshootAllEvents: Locator;
    readonly TroubleshootAnomaly: Locator;
    readonly TroubleshootTriageRules: Locator;

    constructor(page: Page) {
        super(page);
        // Troubleshoot Dropdown ID's
        this.TroubleshootdropdownSummary = page.locator('#dropdown-summary')

         // Troubleshoot child tab Id's
        this.TroubleshootTriageInbox = page.locator('#fingerprint')
        this.TroubleubleshootEventsByType = page.locator('#grouped_events')
        this.TroubleshootPodErrors = page.locator('#pod_error')
        this.TroubleshootNodeErrors = page.locator('#node_errors')
        this.TroubleshootApplicationErrors = page.locator('#app_errors')
        this.TroubleshootAllEvents = page.locator('#all_events')
        this.TroubleshootAnomaly = page.locator('#anomaly')
        this.TroubleshootTriageRules = page.locator('#triage-rules')
    }
}



import { Page } from "@playwright/test";
import { ClusterDetailsLocators } from "../ClusterDetailsLocators";

export class TroubleshootTabLocator extends ClusterDetailsLocators {
    // Child sub-tab ids under the "Troubleshoot" section. For every tab the
    // horizontal strip — GAMMA — uses `#<id>` and the anchor dropdown — BETA —
    // uses `#dropdown-<id>`, so clickTab can fall back from GAMMA to BETA.
    readonly TroubleshootdropdownSummary = "summary";
    readonly TroubleshootTriageInbox = "fingerprint";
    readonly TroubleubleshootEventsByType = "grouped_events";
    readonly TroubleshootPodErrors = "pod_error";
    readonly TroubleshootNodeErrors = "node_errors";
    readonly TroubleshootApplicationErrors = "app_errors";
    readonly TroubleshootAllEvents = "all_events";
    readonly TroubleshootAnomaly = "anomaly";
    readonly TroubleshootTriageRules = "triage-rules";
    readonly TroubleshootServiceCriticality = "service-criticality";

    constructor(page: Page) {
        super(page);
    }

    // Step 1 — open the "Troubleshoot" section.
    // Click the ALPHA anchor tab and verify the url hash became #events.
    // Retry the whole click up to 3 times — the redirect must succeed.
    async navigateToTroubleshootTab(maxRetries = 3): Promise<void> {
        await this.page.waitForURL(/\/kubernetes\/details\/[^/?#]+/, { timeout: 30000 });

        for (let attempt = 1; attempt <= maxRetries; attempt++) {
            await this.AnchorTabTroubleshoot.waitFor({ state: "visible", timeout: 15000 });
            await this.AnchorTabTroubleshoot.click();
            // Move mouse away so the hover-opened dropdown backdrop doesn't intercept clicks.
            await this.page.mouse.move(0, 0);

            await this.page.waitForURL(/#events/, { timeout: 5000 }).catch(() => {});
            if (/#events/.test(this.page.url())) return;

            console.warn(`[TroubleshootTabLocator] navigateToTroubleshootTab attempt ${attempt}/${maxRetries} failed — URL: ${this.page.url()}`);
            await this.page.waitForTimeout(1500);
        }
        throw new Error(
            `[TroubleshootTabLocator] Failed to open Troubleshoot section after ${maxRetries} attempts. Current URL: ${this.page.url()}`
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

        await this.AnchorTabTroubleshoot.hover();
        await betaItem.waitFor({ state: "visible", timeout: 5000 });
        await betaItem.click();
        await this.page.mouse.move(0, 0);
    }
}

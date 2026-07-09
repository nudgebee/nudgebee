import { Page } from "@playwright/test";
import { ClusterDetailsLocators } from "../ClusterDetailsLocators";

export class SecurityAndToolsTabLocator extends ClusterDetailsLocators {

    // Child sub-tab ids under the "Security & Tools" section. For each tab the
    // horizontal strip — GAMMA — uses id `<id>` and the anchor dropdown — BETA —
    // uses id `dropdown-<id>`, so clickTab can fall back from GAMMA to BETA.
    // (One id — "Upgrade Planner" — contains a space, so clickTab uses [id="..."].)
    readonly ImageScanDropdown = "image-scan";
    readonly CISScanDropdown = "cis-scan";
    readonly SensitiveLogsDropdown = "sensitive-log";
    readonly ClusterUpgradeDropdown = "cluster-upgrade";
    readonly UpgradePlannerDropdown = "Upgrade Planner";
    readonly CertificateIssuesDropdown = "ssl-certificate-issues";
    readonly HelmUpgradeDropdown = "helm-upgrade";

    constructor(page: Page) {
        super(page);
    }

    // Step 1 — open the "Security & Tools" section.
    // Click the ALPHA anchor tab and verify the url hash became #security.
    // Retry the whole click up to 3 times — the redirect must succeed.
    async navigateToSecurityAndToolsTab(maxRetries = 3): Promise<void> {
        await this.openClusterFromConfig();
        await this.page.waitForURL(/\/kubernetes\/details\/[^/?#]+/, { timeout: 30000 });

        for (let attempt = 1; attempt <= maxRetries; attempt++) {
            await this.AnchorTabSecurityAndTools.waitFor({ state: "visible", timeout: 15000 });
            await this.AnchorTabSecurityAndTools.click();
            // Move mouse away so the hover-opened dropdown backdrop doesn't intercept clicks.
            await this.page.mouse.move(0, 0);

            await this.page.waitForURL(/#security/, { timeout: 5000 }).catch(() => {});
            if (/#security/.test(this.page.url())) return;

            console.warn(`[SecurityAndToolsTabLocator] navigateToSecurityAndToolsTab attempt ${attempt}/${maxRetries} failed — URL: ${this.page.url()}`);
            await this.page.waitForTimeout(1500);
        }
        throw new Error(
            `[SecurityAndToolsTabLocator] Failed to open Security & Tools section after ${maxRetries} attempts. Current URL: ${this.page.url()}`
        );
    }

    // Step 2 — click a child sub-tab by id.
    // Plan A: click the GAMMA tab in the horizontal strip (id `<id>`).
    // Plan B (fallback): if GAMMA isn't visible, hover ALPHA to open the BETA
    // dropdown and click its item (id `dropdown-<id>`).
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

        await this.AnchorTabSecurityAndTools.hover();
        await betaItem.waitFor({ state: "visible", timeout: 5000 });
        await betaItem.click();
        await this.page.mouse.move(0, 0);
    }
}

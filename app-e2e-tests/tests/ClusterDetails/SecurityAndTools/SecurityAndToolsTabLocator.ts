import { Page, Locator } from "@playwright/test";
import { ClusterDetailsLocators } from "../ClusterDetailsLocators";

export class SecurityAndToolsTabLocator extends ClusterDetailsLocators {

    // SecurityAndTools dropdown id's
    readonly ImageScanDropdown: Locator;
    readonly CISScanDropdown: Locator;
    readonly SensitiveLogsDropdown: Locator;
    readonly ClusterUpgradeDropdown: Locator;
    readonly UpgradePlannerDropdown: Locator;
    readonly CertificateIssuesDropdown: Locator;
    readonly HelmUpgradeDropdown: Locator;

    constructor(page: Page) {
        super(page);

        // SecurityAndTools dropdown id's
        this.ImageScanDropdown = page.locator('#dropdown-image-scan');
        this.CISScanDropdown = page.locator('#dropdown-cis-scan');
        this.SensitiveLogsDropdown = page.locator('#dropdown-sensitive-log');
        this.ClusterUpgradeDropdown = page.locator('#dropdown-cluster-upgrade');
        this.UpgradePlannerDropdown = page.locator('[id="dropdown-Upgrade Planner"]');
        this.CertificateIssuesDropdown = page.locator('#dropdown-ssl-certificate-issues');
        this.HelmUpgradeDropdown = page.locator('#dropdown-helm-upgrade');
    }

    async navigateToSecurityAndToolsTab() {
        await this.openClusterFromConfig();
        await this.page.waitForURL(/\/kubernetes\/details\/.*#summary/);
        await this.AnchorTabSecurityAndTools.waitFor({ state: "visible" });
        await this.AnchorTabSecurityAndTools.click();
    }
}

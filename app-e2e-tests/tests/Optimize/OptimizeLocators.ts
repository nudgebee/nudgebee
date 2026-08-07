import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";

// Global Optimize page (/optimise) — sub-tabs rendered by AnchorComponent as `#anchor-tab-<id>`.
export const OptimizeTabs = {
    summary: "summary",
    recommendations: "recommendations",
    resolutions: "resolutions",
    autoOptimize: "auto-optimize",
} as const;

export type OptimizeTab = typeof OptimizeTabs[keyof typeof OptimizeTabs];

export class OptimizeLocators extends CommonLocators {
    // Anchor-strip tab locators.
    readonly SummaryTab: Locator;
    readonly RecommendationsTab: Locator;
    readonly ResolutionsTab: Locator;
    readonly AutoOptimizeTab: Locator;

    constructor(page: Page) {
        super(page);
        this.SummaryTab = page.locator("#anchor-tab-summary");
        this.RecommendationsTab = page.locator("#anchor-tab-recommendations");
        this.ResolutionsTab = page.locator("#anchor-tab-resolutions");
        this.AutoOptimizeTab = page.locator("#anchor-tab-auto-optimize");
    }

    // Resolve a tab id to its named Locator.
    private tabLocator(tab: OptimizeTab): Locator {
        switch (tab) {
            case OptimizeTabs.summary:
                return this.SummaryTab;
            case OptimizeTabs.recommendations:
                return this.RecommendationsTab;
            case OptimizeTabs.resolutions:
                return this.ResolutionsTab;
            case OptimizeTabs.autoOptimize:
                return this.AutoOptimizeTab;
        }
    }

    private optimiseUrl(fragment?: string): string {
        const base = (process.env.BASE_URL || "").replace(/\/$/, "");
        return `${base}/optimise${fragment ? `#${fragment}` : ""}`;
    }

    // Open the Optimize page — Plan A: retry the sidebar button; Plan B: direct goto /optimise.
    async navigateToOptimize(maxRetries = 3): Promise<void> {
        await this.page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 30000 }).catch(() => {});

        for (let attempt = 1; attempt <= maxRetries; attempt++) {
            // Click + URL-wait in one try so a failed click retries immediately instead of eating the 8s timeout.
            try {
                await this.OptimizeBtn.waitFor({ state: "visible", timeout: 15000 });
                await this.OptimizeBtn.click();
                await this.page.mouse.move(0, 0);
                await this.page.waitForURL(/\/optimise/, { timeout: 8000 });
            } catch {
                // Click or navigation failed — fall through to the retry.
            }

            if (/\/optimise/.test(this.page.url())) {
                await this.page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 30000 }).catch(() => {});
                return;
            }

            console.warn(`[OptimizeLocators] navigateToOptimize attempt ${attempt}/${maxRetries} via sidebar failed — URL: ${this.page.url()}`);
            await this.page.waitForTimeout(1500);
        }

        console.warn("[OptimizeLocators] Sidebar navigation exhausted — falling back to direct goto /optimise");
        await this.page.goto(this.optimiseUrl());
        await this.page.waitForURL(/\/optimise/, { timeout: 15000 });
        await this.page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 30000 }).catch(() => {});
    }

    // Active-tab check — prefer the button's data-tab-selected flag, fall back to the URL fragment.
    private async isTabActive(tab: OptimizeTab): Promise<boolean> {
        const selected = await this.tabLocator(tab).getAttribute("data-tab-selected").catch(() => null);
        if (selected === "true") return true;
        const url = this.page.url();
        // Summary is the default tab — active when the URL carries no hash yet.
        if (tab === OptimizeTabs.summary && !url.includes("#")) return true;
        return url.includes(`#${tab}`);
    }

    // Open a sub-tab — Plan A: click the strip tab and verify it went active; Plan B: hash-route goto.
    async gotoTab(tab: OptimizeTab): Promise<void> {
        const tabLocator = this.tabLocator(tab);

        try {
            await tabLocator.waitFor({ state: "visible", timeout: 10000 });

            // Clicking the tab that is already active (Summary is the page
            // default) is a no-op: no request fires, so an API-validation caller
            // sees zero operations. Reload so the tab's data is really refetched.
            if (await this.isTabActive(tab)) {
                await this.page.reload();
                await this.page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 30000 }).catch(() => {});
                return;
            }

            await tabLocator.click();
            await this.page.mouse.move(0, 0);
            await this.page.waitForTimeout(500);
            if (await this.isTabActive(tab)) {
                // Wait for the tab's data to settle before returning, so validation doesn't start mid-load.
                await this.page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 30000 }).catch(() => {});
                return;
            }
            console.warn(`[OptimizeLocators] gotoTab(${tab}) clicked but tab not active — falling back to hash navigation`);
        } catch {
            console.warn(`[OptimizeLocators] gotoTab(${tab}) strip click failed — falling back to hash navigation`);
        }

        await this.page.goto(this.optimiseUrl(tab));
        await this.page.waitForURL(new RegExp(`/optimise#${tab}`), { timeout: 15000 }).catch(() => {});
        await this.page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 30000 }).catch(() => {});
    }
}

import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";

// Global Troubleshoot page (/troubleshoot). Top-level tabs are rendered by
// AnchorComponent; these entries carry no `id`, so their dom id falls back to
// `anchor-tab-<name>` (name has spaces). The hash `fragment` differs from the name.
export const TroubleshootTabs = {
    allEvents: { name: "All Events", fragment: "all-events" },
    investigations: { name: "Investigations", fragment: "investigations" },
    knowledgeGraph: { name: "Knowledge Graph", fragment: "kg" },
} as const;

export type TroubleshootTab = typeof TroubleshootTabs[keyof typeof TroubleshootTabs];

export class TroubleshootLocators extends CommonLocators {
    // Anchor-strip tab locators.
    readonly AllEventsTab: Locator;
    readonly InvestigationsTab: Locator;
    readonly KnowledgeGraphTab: Locator;

    constructor(page: Page) {
        super(page);
        this.AllEventsTab = page.locator(`[id="anchor-tab-${TroubleshootTabs.allEvents.name}"]`);
        this.InvestigationsTab = page.locator(`[id="anchor-tab-${TroubleshootTabs.investigations.name}"]`);
        this.KnowledgeGraphTab = page.locator(`[id="anchor-tab-${TroubleshootTabs.knowledgeGraph.name}"]`);
    }

    // Resolve a tab to its named Locator.
    private tabLocator(tab: TroubleshootTab): Locator {
        if (tab === TroubleshootTabs.allEvents) return this.AllEventsTab;
        if (tab === TroubleshootTabs.investigations) return this.InvestigationsTab;
        return this.KnowledgeGraphTab;
    }

    private troubleshootUrl(fragment?: string): string {
        const base = (process.env.BASE_URL || "").replace(/\/$/, "");
        return `${base}/troubleshoot${fragment ? `#${fragment}` : ""}`;
    }

    // Open the Troubleshoot page — Plan A: retry the sidebar button; Plan B: direct goto /troubleshoot.
    async navigateToTroubleshoot(maxRetries = 3): Promise<void> {
        await this.page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 30000 }).catch(() => {});

        for (let attempt = 1; attempt <= maxRetries; attempt++) {
            await this.TroubleshootBtn.waitFor({ state: "visible", timeout: 15000 }).catch(() => {});
            await this.TroubleshootBtn.click().catch(() => {});
            await this.page.mouse.move(0, 0);

            await this.page.waitForURL(/\/troubleshoot/, { timeout: 8000 }).catch(() => {});
            if (/\/troubleshoot/.test(this.page.url())) {
                await this.page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 30000 }).catch(() => {});
                return;
            }

            console.warn(`[TroubleshootLocators] navigateToTroubleshoot attempt ${attempt}/${maxRetries} via sidebar failed — URL: ${this.page.url()}`);
            await this.page.waitForTimeout(1500);
        }

        console.warn("[TroubleshootLocators] Sidebar navigation exhausted — falling back to direct goto /troubleshoot");
        await this.page.goto(this.troubleshootUrl());
        await this.page.waitForURL(/\/troubleshoot/, { timeout: 15000 });
        await this.page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 30000 }).catch(() => {});
    }

    // Active-tab check — prefer the button's data-tab-selected flag, fall back to the URL fragment.
    private async isTabActive(tab: TroubleshootTab): Promise<boolean> {
        const selected = await this.tabLocator(tab).getAttribute("data-tab-selected").catch(() => null);
        if (selected === "true") return true;
        return this.page.url().includes(`#${tab.fragment}`);
    }

    // Open a top-level tab — Plan A: click the strip tab and verify it went active; Plan B: hash-route goto.
    async gotoTab(tab: TroubleshootTab): Promise<void> {
        const tabLocator = this.tabLocator(tab);

        try {
            await tabLocator.waitFor({ state: "visible", timeout: 10000 });
            await tabLocator.click();
            await this.page.mouse.move(0, 0);
            await this.page.waitForTimeout(500);
            if (await this.isTabActive(tab)) return;
            console.warn(`[TroubleshootLocators] gotoTab(${tab.name}) clicked but tab not active — falling back to hash navigation`);
        } catch {
            console.warn(`[TroubleshootLocators] gotoTab(${tab.name}) strip click failed — falling back to hash navigation`);
        }

        await this.page.goto(this.troubleshootUrl(tab.fragment));
        await this.page.waitForURL(new RegExp(`/troubleshoot#${tab.fragment}`), { timeout: 15000 }).catch(() => {});
        await this.page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 30000 }).catch(() => {});
    }
}

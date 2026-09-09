// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";

// The K8s quick-link set, in the order QUICK_LINKS_CONFIG declares it
// (app/src/pages/home/index.jsx). `fragment` is what buildUrl() appends to
// /kubernetes/details/<accountId>, so each pair is one deep-link contract.
export interface QuickLink {
  name: string;
  fragment: string;
}

export const K8S_QUICK_LINKS: QuickLink[] = [
  { name: "Query Logs", fragment: "monitoring/logs" },
  { name: "Recent Errors", fragment: "monitoring/groups" },
  { name: "Query Metrics", fragment: "monitoring/query" },
  { name: "View Traces", fragment: "monitoring/traces" },
  { name: "Service Maps", fragment: "monitoring/service-map" },
  { name: "View Applications", fragment: "kubernetes/applications" },
  { name: "View Pods", fragment: "kubernetes/pods" },
  { name: "Security", fragment: "security/image-scan" },
  { name: "Troubleshoot", fragment: "events/summary" },
  { name: "Optimize", fragment: "optimize/summary" },
];

// CardsBlock titles. Optimize is dropped for CloudFoundry and Security for anything
// but K8s, so only Troubleshoot is unconditional.
export const TROUBLESHOOT = "Troubleshoot";
export const OPTIMIZE = "Optimize";
export const SECURITY = "Security & Compliance";

// The subtitle a section falls back to when it holds no items at all. Any other
// subtitle is the counted form ("3 issues · 2 workloads affected").
export const FALLBACK_SUBTITLE: Record<string, string> = {
  [TROUBLESHOOT]: "Active incidents and event trends",
  [OPTIMIZE]: "Right-sizing, storage, and cost recommendations",
  [SECURITY]: "Vulnerabilities, certificates, image scans",
};

// CardsBlock renders `footer` only when it is NOT showing an empty state
// (footer={shouldShowEmptyState ? null : footer}), which is what ties the two together.
export const SECTION_FOOTER: Record<string, string> = {
  [TROUBLESHOOT]: "View all issues",
  [OPTIMIZE]: "View all recommendations",
  [SECURITY]: "View security dashboard",
};

// Where each footer's window.open lands. Asserted as a pathname + account pair rather
// than a full string, since the pages append their own filter params.
export const SECTION_FOOTER_PATH: Record<string, string> = {
  [TROUBLESHOOT]: "/troubleshoot",
  [OPTIMIZE]: "/optimise",
  [SECURITY]: "/kubernetes/details",
};

export class HomeLocators extends CommonLocators {
  readonly quickLinksTitle: Locator;
  readonly quickLinksGrid: Locator;
  readonly quickLinkAnchors: Locator;

  readonly automationsTitle: Locator;
  readonly statConfigured: Locator;
  readonly statTriggered: Locator;
  readonly statEventBased: Locator;

  readonly followUpsViewAll: Locator;

  constructor(page: Page) {
    super(page);

    // No fallback and no higher rung available: HomeWidgets wraps the links in a bare
    // DSCard whose header is a plain Typography — no testid, no id, no role, no
    // accessible name. `grep -c 'data-testid' app/src/pages/home/index.jsx` returns 0.
    this.quickLinksTitle = page.getByText("Quick Links", { exact: true });

    // Anchored to the card's own title, then the NEAREST enclosing div that also holds a
    // detail-page link — on a reverse XPath axis, predicate [1] is the closest ancestor,
    // so this lands on the DSCard root rather than a layout wrapper further out.
    //
    // Not `div:has(> a[href*="/details/"])`: more than one element on the page satisfies
    // that, and `.first()` took the earlier one — a header container around the cluster
    // dropdown, whose own first anchor is the Agent Health link that CustomDropdown.jsx:510
    // renders. CI run 32333115011 failed all ten tests on it, every one reporting
    // `locator resolved to <a href="/agentHealth?accountId=…#agent">`. Binding to the title
    // is what makes the match unique instead of merely first.
    this.quickLinksGrid = this.quickLinksTitle.locator('xpath=ancestor::div[.//a[contains(@href,"/details/")]][1]');
    // Every match is a quick link: the card holds no other anchors, and scoping through it
    // keeps the sidebar rail's identically-named Troubleshoot/Optimize links out.
    this.quickLinkAnchors = this.quickLinksGrid.locator('a[href*="/details/"]');

    this.automationsTitle = page.getByText("Automations", { exact: true });

    // Rung 3 by necessity: ds/Stat renders `id` and nothing else addressable — no
    // testid, and its role is 'button' only while a click handler is wired, which
    // AutomationsCard omits for a zero count. The ids are the page's own
    // (app/src/pages/home/index.jsx), so a fallback would have to be structural.
    this.statConfigured = page.locator("#automations-configured-stat");
    this.statTriggered = page.locator("#automations-triggered-stat");
    this.statEventBased = page.locator("#automations-event-based-stat");

    this.followUpsViewAll = page.getByTestId("follow-ups-view-all");
  }

  // CollapsableCard renders its header as <button type="button" aria-expanded>, and the
  // sidebar rail exposes buttons named "Troubleshoot" and "Optimize" too — so the
  // attribute is what disambiguates, not a wider fallback. Both halves of the .and()
  // must land on the same element, so this cannot drift to the rail.
  sectionHeader(title: string): Locator {
    return this.page.getByRole("button", { name: title }).and(this.page.locator("button[aria-expanded]")).first();
  }

  // The header button is a direct child of the Card that CollapsableCard renders, and
  // that Card is never given an id — CardsBlock's `id` prop is consumed for the
  // localStorage persistence key and never reaches the DOM.
  sectionCard(title: string): Locator {
    return this.sectionHeader(title).locator("xpath=..");
  }

  sectionFooter(title: string): Locator {
    return this.sectionCard(title).getByRole("button", { name: SECTION_FOOTER[title] });
  }

  // @ui/Skeleton renders role="status" aria-busy="true", so the loading rows CardsBlock
  // swaps in while an insight fetch is open are excluded structurally rather than by copy.
  sectionSkeletons(title: string): Locator {
    return this.sectionCard(title).locator('[aria-busy="true"]');
  }

  quickLink(name: string): Locator {
    return this.quickLinksGrid
      .getByRole("link", { name })
      .or(this.quickLinksGrid.locator("a").filter({ hasText: name }))
      .first();
  }

  // localStorage key CollapsableCard persists a section's open state under
  // (STORAGE_PREFIX + id in app/src/components/common/ds/CollapsableCard.tsx, with the
  // id built by CardsBlock as `home-section-<lowercased title, spaces to dashes>`).
  static sectionStorageKey(title: string): string {
    return `ds:collapsable-card:home-section-${title.toLowerCase().replace(/\s+/g, "-")}`;
  }
}

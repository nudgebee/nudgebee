// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";

// The three provider sections AccountOverview can render, in the order they
// appear in the page's own filterOptions useMemo (app/src/pages/overview/index.jsx).
// `id` is the DOM id SectionHeading stamps on its wrapper Box; `name` is the
// label the anchor jump-nav button carries.
export interface OverviewSection {
  id: string;
  name: string;
  kind: "k8s" | "cloud" | "vm";
}

export const SECTION_K8S: OverviewSection = { id: "clusters", name: "Clusters", kind: "k8s" };
export const SECTION_CLOUD: OverviewSection = { id: "cloud-accounts", name: "Cloud Accounts", kind: "cloud" };
export const SECTION_VM: OverviewSection = { id: "vm-fleets", name: "Self-hosted VMs", kind: "vm" };

// The three section headings AccountOverview renders — matched against the
// visible text inside the Heading component, not the anchor button label.
export const HEADING_K8S = "Kubernetes Clusters";
export const HEADING_CLOUD = "Cloud Accounts";
export const HEADING_VM = "Self-hosted VMs";

// Onboarding panel copy, when the tenant has no accounts of any kind. Asserted
// as an absence check on a populated tenant so the negative branch is bound.
export const EMPTY_STATE_HEADING = "Get started with infrastructure monitoring";

export class OverviewLocators extends CommonLocators {
  // The container that AccountOverview mounts inside — the outer Box the page
  // wraps with ErrorBoundary. Used as a scope for section-relative queries so a
  // duplicate id from a page transition cannot resolve here.
  readonly pageRoot: Locator;

  // The K8s section: heading text is composed from the Heading component (span
  // holds the count), and each cluster card wraps in Box id="cluster_box_<name>".
  readonly clustersHeading: Locator;
  readonly clusterCards: Locator;

  // Cloud Accounts section — heading and each Box id="account_box_<name>". The
  // per-card title link is <Link id="account-link-<accountId>"> which points at
  // /cloud-account/details/<accountId>#summary.
  readonly cloudHeading: Locator;
  readonly cloudCards: Locator;
  readonly cloudCardLinks: Locator;

  // Self-hosted VM section — heading and cards. The card title is a keyboard-role
  // link (Box role='link' id="vm-account-link-<accountId>") rather than an <a>
  // because /vm has no account path segment; the click handler pushes the router.
  readonly vmHeading: Locator;
  readonly vmCards: Locator;

  // AnchorComponent's sub-section jump nav: one <Button> per opt in
  // filterOptions[0].options, rendered as plain MUI Buttons with the opt.name
  // as text. No data-testid, no id, no role= override — MUI Button ships
  // role='button' by default.
  readonly anchorNavButtons: Locator;

  // Empty-state onboarding panel (only when the tenant has zero accounts of
  // any kind). Two DsButtons carry stable ids.
  readonly emptyStateHeading: Locator;
  readonly addClusterButton: Locator;
  readonly connectCloudAccountButton: Locator;

  constructor(page: Page) {
    super(page);

    // Anchored to the section headings themselves — Overview mounts under the
    // shared PageLayout, so binding to the page's own Heading text is the
    // narrowest scope that reliably survives the sidebar's own Overview link.
    this.pageRoot = page.locator("body");

    // Heading component composes title + optional (count) span into one visible
    // block. Matching by containing text picks it up while the count is still
    // trailing behind it — role primary with a scoped text fallback.
    this.clustersHeading = page
      .getByRole("heading", { name: HEADING_K8S })
      .or(page.getByText(HEADING_K8S, { exact: false }))
      .first();
    this.cloudHeading = page
      .getByRole("heading", { name: HEADING_CLOUD })
      .or(page.getByText(HEADING_CLOUD, { exact: false }))
      .first();
    this.vmHeading = page
      .getByRole("heading", { name: HEADING_VM })
      .or(page.getByText(HEADING_VM, { exact: false }))
      .first();

    // Each cluster card is Box id="cluster_box_<account_name>" — CSS attribute
    // form because account names contain '.' and ':' that #-syntax reads as
    // class chains. The wildcard is the CSS starts-with selector.
    this.clusterCards = page.locator('[id^="cluster_box_"]');
    // Cloud and VM sections share the same account_box_ prefix on the outer
    // wrapper; scoping by the per-card link/id is what tells them apart.
    this.cloudCards = page.locator('[id^="account_box_"]:has([id^="account-link-"])');
    this.cloudCardLinks = page.locator('[id^="account-link-"]');
    this.vmCards = page.locator('[id^="account_box_"]:has([id^="vm-account-link-"])');

    // The sub-section jump nav renders a plain MUI Button per entry with the
    // opt.name as its text content. Scoping by the visible section names keeps
    // the query away from the tab strip AnchorComponent renders when
    // hideParentTabs is false — Overview hides it, but the same locator is
    // reused in assertions that check what is NOT in the strip.
    this.anchorNavButtons = page
      .getByRole("button", { name: new RegExp(`^(${HEADING_CLOUD}|${SECTION_K8S.name}|${SECTION_VM.name})$`) });

    this.emptyStateHeading = page.getByText(EMPTY_STATE_HEADING, { exact: true });
    // Rung 3 primary with a role+name fallback on the same page — DsButton
    // renders its `id` prop, and neither of these carries a data-testid. Only
    // one route mounts at a time so the fallback stays scoped to this page.
    this.addClusterButton = page
      .locator("#add-k8s-account")
      .or(page.getByRole("button", { name: "Add Cluster" }))
      .first();
    this.connectCloudAccountButton = page
      .locator("#connect-cloud-account")
      .or(page.getByRole("button", { name: "Connect Cloud Account" }))
      .first();
  }

  // Anchor jump-nav button for one section by its visible name. Scoped to
  // exact so a Cloud Accounts button can never resolve to the sidebar's
  // Cloud Accounts nav entry inside the Infra flyout.
  anchorNavButton(sectionName: string): Locator {
    return this.page.getByRole("button", { name: sectionName, exact: true }).first();
  }

  // The DOM anchor SectionHeading stamps on its wrapper Box. Present only when
  // the section rendered, so its count is the same signal as the heading.
  sectionAnchor(id: string): Locator {
    return this.page.locator(`[id="${id}"]`);
  }

  // Cluster card title Link, rendered by ClusterViewCard as
  // <Link id={clusterName} href="/kubernetes/details/<accountId>#summary">.
  // clusterName can contain '.' / ':' so we take the attribute-form id;
  // fallback is the CSS starts-with on href, still scoped to the card.
  clusterCardLink(clusterName: string): Locator {
    const box = this.page.locator(`[id="cluster_box_${clusterName}"]`).first();
    return box
      .locator(`[id="${clusterName}"]`)
      .or(box.locator('a[href^="/kubernetes/details/"]'))
      .first();
  }

  // Cloud account card title Link — <Link id="account-link-<accountId>">
  // wrapped inside the account_box.
  cloudCardLink(accountId: string): Locator {
    return this.page.locator(`[id="account-link-${accountId}"]`).first();
  }
}

// Not for OSS
import { test, expect } from "@playwright/test";
import { openOverview, readRenderedSections, waitForAnySection } from "./overviewHelper";
import { EMPTY_STATE_HEADING, HEADING_CLOUD, HEADING_K8S, HEADING_VM, SECTION_CLOUD, SECTION_K8S, SECTION_VM } from "./overviewLocators";

// Overview — the fleet-wide landing surface at /overview (app/src/pages/overview/index.jsx).
// It renders up to three sections — Kubernetes Clusters, Cloud Accounts and Self-hosted VMs —
// each drawn only when the tenant has accounts of that kind. The module is read-only: no
// create, edit or delete path lives here, so nothing this suite runs writes to the shared
// dev tenant. The only mutation any test performs is a browser navigation, and each spec
// runs in a context built fresh from the storageState global-setup saved.
//
// The empty-state onboarding panel (Add Cluster / Connect Cloud Account) is exercised
// as an absence check on a populated tenant, so the negative branch is bound without
// having to construct a zero-account tenant to trigger the positive one.

test.beforeEach(() => {
  test.setTimeout(180000);
});

test.describe("Overview", () => {
  test(
    "Overview sanity - open /overview, verify the Kubernetes Clusters section renders with its counted heading and its account cards",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      const { locators } = await openOverview(page);
      await waitForAnySection(locators);

      await test.step("The Kubernetes Clusters heading is on the page", async () => {
        await expect(locators.clustersHeading).toBeVisible({ timeout: 60000 });
        await expect(locators.sectionAnchor(SECTION_K8S.id)).toHaveCount(1);
      });

      await test.step("At least one cluster card rendered under the section", async () => {
        // The count is the contract, not just the heading: the section wrapper
        // is rendered while the fetch is still open (skeletons stand in). A zero
        // card count once the heading is here means the query returned nothing.
        await expect(locators.clusterCards.first()).toBeVisible({ timeout: 90000 });
        expect(await locators.clusterCards.count()).toBeGreaterThan(0);
      });
    }
  );

  test(
    "Overview sanity - open /overview, verify the header omits the cluster dropdown so no single account stays in scope while the fleet view is open",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      const { locators } = await openOverview(page);
      await waitForAnySection(locators);

      // /overview's header entry is configured showActiveCluster:false
      // (app/src/components/common/header/Header1.jsx:268), so the ClusterDropdown
      // gated on that flag (Header1.jsx:898) is never rendered on this route — the
      // page is fleet-wide and must not carry a single account in scope. The
      // dropdown's input id is the contract; asserting its absence is what a
      // regression that re-enabled the dropdown here would trip.
      // Rung 3 id-only, no fallback: an absence check must not widen, or an
      // unrelated element could satisfy the count and hide the regression.
      const clusterInput = page.locator("#auto-complete-global-cluster");
      await expect(clusterInput).toHaveCount(0, { timeout: 30000 });

      // The K8s section still renders — the accounts themselves are unaffected,
      // only the header's account picker is withheld. This keeps the test from
      // passing on a page that simply failed to load.
      await expect(locators.clustersHeading).toBeVisible({ timeout: 60000 });
    }
  );

  test(
    "Overview - open /overview, verify each rendered cluster card wraps its title link in the ClusterViewCard contract that points at /kubernetes/details/{accountId}#summary",
    { tag: ["@dev", "@smoke", "@functional"] },
    async ({ page }) => {
      const { locators } = await openOverview(page);
      await waitForAnySection(locators);
      await expect(locators.clusterCards.first()).toBeVisible({ timeout: 90000 });

      const cardCount = await locators.clusterCards.count();
      expect(cardCount, "no Kubernetes cluster cards rendered — the configured cluster's tenant has no K8s account").toBeGreaterThan(0);

      // Scoped to the first card so a per-cluster contract is asserted end-to-end
      // rather than reading against the union of every card on the page.
      const firstCard = locators.clusterCards.first();
      const titleLink = firstCard.locator('a[href^="/kubernetes/details/"]').first();
      await expect(titleLink).toBeVisible({ timeout: 60000 });
      const href = (await titleLink.getAttribute("href")) ?? "";
      // The fragment is the half that decides which tab mounts on the details page,
      // and ClusterViewCard hardcodes #summary — a card that dropped it would send
      // the user to whatever tab happened to be sticky in localStorage.
      expect(href).toMatch(/^\/kubernetes\/details\/[^#?]+#summary$/);
    }
  );

  test(
    "Overview - open /overview and click a cluster card's title, verify /kubernetes/details/{accountId} opens on its summary tab then browser Back restores the fleet view",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const { locators } = await openOverview(page);
      await waitForAnySection(locators);
      await expect(locators.clusterCards.first()).toBeVisible({ timeout: 90000 });

      const titleLink = locators.clusterCards.first().locator('a[href^="/kubernetes/details/"]').first();
      const href = (await titleLink.getAttribute("href")) ?? "";
      const accountId = /\/kubernetes\/details\/([^#?]+)/.exec(href)?.[1] ?? "";
      expect(accountId, "could not extract accountId from the cluster card link").not.toEqual("");

      await titleLink.click();

      await test.step("The details page opens on the tab the fragment named", async () => {
        await expect(page).toHaveURL(new RegExp(`/kubernetes/details/${accountId}`), { timeout: 90000 });
        await expect(page).toHaveURL(/#summary/, { timeout: 30000 });
      });

      await test.step("Browser Back restores /overview, not just the URL", async () => {
        await page.goBack();
        await expect(page).toHaveURL(/\/overview/, { timeout: 90000 });
        await expect(locators.clustersHeading).toBeVisible({ timeout: 60000 });
      });
    }
  );

  test(
    "Overview - open /overview, verify the anchor jump-nav renders one button per section the tenant has, and no button for a section kind it does not",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const { locators } = await openOverview(page);
      await waitForAnySection(locators);

      const sections = await readRenderedSections(locators);
      expect(sections.hasK8s || sections.hasCloud || sections.hasVm, "no sections rendered on /overview").toBeTruthy();

      await test.step("Every rendered section kind has its anchor button", async () => {
        if (sections.hasK8s) {
          await expect(locators.anchorNavButton(SECTION_K8S.name)).toBeVisible({ timeout: 30000 });
        }
        if (sections.hasCloud) {
          await expect(locators.anchorNavButton(SECTION_CLOUD.name)).toBeVisible({ timeout: 30000 });
        }
        if (sections.hasVm) {
          await expect(locators.anchorNavButton(SECTION_VM.name)).toBeVisible({ timeout: 30000 });
        }
      });

      await test.step("No anchor button for a section the tenant does not have", async () => {
        if (!sections.hasK8s) {
          await expect(locators.anchorNavButton(SECTION_K8S.name)).toHaveCount(0);
        }
        if (!sections.hasCloud) {
          await expect(locators.anchorNavButton(SECTION_CLOUD.name)).toHaveCount(0);
        }
        if (!sections.hasVm) {
          await expect(locators.anchorNavButton(SECTION_VM.name)).toHaveCount(0);
        }
      });
    }
  );

  test(
    "Overview - open /overview and click the Clusters anchor button, verify the Kubernetes Clusters section heading scrolls into the viewport",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const { locators } = await openOverview(page);
      await waitForAnySection(locators);

      const sections = await readRenderedSections(locators);
      test.skip(!sections.hasK8s, "tenant has no K8s clusters — the Clusters anchor is intentionally not rendered");

      const anchor = locators.anchorNavButton(SECTION_K8S.name);
      await expect(anchor).toBeVisible({ timeout: 30000 });
      await anchor.click();

      // AnchorComponent.handleClickScroll calls element.scrollIntoView({behavior:'smooth'}).
      // Playwright's own visibility check does not require the element to be in the viewport,
      // so a scroll into view is proved by asking the browser for the boundingClientRect.top —
      // it is negative when the target scrolled above the current viewport, and near zero
      // once smooth-scroll settles it. Poll rather than read once.
      const anchorBox = locators.sectionAnchor(SECTION_K8S.id).first();
      await expect(anchorBox).toBeVisible({ timeout: 30000 });
      await expect(async () => {
        const top = await anchorBox.evaluate((el: HTMLElement) => el.getBoundingClientRect().top);
        const viewport = await page.evaluate(() => window.innerHeight);
        expect(top).toBeLessThan(viewport);
        expect(top).toBeGreaterThan(-200);
      }).toPass({ timeout: 15000, intervals: [250, 500, 1000] });
    }
  );

  test(
    "Overview - open /overview on a populated tenant, verify the empty-state onboarding panel and its Add Cluster / Connect Cloud Account controls are absent",
    { tag: ["@dev", "@regression", "@validation"] },
    async ({ page }) => {
      const { locators } = await openOverview(page);
      await waitForAnySection(locators);

      const sections = await readRenderedSections(locators);
      const hasAny = sections.hasK8s || sections.hasCloud || sections.hasVm;
      expect(hasAny, "the shared dev tenant is expected to have at least one account — the empty-state branch is not being validated on a real zero-account tenant").toBeTruthy();

      // hasNoAccounts is `!loading && !loadingAccountList && !hasK8s && !hasCloud && !hasVm`,
      // so a tenant with any accounts must never render the onboarding panel. Assert
      // both the heading and the two branded action buttons — a partial render would
      // otherwise slip through a heading-only check. Id-only for the absence check —
      // a wider fallback would let an unrelated element flip the toHaveCount(0).
      await expect(locators.emptyStateHeading).toHaveCount(0);
      await expect(page.locator("#add-k8s-account")).toHaveCount(0);
      await expect(page.locator("#connect-cloud-account")).toHaveCount(0);
      // Belt and braces — the copy is unique enough that any hit means the panel mounted.
      await expect(page.getByText(EMPTY_STATE_HEADING, { exact: true })).toHaveCount(0);
    }
  );

  test(
    "Overview - open /overview and deep-link an unknown hash, verify the URL is preserved as /overview and the Account Overview surface still renders",
    { tag: ["@dev", "@regression", "@negative"] },
    async ({ page }) => {
      const { locators } = await openOverview(page);
      await waitForAnySection(locators);

      // Overview's filterOptions declares only one parent tab (fragment='overview'), and
      // AnchorComponent's getInitialState falls back to tab 0 for any hash it cannot
      // resolve — including a stale bookmark to a fragment the page no longer offers.
      // The page should not dead-end and should not force a redirect to /home.
      await page.evaluate(() => {
        window.location.hash = "no-such-anchor";
      });

      await expect(page).toHaveURL(/\/overview/, { timeout: 30000 });
      // The main heading survives the hash change — AnchorComponent's handleClickScroll
      // catches missing target elements silently, so the fallback lands on tab 0's body.
      await expect(locators.clustersHeading.or(locators.cloudHeading).or(locators.vmHeading).first()).toBeVisible({ timeout: 60000 });
    }
  );

  test(
    "Overview - open /overview when the tenant has cloud accounts, verify each Cloud Accounts card links at /cloud-account/details/{accountId}#summary and never at another provider's details path",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const { locators } = await openOverview(page);
      await waitForAnySection(locators);

      const sections = await readRenderedSections(locators);
      test.skip(!sections.hasCloud, "tenant has no cloud accounts — the Cloud Accounts section is intentionally not rendered");

      await expect(locators.cloudHeading).toBeVisible({ timeout: 60000 });
      await expect(locators.cloudCardLinks.first()).toBeVisible({ timeout: 60000 });

      // Resolved in one pass rather than re-querying nth(i) per iteration, so the set
      // cannot shift underneath the loop if a late summary re-render lands mid-check.
      const links = await locators.cloudCardLinks.all();
      expect(links.length).toBeGreaterThan(0);

      // Every card's href — a stray VM or K8s account slipping into this section
      // would fail this contract straight away. The pattern is anchored on purpose:
      // CloudAccountOverviewCard.tsx:88 builds the href as a template literal with no
      // query string, so the exact shape IS the contract and a substring check would
      // accept a wrong-prefix or trailing-segment path just as happily.
      for (const link of links) {
        const href = (await link.getAttribute("href")) ?? "";
        expect(href).toMatch(/^\/cloud-account\/details\/[^#?]+#summary$/);
      }
    }
  );

  test(
    "Overview - open /overview and reload the page, verify the fleet view survives a hard reload with the K8s section back on screen",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const { locators } = await openOverview(page);
      await waitForAnySection(locators);
      await expect(locators.clustersHeading.or(locators.cloudHeading).or(locators.vmHeading).first()).toBeVisible({ timeout: 60000 });

      const before = await readRenderedSections(locators);

      await page.reload();
      // Re-read locators against the fresh DOM — the reload replaced every node.
      await expect(page).toHaveURL(/\/overview/, { timeout: 60000 });
      await waitForAnySection(locators);

      const after = await readRenderedSections(locators);
      // The section set is a property of the tenant, not the page state, so it must
      // be identical across the reload. A drop would mean an effect no longer fires
      // on a rehydrated mount — a class of regression a plain "still visible" check
      // would miss on a tenant with more than one kind of account.
      expect(after.hasK8s).toEqual(before.hasK8s);
      expect(after.hasCloud).toEqual(before.hasCloud);
      expect(after.hasVm).toEqual(before.hasVm);
      // The configured cluster is a K8s one, so this side always holds — assert
      // rather than skip so the test still verifies the reload path directly.
      expect(after.hasK8s).toBeTruthy();
    }
  );
});

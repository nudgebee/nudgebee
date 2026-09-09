// Not for OSS
import { test, expect } from "@playwright/test";
import {
  openHome,
  openHomeUnscoped,
  expectSectionSettled,
  expectExpanded,
  readSectionHeaderText,
  HOME_SECURITY_OPS,
  HOME_WORKFLOW_OPS,
} from "./homeHelper";
import { HomeLocators, K8S_QUICK_LINKS, TROUBLESHOOT, OPTIMIZE, SECURITY, FALLBACK_SUBTITLE, SECTION_FOOTER, SECTION_FOOTER_PATH } from "./homeLocators";

// Home — the landing surface every sign-in arrives on (app/src/pages/home/index.jsx, which
// `/` redirects to). It is a read-only dashboard: three collapsible insight sections down
// the left, and Automations / Follow-ups / Quick Links down the right. It offers no create,
// edit or delete path, so nothing here writes to the shared dev environment.
//
// The one piece of state a test does change is a section's collapsed flag, which
// CollapsableCard persists to localStorage. Each test runs in a context built fresh from the
// storageState global-setup saved, so that write never leaves the test that made it — the
// suite stays order-free and safe to run twice.

// Every test signs in, resolves the account, then loads Home against live insight APIs.
test.beforeEach(() => {
  test.setTimeout(180000);
});

test(
  "Home sanity - open Home for the selected cluster, verify the Troubleshoot and Optimize sections and the Quick Links card render",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const { locators } = await openHome(page);

    await test.step("Both unconditional insight sections are on screen and expanded", async () => {
      for (const title of [TROUBLESHOOT, OPTIMIZE]) {
        await expect(locators.sectionHeader(title)).toBeVisible();
        // defaultOpen is true and nothing in this context has collapsed them.
        await expectExpanded(locators.sectionHeader(title), true);
      }
    });

    await test.step("The right rail offers Quick Links with its links loaded", async () => {
      await expect(locators.quickLinksTitle).toBeVisible();
      // Not just "the grid exists": while HomeWidgets is resolving the provider it renders
      // a skeleton grid holding no anchors at all, which would satisfy a visibility check.
      await expect(locators.quickLinkAnchors.first()).toBeVisible();
      expect(await locators.quickLinkAnchors.count()).toBeGreaterThan(0);
    });
  }
);

test(
  "Home sanity - open the bare /home link, verify the cluster dropdown adopts the account into the URL and the quick links bind to that same account",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const locators = await openHomeUnscoped(page);

    // Home reads the account it fetches for from router.query.accountId and does nothing
    // without one; it is the header dropdown that supplies it. This is the contract every
    // deep link into the module — the root redirect, the sidebar's Home entry — depends on.
    await expect(page).toHaveURL(/[?&]accountId=[^&#]+/, { timeout: 60000 });

    const adopted = /[?&]accountId=([^&#]+)/.exec(page.url())?.[1] ?? "";
    // A blank or literal "undefined" satisfies the pattern above while leaving every fetch
    // on the page unanswered, so the value itself is checked rather than its presence.
    expect(adopted).not.toEqual("");
    expect(adopted).not.toEqual("undefined");

    await test.step("The quick links deep-link into the account that was adopted", async () => {
      await expect(locators.quickLinkAnchors.first()).toHaveAttribute("href", new RegExp(`/details/${adopted}(#|$)`), { timeout: 30000 });
    });
  }
);

test(
  "Home - open Quick Links on a K8s cluster, verify all ten links deep-link into that cluster's details page each with its own fragment",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    const { locators, accountId } = await openHome(page);

    // The count is the contract, not just the presence of the ten names: a grants-only
    // custom role renders a disabled tooltip in place of a link, and asserting only that
    // each expected link exists would pass just as happily on a longer or shorter set.
    await expect(locators.quickLinkAnchors).toHaveCount(K8S_QUICK_LINKS.length);

    for (const link of K8S_QUICK_LINKS) {
      await test.step(`${link.name} points at #${link.fragment}`, async () => {
        await expect(locators.quickLink(link.name)).toHaveAttribute("href", `/kubernetes/details/${accountId}#${link.fragment}`);
      });
    }
  }
);

test(
  "Home - click the Query Logs quick link, verify the cluster details page opens on its monitoring logs tab, then go back and verify Home returns",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const { locators, accountId } = await openHome(page);

    await locators.quickLink("Query Logs").click();

    await test.step("The details page opens on the tab the fragment named", async () => {
      await expect(page).toHaveURL(new RegExp(`/kubernetes/details/${accountId}`), { timeout: 90000 });
      // The fragment is the half that decides which tab mounts, and nothing rewrites it
      // here: ClusterDropDown skips its accountId push on /kubernetes/details/[KubernetesDetails].
      await expect(page).toHaveURL(/#monitoring\/logs/, { timeout: 30000 });
    });

    await test.step("Browser Back restores Home, not just the address bar", async () => {
      await page.goBack();
      await expect(page).toHaveURL(/\/home/, { timeout: 90000 });
      await expect(locators.quickLinksTitle).toBeVisible({ timeout: 60000 });
      await expect(locators.sectionHeader(TROUBLESHOOT)).toBeVisible({ timeout: 60000 });
    });
  }
);

test(
  "Home - collapse the Troubleshoot section from its header, verify its body and View all issues footer unmount and the header reports collapsed",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const { locators } = await openHome(page);
    await expectSectionSettled(locators, TROUBLESHOOT);

    const header = locators.sectionHeader(TROUBLESHOOT);
    await expectExpanded(header, true);

    // Captured while open so the assertion after the click is about the collapse rather
    // than about whether this account happens to have issues at all — the footer only
    // renders when the section has items (CardsBlock passes null for it otherwise).
    const footerWasShown = (await locators.sectionFooter(TROUBLESHOOT).count()) > 0;

    await header.click();

    await expectExpanded(header, false);
    // MUI Collapse is mounted with unmountOnExit, so a collapsed section leaves nothing of
    // its body behind — the footer going to zero is what proves the body really closed and
    // not merely that a class changed.
    await expect(locators.sectionFooter(TROUBLESHOOT)).toHaveCount(0);

    if (footerWasShown) {
      await header.click();
      await expectExpanded(header, true);
      await expect(locators.sectionFooter(TROUBLESHOOT)).toHaveCount(1);
    }
  }
);

test(
  "Home - collapse the Optimize section and reload the page, verify it is still collapsed and re-expanding it brings its body back",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const { locators } = await openHome(page);
    await expectSectionSettled(locators, OPTIMIZE);

    const header = locators.sectionHeader(OPTIMIZE);
    await expectExpanded(header, true);

    await header.click();
    await expectExpanded(header, false);

    await test.step("The collapsed state is written to storage, not just held in React state", async () => {
      const key = HomeLocators.sectionStorageKey(OPTIMIZE);
      const stored = await page.evaluate((k) => window.localStorage.getItem(k), key);
      expect(stored).toEqual("closed");
    });

    await test.step("A reload restores the section closed rather than falling back to open", async () => {
      await page.reload();
      // Read the header afresh: the reload replaced every node, so the earlier handle is stale.
      const reloaded = locators.sectionHeader(OPTIMIZE);
      await expect(reloaded).toBeVisible({ timeout: 90000 });
      await expectExpanded(reloaded, false);

      await reloaded.click();
      await expectExpanded(reloaded, true);
      const key = HomeLocators.sectionStorageKey(OPTIMIZE);
      expect(await page.evaluate((k) => window.localStorage.getItem(k), key)).toEqual("open");
    });
  }
);

test(
  "Home - read the Troubleshoot section header, verify a counted subtitle comes with the View all issues footer and the fallback subtitle comes with an empty state instead",
  { tag: ["@dev", "@regression", "@validation"] },
  async ({ page }) => {
    const { locators } = await openHome(page);
    await expectSectionSettled(locators, TROUBLESHOOT);

    const headerText = await readSectionHeaderText(locators, TROUBLESHOOT);
    const isFallback = headerText.includes(FALLBACK_SUBTITLE[TROUBLESHOOT]);

    if (isFallback) {
      // No items: CardsBlock swaps the rows for renderContent()'s empty panel and drops the
      // footer with them. Both halves are asserted — a section that quietly rendered neither
      // would otherwise read as a pass.
      await expect(locators.sectionFooter(TROUBLESHOOT)).toHaveCount(0);
      await expect(locators.sectionCard(TROUBLESHOOT)).toContainText(/quiet|I'll surface|watching/i);
    } else {
      // The counted form the subtitle takes once the section holds items:
      // "<n> issue(s)", optionally followed by "· <n> workload(s) affected".
      expect(headerText).toMatch(/\d+ issues?( · \d+ workloads? affected)?/);
      await expect(locators.sectionFooter(TROUBLESHOOT)).toHaveCount(1);
      await expect(locators.sectionFooter(TROUBLESHOOT)).toBeVisible();
    }
  }
);

test(
  "Home - click View all issues in the Troubleshoot footer, verify the troubleshoot page opens in a new tab scoped to the same account",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const { locators, accountId } = await openHome(page);
    await expectSectionSettled(locators, TROUBLESHOOT);

    const footer = locators.sectionFooter(TROUBLESHOOT);
    if ((await footer.count()) === 0) {
      // The account reported no open insights, so CardsBlock rendered its empty state and
      // no footer at all. Assert that pairing rather than the navigation, which does not
      // exist to be exercised on this data.
      await expect(locators.sectionCard(TROUBLESHOOT)).toContainText(/quiet|I'll surface|watching/i);
      return;
    }

    // The footer uses window.open(..., '_blank'), so the destination arrives as a popup
    // rather than a navigation on this page.
    const popupPromise = page.waitForEvent("popup", { timeout: 60000 });
    await footer.click();
    const popup = await popupPromise;

    await expect(popup).toHaveURL(new RegExp(`${SECTION_FOOTER_PATH[TROUBLESHOOT]}\\?`), { timeout: 90000 });
    expect(popup.url()).toContain(`accountId=${accountId}`);

    // Home itself must be untouched by the new tab.
    await expect(page).toHaveURL(/\/home/);
    await popup.close();
  }
);

test(
  "Home sanity - open Home on a K8s cluster, verify the Security & Compliance section renders alongside Troubleshoot and Optimize with its security dashboard footer",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    // The image-scan and certificate fetches decide both halves of what this test reads —
    // the subtitle's counts and, through hasExternalData, whether the footer renders. They
    // clear the same loading flag as the insight query, so the skeletons can come down
    // while they are still open; waiting for them is what stops a stale read passing.
    const { locators } = await openHome(page, HOME_SECURITY_OPS);

    // Security & Compliance is rendered only for cloud_provider === 'K8s', and openHome has
    // already established the account is one — so on this run the section is required, not optional.
    await expect(locators.sectionHeader(SECURITY)).toBeVisible({ timeout: 60000 });
    await expectSectionSettled(locators, SECURITY);
    await expectExpanded(locators.sectionHeader(SECURITY), true);

    const headerText = await readSectionHeaderText(locators, SECURITY);
    if (headerText.includes(FALLBACK_SUBTITLE[SECURITY])) {
      await expect(locators.sectionFooter(SECURITY)).toHaveCount(0);
    } else {
      // The counted form lists whichever of the three sources reported something.
      expect(headerText).toMatch(/\d+ (critical CVEs|certs expiring|ops issues)/);
      await expect(locators.sectionFooter(SECURITY)).toHaveText(SECTION_FOOTER[SECURITY]);
    }
  }
);

test(
  "Home - open the Automations card, verify each stat carries a count and the card is absent only when all three counts are zero",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    // getWorkflowData awaits all three counts before it calls setWorkflowData, so the
    // card's render decision is not final until they have all landed. A bare count() does
    // not retry, so reading it early would silently take the "absent" branch and pass.
    const { locators } = await openHome(page, HOME_WORKFLOW_OPS);
    await expect(locators.quickLinkAnchors.first()).toBeVisible({ timeout: 60000 });

    const present = (await locators.statConfigured.count()) > 0;

    if (!present) {
      // AutomationsCard returns null when totalCount, actionedCount and configuredCount are
      // all 0 — deliberately, so an all-zero row does not read as a failed fetch. It is
      // all-or-nothing, so the other two stats must be gone with it.
      await expect(locators.statTriggered).toHaveCount(0);
      await expect(locators.statEventBased).toHaveCount(0);
      await expect(locators.automationsTitle).toHaveCount(0);
      return;
    }

    await test.step("All three stats render a number, never a blank or a dash", async () => {
      for (const stat of [locators.statConfigured, locators.statTriggered, locators.statEventBased]) {
        await expect(stat).toBeVisible();
        await expect(stat).toContainText(/\d+/);
      }
      await expect(locators.statConfigured).toContainText("Configured");
      await expect(locators.statTriggered).toContainText("Triggered");
      await expect(locators.statEventBased).toContainText("Event-based");
    });

    await test.step("At least one count is non-zero, which is why the card rendered at all", async () => {
      const counts = await Promise.all(
        [locators.statConfigured, locators.statTriggered, locators.statEventBased].map(async (stat) =>
          Number(/\d+/.exec((await stat.textContent()) ?? "")?.[0] ?? 0)
        )
      );
      expect(counts.reduce((sum, n) => sum + n, 0)).toBeGreaterThan(0);
    });
  }
);

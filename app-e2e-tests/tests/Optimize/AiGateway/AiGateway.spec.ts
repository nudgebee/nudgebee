// Not for OSS
import { test, expect } from "@playwright/test";
import {
  expectScreenNotSelected,
  expectScreenSelected,
  expectToggleChecked,
  openGateway,
  openGatewayWithFragment,
  openScreen,
  parkCursor,
  selectFilterOption,
} from "./aiGatewayHelper";

// Optimize -> AI Gateway: the BYO-token gateway usage module at /optimise#ai-gateway
// (app/src/components/llm/gateway-usage/GatewayUsage.tsx). Its own screens live on an inner
// CustomTabs strip that writes #ai-gateway/<screen>, so every screen is deep-linkable.
//
// The module is read-only reporting over traffic the gateway already forwarded — its only
// write surface is the Connect screen's API-token modal, which mints a live credential
// against the shared dev tenant. This suite therefore opens no modal and creates no entity,
// so the module offers it no create or form-validation flow to cover; written up in the PR.
//
// Every screen has two legitimate bodies — its table, or the empty state its view renders in
// place of one — because what the shared dev tenant has forwarded through the gateway is not
// under test. So the assertions here are on the module's own behaviour: which screen the
// strip and the URL agree on, which controls each screen admits, whether a filter selection
// survives a screen switch, and what a filter or a search actually does to the list. Those
// hold whether or not the tenant has gateway traffic in the window.
const SPEC_TIMEOUT_MS = 180000;

// Not a screen the module knows — subTabFromHash() in GatewayUsage.tsx checks the
// sub-fragment against TAB_IDS before honouring it, and this is what that check must reject.
const UNKNOWN_SCREEN_FRAGMENT = "not-a-screen";

// A session id no gateway request can carry, so the Sessions search has nothing to match.
// Suffixed per run rather than pinned, so two runs never share a query the backend cached.
const ABSENT_SESSION_ID = `e2e-no-such-session-${Date.now()}`;

test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test.describe("AI Gateway", () => {
  test(
    "AI Gateway sanity - open Optimise on the AI Gateway tab with no sub-fragment, verify the screen strip lists Connect, Overview, Models, Users, Requests, Sessions, Tools and Governance and opens on Overview",
    { tag: ["@dev", "@test", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await openGateway(page);

      await test.step("Every screen the module declares is on the strip", async () => {
        const strip = [
          { screen: "connect", name: "Connect" },
          { screen: "overview", name: "Overview" },
          { screen: "models", name: "Models" },
          { screen: "users", name: "Users" },
          { screen: "requests", name: "Requests" },
          { screen: "sessions", name: "Sessions" },
          { screen: "tools", name: "Tools" },
          { screen: "governance", name: "Governance" },
        ] as const;
        for (const { screen, name } of strip) {
          await expect(locators.screenTab(screen)).toBeVisible();
          await expect(locators.screenTab(screen)).toContainText(name);
        }
      });

      await test.step("A bare #ai-gateway opens on Overview, not on the strip's first tab", async () => {
        // DEFAULT_TAB is 'overview' while Connect sits first on the strip, so this pins the
        // declared default rather than whichever tab happens to be leftmost.
        await expectScreenSelected(locators, "overview");
        await expectScreenNotSelected(locators, "connect");
      });
    }
  );

  test(
    "AI Gateway sanity - open the Overview screen, verify the KPI row, the usage-over-time chart and the provider, model and user breakdown tables all render",
    { tag: ["@dev", "@test", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await openGateway(page, "overview");

      // OverviewView renders all five unconditionally once the aggregate call settles — the
      // breakdowns draw their own no-data body rather than being withheld — so each one
      // missing is a real regression, not an artefact of what this tenant forwarded.
      await expect(locators.kpiRow).toBeVisible({ timeout: 60000 });
      await expect(locators.usageOverTime).toBeVisible({ timeout: 60000 });
      await expect(locators.providerTable).toBeVisible({ timeout: 60000 });
      await expect(locators.modelTable).toBeVisible({ timeout: 60000 });
      await expect(locators.userTable).toBeVisible({ timeout: 60000 });
    }
  );

  test(
    "AI Gateway - open Overview, click through to Models and then Sessions, click back to Overview, verify each screen becomes the selected one and the URL sub-fragment follows it",
    { tag: ["@dev", "@test", "@smoke", "@functional"] },
    async ({ page }) => {
      const locators = await openGateway(page, "overview");
      await parkCursor(page);

      for (const screen of ["models", "sessions", "overview"] as const) {
        await test.step(`The ${screen} screen opens and owns the URL`, async () => {
          await openScreen(locators, screen);
          // GatewayUsage writes #ai-gateway/<screen> on every tab change so a screen can be
          // shared as a link and survives a reload — a real product contract, not incidental
          // state, and the next test reloads against it.
          await page.waitForURL(new RegExp(`#ai-gateway/${screen}`), { timeout: 30000 });
        });
      }

      // Back on Overview, its own content is what replaced the Sessions body.
      await expect(locators.kpiRow).toBeVisible({ timeout: 60000 });
    }
  );

  test(
    "AI Gateway - open the Tools screen from the strip, reload the page, verify the reader lands back on Tools rather than on the default Overview screen",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openGateway(page, "overview");
      await parkCursor(page);

      await openScreen(locators, "tools");
      await page.waitForURL(/#ai-gateway\/tools/, { timeout: 30000 });

      await page.reload();

      // The sub-tab is seeded from the hash on mount, so the URL the previous step wrote is
      // what has to bring the reader back — this is the state actually surviving the reload,
      // not the click being replayed.
      await expect(locators.root).toBeVisible({ timeout: 60000 });
      await expectScreenSelected(locators, "tools");
      await expect(locators.kpiRow).toHaveCount(0);
    }
  );

  test(
    "AI Gateway - deep link straight to #ai-gateway/requests, verify the Requests screen opens without passing through Overview",
    { tag: ["@dev", "@test", "@smoke", "@functional"] },
    async ({ page }) => {
      const locators = await openGateway(page, "requests");

      await expectScreenSelected(locators, "requests");
      await expectScreenNotSelected(locators, "overview");

      // Overview's own widgets must not be on screen — they are what a fall-back to the
      // default screen would have rendered, so their absence is what makes this a deep-link
      // assertion rather than a repeat of the sanity test.
      await expect(locators.kpiRow).toHaveCount(0);
      await expect(locators.requestsStatusFilter).toBeVisible({ timeout: 60000 });
    }
  );

  test(
    `AI Gateway - deep link to #ai-gateway/${UNKNOWN_SCREEN_FRAGMENT}, verify the unknown sub-fragment is rejected and the Overview screen opens instead of an empty body`,
    { tag: ["@dev", "@test", "@regression", "@negative"] },
    async ({ page }) => {
      const locators = await openGatewayWithFragment(page, UNKNOWN_SCREEN_FRAGMENT);

      // subTabFromHash() resolves an unrecognised sub-fragment to DEFAULT_TAB, so it must
      // leave Overview in place rather than select nothing and render blank.
      await expectScreenSelected(locators, "overview");
      await expect(locators.kpiRow).toBeVisible({ timeout: 60000 });
    }
  );

  test(
    "AI Gateway - open Overview, switch the chart metric from Cost to Requests, verify Requests becomes the checked option, Cost is released and the chart heading reads Requests over time",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openGateway(page, "overview");

      const cost = locators.toggleOption(locators.metricGroup, "Cost");
      const requests = locators.toggleOption(locators.metricGroup, "Requests");

      await expect(locators.metricGroup).toBeVisible({ timeout: 60000 });
      await expectToggleChecked(cost, true);

      await requests.click();

      await expectToggleChecked(requests, true);
      await expectToggleChecked(cost, false);

      // SectionHeader is written from the selected metric, so the heading is the readable
      // proof the toggle reached the chart rather than only restyling itself.
      await expect(locators.chartHeading("Requests")).toBeVisible({ timeout: 30000 });
      await expect(locators.chartHeading("Cost")).toHaveCount(0);
    }
  );

  test(
    "AI Gateway - set the chart granularity to Hour on Overview, open the Governance screen, verify the Hour selection carries across the screen switch",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openGateway(page, "overview");
      await parkCursor(page);

      await expect(locators.granularityGroup).toBeVisible({ timeout: 60000 });
      const hour = locators.toggleOption(locators.granularityGroup, "Hour");
      await hour.click();
      await expectToggleChecked(hour, true);

      await openScreen(locators, "governance");

      // Granularity lives on the shell's shared GatewayFilters, so a screen switch must
      // re-render the toggle on the carried value rather than resetting it to the default.
      // The bar is re-rendered by the switch, so this is read back off the new group, not
      // the one clicked above.
      await expect(locators.granularityGroup).toBeVisible({ timeout: 60000 });
      await expectToggleChecked(locators.toggleOption(locators.granularityGroup, "Hour"), true);
      await expectToggleChecked(locators.toggleOption(locators.granularityGroup, "Day"), false);
    }
  );

  test(
    "AI Gateway - open the Connect screen, verify the shared date and granularity bar is withheld and the token controls take its place, then return to Overview and verify the bar comes back",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openGateway(page, "overview");
      await parkCursor(page);

      await expect(locators.filterBar).toBeVisible({ timeout: 60000 });

      await test.step("Connect drops the bar — a setup screen reads no date window", async () => {
        await openScreen(locators, "connect");
        await expect(locators.connectRoot).toBeVisible({ timeout: 60000 });
        await expect(locators.filterBar).toHaveCount(0);
        await expect(locators.generateTokenBtn).toBeVisible();
      });

      await test.step("Leaving Connect restores the bar for the data screens", async () => {
        await openScreen(locators, "overview");
        await expect(locators.filterBar).toBeVisible({ timeout: 60000 });
        await expect(locators.connectRoot).toHaveCount(0);
      });
    }
  );

  test(
    "AI Gateway - open the Sessions screen, search for a session id that no request can carry, verify the list resolves to the No sessions empty state",
    { tag: ["@dev", "@test", "@regression", "@negative", "@search"] },
    async ({ page }) => {
      const locators = await openGateway(page, "sessions");

      await expect(locators.sessionsSearch).toBeVisible({ timeout: 60000 });
      await locators.sessionsSearch.fill(ABSENT_SESSION_ID);
      await expect(locators.sessionsSearch).toHaveValue(ABSENT_SESSION_ID);

      // SessionsView debounces the box by 400ms before it re-queries, and the expect below
      // retries across that window — a term nothing can match must leave the empty state, not
      // a stale table, as the settled body.
      await expect(locators.sessionsEmpty).toBeVisible({ timeout: 60000 });
      await expect(locators.sessionsBody).toHaveCount(0);
    }
  );

  test(
    "AI Gateway - open the Requests screen, set the Status filter to Error, verify the trigger reports Error and the list settles with no 2xx request left in it",
    { tag: ["@dev", "@test", "@regression", "@search"] },
    async ({ page }) => {
      const locators = await openGateway(page, "requests");

      await expect(locators.requestsStatusFilter).toBeVisible({ timeout: 60000 });
      await selectFilterOption(locators, locators.requestsStatusFilter, "Error");
      await expect(locators.requestsStatusFilter).toContainText("Error");

      // Two bodies are legitimate here and neither is a missing element: a tenant with error
      // traffic in the window gets rows, one without gets the filtered empty state. Polled
      // rather than read once, because CustomTable keeps the id'd tbody attached for the
      // render between the selection and the loading swap — a single read can catch the
      // pre-filter rows.
      await expect
        .poll(
          async () => {
            if (await locators.requestsFilteredEmpty.isVisible()) return "filtered-empty";
            const codes = await locators.requestsStatusCells.allInnerTexts();
            // A blank cell is never a settled read: allInnerTexts() returns "" for a cell
            // that is not currently rendered, and "" parses to NaN, which the 2xx filter
            // drops — so without this the poll could answer "errors-only" off a row that
            // had not painted yet. StatusPill always writes either the code or an em-dash,
            // so no real row is excluded by it.
            if (!codes.length || codes.some((raw) => !raw.trim())) return "unsettled";
            const twoXx = codes
              .map((raw) => Number.parseInt(raw.trim(), 10))
              .filter((code) => !Number.isNaN(code) && code >= 200 && code <= 299);
            return twoXx.length ? "unsettled" : "errors-only";
          },
          {
            message: "The Requests list never settled on error-only rows or the filtered empty state after Status=Error",
            timeout: 60000,
          }
        )
        .not.toBe("unsettled");

      // The unfiltered empty body is titled "No requests in this range" and offers "View last
      // 30 days" instead, so the Clear filters action is what proves the filter reached the
      // query rather than the window simply being empty.
      if (await locators.requestsFilteredEmpty.isVisible()) {
        await expect(locators.requestsClearFiltersBtn).toBeVisible();
      }
    }
  );
});

// Not for OSS
import { test, expect } from "@playwright/test";
import { CONVERSATIONS_TABLE } from "./llmAnalyserLocators";
import {
  expectScreenNotSelected,
  expectScreenSelected,
  expectToggleChecked,
  openAnalyser,
  openAnalyserWithFragment,
  openScreen,
  parkCursor,
} from "./llmAnalyserHelper";

// Optimize -> LLM Analyser: the AI cost and usage module at /optimise#cost-analyser
// (app/src/components/llm/cost-analyser/CostAnalyser.tsx). Its own screens live on an inner
// CustomTabs strip that writes #cost-analyser/<screen>, so every screen is deep-linkable.
//
// The module is read-only analytics over what the tenant's assistants already spent — the
// only write it offers is the Cost Report schedule modal, which is gated on both the
// AI_COST_REPORT tenant flag and a tenant-wide role, and which posts a recurring digest into
// a shared Slack channel. This suite therefore never opens that modal, so the module has no
// create or form-validation surface it can reach; written up in the PR.
//
// Every screen has two legitimate bodies — its table, or the empty state its view renders in
// place of one — because what the shared dev tenant has spent is not under test. So the
// assertions here are on the module's own behaviour: which screen the strip and the URL
// agree on, which filters each screen admits, and whether a filter selection survives a
// screen switch and a Reset. Those hold whether or not the tenant has AI usage in the window.
const SPEC_TIMEOUT_MS = 180000;

// Not a screen the module knows — CostAnalyser.tsx checks the sub-fragment against
// VALID_TAB_IDS before honouring it, and this is what that check must reject.
const UNKNOWN_SCREEN_FRAGMENT = "not-a-screen";

test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test.describe("LLM Analyser", () => {
  test(
    "LLM Analyser sanity - open Optimise on the LLM Analyser tab with no sub-fragment, verify the screen strip lists Overview, Conversations, Models, Agents, Tools and Users and opens on Overview",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await openAnalyser(page);

      await test.step("Every screen the module declares is on the strip", async () => {
        // tabOptions in CostAnalyser.tsx. Cost Report is excluded — it is admitted only for
        // isTenantWideRole(), so asserting it would test the run user's role, not the module.
        const strip = [
          { screen: "overview", name: "Overview" },
          { screen: "conversations", name: "Conversations" },
          { screen: "models", name: "Models" },
          { screen: "agents", name: "Agents" },
          { screen: "tools", name: "Tools" },
          { screen: "users", name: "Users" },
          { screen: "critiques", name: "Critiques" },
        ] as const;
        for (const { screen, name } of strip) {
          await expect(locators.screenTab(screen)).toBeVisible();
          await expect(locators.screenTab(screen)).toContainText(name);
        }
      });

      await test.step("A bare #cost-analyser opens on Overview", async () => {
        await expectScreenSelected(locators, "overview");
        await expectScreenNotSelected(locators, "conversations");
      });
    }
  );

  test(
    "LLM Analyser sanity - open the Overview screen, verify the KPI row, the cost-over-time chart, the cost breakdown widgets and the top-conversations table all render",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await openAnalyser(page, "overview");

      // OverviewView renders all four unconditionally once the metrics call settles — they
      // draw their own zero/empty state rather than being withheld — so each one missing is
      // a real regression, not an artefact of what this tenant spent.
      await expect(locators.kpiRow).toBeVisible({ timeout: 60000 });
      await expect(locators.costOverTime).toBeVisible({ timeout: 60000 });
      await expect(locators.breakdowns).toBeVisible({ timeout: 60000 });
      await expect(locators.topConversationsTable).toBeVisible({ timeout: 60000 });
    }
  );

  test(
    "LLM Analyser - open Overview, click through to Conversations and then Models, click back to Overview, verify each screen becomes the selected one and the URL sub-fragment follows it",
    { tag: ["@dev", "@smoke", "@functional"] },
    async ({ page }) => {
      const locators = await openAnalyser(page, "overview");
      await parkCursor(page);

      for (const screen of ["conversations", "models", "overview"] as const) {
        await test.step(`The ${screen} screen opens and owns the URL`, async () => {
          await openScreen(locators, screen);
          // CustomTabs' behavior='router' mode renders each tab as a Next Link whose href
          // carries the parent and child fragments, so the URL is a real product contract
          // here (the Slack digest links into it), not incidental state.
          await page.waitForURL(new RegExp(`#cost-analyser/${screen}`), { timeout: 30000 });
        });
      }

      // Back on Overview, its own content is what replaced the Models body.
      await expect(locators.kpiRow).toBeVisible({ timeout: 60000 });
    }
  );

  test(
    "LLM Analyser - move from Overview to Agents to Users to Critiques, verify the filter bar drops its Agent picker on Agents, drops its User picker on Users, and disappears entirely on Critiques",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openAnalyser(page, "overview");
      await parkCursor(page);

      await test.step("Overview offers both the Agent and the User picker", async () => {
        await expect(locators.filterBar).toBeVisible({ timeout: 60000 });
        await expect(locators.agentFilter).toBeVisible();
        await expect(locators.userFilter).toBeVisible();
      });

      await test.step("Agents hides the shared Agent picker, keeping its own", async () => {
        await openScreen(locators, "agents");
        await expect(locators.filterBar).toBeVisible();
        await expect(locators.agentFilter).toHaveCount(0);
        await expect(locators.userFilter).toBeVisible();
      });

      await test.step("Users hides the User picker rather than collapsing its own leaderboard", async () => {
        await openScreen(locators, "users");
        await expect(locators.filterBar).toBeVisible();
        await expect(locators.userFilter).toHaveCount(0);
        await expect(locators.agentFilter).toBeVisible();
      });

      await test.step("Critiques drops the whole bar — it carries its own cross-tenant filters", async () => {
        await openScreen(locators, "critiques");
        await expect(locators.filterBar).toHaveCount(0);
      });
    }
  );

  test(
    "LLM Analyser - open Overview, switch the chart granularity from day to week, verify week becomes the checked option and day is released",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openAnalyser(page, "overview");

      const day = locators.toggleOption(locators.granularityGroup, "Day");
      const week = locators.toggleOption(locators.granularityGroup, "Week");

      await expect(locators.granularityGroup).toBeVisible({ timeout: 60000 });
      await expectToggleChecked(day, true);

      await week.click();

      await expectToggleChecked(week, true);
      await expectToggleChecked(day, false);
    }
  );

  test(
    "LLM Analyser - set the granularity to week on Overview, open the Models screen, verify the week selection carries across the screen switch",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openAnalyser(page, "overview");
      await parkCursor(page);

      const week = locators.toggleOption(locators.granularityGroup, "Week");
      await expect(locators.granularityGroup).toBeVisible({ timeout: 60000 });
      await week.click();
      await expectToggleChecked(week, true);

      await openScreen(locators, "models");

      // Granularity lives on the shared CostFilters object, so Models — the only other
      // screen that reads time_series — must re-render the toggle on the carried value
      // rather than resetting it to the default. The group is re-mounted by the screen
      // switch, so this is read back off the new one, not the one clicked above.
      await expect(locators.granularityGroup).toBeVisible({ timeout: 60000 });
      await expectToggleChecked(locators.toggleOption(locators.granularityGroup, "Week"), true);
      await expectToggleChecked(locators.toggleOption(locators.granularityGroup, "Day"), false);
    }
  );

  test(
    "LLM Analyser - set the granularity to week on Overview, click Reset, verify the granularity returns to the default day and the reader stays on Overview",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openAnalyser(page, "overview");

      const day = locators.toggleOption(locators.granularityGroup, "Day");
      const week = locators.toggleOption(locators.granularityGroup, "Week");

      await expect(locators.granularityGroup).toBeVisible({ timeout: 60000 });
      await week.click();
      await expectToggleChecked(week, true);

      await locators.resetButton.click();

      // reset() in CostAnalyser.tsx restores defaultFilters(), whose granularity is 'day'.
      // It is a filter-state reset only — it must not move the reader off the screen they
      // were reading, which is what the second assertion pins.
      await expectToggleChecked(day, true);
      await expectToggleChecked(week, false);
      await expectScreenSelected(locators, "overview");
    }
  );

  test(
    "LLM Analyser - open the Conversations screen, switch the preset from All to Top 5 by cost, verify the preset becomes the selected one, the caption names it, and the table lists no more than five conversations",
    { tag: ["@dev", "@regression", "@search"] },
    async ({ page }) => {
      const locators = await openAnalyser(page, "conversations");

      const all = locators.toggleOption(locators.conversationPresets, "All");
      const topByCost = locators.toggleOption(locators.conversationPresets, "Top 5 by cost");

      await expect(locators.conversationPresets).toBeVisible({ timeout: 60000 });
      await expectToggleChecked(all, true);

      await topByCost.click();
      await expectToggleChecked(topByCost, true);
      await expectToggleChecked(all, false);

      // ConversationsView re-queries with limit=PRESET_ROWS, so the caption is written from
      // the rows that came back — it is the readable proof the preset reached the request
      // rather than only the toggle's styling.
      await expect(locators.root.getByText(/Top \d+ by cost of/)).toBeVisible({ timeout: 60000 });

      await locators.waitForTable(CONVERSATIONS_TABLE, locators.conversationsEmpty);

      // Polled, not a bare count(): the preset refetches, and CustomTable keeps the id'd
      // tbody attached for the render between the click and the loading swap — so a
      // one-shot read can catch the pre-filter rows. How many rows come back is tenant
      // data, so the settled state is the cap the preset promises, not an exact number.
      await expect
        .poll(async () => locators.conversationsRows.count(), {
          message: "The conversations table never settled at or below the preset's five-row cap",
          timeout: 30000,
        })
        .toBeLessThanOrEqual(5);
    }
  );

  test(
    "LLM Analyser - deep link straight to #cost-analyser/models, verify the Models screen opens without passing through Overview",
    { tag: ["@dev", "@smoke", "@functional"] },
    async ({ page }) => {
      const locators = await openAnalyser(page, "models");

      await expectScreenSelected(locators, "models");
      await expectScreenNotSelected(locators, "overview");

      // Overview's own widgets must not be on screen — they are what a fall-back to the
      // default screen would have rendered, so their absence is what makes this a deep-link
      // assertion rather than a repeat of the sanity test.
      await expect(locators.kpiRow).toHaveCount(0);
    }
  );

  test(
    `LLM Analyser - deep link to #cost-analyser/${UNKNOWN_SCREEN_FRAGMENT}, verify the unknown sub-fragment is rejected and the Overview screen opens instead of an empty body`,
    { tag: ["@dev", "@regression", "@negative"] },
    async ({ page }) => {
      const locators = await openAnalyserWithFragment(page, UNKNOWN_SCREEN_FRAGMENT);

      // VALID_TAB_IDS in CostAnalyser.tsx gates the sub-fragment, so an unrecognised one
      // must leave the default screen in place rather than select nothing and render blank.
      await expectScreenSelected(locators, "overview");
      await expect(locators.kpiRow).toBeVisible({ timeout: 60000 });
    }
  );
});

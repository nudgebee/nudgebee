// Not for OSS
import { test, expect } from "@playwright/test";
import {
  RERUNNABLE_STATUSES,
  SCOREBOARD_LABELS,
  openDigestsTab,
  railIncidentCount,
  remountDigestsTab,
  requireDigestWeeks,
  requireFindings,
  waitForNewWeekRead,
  waitForWeekReview,
} from "./digestsHelper";

// Nubi > b-Cortex > Insights > Digests (app/src/ee/components/memory2/DigestsTab.jsx).
// The tab is read-only apart from one control, "Generate this week again", and that run
// is tenant-wide and billable by the component's own description, so nothing here starts
// one — the re-run case asserts which weeks the control is offered for and stops there.
// That leaves the suite with no fixtures to create and nothing to clean up, so every case
// is order-free and safe to run twice.
//
// Digests is the Insights group's default sub-tab, so selecting the group lands on it.
// The cases below depend on the tenant having at least one generated week; the digest
// generator runs every six hours and backfills missed weeks, but requireDigestWeeks()
// still says so by name rather than letting an ungenerated tenant surface as a timeout.
test.describe("Nubi Digests Tab", () => {
  test(
    "Nubi Digests sanity - open b-Cortex, select the Insights group, verify Digests is the sub-tab that lands and its title and weekly-review description render",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openDigestsTab(page);

      await expect(locators.tabDescription).toBeVisible({ timeout: 20000 });
      await expect(locators.headerTitle).toHaveText("Digests", { timeout: 15000 });
      await expect(locators.digestsTab).toBeVisible({ timeout: 15000 });
    }
  );

  test(
    "Nubi Digests sanity - open the Digests tab, verify it settles on either the week rail or the no-digests-yet empty state and never on the load-failure state",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openDigestsTab(page);

      // DigestsTab tracks a failed list read separately from an empty one so a broken
      // backend cannot read as "this tenant has nothing yet". Asserting the failure state
      // is absent is what keeps the branch below from passing on a broken environment.
      await expect(locators.listErrorState).toHaveCount(0);

      const weeks = await locators.weekIncidentCounts.count();
      if (weeks === 0) {
        await expect(locators.emptyState).toBeVisible({ timeout: 15000 });
      } else {
        await expect(locators.emptyState).toHaveCount(0);
        // The rail and its count lines are rendered together, one per stored week, so a
        // mismatch means a week rendered without its button or without its figures.
        await expect(locators.weekButtons).toHaveCount(weeks);
      }
    }
  );

  test(
    "Nubi Digests sanity - open the Digests tab, verify every week in the rail carries a date range and a readable incident count",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openDigestsTab(page);
      const weeks = await requireDigestWeeks(locators, 1);

      await expect(locators.weekButtons).toHaveCount(weeks);

      for (let index = 0; index < weeks; index++) {
        // The range is rendered as "Mon D – Mon D" with an en dash, and both halves come
        // from stored dates — a blank one means the row rendered without its period.
        await expect(locators.weekDateRange(index)).not.toBeEmpty();
        await expect(locators.weekDateRange(index)).toContainText("–");

        // Throws with the offending line when the count is not a number, so an unreadable
        // rail entry fails here rather than further down some other case.
        const incidents = await railIncidentCount(locators, index);
        expect(incidents).toBeGreaterThanOrEqual(0);
      }
    }
  );

  test(
    "Nubi Digests - open the Digests tab, verify the selected week's scoreboard renders all four review figures with values",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openDigestsTab(page);
      await requireDigestWeeks(locators, 1);
      await waitForWeekReview(locators);

      for (const label of SCOREBOARD_LABELS) {
        await expect(locators.scoreboardLabel(label)).toBeVisible({ timeout: 20000 });
        // The tile's figure, read as the label's preceding sibling rather than
        // positionally — DigestsTab renders the value above the label in each Card.
        await expect(locators.scoreboardValue(label)).not.toBeEmpty();
      }
    }
  );

  test(
    "Nubi Digests - open the Digests tab, verify the secondary figures line reports recurrence, new incidents, failure classes, P1 share and learnings",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openDigestsTab(page);
      await requireDigestWeeks(locators, 1);
      await waitForWeekReview(locators);

      // Matched as one whole string: DigestsTab joins all five figures unconditionally,
      // so a dropped segment is a regression rather than a section this week lacks.
      await expect(locators.secondaryFigures).toHaveCount(1, { timeout: 20000 });
      await expect(locators.secondaryFigures).toBeVisible();
    }
  );

  test(
    "Nubi Digests - select the second week in the rail, verify the tab requests that week's review and re-renders the scoreboard for it",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(210000);
      const { locators, log } = await openDigestsTab(page);
      await requireDigestWeeks(locators, 2);
      await waitForWeekReview(locators);

      expect(log.periods.length).toBeGreaterThan(0);
      const landedOn = log.periods[log.periods.length - 1];
      const seen = log.periods.length;

      await locators.weekButton(1).click();

      // The rail shows "Sep 1 – Sep 7" while the digest's storage key is a YYYY-MM-DD the
      // DOM never carries, so the completed read is the only place the newly selected week
      // is observable — and fetching a different key is exactly what the click must do.
      const requested = await waitForNewWeekRead(log, seen);
      expect(requested).not.toBe(landedOn);

      await waitForWeekReview(locators);
      await expect(locators.scoreboardLabel(SCOREBOARD_LABELS[0])).toBeVisible({ timeout: 20000 });
    }
  );

  test(
    "Nubi Digests - expand the first incident under What broke & why, verify the row opens and its body names the alert class",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(210000);
      const { locators } = await openDigestsTab(page);
      await requireDigestWeeks(locators, 1);
      await waitForWeekReview(locators);
      await requireFindings(locators, 1);

      await expect(locators.whatBrokeHeading).toBeVisible({ timeout: 20000 });

      const firstFinding = locators.findingSummaries.first();
      await expect(firstFinding).toHaveAttribute("aria-expanded", "false", { timeout: 15000 });

      await firstFinding.click();
      await expect(firstFinding).toHaveAttribute("aria-expanded", "true", { timeout: 20000 });

      // ds/Accordion is selection='multi' here, so one click must open exactly one row.
      await expect(locators.openFindingBodies).toHaveCount(1, { timeout: 20000 });

      // The alert class is the finding's aggregation key — the one labelled row that is
      // always populated, since it is also the key the accordion item is identified by.
      const alertClassLabel = locators.openFindingBodies.first().getByText("Alert class", { exact: true });
      await expect(alertClassLabel).toBeVisible({ timeout: 15000 });
      await expect(alertClassLabel.locator("xpath=following-sibling::*[1]")).not.toBeEmpty();
    }
  );

  test(
    "Nubi Digests - expand an incident and click it again, verify the row collapses and its body is no longer shown",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(210000);
      const { locators } = await openDigestsTab(page);
      await requireDigestWeeks(locators, 1);
      await waitForWeekReview(locators);
      await requireFindings(locators, 1);

      const firstFinding = locators.findingSummaries.first();
      await firstFinding.click();
      await expect(firstFinding).toHaveAttribute("aria-expanded", "true", { timeout: 20000 });
      await expect(locators.openFindingBodies).toHaveCount(1, { timeout: 20000 });

      await firstFinding.click();

      await expect(firstFinding).toHaveAttribute("aria-expanded", "false", { timeout: 20000 });
      // MUI keeps a collapsed panel mounted but hidden, so this counts visible bodies
      // rather than rendered ones — a detached-only check would pass while it is on screen.
      await expect(locators.openFindingBodies).toHaveCount(0, { timeout: 20000 });
    }
  );

  test(
    "Nubi Digests - leave Digests for the Memory group and come back, verify the tab refetches and lands on the newest week again",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const { locators, log } = await openDigestsTab(page);
      await requireDigestWeeks(locators, 1);
      await waitForWeekReview(locators);

      expect(log.periods.length).toBeGreaterThan(0);
      // The first read of the session is the newest week: DigestsTab selects rows[0] as
      // soon as the list resolves, so this is the key a fresh mount has to come back to.
      const newestWeek = log.periods[0];
      const seen = log.periods.length;

      await remountDigestsTab(locators);

      const refetched = await waitForNewWeekRead(log, seen);
      expect(refetched).toBe(newestWeek);

      await waitForWeekReview(locators);
      await expect(locators.scoreboardLabel(SCOREBOARD_LABELS[0])).toBeVisible({ timeout: 20000 });
    }
  );

  test(
    "Nubi Digests - open the Digests tab, verify a fully generated week offers no re-run control and any control on offer belongs to a failed or partial week",
    { tag: ["@dev", "@regression", "@validation"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openDigestsTab(page);
      await requireDigestWeeks(locators, 1);
      await waitForWeekReview(locators);

      // The rail entry only carries a status chip when its digest is not 'generated', so
      // the chip's absence is what identifies a healthy week.
      const statusChips = await locators.weekStatusChip(0).count();
      const rerunControls = await locators.rerunWeekBtn.count();

      if (statusChips === 0) {
        expect(rerunControls).toBe(0);
      } else {
        const status = ((await locators.weekStatusChip(0).first().textContent()) ?? "").trim();
        expect(RERUNNABLE_STATUSES).toContain(status);
      }

      // The other direction, which holds whether or not the signed-in user is the tenant
      // admin the control is additionally gated on: a re-run is never offered for a week
      // that generated cleanly.
      if (rerunControls > 0) {
        expect(statusChips).toBeGreaterThan(0);
      }
    }
  );
});

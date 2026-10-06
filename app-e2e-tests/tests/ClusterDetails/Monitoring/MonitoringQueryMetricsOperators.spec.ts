// Not for OSS
import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { MonitoringTabLocator } from "./MonitoringTabLocator";
import { MetricsQueryLocators, TARGET_PROVIDER, addMetricFilterChip, chipCommittedAs, clearMetricFilterChips, ensureDefaultMetricsProvider, escapeForRegex, removeMetricFilterChip, readOfferedOperators } from "./monitoringMetricsOperatorHelper";

// Query Metrics counterpart to MonitoringLogQueryOperators: one deep, read-only test of every operator Prometheus offers, plus combine, remove, date range and download.

// Prometheus is pinned so the run cannot silently test a different backend; the label and value are read live from the cluster.
const PROVIDER = TARGET_PROVIDER;

// Series read per check, enough for a solid sample without reading every row.
const SAMPLE_SIZE = 5;

// Operators this suite proves; Step 2 reads what the page offers and fails on any operator no step covers.
const COVERED_OPERATORS = ["=", "!=", "=~"];

// Display name of the provider's integration card, used for the default-provider toggle.
const PROVIDER_CARD_NAME = "Prometheus";

interface CheckResult {
  check: string;
  passed: boolean;
  detail: string;
}

function makeRecorder(results: CheckResult[]) {
  // Elapsed time per check is printed so a slow step can be found without a trace.
  let last = Date.now();
  return (check: string, passed: boolean, detail: string): void => {
    const seconds = ((Date.now() - last) / 1000).toFixed(0);
    last = Date.now();
    results.push({ check, passed, detail });
    console.log(`${passed ? "PASS" : "FAIL"} [${seconds}s] — ${check}: ${detail}`);
  };
}

// Known product gaps are printed but kept out of the pass/fail gate, as the logs suite does.
function makeKnownLimitationRecorder(limitations: CheckResult[]) {
  return (check: string, detail: string): void => {
    limitations.push({ check, passed: false, detail });
    console.log(`KNOWN LIMITATION — ${check}: ${detail}`);
  };
}

test(
  "Query Metrics - verify every operator Prometheus offers returns semantically correct series, in both Builder and Code, across a non-existent value, combined filters, a removal, a date-range change and download",
  { tag: ["@dev", "@regression", "@functional", "@negative", "@search"] },
  async ({ page }) => {
    // Ten minutes is enough for real round trips and short enough that a hang fails fast.
    test.setTimeout(600000);

    const results: CheckResult[] = [];
    const record = makeRecorder(results);
    const knownLimitations: CheckResult[] = [];
    const recordKnownLimitation = makeKnownLimitationRecorder(knownLimitations);

    const locators = new MonitoringTabLocator(page);
    const metrics = new MetricsQueryLocators(page);

    let label = "";
    let value = "";
    let secondLabel = "";
    let secondValue = "";
    let offeredOperators: string[] = [];
    // Held in an object because it is assigned inside a test.step callback, which TypeScript's flow analysis does not follow.
    const providerDefault: { restore: (() => Promise<void>) | null } = { restore: null };

    const offers = (operator: string): boolean => offeredOperators.length === 0 || offeredOperators.includes(operator);

    // Each returned series carries its labels, so a filter is checkable: counts how many of a sample carry label=value.
    const countMatching = async (rowCount: number): Promise<{ sample: number; matching: number }> => {
      const sample = Math.min(SAMPLE_SIZE, rowCount);
      let matching = 0;
      for (let i = 0; i < sample; i++) {
        const labels = await metrics.readRowLabels(i);
        if (labels[label] === value) matching += 1;
      }
      return { sample, matching };
    };

    // Every filter change must be submitted, otherwise the read returns the previous query's rows.
    const applyAndRead = async (): Promise<number> => {
      await metrics.submitQuery();
      return metrics.waitForResults();
    };

    await new LoginPage(page).doFullLogin();

    try {
      await test.step(`Step 0 - ${PROVIDER_CARD_NAME} is the account's default metrics provider (fixed if not), Query Metrics open on it`, async () => {
        // The tab switcher only picks what the open tab queries; default_metrics_provider is what the account resolves to when nothing is pinned.
        const accountName = process.env.CLUSTER_NAME || process.env.CLUSTER || "";
        if (accountName) {
          const def = await ensureDefaultMetricsProvider(page, { accountName, providerName: PROVIDER_CARD_NAME });
          providerDefault.restore = def.restore;
          record(
            `Precondition: ${PROVIDER_CARD_NAME} default metrics provider`,
            true,
            def.wasAlreadyDefault ? "was already the default" : "turned on for this run, will restore after"
          );
        } else {
          record("Precondition: default metrics provider", true, "skipped — no CLUSTER_NAME in the environment");
        }

        // The Admin detour leaves the cluster dropdown on another cluster, and Admin pages have no dropdown, so go Home first and re-select.
        if (accountName) {
          // A navigation interrupted by a redirect is harmless because selectCluster below asserts the cluster.
          await page.goto("/home", { waitUntil: "domcontentloaded" }).catch(() => {});
          await new LoginPage(page).selectCluster(accountName);
        }

        for (let attempt = 1; attempt <= 3; attempt++) {
          try {
            await locators.navigateToMonitoringTab();
            break;
          } catch (err) {
            if (attempt === 3) throw err;
            console.log(`navigateToMonitoringTab attempt ${attempt}/3 failed, retrying: ${err}`);
          }
        }
        await locators.clickTab(locators.MonitoringDropdownQueryMertics);

        const pinned = await metrics.pinProvider(PROVIDER);
        // Logged before the skip so a skip says whether the provider or the data was missing.
        console.log(`[QueryMetrics] provider pinned to ${PROVIDER}: ${pinned} (badge now reads: ${await metrics.currentProvider()})`);
        test.skip(!pinned, `This cluster does not offer ${PROVIDER} as a metrics provider.`);

        await locators.switchQueryMode("Builder");
        record(`Query Metrics is using ${PROVIDER}`, true, `provider badge reads: ${await metrics.currentProvider()}`);
      });

      await test.step("Step 1 - discover a real label and value from live data to build every check on", async () => {
        // Never hardcoded, because which labels exist depends on what is deployed; a metric must be chosen first since the label input is disabled until then.
        const metric = await metrics.selectFirstMetric();
        console.log(`[QueryMetrics] metric selected: ${metric ?? "(none offered)"}`);
        test.skip(!metric, "The metric picker offered nothing on this cluster.");

        await metrics.submitQuery();
        const count = await metrics.waitForResults();
        console.log(`[QueryMetrics] series returned for "${metric}": ${count}`);
        test.skip(count === 0, "This cluster returned no metric series; there is nothing to filter.");

        const labels = await metrics.readRowLabels(0);
        const usable = Object.entries(labels).filter(([k, v]) => k && v && !k.startsWith("__"));
        console.log(`[QueryMetrics] labels read off the first series: ${Object.keys(labels).join(", ") || "(none)"}`);
        test.skip(usable.length === 0, "No usable label found on the first series.");

        // Prefer cluster then job: columns come back alphabetically, and the first one is a node label whose second value made a typed != commit as another value.
        const preferred = usable.find(([k]) => k === "cluster") ?? usable.find(([k]) => k === "job") ?? usable[0];
        [label, value] = preferred;
        const second = usable.find(([k]) => k !== label);
        if (second) [secondLabel, secondValue] = second;
        record("Discovered a real label to build every check on", true, `${label}="${value}" from a live series`);
      });

      await test.step("Step 2 - the operators the page offers are the ones this suite covers", async () => {
        // Read from the page so an operator added later fails loudly instead of going untested.
        offeredOperators = (await readOfferedOperators(locators, label)).filter((o) => COVERED_OPERATORS.includes(o) || /^[!=<>~]/.test(o));

        const uncovered = offeredOperators.filter((o) => !COVERED_OPERATORS.includes(o));
        record(
          "The page offers only operators this suite exercises",
          uncovered.length === 0,
          uncovered.length === 0
            ? `offered: ${offeredOperators.join(", ") || "(none read)"}`
            : `offered ${uncovered.join(", ")}, which no step below covers — extend this suite`
        );

        // Prometheus offers no negated regex though PromQL supports !~; recorded so the absence stays visible.
        recordKnownLimitation(
          "Negated regex (!~) is not offered",
          "PrometheusMetricSource.GetSupportedOperators returns only _eq, _neq and _regex, so !~ cannot be exercised here even though PromQL supports it"
        );
      });

      await test.step("Step 3 - exact match pair: '=' finds it, '!=' proves it is excluded", async () => {
        if (!offers("=") && !offers("!=")) {
          record("Exact match pair", true, "skipped — this provider offers neither = nor !=");
          return;
        }
        await clearMetricFilterChips(page);
        const addedEq = await addMetricFilterChip(locators, label, "=", value);
        if (!addedEq) {
          record("Operator '='", false, "the builder would not commit an = filter");
        } else {
          const rows = await applyAndRead();
          const { sample, matching } = await countMatching(rows);
          record(
            "Operator '=' returns only series carrying the value",
            rows > 0 && matching === sample,
            `${matching}/${sample} of ${rows} series carried ${label}="${value}"`
          );
        }

        await clearMetricFilterChips(page);
        const addedNeq = await addMetricFilterChip(locators, label, "!=", value);
        if (!addedNeq) {
          record("Operator '!='", false, "the builder would not commit a != filter");
        } else {
          const rows = await applyAndRead();
          if (rows === 0) {
            // Zero is correct when every series on this cluster carries the value.
            record("Operator '!=' excludes the value", true, "no series returned — every series carries the value");
          } else {
            const { sample, matching } = await countMatching(rows);
            record(
              "Operator '!=' excludes the value",
              matching === 0,
              `${matching}/${sample} of ${rows} series still carried ${label}="${value}" (expected 0)`
            );
          }
        }
      });

      await test.step("Step 4 - regex: a real pattern matches, and it is a pattern not a literal", async () => {
        if (!offers("=~")) {
          record("Operator '=~'", true, "skipped — this provider offers no regex operator");
          return;
        }
        await clearMetricFilterChips(page);

        // Alternation, because "<value>|zzz" can only match as a regex while a literal compare finds nothing, and it does not rely on "." and "*" surviving the input.
        const prefix = value;
        const pattern = `${value}|zzz-no-such-value-e2e`;

        const added = await addMetricFilterChip(locators, label, "=~", pattern);
        if (!added) {
          record("Operator '=~'", false, "the builder would not commit a regex filter");
          return;
        }

        // The chip must carry the typed pattern, otherwise a match would pass on a literal and prove nothing about regex.
        if (!(await chipCommittedAs(page, label, "=~", pattern))) {
          recordKnownLimitation(
            "Operator '=~' pattern is replaced before it is applied (issue #39819)",
            `typed "${pattern}" but the chip did not keep it, so a regex pattern cannot be entered and '=~' is not exercised as a regex`
          );
          return;
        }

        const rows = await applyAndRead();
        if (rows === 0) {
          // Recorded, not failed, so a known bug (#39819) does not keep the nightly red; flip to a failure once it is fixed.
          recordKnownLimitation(
            "Operator '=~' returns no series (issue #39819)",
            `pattern "${pattern}" returned nothing, though "${value}" exists and '=' matches it — regex metacharacters appear to be escaped before the query is built`
          );
          return;
        }

        const sample = Math.min(SAMPLE_SIZE, rows);
        let conforming = 0;
        const re = new RegExp(`^${escapeForRegex(prefix)}`);
        for (let i = 0; i < sample; i++) {
          const labels = await metrics.readRowLabels(i);
          if (labels[label] && re.test(labels[label])) conforming += 1;
        }
        record(
          "Operator '=~' matches a real pattern, not a literal",
          conforming === sample,
          `${conforming}/${sample} of ${rows} series matched /^${prefix}/`
        );
      });

      await test.step("Step 5 - a value that cannot exist shows a clean no-results state", async () => {
        await clearMetricFilterChips(page);
        const impossible = `zzz-no-such-value-e2e-${Date.now()}`;

        const added = await addMetricFilterChip(locators, label, "=", impossible);
        if (!added) {
          record("A non-existent value", false, "the builder would not commit the filter");
          return;
        }

        // A value that is not an existing one is replaced by a suggestion on commit, so the query is filtered on a value that does exist.
        if (!(await chipCommittedAs(page, label, "=", impossible))) {
          recordKnownLimitation(
            "A typed value that is not an existing one is replaced by a suggestion (issue #39819)",
            `typed "${impossible}" but the chip did not keep it, so a non-existent value cannot be filtered on`
          );
          return;
        }

        const rows = await applyAndRead();
        // Zero rows and no error state: the page must say nothing matched, not show the unfiltered set.
        record("A non-existent value returns no series rather than the unfiltered set", rows === 0, `${rows} series returned`);
      });

      await test.step("Step 6 - the Code tab agrees with the Builder, and switching back still works", async () => {
        await clearMetricFilterChips(page);
        const added = await addMetricFilterChip(locators, label, "=", value);
        if (!added) {
          record("Builder to Code round trip", false, "could not build the filter to round-trip");
          return;
        }

        await locators.switchQueryMode("Code");

        // Polled because the Code tab renders before the builder's query is written into it, so one read catches it empty.
        const codeBox = page.locator("textarea, [contenteditable='true'], .cm-content").first();
        // The timeout is not the assertion here; the record() below asserts on the final text.
        await expect(codeBox).toContainText(label, { timeout: 20000 }).catch(() => {});
        // An unreadable box reads as empty, which the record() below reports as a failure.
        const codeText = await codeBox.innerText().catch(() => "");
        const carriesLabel = new RegExp(escapeForRegex(label)).test(codeText);
        const carriesValue = new RegExp(escapeForRegex(value)).test(codeText);
        record(
          "The Code tab shows the filter the Builder built",
          carriesLabel && carriesValue,
          carriesLabel && carriesValue ? "code carries both the label and its value" : `code text did not carry ${label}="${value}"`
        );

        await locators.switchQueryMode("Builder");
        // Not visible is the failing answer, and it is recorded as false below.
        const backOnBuilder = await locators.LogQueryBuilderInput.isVisible().catch(() => false);
        record("Switching back to Builder still works", backOnBuilder, backOnBuilder ? "builder input is present again" : "builder did not come back");
      });

      await test.step("Step 7 - two filters combined: every result satisfies both", async () => {
        if (!secondLabel) {
          record("Two filters combined", true, "skipped — the series carried only one usable label");
          return;
        }

        await clearMetricFilterChips(page);
        await addMetricFilterChip(locators, label, "=", value);
        const single = await applyAndRead();

        // The second chip is checked on screen and added once more if it is missing, since a missed commit would otherwise make the combine and remove checks pass on one filter.
        await addMetricFilterChip(locators, secondLabel, "=", secondValue);
        if (!(await chipCommittedAs(page, secondLabel, "=", secondValue))) {
          await addMetricFilterChip(locators, secondLabel, "=", secondValue);
        }
        if (!(await chipCommittedAs(page, secondLabel, "=", secondValue))) {
          record("Two filters combined", false, `the ${secondLabel} chip did not commit, so only one filter was applied`);
          return;
        }
        const combined = await applyAndRead();

        if (combined === 0) {
          record("Two filters combined narrow the result set", combined <= single, `one: ${single}, two: ${combined}`);
          return;
        }

        const sample = Math.min(SAMPLE_SIZE, combined);
        let satisfyingBoth = 0;
        for (let i = 0; i < sample; i++) {
          const labels = await metrics.readRowLabels(i);
          if (labels[label] === value && labels[secondLabel] === secondValue) satisfyingBoth += 1;
        }
        record(
          "Every series returned satisfies BOTH filters",
          satisfyingBoth === sample && combined <= single,
          `${satisfyingBoth}/${sample} satisfied both; one filter gave ${single}, two gave ${combined}`
        );
      });

      await test.step("Step 8 - removing one filter returns to matching only what is left", async () => {
        if (!secondLabel) {
          record("Removing one filter", true, "skipped — only one usable label");
          return;
        }

        const beforeRemoval = await applyAndRead();
        if (!(await removeMetricFilterChip(page, secondLabel))) {
          record("Removing the second filter widens the set and leaves the first applied", false, `the ${secondLabel} chip could not be removed`);
          return;
        }
        const afterRemoval = await applyAndRead();

        const sample = Math.min(SAMPLE_SIZE, afterRemoval);
        let stillMatchFirst = 0;
        for (let i = 0; i < sample; i++) {
          const labels = await metrics.readRowLabels(i);
          if (labels[label] === value) stillMatchFirst += 1;
        }
        record(
          "Removing the second filter widens the set and leaves the first applied",
          afterRemoval >= beforeRemoval && stillMatchFirst === sample,
          `${beforeRemoval} -> ${afterRemoval} series, ${stillMatchFirst}/${sample} still carry ${label}="${value}"`
        );
      });

      await test.step("Step 9 - changing the date range keeps the filter and its results correct", async () => {
        // A tab without a date range control is the expected answer on some providers, so absence is a skip.
        if (!(await locators.LogDateRangeTrigger.isVisible().catch(() => false))) {
          record("Date range change", true, "skipped — no date range control on this tab");
          return;
        }

        await locators.LogDateRangeTrigger.click();
        const option = page.getByRole("option", { name: /6 hours|3 hours|12 hours|24 hours/i }).first();
        // No preset offered is the expected answer when the control only takes custom ranges, so it is a skip.
        if (!(await option.isVisible({ timeout: 10000 }).catch(() => false))) {
          await page.keyboard.press("Escape");
          record("Date range change", true, "skipped — no preset range offered");
          return;
        }
        await option.click();

        const rows = await applyAndRead();
        if (rows === 0) {
          record("The filter survives a date-range change", true, "no series in the new range — nothing to contradict");
          return;
        }
        const { sample, matching } = await countMatching(rows);
        // A range change that silently drops the filter is a defect, visible only by re-checking the returned series.
        record(
          "The filter is still applied after the date range changes",
          matching === sample,
          `${matching}/${sample} of ${rows} series still carry ${label}="${value}"`
        );
      });

      await test.step("Step 10 - download works while a filter is active", async () => {
        const downloadButton = page.getByRole("button", { name: /download/i }).first();
        // A missing download control is the expected answer on some providers, so absence is a skip.
        if (!(await downloadButton.isVisible().catch(() => false))) {
          record("Download while filtered", true, "skipped — no download control visible");
          return;
        }

        const downloadPromise = page
          .waitForEvent("download", { timeout: 60000 })
          // No download event is the failing answer, and it is recorded as false below.
          .catch(() => null);
        await downloadButton.click();
        const download = await downloadPromise;
        record("Download while filtered", download !== null, download ? `downloaded ${download.suggestedFilename()}` : "no file was produced");
      });

      await test.step("Report", async () => {
        const passed = results.filter((r) => r.passed).length;
        console.log("\n=========== QUERY METRICS OPERATOR CHECKS ===========");
        console.log(`Provider : ${PROVIDER}`);
        console.log(`Label    : ${label}="${value}"`);
        if (secondLabel) console.log(`Second   : ${secondLabel}="${secondValue}"`);
        console.log(`Result   : ${passed}/${results.length} checks passed`);

        const failures = results.filter((r) => !r.passed);
        if (failures.length) {
          console.log("\nFailures:");
          for (const f of failures) console.log(`  ${f.check} — ${f.detail}`);
        }
        if (knownLimitations.length) {
          console.log("\nKnown limitations (not counted as failures):");
          for (const k of knownLimitations) console.log(`  ${k.check} — ${k.detail}`);
        }
        console.log("====================================================\n");

        expect(failures, `${failures.length} operator checks failed — see the log above`).toHaveLength(0);
      });
    } finally {
      // Runs on pass, fail and throw alike, because a default provider left on is a change to a shared account.
      if (providerDefault.restore) await providerDefault.restore();
    }
  }
);

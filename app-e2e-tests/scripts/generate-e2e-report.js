// Builds a single self-contained HTML report from the run data written by
// notifications/E2EReportReporter.ts, styled to match
// llm/benchmark/benchmark_server/static/index.html.
//
// Usage: node scripts/generate-e2e-report.js [resultsFile] [outFile] [env]
const fs = require("fs");
const path = require("path");

const resultsFile = process.argv[2] || path.join("playwright-report", "e2e-results.json");
const outFile = process.argv[3] || "e2e-report.html";
const envName = process.argv[4] || "dev";
// CI wipes the report folder at the start of every run, so the trend has to be
// kept somewhere that survives it — E2E_REPORT_HISTORY points there.
const historyFile =
  process.env.E2E_REPORT_HISTORY || path.join(path.dirname(outFile), "e2e-report-history.json");

// Playwright auto-generates a step for every API call it makes. Those are the
// execution trace, not the intent — the steps worth reading are the ones the
// spec author named with test.step(), which are the only ones carrying prose
// names AND child steps. A custom expect() message also gets a prose name but
// never has children, which is how the two are told apart.
const AUTO_STEP =
  /^(Before Hooks|After Hooks|Register locator handler|Navigate to|Wait for|Expect |Mouse |Click |Fill |Press |Type |Set |Hover|Check |Uncheck|Select |Query |Focus|Blur|Close|Create|Launch|Screenshot|screenshot|error-context|trace|Scroll|Drag|Tap|Evaluate|Reload|Go back|Go forward)/;

// Actions that say nothing about intent — cursor parking, arbitrary sleeps,
// load-state waits. Dropping them is what makes the fallback list readable.
const DEAD_ACTION =
  /^(Mouse move|Wait for timeout|Wait for load state|Wait for navigation|Press "Escape"|Register locator handler|Screenshot|screenshot|error-context|trace)/;

// Playwright's own step names are machine-shaped. These rewrites keep the
// meaning but read as sentences.
function humanizeAction(name) {
  let m = name.match(/^Navigate to "(.+)"$/);
  if (m) return "Open " + m[1];
  m = name.match(/^Expect "(?:to)?([A-Za-z]+)" (.+)$/);
  if (m) {
    const check = m[1].replace(/([A-Z])/g, " $1").trim().toLowerCase();
    return "Expect " + shortLocator(m[2]) + " to " + check;
  }
  m = name.match(/^(Click|Fill|Hover|Check|Uncheck|Select|Focus|Tap|Drag) (.+)$/);
  if (m) return m[1] + " " + shortLocator(m[2]);
  m = name.match(/^Wait for selector (.+)$/);
  if (m) return "Wait for " + shortLocator(m[1]);
  return name;
}

// locator('#id') / getByRole('tab', { name: 'X' }) -> the part a human reads.
function shortLocator(s) {
  let m = s.match(/locator\('([^']+)'\)/);
  if (m) return m[1];
  m = s.match(/getBy\w+\('([^']+)'/);
  if (m) return '"' + m[1] + '"';
  m = s.match(/name:\s*'([^']+)'/);
  if (m) return '"' + m[1] + '"';
  return s.length > 60 ? s.slice(0, 60) + "…" : s;
}

// Failure screenshots ride along as step attachments. Inlining them keeps the
// report a single portable file.
function findScreenshot(raw) {
  let found = null;
  (function walk(steps) {
    for (const s of steps || []) {
      for (const a of s.attachments || []) {
        if (!found && (a.type || "").indexOf("image") === 0) found = a.source;
      }
      walk(s.steps);
    }
  })(raw.steps);
  for (const a of raw.attachments || []) {
    if (!found && (a.type || "").indexOf("image") === 0) found = a.source;
  }
  // The file sits in test-results/ on the same machine, but a missing or
  // unreadable screenshot must not take the whole report down with it.
  if (!found) return "";
  const mime = /\.jpe?g$/i.test(found) ? "image/jpeg" : "image/png";
  try {
    return "data:" + mime + ";base64," + fs.readFileSync(found).toString("base64");
  } catch (e) {
    return "";
  }
}

function extractSteps(raw) {
  const top = (raw.steps || []).filter((s) => s.status);
  const named = top.filter(
    (s) =>
      !AUTO_STEP.test(s.name || "") && (s.steps || []).length && s.category !== "fixture" && s.category !== "hook"
  );
  // A failed attempt whose named steps all passed broke outside them — every
  // failure carries a "Worker Cleanup" fixture step, for one — and listing
  // only those would hide where it actually broke. The action trace shows it.
  const brokeOutside = raw.status === "failed" && !named.some((s) => s.status !== "passed");
  if (named.length && !brokeOutside) {
    const items = [];
    for (const s of named) {
      items.push({ name: s.name || "", status: s.status, duration: (s.stop || 0) - (s.start || 0), at: s.at || "" });
      // Only a failed step gets expanded into its actions — that is where the
      // "which click actually broke" answer lives, and doing it for passing
      // steps would bury the list again.
      if (s.status !== "passed") {
        for (const child of s.steps || []) {
          if (DEAD_ACTION.test(child.name || "")) continue;
          items.push({
            name: humanizeAction(child.name || ""),
            status: child.status,
            duration: (child.stop || 0) - (child.start || 0),
            at: child.at || "",
            child: true,
          });
        }
      }
    }
    return { kind: "named", items: items };
  }
  // No authored steps — fall back to the action trace so the row still shows
  // what the test actually did, clearly labelled as the weaker substitute.
  return {
    kind: "actions",
    outside: brokeOutside && named.length > 0,
    items: top
      .filter((s) => !/^(Before Hooks|After Hooks)/.test(s.name || "") && !DEAD_ACTION.test(s.name || ""))
      .map((s) => ({
        name: humanizeAction(s.name || ""),
        status: s.status,
        duration: (s.stop || 0) - (s.start || 0),
        at: s.at || "",
      })),
  };
}

// A Playwright action is titled with what it typed, so the login step arrives
// as Type "<the LDAP password>" and would be printed in every failing test's
// trace — in a file that is posted to Slack. Every value of a secret-looking
// variable in this process's environment (the workflow exports the whole
// .env secret into it) is blanked out of the run data before anything reads
// it. Values under six characters are left alone: "admin" is not a secret,
// and blanking it would blank every tests/admin/ path in the report.
const SECRET_KEY = /PASS|SECRET|TOKEN|API_?KEY|PRIVATE|CREDENTIAL|USERNAME|AUTH|WEBHOOK|COOKIE|SESSION|ACCESS_KEY|CLIENT_ID/i;
const secretValues = Object.keys(process.env)
  .filter((k) => SECRET_KEY.test(k))
  .map((k) => String(process.env[k] || "").trim())
  .filter((v) => v.length >= 6)
  .sort((a, b) => b.length - a.length);

function scrub(value) {
  if (typeof value === "string") {
    let out = value;
    for (const secret of secretValues) if (out.indexOf(secret) !== -1) out = out.split(secret).join("•••");
    return out;
  }
  if (Array.isArray(value)) return value.map(scrub);
  if (value && typeof value === "object") {
    const out = {};
    for (const k in value) out[k] = scrub(value[k]);
    return out;
  }
  return value;
}

// Older reporter builds wrote the bare attempt array, with no run timing.
function readRunFile(file) {
  const data = scrub(JSON.parse(fs.readFileSync(file, "utf8")));
  if (Array.isArray(data)) return { run: null, attempts: data };
  return { run: data.run || null, attempts: data.attempts || [] };
}

function readResults(rawAttempts) {
  const attempts = {};
  for (const raw of rawAttempts) {
    // A retry is the same test again, so the file path and title together are
    // what identify it across attempts.
    const key = raw.suite + "::" + raw.name;
    (attempts[key] = attempts[key] || []).push(raw);
  }

  return Object.values(attempts).map((runs) => {
    runs.sort((a, b) => (a.retry || 0) - (b.retry || 0));
    const final = runs[runs.length - 1];
    // Playwright retries a failure; landing on passed after an earlier
    // failure is the definition of flaky, and it is the number the team
    // actually acts on — a green suite hiding retries is not green.
    const flaky = final.status === "passed" && runs.some((r) => r.status !== "passed");
    // A flaky test ends green, so its final attempt carries no error: the
    // failed attempt is the only record of why it flaked.
    const failedTry = flaky ? runs.find((r) => r.status !== "passed" && r.status !== "skipped") : null;
    // Failed, then skipped on the retry — typically a serial describe whose
    // earlier test broke. It never passed, so it is a failure, which is also
    // how SlackReporter counts it: its skipped branch never clears a failure.
    const lastFailure = runs.filter((r) => r.status === "failed").pop();
    const failedThenSkipped = final.status === "skipped" && !!lastFailure;
    const detail = failedTry || (failedThenSkipped ? lastFailure : final);
    return {
      name: final.name || "",
      status: failedThenSkipped ? "failed" : final.status || "unknown",
      flaky: flaky,
      failedAttempt: failedTry ? (failedTry.retry || 0) + 1 : 0,
      retries: runs.length - 1,
      duration: (final.stop || 0) - (final.start || 0),
      start: runs[0].start || 0,
      stop: runs.reduce((m, r) => Math.max(m, r.stop || 0), 0),
      tags: final.tags || [],
      suite: final.suite || "",
      line: final.line || 0,
      message: (detail.statusDetails || {}).message || "",
      trace: (detail.statusDetails || {}).trace || "",
      // Same rule the Slack reporter uses (SlackReporter.isApiFailure): a
      // failure carrying a [GraphQLWatcher] error is a backend/API failure and
      // is reported apart from ordinary test failures, so the two summaries
      // can't disagree.
      apiFailure:
        final.status !== "passed" &&
        ((detail.statusDetails || {}).message || "").indexOf("[GraphQLWatcher]") !== -1,
      // A clean pass does not need its raw action trace: nobody reads what a
      // green test clicked, and those traces are most of the report's size --
      // 14MB for the 1082-test nightly, past what Slack will preview. Authored
      // test.step() lists stay, for every outcome.
      steps: (function (st) {
        const cleanPass = final.status === "passed" && !flaky;
        return cleanPass && st.kind === "actions" ? { kind: st.kind, items: [] } : st;
      })(extractSteps(detail)),
      screenshot: final.status === "passed" && !failedTry ? "" : findScreenshot(detail),
    };
  });
}

function percentile(sorted, p) {
  if (!sorted.length) return 0;
  const idx = Math.min(sorted.length - 1, Math.ceil((p / 100) * sorted.length) - 1);
  return sorted[Math.max(0, idx)];
}

function summarise(tests) {
  // A skipped test takes no time, and counting its 0s would pull the average
  // and P95 down by however many tests were skipped.
  const durations = tests
    .filter((t) => t.status !== "skipped")
    .map((t) => t.duration)
    .sort((a, b) => a - b);
  const count = (s) => tests.filter((t) => t.status === s).length;
  const passed = count("passed");
  const apiFailures = tests.filter((t) => t.apiFailure).length;
  const retried = tests.filter((t) => t.retries > 0);
  // A skipped test says nothing about the product either way, so it stays
  // out of the rate rather than dragging it down.
  const executed = tests.length - count("skipped");
  return {
    total: tests.length,
    passed: passed,
    // Excludes API failures, which are counted and listed on their own —
    // mirrors the Slack summary so the two never report different numbers.
    failed: count("failed") + count("broken") - apiFailures,
    apiFailures: apiFailures,
    broken: 0,
    skipped: count("skipped"),
    retried: retried.length,
    flaky: tests.filter((t) => t.flaky).length,
    passRate: executed ? (passed / executed) * 100 : 0,
    // Time actually spent, so a test skipped part-way (after its hooks ran)
    // still counts here — it is only the per-test figures it would skew.
    totalMs: tests.reduce((a, t) => a + t.duration, 0),
    avgMs: durations.length ? durations.reduce((a, b) => a + b, 0) / durations.length : 0,
    p95Ms: percentile(durations, 95),
    maxMs: durations.length ? durations[durations.length - 1] : 0,
  };
}

// A group where nothing ran has no rate at all; it sorts last instead of
// posing as the weakest area at 0%.
const allSkipped = (r) => (r.total === r.skipped ? 1 : 0);

function byTag(tests) {
  const map = {};
  for (const t of tests) {
    for (const tag of t.tags) {
      const row = (map[tag] =
        map[tag] || { tag: tag, total: 0, passed: 0, failed: 0, flaky: 0, skipped: 0, green: 0, ms: 0 });
      row.total++;
      // Same rule as the tiles: the four columns are exclusive and sum to
      // total, while the rate counts flaky as green and leaves skipped out.
      if (t.flaky) row.flaky++;
      else if (t.status === "passed") row.passed++;
      else if (t.status === "skipped") row.skipped++;
      else row.failed++;
      if (t.status === "passed") row.green++;
      row.ms += t.duration;
    }
  }
  return Object.values(map)
    .map((r) =>
      Object.assign(r, {
        rate: r.total - r.skipped ? (r.green / (r.total - r.skipped)) * 100 : 0,
        avgMs: r.ms / r.total,
      })
    )
    .sort((a, b) => allSkipped(a) - allSkipped(b) || a.rate - b.rate || b.total - a.total);
}

// Quarantined tests are excluded from every run (--grep-invert @quarantine),
// so they never appear in results — reading the source is the only way to
// report them at all. Matches test("title", { tag: [... "@quarantine"] }),
// whether the declaration sits on one line or wraps.
function findQuarantined(dir, rootDir, out) {
  out = out || [];
  rootDir = rootDir || dir;
  if (!fs.existsSync(dir)) return out;
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      findQuarantined(full, rootDir, out);
    } else if (entry.name.endsWith(".spec.ts")) {
      const src = fs.readFileSync(full, "utf8");
      // test.skip(...) counts too: several quarantined tests are hard-skipped
      // in source rather than only tagged, and matching bare test( alone
      // silently undercounts them.
      const re = /test(\.skip|\.fixme|\.only)?\(\s*(["'`])((?:\\.|(?!\2)[\s\S])*?)\2\s*,\s*\{[^}]*?"@quarantine"/g;
      let m;
      while ((m = re.exec(src)) !== null) {
        out.push({
          name: m[3].replace(/\s+/g, " ").trim(),
          file: path.relative(rootDir, full).split(path.sep).join("/"),
          skipped: m[1] === ".skip" || m[1] === ".fixme",
        });
      }
    }
  }
  return out;
}

function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
    return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
  });
}

// Titles come from test("...") and follow two conventions in this suite:
// "Module -> Sub -> Case" and "Module - what it does, verify ...". Splitting
// the module path off the intent lets the list be scanned by intent instead
// of by the repeated prefix every row in a file shares.
function splitTitle(name) {
  const arrow = name.split(/\s*-+>\s*/);
  if (arrow.length > 1) {
    return { context: arrow.slice(0, -1).join(" › ").trim(), intent: arrow[arrow.length - 1].trim() };
  }
  const dash = name.match(/^([^-]{3,40}?)\s+-\s+(.*)$/s);
  if (dash) return { context: dash[1].trim(), intent: dash[2].trim() };
  return { context: "", intent: name };
}

const secs = (ms) => (ms / 1000).toFixed(1) + "s";
const mins = (ms) => (ms >= 60000 ? (ms / 60000).toFixed(1) + "m" : secs(ms));

// Pinned rather than left to the machine locale: the report is generated on a
// CI runner and read in India, and the Slack summary for the same run already
// stamps IST -- two different times for one run is a support question.
const IST = "Asia/Kolkata";
const fmtDateTime = (t) => new Date(t).toLocaleString("en-IN", { timeZone: IST });
// Several full runs can land on one day, so a trend label needs the time too.
const fmtStamp = (t) =>
  new Date(t).toLocaleString("en-IN", {
    timeZone: IST,
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });

const runFile = readRunFile(resultsFile);
const tests = readResults(runFile.attempts);
const startTimes = tests.map((t) => t.start).filter(Boolean);
const runStartedAt = startTimes.length ? startTimes.reduce((a, b) => (b < a ? b : a)) : Date.now();
// Wall time, not the sum of test durations: tests run on several workers at
// once. Older results files carry no run clock, so the span of the attempts
// stands in for it.
const lastStop = tests.reduce((m, t) => Math.max(m, t.stop || 0), 0);
const wallMs =
  runFile.run && runFile.run.duration ? runFile.run.duration : lastStop > runStartedAt ? lastStop - runStartedAt : 0;
const stats = summarise(tests);
const tags = byTag(tests);
// …/app-e2e-tests/playwright-report/e2e-results.json -> …/app-e2e-tests/tests
const testsDir = path.join(path.dirname(path.dirname(path.resolve(resultsFile))), "tests");
const quarantinedTests = findQuarantined(testsDir);
const quarantined = quarantinedTests.length;
// @quarantine is meant to be left out with --grep-invert, but neither e2e
// workflow passes it, so these tests do run and are counted. The report says
// what happened in this run instead of assuming the exclusion.
const isQuarantined = (t) => t.tags.indexOf("quarantine") !== -1;
const quarantinedRan = tests.filter(isQuarantined);
const quarantineResult = (q) =>
  quarantinedRan.find(
    (t) => t.suite.split(path.sep).join("/") === q.file && (t.name === q.name || t.name.endsWith(" - " + q.name))
  );

// Trend is just each run's summary appended to a file the next run reads —
// no separate history mechanism to carry between builds.
let history = [];
if (fs.existsSync(historyFile)) {
  try {
    // Restored from a CI cache, so it is not necessarily the array it was.
    const parsed = scrub(JSON.parse(fs.readFileSync(historyFile, "utf8")));
    history = Array.isArray(parsed) ? parsed : [];
  } catch (e) {
    history = [];
  }
}
// Outcome of the previous run. It must skip any entry for THIS run, which a
// re-generation from the same results would otherwise leave on record —
// comparing a run against itself reports every failure as already-known —
// and any later run, so regenerating an older run's report compares it with
// what came before it, not after. ISO stamps compare correctly as strings.
const thisRunStamp = new Date(runStartedAt).toISOString();
const priorRuns = history.filter((h) => h.when < thisRunStamp);
const previous = priorRuns.length ? priorRuns[priorRuns.length - 1] : null;

const outcomeOf = (t) =>
  t.flaky ? "flaky" : t.apiFailure ? "apifailed" : t.status;

// Per-test outcomes are stored, not just the totals: the totals give the
// trend line, the per-test map gives new-vs-known and flaky frequency.
const testOutcomes = {};
// Seconds per test, for spotting a test that suddenly got slower; the first
// line of each failure, for telling "same breakage" from "broke differently".
const testDurations = {};
const testErrors = {};
for (const t of tests) {
  const o = outcomeOf(t);
  testOutcomes[t.name] = o;
  if (t.status !== "skipped") testDurations[t.name] = Math.round(t.duration / 1000);
  if (o === "failed" || o === "apifailed") testErrors[t.name] = headline(t).slice(0, 160);
}

// Regenerating from the same results is not a new run. Replacing the entry
// keeps the comparison pointing at the genuinely previous execution.
history = history.filter((h) => h.when !== thisRunStamp);

const ghRunUrl =
  process.env.GITHUB_SERVER_URL && process.env.GITHUB_REPOSITORY && process.env.GITHUB_RUN_ID
    ? `${process.env.GITHUB_SERVER_URL}/${process.env.GITHUB_REPOSITORY}/actions/runs/${process.env.GITHUB_RUN_ID}`
    : "";

history.push({
  // The run's own start time, not when this script happened to run — the
  // report is often generated well after the tests finished.
  when: new Date(runStartedAt).toISOString(),
  run: process.env.GITHUB_RUN_NUMBER || "",
  url: ghRunUrl,
  sha: process.env.E2E_COMMIT_SHA || process.env.GITHUB_SHA || "",
  passed: stats.passed,
  failed: stats.failed + stats.apiFailures,
  apiFailures: stats.apiFailures,
  flaky: stats.flaky,
  skipped: stats.skipped,
  passRate: Number(stats.passRate.toFixed(1)),
  durationMs: wallMs,
  tests: testOutcomes,
  durations: testDurations,
  errors: testErrors,
});
const currentEntry = history[history.length - 1];
history.sort((a, b) => (a.when < b.when ? -1 : a.when > b.when ? 1 : 0));
history = history.slice(-20);
// A partial run still shows the trend, but must not join it: a 9-test affected
// run recorded next to a 1000-test nightly makes the line meaningless and the
// against-previous comparison wrong. That holds for the chart as much as for
// the file, so a partial run is left off both.
const partialRun = !!process.env.E2E_REPORT_TREND_READONLY;
if (!partialRun) {
  fs.mkdirSync(path.dirname(historyFile), { recursive: true });
  // Compact: per-test maps across 20 runs are thousands of keys, and nobody
  // reads this file by hand.
  fs.writeFileSync(historyFile, JSON.stringify(history));
}
// The runs up to and including this one. The file can also hold later runs
// when an older run's report is regenerated; those are not this run's past.
const upToNow = priorRuns.concat([currentEntry]).slice(-20);
const trendHistory = partialRun ? priorRuns.slice(-20) : upToNow;

const isBad = (o) => o === "failed" || o === "apifailed" || o === "broken";

// Three buckets, because they call for different reactions: a regression to
// investigate now, a known breakage, and a test the previous run never
// exercised (so there is nothing to compare it against).
const failingNow = tests.filter((t) => isBad(outcomeOf(t)));
const newFailures = [];
const knownFailures = [];
const unseenBefore = [];
for (const t of failingNow) {
  if (!previous || !previous.tests) unseenBefore.push(t);
  else if (!(t.name in previous.tests)) unseenBefore.push(t);
  else if (isBad(previous.tests[t.name])) knownFailures.push(t);
  else newFailures.push(t);
}

// How long a test has been broken: this run plus every earlier recorded run it
// also failed in, back to the first one it did not. "atLeast" means the streak
// runs off the start of the history, so the real start is older still.
function failStreak(t) {
  let runs = 1;
  let since = runStartedAt;
  let i = priorRuns.length - 1;
  for (; i >= 0; i--) {
    if (!isBad((priorRuns[i].tests || {})[t.name])) break;
    runs++;
    since = Date.parse(priorRuns[i].when);
  }
  return { runs: runs, since: since, atLeast: i < 0 && priorRuns.length > 0 };
}

const streakText = (t) => {
  const s = failStreak(t);
  if (s.runs < 2) return "";
  return `failing ${s.atLeast ? "for at least" : "for"} ${s.runs} full runs, since ${fmtStamp(s.since)}`;
};

// Red in the last full run, green now — the good news a monitor otherwise has
// to work out by noticing a name has disappeared from the failure list.
const fixedNow = previous
  ? tests.filter((t) => isBad((previous.tests || {})[t.name]) && (t.status === "passed" || t.flaky))
  : [];

// Whether a failure that was already there is still the same failure. Older
// history entries carry no error text, so there is nothing to compare.
function errorChange(t) {
  const before = previous && previous.errors ? previous.errors[t.name] : undefined;
  if (before === undefined || !isBad((previous.tests || {})[t.name])) return null;
  return before === headline(t).slice(0, 160) ? { same: true } : { same: false, before: before };
}

// A test that passes but takes far longer than it usually does is often the
// next failure: a spinner that got slower, a query about to hit its timeout.
// Needs a few earlier samples to know what "usual" is.
function slowdown(t) {
  if (t.status !== "passed") return null;
  const samples = priorRuns
    .map((h) => (h.durations || {})[t.name])
    .filter((v) => typeof v === "number")
    .sort((a, b) => a - b);
  if (samples.length < 3) return null;
  const usual = samples[Math.floor(samples.length / 2)];
  const now = t.duration / 1000;
  return now >= usual * 2 && now - usual >= 20 ? { now: now, usual: usual } : null;
}
const slowerNow = tests
  .map((t) => ({ t: t, s: slowdown(t) }))
  .filter((x) => x.s)
  .sort((a, b) => b.s.now - b.s.usual - (a.s.now - a.s.usual));

// Everything the page's own history views need, packed small: each test name
// once, then one character per test per run instead of a map per run, so
// twenty runs of a 1000-test suite stay in the tens of kilobytes.
const pageRuns = upToNow;
const OUTCOME_CODE = { passed: "P", failed: "F", apifailed: "A", flaky: "K", skipped: "S" };
const pageNames = [];
const nameIdx = new Map();
for (const h of pageRuns) {
  for (const name in h.tests || {}) {
    if (!nameIdx.has(name)) {
      nameIdx.set(name, pageNames.length);
      pageNames.push(name);
    }
  }
}
const RUN_DATA = {
  names: pageNames,
  runs: pageRuns.map((h) => {
    const errs = {};
    for (const name in h.errors || {}) errs[nameIdx.get(name)] = h.errors[name];
    return {
      when: h.when,
      run: h.run || "",
      url: h.url || "",
      partial: h === currentEntry && partialRun,
      passRate: h.passRate,
      failed: h.failed,
      flaky: h.flaky,
      skipped: h.skipped,
      durationMs: h.durationMs || 0,
      o: pageNames.map((n) => OUTCOME_CODE[(h.tests || {})[n]] || "-").join(""),
      d: h.durations ? pageNames.map((n) => (h.durations[n] == null ? null : h.durations[n])) : null,
      e: errs,
    };
  }),
};

// The other environment's latest full run, read from its own trend history
// (E2E_COMPARE_HISTORY, restored from that environment's cache). It answers
// "does this also fail on test?" — the same spec failing in both points at the
// test or at code both branches share; failing in one points at that
// environment or at code the other branch does not have yet.
const compareEnv = (process.env.E2E_COMPARE_ENV || "").trim();
const other = (function () {
  const file = process.env.E2E_COMPARE_HISTORY;
  if (!compareEnv || !file || !fs.existsSync(file)) return null;
  try {
    const parsed = scrub(JSON.parse(fs.readFileSync(file, "utf8")));
    return Array.isArray(parsed) && parsed.length ? parsed[parsed.length - 1] : null;
  } catch (e) {
    return null;
  }
})();
const HERE = envName.toUpperCase();
const THERE = compareEnv.toUpperCase();
const otherOutcome = (t) => (other ? (other.tests || {})[t.name] : undefined);

// Every test that ran here, sorted by how it fared in both. A test failing on
// the other side but not run here says nothing about this run, so it is left
// out — on an affected-tests run that would be most of the other suite.
const xBoth = [];
const xHere = [];
const xThere = [];
const xNotThere = [];
if (other) {
  for (const t of tests) {
    const here = outcomeOf(t);
    const there = otherOutcome(t);
    if (isBad(here)) {
      if (there === undefined) xNotThere.push(t);
      else if (isBad(there)) xBoth.push(t);
      else xHere.push(t);
    } else if (here !== "skipped" && isBad(there)) {
      xThere.push(t);
    }
  }
}
const xCat = new Map();
xBoth.forEach((t) => xCat.set(t, "xboth"));
xHere.forEach((t) => xCat.set(t, "xhere"));
xThere.forEach((t) => xCat.set(t, "xthere"));
xNotThere.forEach((t) => xCat.set(t, "xnothere"));

// Totals worked out from each run's per-test outcomes, the same way for both,
// so the two columns are like for like whatever each entry's summary fields.
function tally(outcomes) {
  const r = { total: 0, passed: 0, failed: 0, flaky: 0, skipped: 0 };
  for (const name in outcomes) {
    const o = outcomes[name];
    r.total++;
    if (o === "passed") r.passed++;
    else if (o === "flaky") r.flaky++;
    else if (o === "skipped") r.skipped++;
    else r.failed++;
  }
  const ran = r.total - r.skipped;
  r.rate = ran ? ((r.passed + r.flaky) / ran) * 100 : 0;
  return r;
}

if (other) {
  const oerrs = {};
  for (const name in other.errors || {}) if (nameIdx.has(name)) oerrs[nameIdx.get(name)] = other.errors[name];
  RUN_DATA.other = {
    env: THERE,
    when: other.when,
    run: other.run || "",
    url: other.url || "",
    o: pageNames.map((n) => OUTCOME_CODE[(other.tests || {})[n]] || "-").join(""),
    e: oerrs,
  };
}

// The same test across the recent full runs, oldest first, ending with this
// run: a row of dots says "broke today" versus "has been red all week" at a
// glance, which the single previous-run comparison cannot.
const HISTORY_DOTS = 10;
function testHistory(t) {
  return priorRuns
    .slice(-(HISTORY_DOTS - 1))
    .map((h) => ({ when: h.when, o: (h.tests || {})[t.name] }))
    .concat([{ when: new Date(runStartedAt).toISOString(), o: outcomeOf(t), current: true }]);
}

// How often each test has flaked across the runs on record. One flake is
// noise; the same test flaking repeatedly is the thing to act on.
const flakeCounts = {};
for (const h of upToNow) {
  for (const name in h.tests || {}) {
    if (h.tests[name] === "flaky") flakeCounts[name] = (flakeCounts[name] || 0) + 1;
  }
}
const repeatFlakes = Object.keys(flakeCounts)
  .map((name) => ({ name: name, count: flakeCounts[name] }))
  .sort((a, b) => b.count - a.count);

// Failures grouped by what actually broke. Rules are ordered most specific
// first; the fallback keeps the raw first line so nothing is silently
// swallowed into a catch-all.
const CAUSE_RULES = [
  [/\[GraphQLWatcher\]/, "Backend / GraphQL query failed"],
  [/intercepts pointer events/, "Click blocked by an overlay or backdrop"],
  [/Test timeout of \d+ms exceeded/, "Test exceeded its overall timeout"],
  [/page\.waitForURL/, "Navigation never reached the expected URL"],
  [/locator\.click.*Timeout|Timeout.*locator\.click/, "Click timed out — element never became actionable"],
  [/waitForSelector|waitFor\(.*Timeout/, "Waited for an element that never appeared"],
  [/toBeVisible/, "Element never became visible"],
  [/toHaveText|toContainText/, "Text did not match what was expected"],
  [/toHaveCount/, "Unexpected number of elements"],
  [/toHaveAttribute|toHaveValue/, "Attribute or value did not match"],
  [/net::ERR|ECONNREFUSED|Navigation failed|net::/, "Network or navigation failure"],
  [/strict mode violation/, "Locator matched more than one element"],
];

function causeOf(t) {
  const msg = t.message || t.trace || "";
  for (const rule of CAUSE_RULES) if (rule[0].test(msg)) return rule[1];
  const first = msg.split("\n").map((l) => l.trim()).filter(Boolean)[0] || "Unclassified failure";
  return first.slice(0, 80);
}

const causeMap = {};
for (const t of failingNow) {
  const c = causeOf(t);
  (causeMap[c] = causeMap[c] || []).push(t);
}
const causes = Object.keys(causeMap)
  .map((c) => ({ cause: c, tests: causeMap[c], count: causeMap[c].length }))
  .sort((a, b) => b.count - a.count);

const slowest = tests.slice().sort((a, b) => b.duration - a.duration).slice(0, 10);

// Module = the spec's folder, which maps to a product area. The filename
// alone is too granular and the top folder alone is too coarse.
const moduleMap = {};
for (const t of tests) {
  const parts = String(t.suite || "").split(/[\\/]/);
  const mod = parts.length > 1 ? parts.slice(0, -1).join(" / ") : parts[0] || "root";
  const row = (moduleMap[mod] =
    moduleMap[mod] || { mod: mod, total: 0, passed: 0, failed: 0, flaky: 0, skipped: 0, ms: 0 });
  row.total++;
  const o = outcomeOf(t);
  if (o === "flaky") row.flaky++;
  else if (o === "passed") row.passed++;
  else if (o === "skipped") row.skipped++;
  else row.failed++;
  row.ms += t.duration;
}
const modules = Object.keys(moduleMap)
  .map((m) => {
    const r = moduleMap[m];
    const ran = r.total - r.skipped;
    return Object.assign(r, { rate: ran ? ((r.passed + r.flaky) / ran) * 100 : 0, avgMs: r.ms / r.total });
  })
  .sort((a, b) => allSkipped(a) - allSkipped(b) || a.rate - b.rate || b.total - a.total);

// Bee mark from app/src/assets/images/NB-logo-with-text.svg (wordmark path
// dropped). Square viewBox so it renders cleanly at favicon size.
const faviconSvg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="-1 -1 35 35">
<path d="M10.4581 14.8555C10.4581 14.8555 9.94265 23.6184 15.7417 30.0618C15.7417 30.0618 9.44157 31.5602 7.10758 24.1339C4.98127 17.3684 10.4581 14.8555 10.4581 14.8555Z" fill="#FACF39"/>
<path d="M17.6273 13.6992C17.6273 13.6992 17.1782 21.3331 22.23 26.9462C22.23 26.9462 16.7417 28.2516 14.7085 21.7821C12.8561 15.8883 17.6273 13.6992 17.6273 13.6992Z" fill="#FACF39"/>
<path d="M4.33541 20.3789C4.33541 20.3789 3.98654 26.3096 7.91129 30.6705C7.91129 30.6705 4.17414 30.901 2.57941 26.4873C1.39113 23.1985 4.33541 20.3789 4.33541 20.3789Z" fill="#FACF39"/>
<path d="M23.6997 11.4089C23.6997 11.4089 10.7486 -4.82841 1.85673 2.32372C-7.03511 9.47585 23.6997 11.4089 23.6997 11.4089Z" fill="#FACF39"/>
<path d="M28.195 21.7298C30.4125 21.7298 32.2102 19.9321 32.2102 17.7145C32.2102 15.4969 30.4125 13.6992 28.195 13.6992C25.9774 13.6992 24.1797 15.4969 24.1797 17.7145C24.1797 19.9321 25.9774 21.7298 28.195 21.7298Z" fill="#FACF39"/>
</svg>`;
const faviconUri = "data:image/svg+xml;base64," + Buffer.from(faviconSvg).toString("base64");

const sColor = (v) => (v >= 90 ? "#10B981" : v >= 70 ? "#F59E0B" : "#EF4444");

const card = (lbl, value, color, extra, hint) =>
  `<div class="bg-white border border-gray-100 rounded-xl p-3 text-center shadow-sm"${hint ? ` title="${esc(hint)}"` : ""}>
    <div class="text-[9px] font-semibold text-gray-400 uppercase tracking-wide mb-1">${esc(lbl)}</div>
    <div class="text-[14px] font-bold${color ? "" : " text-gray-700"}"${color ? ` style="color:${color}"` : ""}>${value}</div>
    ${extra || ""}
  </div>`;

// Change against the previous full run, coloured by whether it is good news.
// Totals are only compared between full runs — a partial run is a different
// size, so "3 fewer failures" would just mean "fewer tests ran".
function delta(cur, prev, higherIsBetter, fmt) {
  if (partialRun || prev == null || !isFinite(prev)) return "";
  const d = cur - prev;
  const label = fmt(Math.abs(d));
  if (!d || /^0(\.0+)?\D*$/.test(label)) {
    return '<div class="text-[9px] text-gray-400 mt-0.5">same as last run</div>';
  }
  const good = higherIsBetter ? d > 0 : d < 0;
  return `<div class="text-[9px] font-semibold mt-0.5 ${good ? "text-green-600" : "text-red-500"}">${
    d > 0 ? "&#9650;" : "&#9660;"
  } ${label} vs last run</div>`;
}
const int = (n) => String(Math.round(n));
const prevOf = (field) => (previous && previous[field] != null ? previous[field] : null);

const section = (title, note) =>
  `<div class="text-[12px] font-semibold text-gray-600 mb-1.5 mt-2 border-b border-gray-200 pb-1">${esc(title)}` +
  (note ? `<span class="text-[10px] font-normal text-gray-400 ml-1">${esc(note)}</span>` : "") +
  `</div>`;

// Playwright's message opens with the assertion/timeout line and then dumps
// the whole retry call log, which buries the one line that says what broke.
function headline(t) {
  const raw = (t.message || t.trace || "").split("\n").map((l) => l.trim()).filter(Boolean);
  const first = raw.find((l) => !l.startsWith("- ") && !l.startsWith("Call log"));
  return (first || raw[0] || "").slice(0, 300);
}

const statusPill = (t) => {
  if (t.flaky) return '<span class="text-[11px] font-semibold text-amber-500">FLAKY</span>';
  if (t.apiFailure) return '<span class="text-[11px] font-semibold text-red-600">API FAIL</span>';
  const map = {
    passed: '<span class="text-[11px] font-semibold text-green-600">PASS</span>',
    failed: '<span class="text-[11px] font-semibold text-red-500">FAIL</span>',
    broken: '<span class="text-[11px] font-semibold text-yellow-600">BROKEN</span>',
    skipped: '<span class="text-[11px] font-semibold text-gray-400">SKIPPED</span>',
  };
  return map[t.status] || '<span class="text-[11px] text-gray-400">' + esc(t.status) + "</span>";
};

// Who/what started this run. GitHub sets these in the job; running locally
// they are simply absent. The actor is only meaningful for a manual dispatch —
// on a scheduled run it is the automation, not a person, so it is not shown.
const env = process.env;
const isDispatch = env.GITHUB_EVENT_NAME === "workflow_dispatch";
const runUrl =
  env.GITHUB_SERVER_URL && env.GITHUB_REPOSITORY && env.GITHUB_RUN_ID
    ? `${env.GITHUB_SERVER_URL}/${env.GITHUB_REPOSITORY}/actions/runs/${env.GITHUB_RUN_ID}`
    : "";
// A dispatch with no selection at all is a full-suite run; anything else
// records exactly what the person narrowed it to.
const selection = [
  ["Module", env.INPUT_MODULE],
  ["Test file", env.INPUT_TEST_FILE],
  ["Test name", env.INPUT_TEST_NAME],
  ["Tag", env.INPUT_TAG],
].filter((s) => s[1] && String(s[1]).trim());

const chip = (lbl, value, accent) =>
  `<span class="inline-flex items-center gap-1 text-[10px] px-2 py-1 rounded-md border ${
    accent ? "border-nb-yellow/40 bg-amber-50 text-amber-800" : "border-gray-200 bg-white text-gray-600"
  }">
    <span class="uppercase tracking-wide text-[9px] opacity-70">${esc(lbl)}</span>
    <span class="font-semibold">${esc(value)}</span>
  </span>`;

// The exact code that ran, so a failure can be read against the source.
const repoUrl = env.GITHUB_SERVER_URL && env.GITHUB_REPOSITORY ? `${env.GITHUB_SERVER_URL}/${env.GITHUB_REPOSITORY}` : "";
// A scheduled run's GITHUB_SHA and GITHUB_REF_NAME are the default branch's,
// even when the job checks out another branch (the test environment runs the
// test branch); the workflow passes what it actually checked out.
const sha = env.E2E_COMMIT_SHA || env.GITHUB_SHA || "";
const branch = env.E2E_BRANCH || env.GITHUB_REF_NAME || "";
const commitChip =
  repoUrl && sha
    ? `<a href="${esc(repoUrl + "/commit/" + sha)}" target="_blank" rel="noopener" class="hover:opacity-80">${chip(
        "Commit",
        sha.slice(0, 7)
      )}</a>`
    : "";
const specUrl = (t) =>
  repoUrl && sha && t.suite
    ? `${repoUrl}/blob/${sha}/app-e2e-tests/tests/${t.suite.replace(/\\/g, "/")}${t.line ? "#L" + t.line : ""}`
    : "";

let runContextHtml = "";
if (isDispatch) {
  runContextHtml = `<div class="flex flex-wrap items-center gap-2 mb-4 p-2.5 rounded-xl border border-gray-200 bg-white shadow-sm">
    <span class="material-icons-outlined text-[16px]" style="color:#FB923C">play_circle</span>
    <span class="text-[11px] font-semibold text-gray-700">Manually triggered</span>
    ${chip("By", "@" + (env.GITHUB_TRIGGERING_ACTOR || env.GITHUB_ACTOR || "unknown"), true)}
    ${branch ? chip("Branch", branch) : ""}
    ${env.GITHUB_RUN_NUMBER ? chip("Run", "#" + env.GITHUB_RUN_NUMBER) : ""}
    ${commitChip}
    ${selection.length ? selection.map((s) => chip(s[0], s[1])).join("") : chip("Scope", "Full suite")}
    ${
      runUrl
        ? `<a href="${esc(runUrl)}" target="_blank" rel="noopener"
             class="text-[10px] text-blue-500 hover:underline ml-auto">Open CI run &rarr;</a>`
        : ""
    }
  </div>`;
} else if (runUrl) {
  // Automated run: no person to attribute it to, so none is shown.
  runContextHtml = `<div class="flex flex-wrap items-center gap-2 mb-4 p-2.5 rounded-xl border border-gray-200 bg-white shadow-sm">
    <span class="material-icons-outlined text-[16px]" style="color:#60A5FA">schedule</span>
    <span class="text-[11px] font-semibold text-gray-700">Automated run</span>
    ${chip("Event", env.GITHUB_EVENT_NAME || "unknown")}
    ${branch ? chip("Branch", branch) : ""}
    ${env.GITHUB_RUN_NUMBER ? chip("Run", "#" + env.GITHUB_RUN_NUMBER) : ""}
    ${commitChip}
    ${chip("Scope", partialRun ? "Affected tests only" : "Full suite", partialRun)}
    <a href="${esc(runUrl)}" target="_blank" rel="noopener"
       class="text-[10px] text-blue-500 hover:underline ml-auto">Open CI run &rarr;</a>
  </div>`;
} else {
  runContextHtml = `<div class="flex items-center gap-2 mb-4 p-2.5 rounded-xl border border-gray-200 bg-white shadow-sm">
    <span class="material-icons-outlined text-[16px]" style="color:#9CA3AF">computer</span>
    <span class="text-[11px] text-gray-500">Local run &mdash; not triggered from CI</span>
  </div>`;
}

const headerHtml = `<div class="grid grid-cols-5 gap-3 mb-4">
  ${card("Total Tests", stats.total, "#3B82F6")}
  ${card("Timestamp", fmtDateTime(runStartedAt), "#6B7280")}
  ${card("Environment", esc(envName.toUpperCase()), "#F59E0B")}
  ${card(
    "Duration",
    mins(wallMs),
    "#8B5CF6",
    delta(wallMs, prevOf("durationMs"), false, mins),
    "Wall-clock time of the run, start to finish"
  )}
  ${
    quarantinedRan.length
      ? card(
          "Quarantined",
          quarantined + " tagged",
          "#6B7280",
          `<div class="text-[9px] text-amber-600 mt-0.5">${quarantinedRan.length} ran in this run</div>`,
          "Tagged @quarantine, but this run did not exclude them — they ran and are counted in the totals"
        )
      : card("Quarantined", quarantined + " excluded", "#6B7280")
  }
</div>`;

// One stacked bar of the actual split. A grouped chart here repeated the
// tiles above (pass rate and passed % are the same number), which is noise.
const pct = (n) => (stats.total ? (n / stats.total) * 100 : 0);
const cleanPassed = stats.passed - stats.flaky;
const segments = [
  { l: "Passed", n: cleanPassed, c: "#10B981" },
  { l: "Flaky", n: stats.flaky, c: "#F59E0B" },
  { l: "Failed", n: stats.failed, c: "#EF4444" },
  { l: "API Failures", n: stats.apiFailures, c: "#DC2626" },
  { l: "Skipped", n: stats.skipped, c: "#D1D5DB" },
].filter((s) => s.n > 0);

const filterCard = (lbl, value, color, filter, hint, extra) =>
  `<div onclick="filterTests('${filter}')" title="${esc(hint || "")}"
        class="bg-white border border-gray-100 rounded-xl p-3 text-center shadow-sm cursor-pointer hover:border-gray-300 hover:shadow transition">
    <div class="text-[9px] font-semibold text-gray-400 uppercase tracking-wide mb-1">${esc(lbl)}</div>
    <div class="text-[14px] font-bold${color ? "" : " text-gray-700"}"${color ? ` style="color:${color}"` : ""}>${value}</div>
    ${extra || '<div class="text-[8px] text-gray-300 mt-0.5">click to filter</div>'}
  </div>`;

// Earlier entries predate these fields; without them there is nothing
// like-for-like to compare against, so no badge rather than a wrong one.
const prevTestFailures =
  previous && previous.apiFailures != null ? previous.failed - previous.apiFailures : null;
const prevPassRate = previous && previous.skipped != null ? previous.passRate : null;

// The retried/retry-passed/retry-failed tiles that used to sit under these
// only restated Flaky and Failed, so the retry count rides on those instead.
const resultsHtml = `${section(
  "Results",
  "(click a tile to filter — the five add up to " + stats.total + ", same definitions as the Slack summary)"
)}
<div class="grid grid-cols-6 gap-3 mb-2">
  ${card(
    "Pass Rate",
    stats.passRate.toFixed(1) + "%",
    sColor(stats.passRate),
    delta(stats.passRate, prevPassRate, true, (n) => n.toFixed(1) + "%"),
    "Passed (flaky included) out of the tests that ran — skipped tests are left out"
  )}
  ${filterCard("Passed", cleanPassed, "#10B981", "passed", "Passed first time, no retry needed")}
  ${filterCard("Failed", stats.failed, stats.failed ? "#EF4444" : "#9CA3AF", "failed",
    "Test failures, excluding API failures which are counted separately",
    delta(stats.failed, prevTestFailures, false, int))}
  ${filterCard("API Failures", stats.apiFailures, stats.apiFailures ? "#DC2626" : "#9CA3AF", "apifailed",
    "Backend/GraphQL failures caught by GraphQLWatcher — a broken API, not a broken test",
    delta(stats.apiFailures, prevOf("apiFailures"), false, int))}
  ${filterCard("Flaky", stats.flaky, stats.flaky ? "#F59E0B" : "#9CA3AF", "flaky",
    "Failed first, then passed on retry — same code, same environment, different result. " +
      stats.retried + " test(s) were retried in total.",
    delta(stats.flaky, prevOf("flaky"), false, int))}
  ${filterCard("Skipped", stats.skipped, "#9CA3AF", "skipped", "Tests skipped this run")}
</div>
<div class="bg-white border border-gray-100 rounded-xl p-4 shadow-sm mb-4">
  <div class="flex w-full h-5 rounded-md overflow-hidden">
    ${segments
      .map(
        (s) =>
          `<div style="width:${pct(s.n)}%;background:${s.c}" title="${s.l}: ${s.n}"></div>`
      )
      .join("")}
  </div>
  <div class="flex gap-4 mt-2.5">
    ${segments
      .map(
        (s) => `<div class="flex items-center gap-1.5">
      <span class="inline-block w-2.5 h-2.5 rounded-sm" style="background:${s.c}"></span>
      <span class="text-[10px] text-gray-500">${s.l} <span class="font-semibold text-gray-700">${s.n}</span>
      <span class="text-gray-400">(${pct(s.n).toFixed(1)}%)</span></span>
    </div>`
      )
      .join("")}
  </div>
</div>`;

const durationHtml = `${section("Duration")}
<div class="grid grid-cols-4 gap-3 mb-4">
  ${card(
    "Test Time (sum)",
    mins(stats.totalMs),
    null,
    "",
    "Every test's duration added up. Tests run on several workers at once, so this is more than the run's wall time."
  )}
  ${card("Avg / Test", secs(stats.avgMs))}
  ${card("P95", secs(stats.p95Ms))}
  ${filterCard("Slowest", secs(stats.maxMs), null, "slowest", "Show the 10 longest-running tests in the list below")}
</div>`;

// A group where every test was skipped has no rate to show.
const rateCell = (r) =>
  allSkipped(r)
    ? '<span class="text-[11px] text-center text-gray-400">&mdash;</span>'
    : `<span class="text-[11px] font-semibold text-center" style="color:${sColor(r.rate)}">${r.rate.toFixed(1)}%</span>`;

const TAG_COLS = "grid-cols-[1.6fr_0.5fr_0.4fr_0.4fr_0.4fr_0.4fr_0.4fr_0.5fr]";
const tagHtml = tags.length
  ? `${section("Results by Tag", "(click a tag to see its tests — a test carries several tags, so counts overlap)")}
<div class="bg-white border border-gray-100 rounded-xl shadow-sm overflow-hidden mb-4">
  <div class="grid ${TAG_COLS} gap-2 px-3 py-1.5 text-[9px] font-semibold text-gray-400 uppercase tracking-wide bg-gray-50 border-b border-gray-100">
    <span>Tag</span><span class="text-center">Pass Rate</span><span class="text-center">Total</span><span class="text-center">Passed</span>
    <span class="text-center">Failed</span><span class="text-center">Flaky</span><span class="text-center">Skipped</span><span class="text-center">Avg Time</span>
  </div>
  <div data-paged="10" class="flex flex-col gap-[1px]">
    ${tags
      .map(
        (r) => `<div onclick="filterTests('tag-${esc(r.tag)}')"
           class="pg-row grid ${TAG_COLS} gap-2 px-3 py-1.5 bg-white border border-gray-100 rounded-md items-center cursor-pointer hover:bg-gray-50">
      <span class="text-[11px] font-medium text-gray-700">@${esc(r.tag)}</span>
      ${rateCell(r)}
      <span class="text-[11px] text-center font-semibold text-gray-700">${r.total}</span>
      <span class="text-[11px] text-center text-gray-600">${r.passed}</span>
      <span class="text-[11px] text-center ${r.failed ? "text-red-500 font-semibold" : "text-gray-400"}">${r.failed}</span>
      <span class="text-[11px] text-center ${r.flaky ? "text-amber-500 font-semibold" : "text-gray-400"}">${r.flaky}</span>
      <span class="text-[11px] text-center text-gray-400">${r.skipped}</span>
      <span class="text-[11px] text-center text-gray-500">${secs(r.avgMs)}</span>
    </div>`
      )
      .join("")}
  </div>
</div>`
  : "";

// Failures first: the report exists to get someone to the broken test. A
// skipped test did not break anything, so it sits below the flaky ones.
const ordered = tests.slice().sort((a, b) => {
  const rank = (t) => (t.flaky ? 1 : t.status === "passed" ? 3 : t.status === "skipped" ? 2 : 0);
  return rank(a) - rank(b) || b.duration - a.duration;
});
const rowIndex = new Map(ordered.map((t, i) => [t, i]));

const DOT_COLOR = { passed: "#10B981", flaky: "#F59E0B", failed: "#EF4444", apifailed: "#DC2626", skipped: "#D1D5DB" };
const OUTCOME_LABEL = { passed: "passed", flaky: "flaky", failed: "failed", apifailed: "API failure", skipped: "skipped" };

// Only drawn where the history says something: ~1000 all-green rows of dots
// would add a megabyte to a report Slack has to preview, and tell nobody
// anything.
function historyDots(t) {
  if (!priorRuns.length) return "";
  const points = testHistory(t);
  if (points.every((p) => p.o === "passed")) return "";
  return (
    '<span class="inline-flex items-center gap-[3px] ml-1 align-middle" title="Last ' +
    points.length +
    ' full runs, oldest first — the ringed dot is this run">' +
    points
      .map(
        (p) =>
          `<i class="hd${p.current ? " hd-now" : ""}${p.o ? "" : " hd-none"}" style="${
            p.o ? "background:" + (DOT_COLOR[p.o] || "#9CA3AF") : ""
          }" title="${esc(fmtStamp(p.when))}: ${p.o ? OUTCOME_LABEL[p.o] || p.o : "not run"}"></i>`
      )
      .join("") +
    "</span>"
  );
}

// The Needs Attention tiles filter Test Details down to their own tests, so
// each row carries the buckets it falls in. A declaration: the rows are built
// before the lists that name these buckets.
function attentionCats(t) {
  const out = [];
  if (newFailures.indexOf(t) !== -1) out.push("newfail");
  if (knownFailures.indexOf(t) !== -1) out.push("still");
  if (unseenBefore.indexOf(t) !== -1) out.push("unseen");
  if (t.flaky && (flakeCounts[t.name] || 0) >= 2) out.push("repeatflaky");
  if (fixedNow.indexOf(t) !== -1) out.push("fixed");
  if (slowdown(t)) out.push("slower");
  return out;
}

const TEST_COLS = "grid-cols-[2.6fr_0.5fr_0.5fr]";
const testHtml = `${section("Test Details", "(failures and flaky tests first — click a row for the error)")}
<div class="flex items-center gap-2 mb-2">
  <input id="test-search" type="search" oninput="searchTests(this.value)" placeholder="Search by test name, spec file or tag"
         class="flex-1 text-[11px] px-2.5 py-1.5 rounded-md border border-gray-300 bg-white text-gray-700 focus:outline-none focus:border-blue-400">
</div>
<div id="filter-bar" class="hidden items-center gap-2 mb-2">
  <span class="text-[11px] text-gray-500">Showing <span id="filter-label" class="font-semibold text-gray-700"></span>
    — <span id="filter-count" class="font-semibold text-gray-700"></span> test(s)</span>
  <button onclick="filterTests('all')" class="text-[10px] px-2 py-0.5 rounded-md border border-gray-300 text-gray-600 hover:bg-gray-100">Show all</button>
</div>
<div class="bg-white border border-gray-100 rounded-xl shadow-sm overflow-hidden mb-4">
  <div class="grid ${TEST_COLS} gap-2 px-3 py-1.5 text-[9px] font-semibold text-gray-400 uppercase tracking-wide bg-gray-50 border-b border-gray-100">
    <span>Test</span><span class="text-center">Status</span><span class="text-center">Duration</span>
  </div>
  <div id="no-match" class="hidden px-3 py-6 text-center text-[11px] text-gray-400">No tests match this filter in this run.</div>
  <div class="flex flex-col gap-[1px]">
    ${ordered
      .map(function (t, i) {
        // A flaky test is reported as flaky and nothing else. It did end
        // green, but listing it under Passed too makes the tiles overlap and
        // stop adding up to the total.
        const cats = [t.flaky ? "flaky" : t.apiFailure ? "apifailed" : t.status]
          .concat(t.tags.map((x) => "tag-" + x))
          .concat(slowest.indexOf(t) !== -1 ? ["slowest"] : [])
          .concat(xCat.has(t) ? [xCat.get(t)] : [])
          .concat(attentionCats(t))
          .join(" ");
        // A flaky row shows its failed attempt, where a failing step is a
        // real failure — not one the test caught and carried on from.
        const testRecovered = t.status === "passed" && !t.flaky;
        const stepIcon = (s) =>
          s.status === "passed"
            ? '<span class="text-green-500">&#10003;</span>'
            : s.status === "skipped"
            ? '<span class="text-gray-300">&#8226;</span>'
            : testRecovered
            ? '<span class="text-amber-500">&#8635;</span>'
            : '<span class="text-red-500">&#10007;</span>';
        const VISIBLE_STEPS = 12;
        const stepRow = (s, hidden) =>
          `<div class="step-row-${i} ${hidden ? "hidden" : ""} grid grid-cols-[14px_1fr_50px] gap-2 items-start text-[11px] px-2 py-1 rounded ${
            s.status !== "passed" && s.status !== "skipped"
              ? (testRecovered ? "bg-amber-50" : "bg-red-50")
              : "bg-gray-50"
          }"${hidden ? ' data-extra="1"' : ""}>
            <span class="${s.child ? "pl-2" : ""}">${stepIcon(s)}</span>
            <span class="${s.child ? "pl-3 text-gray-500" : ""} ${
            s.status === "passed"
              ? "text-gray-600"
              : testRecovered
              ? "text-amber-700"
              : "text-red-600 font-medium"
          }">${esc(s.name)}${
            s.status !== "passed" && s.status !== "skipped" && testRecovered
              ? '<span class="text-[9px] text-amber-600 bg-amber-100 rounded px-1 ml-1.5">handled — the test caught this and carried on</span>'
              : ""
          }${s.at ? `<span class="text-[9px] text-gray-400 ml-1.5">${esc(s.at)}</span>` : ""}</span>
            <span class="text-right text-gray-400">${secs(s.duration)}</span>
          </div>`;

        // A failing test is read from where it broke, so the visible window
        // sits on the last failed step — the dozen passing steps before it are
        // one click away rather than in front of it.
        const items = t.steps.items;
        let lastBad = -1;
        items.forEach((s, n) => {
          if (s.status !== "passed" && s.status !== "skipped") lastBad = n;
        });
        let from = 0;
        let to = VISIBLE_STEPS;
        if (!testRecovered && lastBad >= VISIBLE_STEPS) {
          from = Math.max(0, lastBad - (VISIBLE_STEPS - 3));
          to = lastBad + 3;
        }
        const hiddenCount = items.filter((_, n) => n < from || n >= to).length;
        const stepsHtml = items.length
          ? `<div class="text-[10px] font-semibold text-gray-400 uppercase tracking-wide mb-1">
              ${t.steps.kind === "named" ? "Steps" : "Playwright actions"}
              ${
                t.steps.kind !== "actions"
                  ? ""
                  : t.steps.outside
                  ? '<span class="normal-case font-normal text-gray-400">— the failure happened outside the named test.step() blocks, so this is the full action trace</span>'
                  : '<span class="normal-case font-normal text-gray-400">— this test defines no named test.step() blocks, so this is the raw action trace</span>'
              }
            </div>
            <div class="flex flex-col gap-[2px] mb-2">
              ${
                from > 0
                  ? `<div class="text-[10px] text-gray-400 px-2">${from} earlier step${from > 1 ? "s" : ""} hidden — "Show all" below lists every step</div>`
                  : ""
              }
              ${items.map((s, n) => stepRow(s, n < from || n >= to)).join("")}
              ${
                hiddenCount
                  ? `<button id="more-${i}" onclick="toggleSteps(event, ${i})"
                       class="text-[10px] text-blue-500 hover:text-blue-700 hover:underline px-2 py-1 text-left">
                       Show all ${items.length} steps
                     </button>`
                  : ""
              }
            </div>`
          : '<div class="text-[11px] text-gray-400 mb-2">No step data recorded for this test.</div>';

        const shotHtml = t.screenshot
          ? `<div class="text-[10px] font-semibold text-gray-400 uppercase tracking-wide mt-2 mb-1">Screenshot at failure</div>
            <img src="${t.screenshot}" alt="Screenshot at the point of failure"
                 class="rounded-md border border-gray-200 max-w-full mb-2">`
          : "";

        const flakyNote = t.flaky
          ? `<div class="text-[11px] text-amber-800 bg-amber-50 border border-amber-200 rounded-md px-2 py-1.5 mb-2">
              Attempt ${t.failedAttempt} failed and the retry passed. The steps, error and screenshot below are from the failed attempt.
            </div>`
          : "";
        const streak = isBad(outcomeOf(t)) ? streakText(t) : "";
        const slow = slowdown(t);
        const link = specUrl(t);

        const errorHtml = t.message || t.trace
          ? `<div class="text-[10px] font-semibold text-red-500 uppercase tracking-wide mb-1">Error</div>
            <div class="text-[11px] font-semibold text-gray-800 bg-red-50 border border-red-100 rounded-md px-2 py-1.5 mb-1.5">${esc(
              headline(t)
            )}</div>
            <details>
              <summary class="text-[10px] text-gray-400 cursor-pointer hover:text-gray-600">Full log</summary>
              <pre class="log-output text-[11px] bg-gray-50 border border-gray-200 rounded-md p-2 mt-1 overflow-auto whitespace-pre-wrap max-h-[260px]">${esc(
                t.message || t.trace
              )}</pre>
            </details>`
          : "";

        return `<div id="test-${i}" class="test-row border-b border-gray-50" data-cats="${cats}" data-slow-rank="${slowest.indexOf(t)}" data-k="${nameIdx.has(t.name) ? nameIdx.get(t.name) : ""}">
      <div class="grid ${TEST_COLS} gap-2 px-3 py-2 items-start cursor-pointer hover:bg-gray-50" onclick="toggleDetail(${i})">
        <span>
          <span class="text-[11px] leading-snug">
            <span class="text-gray-400 mr-1">&#9656;</span>
            ${
              splitTitle(t.name).context
                ? `<span class="text-[9px] text-gray-500 bg-gray-100 rounded px-1 py-0.5 mr-1 align-middle">${esc(
                    splitTitle(t.name).context
                  )}</span>`
                : ""
            }
            <span class="text-gray-800">${esc(splitTitle(t.name).intent)}</span>
          </span>
          <span class="block text-[9px] text-gray-400 mt-0.5 ml-3">
            ${esc(t.suite)}
            ${t.retries ? `<span class="text-amber-500 ml-1">&middot; ${t.retries} retr${t.retries > 1 ? "ies" : "y"}</span>` : ""}
            ${streak ? `<span class="text-red-500 ml-1">&middot; ${esc(streak)}</span>` : ""}
            ${slow ? `<span class="text-amber-600 ml-1">&middot; slower than usual: ${Math.round(slow.now)}s, usually about ${slow.usual}s</span>` : ""}
            ${xBadge(t)}
            ${historyDots(t)}
          </span>
        </span>
        <span class="text-center">${statusPill(t)}</span>
        <span class="text-[11px] text-center text-gray-500">${secs(t.duration)}</span>
      </div>
      <div id="detail-${i}" class="hidden px-6 pb-3 bg-gray-50/50">
        <div class="flex flex-wrap gap-1 py-2">
          ${t.tags
            .map(
              (tag) =>
                `<span class="tag-chip text-[9px] px-1.5 py-0.5 rounded bg-white border border-gray-200 text-gray-500">@${esc(tag)}</span>`
            )
            .join("")}
          ${
            link
              ? `<a href="${esc(link)}" target="_blank" rel="noopener" onclick="event.stopPropagation()"
                   class="text-[10px] text-blue-500 hover:underline ml-auto">View spec on GitHub &rarr;</a>`
              : ""
          }
        </div>
        ${flakyNote}
        <div class="hist-slot mb-2" data-k="${nameIdx.has(t.name) ? nameIdx.get(t.name) : ""}"></div>
        ${stepsHtml}
        ${errorHtml}
        ${shotHtml}
      </div>
    </div>`;
      })
      .join("")}
  </div>
  <div id="tests-pager" class="flex items-center justify-between gap-2 px-3 py-2 border-t border-gray-100 bg-gray-50"></div>
</div>`;

// Every test named in a summary list opens its row in Test Details, so the
// monitor never has to hunt for it by name.
// The title's module prefix rides along: "case 4" alone does not say which
// product area broke.
const miniTest = (t, rich, note) => {
  const bad = isBad(outcomeOf(t));
  const streak = rich && bad ? streakText(t) : "";
  const head = rich && bad ? headline(t) : "";
  const change = rich && bad ? errorChange(t) : null;
  const ctx = splitTitle(t.name).context;
  return `<div onclick="openTest(${rowIndex.get(t)})" class="pg-row px-3 py-1.5 border-b border-gray-50 last:border-0 cursor-pointer hover:bg-gray-50">
    <div class="flex items-baseline gap-2 text-[11px]">
      <span class="text-gray-700 flex-1">${
        ctx ? `<span class="text-[9px] text-gray-500 bg-gray-100 rounded px-1 py-0.5 mr-1">${esc(ctx)}</span>` : ""
      }${esc(splitTitle(t.name).intent)}</span>
      ${t.apiFailure ? '<span class="text-[9px] font-semibold text-red-600">API</span>' : ""}
      ${isQuarantined(t) ? '<span class="text-[9px] text-amber-700 bg-amber-100 rounded px-1">quarantined</span>' : ""}
      <span class="text-[9px] text-gray-400">${esc(t.suite)}</span>
    </div>
    ${head ? `<div class="text-[10px] text-gray-500 mt-0.5 truncate">${esc(head)}</div>` : ""}
    ${
      change
        ? change.same
          ? '<div class="text-[10px] text-gray-400 mt-0.5">same error as the last full run</div>'
          : `<div class="text-[10px] text-amber-600 mt-0.5 truncate">error changed — last full run: ${esc(change.before)}</div>`
        : ""
    }
    ${streak ? `<div class="text-[10px] text-red-500 mt-0.5">${esc(streak)}</div>` : ""}
    ${note ? `<div class="text-[10px] text-gray-500 mt-0.5">${esc(note(t))}</div>` : ""}
  </div>`;
};

const LIST_TONE = {
  red: ["border-red-200", "bg-red-50 text-red-700 border-red-100"],
  green: ["border-green-200", "bg-green-50 text-green-700 border-green-200"],
  gray: ["border-gray-100", "bg-gray-50 text-gray-500 border-gray-100"],
};
const testList = (title, list, tone, note) =>
  list.length
    ? `<div data-paged="10" class="bg-white border ${LIST_TONE[tone][0]} rounded-xl shadow-sm overflow-hidden mb-3">
    <div class="px-3 py-1.5 text-[10px] font-semibold uppercase tracking-wide border-b ${LIST_TONE[tone][1]}">${esc(
      title
    )} <span class="normal-case font-normal">(${list.length})</span></div>
    ${list.map((t) => miniTest(t, true, note)).join("")}
  </div>`
    : "";

// Flaked now and at least once before: the ones worth fixing, as opposed to a
// one-off blip.
const repeatFlakersNow = tests
  .filter((t) => t.flaky && (flakeCounts[t.name] || 0) >= 2)
  .sort((a, b) => flakeCounts[b.name] - flakeCounts[a.name]);

const plural = (x, word) => x + " " + word + (x === 1 ? "" : "s");

// The one line a monitor reads first. Red means someone has to act today;
// amber means known or intermittent trouble; green means nothing to do.
const verdict = (function () {
  if (newFailures.length || stats.apiFailures) {
    // The same three buckets as the cards below, so the numbers match; API
    // failures are a note on top of them rather than a fourth bucket that
    // would count a new API failure twice.
    const parts = [];
    if (!previous) parts.push(plural(failingNow.length, "test") + " failing — no earlier full run on record to compare against");
    if (newFailures.length) parts.push(plural(newFailures.length, "new failure") + " since the last full run");
    if (knownFailures.length) parts.push(knownFailures.length + " still failing from earlier runs");
    if (previous && unseenBefore.length) parts.push(unseenBefore.length + " failing that the last full run did not run");
    if (stats.apiFailures) parts.push(stats.apiFailures + " of these " + (stats.apiFailures === 1 ? "is an API failure" : "are API failures"));
    return { tone: "red", icon: "error", text: parts.join(" · ") };
  }
  if (failingNow.length) {
    return {
      tone: "amber",
      icon: "warning",
      text: previous
        ? "No new failures — " + plural(failingNow.length, "test") + " still failing from earlier runs"
        : plural(failingNow.length, "test") + " failing — no earlier full run on record to compare against",
    };
  }
  if (stats.flaky) {
    return {
      tone: "amber",
      icon: "published_with_changes",
      text: "Nothing failed, but " + plural(stats.flaky, "test") + " only passed on retry",
    };
  }
  return { tone: "green", icon: "check_circle", text: "All " + plural(stats.total - stats.skipped, "test") + " passed" };
})();
if (fixedNow.length) verdict.text += " · " + fixedNow.length + " fixed since the last full run";

const TONE = {
  red: "bg-red-50 border-red-200 text-red-700",
  amber: "bg-amber-50 border-amber-200 text-amber-800",
  green: "bg-green-50 border-green-200 text-green-700",
};

const attentionCards =
  failingNow.length || stats.flaky || fixedNow.length || slowerNow.length
    ? `<div class="grid grid-cols-6 gap-3 mb-3">
  ${filterCard("New failures", newFailures.length, newFailures.length ? "#EF4444" : "#9CA3AF", "newfail", "Failing now, passing in the last full run")}
  ${filterCard("Still failing", knownFailures.length, knownFailures.length ? "#F59E0B" : "#9CA3AF", "still", "Also failed in the last full run")}
  ${filterCard("Not in last run", unseenBefore.length, unseenBefore.length ? "#6B7280" : "#9CA3AF", "unseen", "Failing now; the last full run did not run this test")}
  ${filterCard("Repeat flaky", repeatFlakersNow.length, repeatFlakersNow.length ? "#F59E0B" : "#9CA3AF", "repeatflaky", "Flaky now and in at least one earlier run")}
  ${filterCard("Fixed", fixedNow.length, fixedNow.length ? "#10B981" : "#9CA3AF", "fixed", "Failed in the last full run, passing now")}
  ${filterCard("Got slower", slowerNow.length, slowerNow.length ? "#F59E0B" : "#9CA3AF", "slower", "Passed, but took at least twice its usual time (and 20s+ longer)")}
</div>`
    : "";

const attentionHtml = `${section(
  "Needs Attention",
  previous
    ? "(compared against the last full run, " + fmtStamp(Date.parse(previous.when)) + " — click a test to open it)"
    : "(no earlier full run on record yet, so nothing can be compared)"
)}
<div class="flex items-center gap-2 border rounded-xl px-3 py-2.5 mb-3 ${TONE[verdict.tone]}">
  <span class="material-icons-outlined text-[20px]">${verdict.icon}</span>
  <span class="text-[13px] font-semibold">${esc(verdict.text)}</span>
</div>
${attentionCards}
${testList("New failures — were passing in the last full run", newFailures, "red")}
${testList("Still failing", knownFailures, "gray")}
${testList("Failing, not in the last full run", unseenBefore, "gray")}
${testList("Flaky again — flaked in earlier runs too", repeatFlakersNow, "gray", (t) =>
  "flaked in " + flakeCounts[t.name] + " of the last " + upToNow.length + " runs"
)}
${testList(
  "Got much slower — passed, but well past its usual time",
  slowerNow.map((x) => x.t),
  "gray",
  (t) => {
    const x = slowdown(t);
    return Math.round(x.now) + "s now, usually about " + x.usual + "s";
  }
)}
${testList("Fixed — failed in the last full run, passing now", fixedNow, "green")}`;

// Only rendered when something is failing — on a green run it would be an
// empty box.
const causesHtml = failingNow.length
  ? `${section("Failures by Cause", "(" + failingNow.length + " failure(s) across " + causes.length + " distinct cause(s))")}
<div data-paged="10" class="bg-white border border-gray-100 rounded-xl shadow-sm overflow-hidden mb-4">
  ${causes
    .map(
      (c) => `<div data-paged="10" class="pg-row border-b border-gray-50 last:border-0">
    <div class="flex items-center gap-2 px-3 py-2 bg-gray-50">
      <span class="text-[9px] font-bold text-white bg-red-500 rounded px-1.5 py-0.5">${c.count}</span>
      <span class="text-[11px] font-semibold text-gray-700">${esc(c.cause)}</span>
    </div>
    ${c.tests.map((t) => miniTest(t, false)).join("")}
  </div>`
    )
    .join("")}
</div>`
  : "";

// How a test fared on the other side, in one line under its name.
function xNote(t) {
  const there = otherOutcome(t);
  if (there === undefined) return THERE + ": not in its last full run — the spec may not be on that branch yet";
  const label = OUTCOME_LABEL[there] || there;
  const err = other.errors ? other.errors[t.name] : undefined;
  if (!isBad(there) || !err) return THERE + ": " + label;
  if (isBad(outcomeOf(t)) && err === headline(t).slice(0, 160)) return THERE + ": " + label + " with the same error";
  return THERE + ": " + label + " — " + err;
}

// A declaration, not a const: the Test Details rows above are built first.
function xBadge(t) {
  if (!other || !xCat.has(t)) return "";
  const there = otherOutcome(t);
  const color = there === undefined ? "text-gray-400" : isBad(there) ? "text-red-500" : "text-green-600";
  return `<span class="${color} ml-1">&middot; ${esc(THERE)}: ${esc(there === undefined ? "not run" : OUTCOME_LABEL[there] || there)}</span>`;
}

// Both sides' numbers, worked out once for the full section and for the strip
// at the top of the report.
const xenv = other
  ? (function () {
      const hours = Math.round((runStartedAt - Date.parse(other.when)) / 3600000);
      return {
        here: tally(testOutcomes),
        there: tally(other.tests || {}),
        gap: hours >= 0 ? hours + "h older than this run" : -hours + "h newer than this run",
        thereWhen: fmtStamp(Date.parse(other.when)),
      };
    })()
  : null;

const runLink = (url, run) =>
  url
    ? `<a href="${esc(url)}" target="_blank" rel="noopener" class="text-blue-500 hover:underline">run #${esc(run)}</a>`
    : run
    ? "run #" + esc(run)
    : "";

// One environment at a glance: pass rate, the split of its tests, where the
// numbers came from.
function envCard(label, sub, t, meta) {
  const pct = (n) => (t.total ? (n / t.total) * 100 : 0);
  const segs = [
    [t.passed, "#10B981", "Passed"],
    [t.flaky, "#F59E0B", "Flaky"],
    [t.failed, "#EF4444", "Failed"],
    [t.skipped, "#D1D5DB", "Skipped"],
  ].filter((s) => s[0] > 0);
  const stat = (v, l, c) =>
    `<div><div class="text-[15px] font-bold${c ? "" : " text-gray-700"}"${c ? ` style="color:${c}"` : ""}>${v}</div>
      <div class="text-[9px] uppercase tracking-wide text-gray-400">${l}</div></div>`;
  return `<div class="bg-white border border-gray-100 rounded-xl shadow-sm p-4">
  <div class="flex items-center gap-2 mb-2">
    <span class="env-badge text-[10px] font-bold px-2 py-0.5 rounded bg-navy text-white tracking-wide">${esc(label)}</span>
    <span class="text-[10px] text-gray-400">${esc(sub)}</span>
    <span class="ml-auto text-[10px] text-gray-400">${meta}</span>
  </div>
  <div class="flex items-baseline gap-2 mb-2">
    <span class="text-[28px] font-bold leading-none" style="color:${sColor(t.rate)}">${t.rate.toFixed(1)}%</span>
    <span class="text-[11px] text-gray-500">pass rate &middot; ${t.total} tests</span>
  </div>
  <div class="flex w-full h-2.5 rounded-full overflow-hidden bg-gray-100 mb-3">
    ${segs.map((s) => `<div style="width:${pct(s[0])}%;background:${s[1]}" title="${s[2]}: ${s[0]}"></div>`).join("")}
  </div>
  <div class="grid grid-cols-4 gap-2 text-center">
    ${stat(t.passed, "Passed", "#10B981")}${stat(t.failed, "Failed", t.failed ? "#EF4444" : null)}${stat(
    t.flaky,
    "Flaky",
    t.flaky ? "#F59E0B" : null
  )}${stat(t.skipped, "Skipped", null)}
  </div>
</div>`;
}

// Where the failures sit: only here, on both, only there. Each part filters
// Test Details to its tests. Widths follow the counts, with a floor so a
// small but non-zero part can still be clicked.
function overlapBar() {
  const parts = [
    [xHere.length, "#F59E0B", "xhere", "Only on " + HERE, "usually code on this branch the other has not got yet"],
    [xBoth.length, "#EF4444", "xboth", "On both", "usually the test itself, or code both branches share"],
    [xThere.length, "#6366F1", "xthere", "Only on " + THERE, "usually that environment or its data — or already fixed here"],
  ];
  const sum = parts.reduce((a, p) => a + p[0], 0);
  if (!sum) {
    return '<div class="text-[12px] text-green-600 font-semibold py-3">No test is failing on either side of the comparison.</div>';
  }
  const shown = parts.filter((p) => p[0] > 0);
  const floor = 12;
  const raw = shown.map((p) => Math.max(floor, (p[0] / sum) * 100));
  const scale = 100 / raw.reduce((a, b) => a + b, 0);
  return `<div class="flex h-9 rounded-lg overflow-hidden text-[11px] font-semibold text-white mb-3">
    ${shown
      .map(
        (p, k) => `<div onclick="filterTests('${p[2]}')" title="${esc(p[3])}: ${p[0]} — click to list them"
         class="flex items-center justify-center cursor-pointer hover:opacity-90" style="width:${raw[k] * scale}%;background:${p[1]}">${p[0]}</div>`
      )
      .join("")}
  </div>
  <div class="grid grid-cols-3 gap-3">
    ${parts
      .map(
        (p) => `<div onclick="filterTests('${p[2]}')" class="cursor-pointer rounded-lg px-2 py-1.5 hover:bg-gray-50">
      <div class="flex items-center gap-1.5 text-[11px] font-semibold text-gray-700">
        <span class="inline-block w-2.5 h-2.5 rounded-sm" style="background:${p[1]}"></span>${esc(p[3])}
        <span class="ml-auto" style="color:${p[0] ? p[1] : "#9CA3AF"}">${p[0]}</span>
      </div>
      <div class="text-[10px] text-gray-400 mt-0.5">${esc(p[4])}</div>
    </div>`
      )
      .join("")}
  </div>`;
}

// Every figure side by side, with the gap between them coloured by which way
// is good news for this environment.
function compareTable() {
  const row = (label, a, b, betterUp, fmt) => {
    const d = a - b;
    const f = fmt || ((v) => String(v));
    const color = !d || betterUp === null ? "text-gray-400" : (d > 0) === betterUp ? "text-green-600" : "text-red-500";
    const sign = d > 0 ? "+" : d < 0 ? "−" : "";
    return `<div class="grid grid-cols-[1fr_0.8fr_0.8fr_0.7fr] gap-2 px-3 py-1.5 border-b border-gray-50 last:border-0 text-[11px]">
    <span class="text-gray-500">${label}</span>
    <span class="text-center font-semibold text-gray-700">${f(a)}</span>
    <span class="text-center font-semibold text-gray-700">${f(b)}</span>
    <span class="text-center font-semibold ${color}">${d ? sign + f(Math.abs(d)) : "="}</span>
  </div>`;
  };
  const pctF = (v) => v.toFixed(1) + "%";
  const h = xenv.here;
  const t = xenv.there;
  return `<div class="bg-white border border-gray-100 rounded-xl shadow-sm overflow-hidden">
  <div class="grid grid-cols-[1fr_0.8fr_0.8fr_0.7fr] gap-2 px-3 py-1.5 text-[9px] font-semibold text-gray-400 uppercase tracking-wide bg-gray-50 border-b border-gray-100">
    <span></span><span class="text-center">${esc(HERE)}</span><span class="text-center">${esc(THERE)}</span><span class="text-center">Difference</span>
  </div>
  ${row("Pass rate", h.rate, t.rate, true, pctF)}
  ${row("Tests", h.total, t.total, null)}
  ${row("Passed", h.passed, t.passed, true)}
  ${row("Failed", h.failed, t.failed, false)}
  ${row("Flaky", h.flaky, t.flaky, false)}
  ${row("Skipped", h.skipped, t.skipped, null)}
</div>`;
}

const crossEnvHtml = (function () {
  if (!compareEnv) return "";
  const title = HERE + " vs " + THERE;
  if (!other) {
    return `${section(title)}
<div class="bg-white border border-gray-100 rounded-xl p-6 shadow-sm mb-4 text-center text-[11px] text-gray-400">
  No full ${esc(THERE)} run on record yet, so there is nothing to compare this run against.
</div>`;
  }
  const hereMeta = [
    esc(fmtStamp(runStartedAt)),
    runLink(ghRunUrl, process.env.GITHUB_RUN_NUMBER || ""),
    sha ? "commit " + esc(sha.slice(0, 7)) : "",
  ]
    .filter(Boolean)
    .join(" &middot; ");
  const thereMeta = [
    esc(xenv.thereWhen),
    runLink(other.url, other.run),
    other.sha ? "commit " + esc(String(other.sha).slice(0, 7)) : "",
  ]
    .filter(Boolean)
    .join(" &middot; ");
  return `${section(title, "(this run against the latest full " + THERE + " run — click any count to list those tests)")}
<div class="text-[11px] text-gray-500 mb-2">
  The ${esc(THERE)} figures are ${esc(xenv.gap)}. The two environments run different branches, so a difference can be code that has not been promoted yet.
</div>
<div class="grid grid-cols-2 gap-3 mb-3">
  ${envCard(HERE, partialRun ? "this run · affected tests only" : "this run", xenv.here, hereMeta)}
  ${envCard(THERE, "latest full run", xenv.there, thereMeta)}
</div>
<div class="grid grid-cols-[1.3fr_1fr] gap-3 mb-3">
  <div class="bg-white border border-gray-100 rounded-xl shadow-sm p-4">
    <div class="flex items-center mb-2">
      <span class="text-[12px] font-semibold text-gray-700">Where the failures are</span>
      ${
        xNotThere.length
          ? `<span onclick="filterTests('xnothere')" class="ml-auto cursor-pointer text-[10px] px-2 py-0.5 rounded border border-gray-200 text-gray-500 hover:bg-gray-50">+ ${xNotThere.length} failing here, not run on ${esc(THERE)}</span>`
          : ""
      }
    </div>
    ${overlapBar()}
  </div>
  ${compareTable()}
</div>
${testList("Failing on both " + HERE + " and " + THERE, xBoth, "red", xNote)}
${testList("Failing only on " + HERE, xHere, "gray", xNote)}
${testList("Failing only on " + THERE + " — passing here", xThere, "gray", xNote)}
${testList("Failing here, not in the last full " + THERE + " run", xNotThere, "gray", xNote)}`;
})();

// The same comparison in one line, under the summary at the top of the
// report, for the reader who only wants to know whether both sides agree.
const xenvStripHtml = (function () {
  if (!compareEnv || !other) return "";
  const chip = (n, label, cat, color) =>
    `<span onclick="filterTests('${cat}')" title="List these tests"
       class="cursor-pointer inline-flex items-center gap-1 text-[11px] px-2 py-1 rounded-md border border-gray-200 hover:bg-gray-50">
       <b style="color:${n ? color : "#9CA3AF"}">${n}</b><span class="text-gray-600">${esc(label)}</span></span>`;
  return `<div class="flex flex-wrap items-center gap-2 mb-4 p-2.5 rounded-xl border border-gray-200 bg-white shadow-sm">
  <span class="material-icons-outlined text-[18px]" style="color:#FB923C">compare_arrows</span>
  <span class="text-[12px] font-semibold text-gray-700">${esc(HERE)} vs ${esc(THERE)}</span>
  <span class="text-[12px] font-bold" style="color:${sColor(xenv.here.rate)}">${xenv.here.rate.toFixed(1)}%</span>
  <span class="text-[11px] text-gray-400">vs</span>
  <span class="text-[12px] font-bold" style="color:${sColor(xenv.there.rate)}">${xenv.there.rate.toFixed(1)}%</span>
  <span class="w-px h-5 bg-gray-200 mx-1"></span>
  ${chip(xBoth.length, "failing on both", "xboth", "#EF4444")}
  ${chip(xHere.length, "only on " + HERE, "xhere", "#F59E0B")}
  ${chip(xThere.length, "only on " + THERE, "xthere", "#6366F1")}
  ${xNotThere.length ? chip(xNotThere.length, "not run on " + THERE, "xnothere", "#6B7280") : ""}
  <span class="text-[10px] text-gray-400 ml-auto">${esc(THERE)} data ${esc(xenv.gap)}</span>
  <a href="#xenv" class="text-[11px] text-blue-500 hover:underline">Details &darr;</a>
</div>`;
})();

const flakeHtml = repeatFlakes.length
  ? `${section("Flaky Frequency", "(how often each test flaked across the last " + upToNow.length + " run(s) on record)")}
<div data-paged="10" class="bg-white border border-gray-100 rounded-xl shadow-sm overflow-hidden mb-4">
  <div class="grid grid-cols-[2.6fr_0.6fr] gap-2 px-3 py-1.5 text-[9px] font-semibold text-gray-400 uppercase tracking-wide bg-gray-50 border-b border-gray-100">
    <span>Test</span><span class="text-center">Flaked in</span>
  </div>
  ${repeatFlakes
    .map(
      (f) => `<div class="pg-row grid grid-cols-[2.6fr_0.6fr] gap-2 px-3 py-1.5 items-center border-b border-gray-50 last:border-0">
    <span class="text-[11px] text-gray-700">${
      splitTitle(f.name).context
        ? `<span class="text-[9px] text-gray-500 bg-gray-100 rounded px-1 py-0.5 mr-1">${esc(splitTitle(f.name).context)}</span>`
        : ""
    }${esc(splitTitle(f.name).intent)}</span>
    <span class="text-[11px] text-center ${f.count > 1 ? "text-red-500 font-semibold" : "text-amber-500"}">
      ${f.count} of ${upToNow.length}
    </span>
  </div>`
    )
    .join("")}
</div>`
  : "";

const MOD_COLS = "grid-cols-[1.8fr_0.5fr_0.4fr_0.4fr_0.4fr_0.4fr_0.4fr_0.5fr]";
const moduleHtml = `${section("Results by Module", "(grouped by spec folder — which product area is weakest)")}
<div data-paged="10" class="bg-white border border-gray-100 rounded-xl shadow-sm overflow-hidden mb-4">
  <div class="grid ${MOD_COLS} gap-2 px-3 py-1.5 text-[9px] font-semibold text-gray-400 uppercase tracking-wide bg-gray-50 border-b border-gray-100">
    <span>Module</span><span class="text-center">Pass Rate</span><span class="text-center">Total</span>
    <span class="text-center">Passed</span><span class="text-center">Failed</span>
    <span class="text-center">Flaky</span><span class="text-center">Skipped</span><span class="text-center">Avg Time</span>
  </div>
  ${modules
    .map(
      (m) => `<div class="pg-row grid ${MOD_COLS} gap-2 px-3 py-1.5 items-center border-b border-gray-50 last:border-0">
    <span class="text-[11px] font-medium text-gray-700">${esc(m.mod)}</span>
    ${rateCell(m)}
    <span class="text-[11px] text-center font-semibold text-gray-700">${m.total}</span>
    <span class="text-[11px] text-center text-gray-600">${m.passed}</span>
    <span class="text-[11px] text-center ${m.failed ? "text-red-500 font-semibold" : "text-gray-400"}">${m.failed}</span>
    <span class="text-[11px] text-center ${m.flaky ? "text-amber-500 font-semibold" : "text-gray-400"}">${m.flaky}</span>
    <span class="text-[11px] text-center text-gray-400">${m.skipped}</span>
    <span class="text-[11px] text-center text-gray-500">${secs(m.avgMs)}</span>
  </div>`
    )
    .join("")}
</div>`;

const Q_COLS = "grid-cols-[2.4fr_1fr_0.5fr]";
const quarantineHtml = `${section(
  "Quarantined Tests",
  quarantinedRan.length
    ? "(tagged @quarantine as known-broken — this run did not exclude them, so they ran)"
    : "(excluded from this run via --grep-invert @quarantine — these did NOT run)"
)}
<div class="bg-amber-50 border border-amber-200 rounded-xl px-3 py-2 mb-2 text-[11px] text-amber-800">${
  quarantinedRan.length
    ? `These ${quarantined} tests are tagged known-broken or intentionally disabled. The run did not pass
  --grep-invert @quarantine, so ${quarantinedRan.length} of them ran and are counted in the ${stats.total} above —
  including ${quarantinedRan.filter((t) => isBad(outcomeOf(t))).length} of this run's failures.`
    : `These ${quarantined} tests are known-broken or intentionally disabled, so they are skipped before the run starts.
  They are not counted in the ${stats.total} above.`
} Reasons are tracked in <span class="font-semibold">app-e2e-tests/TAGGING.md</span>.
</div>
<div class="bg-white border border-gray-100 rounded-xl shadow-sm overflow-hidden mb-4">
  <div class="grid ${Q_COLS} gap-2 px-3 py-1.5 text-[9px] font-semibold text-gray-400 uppercase tracking-wide bg-gray-50 border-b border-gray-100">
    <span>Test</span><span>Spec File</span><span class="text-center">This run</span>
  </div>
  <div class="flex flex-col gap-[1px]">
    ${quarantinedTests
      .map(
        (q) => `<div class="q-row grid ${Q_COLS} gap-2 px-3 py-1.5 items-center border-b border-gray-50">
      <span class="text-[11px] text-gray-700">${esc(q.name)}
        ${q.skipped ? '<span class="text-[9px] text-amber-600 bg-amber-50 border border-amber-200 rounded px-1 ml-1">test.skip() in source</span>' : ""}
      </span>
      <span class="text-[10px] text-gray-400">${esc(q.file)}</span>
      <span class="text-center">${
        quarantineResult(q) ? statusPill(quarantineResult(q)) : '<span class="text-[11px] text-gray-400">not run</span>'
      }</span>
    </div>`
      )
      .join("")}
  </div>
  <div id="q-pager" class="flex items-center justify-between px-3 py-2 border-t border-gray-100 bg-gray-50"></div>
</div>`;

const trendPoints = trendHistory.slice(-10);

// Three readings of the same runs: rate per run, what the run was made of,
// and the shape over time. Drawn by hand rather than pulling in a chart
// library — the report has to stay one self-contained file.
function trendBars() {
  return `<div class="flex items-end gap-3 h-[130px] px-2">
    ${trendPoints
      .map(
        (h) => `<div class="flex-1 flex flex-col items-center gap-1" style="max-width:90px">
      <span class="text-[9px] font-semibold" style="color:${sColor(h.passRate)}">${h.passRate.toFixed(0)}%</span>
      <div class="w-full rounded-t" style="height:${Math.max(2, h.passRate)}px;background:${sColor(h.passRate)}"></div>
      <span class="text-[8px] text-gray-400">${fmtStamp(h.when)}</span>
    </div>`
      )
      .join("")}
  </div>`;
}

function trendStacked() {
  const maxTotal = Math.max.apply(
    null,
    trendPoints.map((h) => (h.passed || 0) + (h.failed || 0))
  ) || 1;
  return `<div class="flex items-end gap-3 h-[130px] px-2">
    ${trendPoints
      .map(function (h) {
        const clean = Math.max(0, (h.passed || 0) - (h.flaky || 0));
        const scale = 110 / maxTotal;
        const seg = (n, c) =>
          n > 0 ? `<div style="height:${Math.max(2, n * scale)}px;background:${c}" title="${n}"></div>` : "";
        return `<div class="flex-1 flex flex-col items-center gap-1" style="max-width:90px">
      <span class="text-[9px] text-gray-400">${(h.passed || 0) + (h.failed || 0)}</span>
      <div class="w-full flex flex-col-reverse rounded-t overflow-hidden">
        ${seg(clean, "#10B981")}${seg(h.flaky || 0, "#F59E0B")}${seg(h.failed || 0, "#EF4444")}
      </div>
      <span class="text-[8px] text-gray-400">${fmtStamp(h.when)}</span>
    </div>`;
      })
      .join("")}
  </div>
  <div class="flex gap-4 px-2 pt-2">
    <span class="flex items-center gap-1 text-[10px] text-gray-500"><span class="w-2.5 h-2.5 rounded-sm" style="background:#10B981"></span>Passed</span>
    <span class="flex items-center gap-1 text-[10px] text-gray-500"><span class="w-2.5 h-2.5 rounded-sm" style="background:#F59E0B"></span>Flaky</span>
    <span class="flex items-center gap-1 text-[10px] text-gray-500"><span class="w-2.5 h-2.5 rounded-sm" style="background:#EF4444"></span>Failed</span>
  </div>`;
}

function trendLine() {
  const W = 900, H = 140, padX = 30, padY = 18;
  const n = trendPoints.length;
  const rates = trendPoints.map((h) => h.passRate);
  // A fixed 0-100 axis flattens the range that actually matters (a drop from
  // 98% to 94% is invisible), so the axis follows the data — with a minimum
  // 10-point window so noise is not magnified, and both bounds labelled so
  // the zoom is never misread.
  const lo = Math.max(0, Math.floor(Math.min.apply(null, rates) - 2));
  const hi = Math.min(100, Math.ceil(Math.max.apply(null, rates) + 2));
  const min = hi - lo < 10 ? Math.max(0, hi - 10) : lo;
  const span = hi - min || 1;
  const x = (i) => (n === 1 ? W / 2 : padX + (i * (W - padX * 2)) / (n - 1));
  const y = (v) => H - padY - ((v - min) / span) * (H - padY * 2);
  const ticks = [min, min + span / 2, hi];
  return `<div class="text-gray-400 px-2">
    <svg viewBox="0 0 ${W} ${H}" class="w-full" style="height:160px">
      ${ticks
        .map(
          (g) =>
            `<line x1="${padX}" x2="${W - padX}" y1="${y(g)}" y2="${y(g)}" stroke="currentColor" stroke-opacity="0.2" stroke-width="1"/>
             <text x="2" y="${y(g) + 3}" font-size="9" fill="currentColor">${g.toFixed(0)}%</text>`
        )
        .join("")}
      ${
        n > 1
          ? `<polyline points="${trendPoints.map((h, i) => x(i) + "," + y(h.passRate)).join(" ")}"
               fill="none" stroke="#3B82F6" stroke-width="2.5" vector-effect="non-scaling-stroke"/>`
          : ""
      }
      ${trendPoints
        .map(
          (h, i) =>
            `<circle cx="${x(i)}" cy="${y(h.passRate)}" r="4.5" fill="${sColor(h.passRate)}"><title>${h.passRate}%</title></circle>`
        )
        .join("")}
    </svg>
    <div class="flex justify-between px-1">
      ${trendPoints
        .map(
          (h) =>
            `<span class="text-[8px] text-gray-400">${fmtStamp(h.when)} &middot; ${h.passRate.toFixed(1)}%</span>`
        )
        .join("")}
    </div>
    <div class="text-[9px] text-gray-400 px-1 pt-1">Axis zoomed to ${min.toFixed(0)}-${hi.toFixed(0)}% to show the change.</div>
  </div>`;
}

const trendBtn = (id, label) =>
  `<button onclick="showTrend('${id}')" id="trend-btn-${id}"
     class="trend-btn text-[10px] px-2 py-1 rounded border border-gray-300 text-gray-600 hover:bg-gray-100">${label}</button>`;

const trendHtml =
  trendPoints.length
    ? `${section(
        "Trend",
        "(last " +
          trendPoints.length +
          " full run" +
          (trendPoints.length > 1 ? "s" : "") +
          " on record" +
          (partialRun ? " — this run covered only the affected tests, so it is not plotted" : "") +
          ")"
      )}
<div class="bg-white border border-gray-100 rounded-xl p-3 shadow-sm mb-4">
  <div class="flex items-center gap-1.5 mb-3">
    ${trendBtn("bars", "Pass rate")}${trendBtn("stacked", "Composition")}${trendBtn("line", "Over time")}
    ${
      trendPoints.length < 2
        ? '<span class="ml-auto text-[10px] text-gray-400">Only one run so far — a trend needs at least two.</span>'
        : ""
    }
  </div>
  <div id="trend-bars">${trendBars()}</div>
  <div id="trend-stacked" class="hidden">${trendStacked()}</div>
  <div id="trend-line" class="hidden">${trendLine()}</div>
</div>`
    : `${section("Trend")}
<div class="bg-white border border-gray-100 rounded-xl p-6 shadow-sm mb-4 text-center text-[11px] text-gray-400">
  No runs on record yet — the trend appears once a run has been generated.
</div>`;

// "What failed on Tuesday, and what has changed since?" — every recorded run,
// newest first. The failure lists and comparisons are built in the page from
// RUN_DATA, so twenty runs cost one table here rather than twenty.
const RH_COLS = "grid-cols-[1.3fr_0.6fr_0.6fr_0.5fr_0.5fr_0.6fr_1.6fr]";
const runHistoryHtml = `${section(
  "Run History",
  "(every full run on record, newest first — open one to see what failed, or compare it with this run)"
)}
<div data-paged="10" class="bg-white border border-gray-100 rounded-xl shadow-sm overflow-hidden mb-3">
  <div class="grid ${RH_COLS} gap-2 px-3 py-1.5 text-[9px] font-semibold text-gray-400 uppercase tracking-wide bg-gray-50 border-b border-gray-100">
    <span>When</span><span class="text-center">Run</span><span class="text-center">Pass Rate</span>
    <span class="text-center">Failed</span><span class="text-center">Flaky</span><span class="text-center">Duration</span><span></span>
  </div>
  ${RUN_DATA.runs
    .map((r, ri) => ({ r: r, ri: ri }))
    .reverse()
    .map(({ r, ri }) => {
      const isNow = ri === RUN_DATA.runs.length - 1;
      return `<div class="pg-row grid ${RH_COLS} gap-2 px-3 py-1.5 items-center border-b border-gray-50 last:border-0 ${isNow ? "bg-blue-50/40" : ""}">
    <span class="text-[11px] text-gray-700">${esc(fmtStamp(r.when))}${
        isNow ? ' <span class="text-[9px] font-semibold text-blue-600">THIS RUN</span>' : ""
      }${r.partial ? ' <span class="text-[9px] text-amber-600">(affected tests only)</span>' : ""}</span>
    <span class="text-[11px] text-center">${
      r.url
        ? `<a href="${esc(r.url)}" target="_blank" rel="noopener" class="text-blue-500 hover:underline">#${esc(r.run || "?")}</a>`
        : esc(r.run ? "#" + r.run : "—")
    }</span>
    <span class="text-[11px] font-semibold text-center" style="color:${sColor(r.passRate)}">${r.passRate.toFixed(1)}%</span>
    <span class="text-[11px] text-center ${r.failed ? "text-red-500 font-semibold" : "text-gray-400"}">${r.failed}</span>
    <span class="text-[11px] text-center ${r.flaky ? "text-amber-500 font-semibold" : "text-gray-400"}">${r.flaky}</span>
    <span class="text-[11px] text-center text-gray-500">${r.durationMs ? mins(r.durationMs) : "—"}</span>
    <span class="flex justify-end gap-1.5">
      <button onclick="showRunFailures(${ri})" class="text-[10px] px-2 py-0.5 rounded border border-gray-300 text-gray-600 hover:bg-gray-100">What failed</button>
      ${
        isNow
          ? ""
          : `<button onclick="compareRun(${ri})" class="text-[10px] px-2 py-0.5 rounded border border-blue-300 text-blue-600 hover:bg-blue-50">Compare with this run</button>`
      }
    </span>
  </div>`;
    })
    .join("")}
</div>
<div id="run-panel" class="mb-4"></div>`;

// Inlined as data, not markup. "<" is escaped so a test title can never close
// the script tag early.
const runDataJson = JSON.stringify(RUN_DATA).replace(/</g, "\\u003c");

const html = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>E2E QA Automation — Nudgebee</title>
<link rel="icon" type="image/svg+xml" href="${faviconUri}">
<script src="https://cdn.tailwindcss.com"><\/script>
<script>
tailwind.config = {
  theme: { extend: {
    colors: { navy: '#1B2D4A', nb: { yellow: '#FACF39', blue: '#3B82F6' } },
    fontFamily: { sans: ['Inter', 'Roboto', 'sans-serif'] }
  } }
}
<\/script>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
<link href="https://fonts.googleapis.com/icon?family=Material+Icons+Outlined" rel="stylesheet">
<style>
  body { font-family: 'Inter', sans-serif; -webkit-font-smoothing: antialiased; }
  pre.log-output { font-family: 'SF Mono', Menlo, Consolas, monospace; }
  .nav-link { transition: all 0.2s; }
  .sidebar-wrapper { width: 220px; }
  .main-content-area { margin-left: 220px; }

  /* Dark theme, same override approach the benchmark report uses: recolour
     the Tailwind utilities rather than maintaining a second set of classes. */
  body.dark { background-color: #0b1220 !important; color: #e2e8f0; }
  body.dark .bg-white { background-color: #1e293b !important; }
  body.dark .bg-gray-50, body.dark .bg-gray-50\\/50 { background-color: #0f172a !important; }
  body.dark .bg-gray-100 { background-color: #334155 !important; }
  body.dark .text-gray-800, body.dark .text-gray-700 { color: #e2e8f0 !important; }
  body.dark .text-gray-600 { color: #cbd5e1 !important; }
  body.dark .text-gray-500 { color: #94a3b8 !important; }
  body.dark .text-gray-400 { color: #7c8aa0 !important; }
  body.dark .text-gray-300 { color: #64748b !important; }
  body.dark .border-gray-100,
  body.dark .border-gray-200,
  body.dark .border-gray-300,
  body.dark .border-gray-50 { border-color: #334155 !important; }
  body.dark .bg-red-50 { background-color: #3f1d1d !important; }
  body.dark .border-red-100 { border-color: #7f1d1d !important; }
  body.dark .bg-amber-50 { background-color: #3b2f11 !important; }
  body.dark .border-amber-200 { border-color: #92400e !important; }
  body.dark .text-amber-800 { color: #fcd34d !important; }
  body.dark .text-amber-600 { color: #fbbf24 !important; }
  body.dark .hover\\:bg-gray-50:hover { background-color: #172033 !important; }
  body.dark pre.log-output { background-color: #0f172a !important; color: #e2e8f0; }
  body.dark img { opacity: 0.92; }
  body.dark .bg-green-50 { background-color: #12301f !important; }
  body.dark .bg-blue-50\\/40, body.dark .bg-blue-50\\/30 { background-color: #172a45 !important; }
  body.dark .border-blue-200 { border-color: #1e3a8a !important; }
  body.dark .env-badge { background-color: #475569 !important; }
  body.dark .bg-gray-200 { background-color: #334155 !important; }
  body.dark .border-green-200 { border-color: #166534 !important; }
  body.dark .border-red-200 { border-color: #7f1d1d !important; }
  body.dark .text-red-700 { color: #fca5a5 !important; }
  body.dark .text-green-700 { color: #86efac !important; }
  body.dark input[type=search] { background-color: #0f172a; color: #e2e8f0; }
  /* One dot per recent full run in a test's history strip. */
  .hd { display: inline-block; width: 7px; height: 7px; border-radius: 9999px; }
  .hd-now { box-shadow: 0 0 0 1.5px #fff, 0 0 0 2.5px #6B7280; }
  .hd-none { border: 1px solid #D1D5DB; }
  .flash { animation: flash 1.6s ease-out; }
  @keyframes flash { from { background-color: #FEF3C7; } to { background-color: transparent; } }
</style>
</head>
<body class="bg-gray-50 text-gray-800 text-[13px]">
<div class="flex min-h-screen">
  <aside class="sidebar-wrapper bg-navy text-white flex flex-col flex-shrink-0 fixed h-full z-10 shadow-lg">
    <div class="text-center pt-6 pb-5 border-b border-white/10">
      <div class="mb-2">
        <svg width="32" height="30" viewBox="0 0 30 28" fill="none" xmlns="http://www.w3.org/2000/svg" class="inline-block">
          <path d="M9.8249 13.3367C9.8249 13.3367 9.35771 21.2788 14.6135 27.1186C14.6135 27.1186 8.90357 28.4767 6.7882 21.746C4.86107 15.6142 9.8249 13.3367 9.8249 13.3367Z" fill="#FACF39"/>
          <path d="M16.3256 12.2896C16.3256 12.2896 15.9186 19.2083 20.4972 24.2956C20.4972 24.2956 15.5229 25.4787 13.6802 19.6153C12.0013 14.2736 16.3256 12.2896 16.3256 12.2896Z" fill="#FACF39"/>
          <path d="M4.27727 18.3413C4.27727 18.3413 3.96108 23.7165 7.51819 27.6688C7.51819 27.6688 4.13111 27.8778 2.68576 23.8775C1.60879 20.8968 4.27727 18.3413 4.27727 18.3413Z" fill="#FACF39"/>
          <path d="M21.8266 10.2126C21.8266 10.2126 10.0886 -4.50372 2.02971 1.97845C-6.02921 8.46062 21.8266 10.2126 21.8266 10.2126Z" fill="#FACF39"/>
          <path d="M25.9048 19.5679C27.9146 19.5679 29.5439 17.9386 29.5439 15.9287C29.5439 13.9189 27.9146 12.2896 25.9048 12.2896C23.8949 12.2896 22.2656 13.9189 22.2656 15.9287C22.2656 17.9386 23.8949 19.5679 25.9048 19.5679Z" fill="#FACF39"/>
        </svg>
      </div>
      <div>
        <div class="text-nb-yellow font-bold text-[1.1rem] tracking-[1px] lowercase leading-tight">nudgebee</div>
        <div class="text-slate-400 text-[10px] tracking-wider">E2E QA Automation</div>
      </div>
    </div>
    <nav class="flex-1 flex flex-col text-[13px] mt-2 overflow-y-auto">
      <div class="px-5 pt-4 pb-1 text-[9px] font-semibold uppercase tracking-wider text-slate-500">Overview</div>
      <a href="#results" class="nav-link flex items-center gap-2.5 px-5 py-2 border-l-[3px] border-transparent text-slate-400 hover:text-white hover:bg-white/10">
        <span class="material-icons-outlined text-[17px]" style="color:#34D399">check_circle</span>
        <span>Results</span>
      </a>
      <a href="#duration" class="nav-link flex items-center gap-2.5 px-5 py-2 border-l-[3px] border-transparent text-slate-400 hover:text-white hover:bg-white/10">
        <span class="material-icons-outlined text-[17px]" style="color:#60A5FA">schedule</span>
        <span>Duration</span>
      </a>
      <a href="#attention" class="nav-link flex items-center gap-2.5 px-5 py-2 border-l-[3px] border-transparent text-slate-400 hover:text-white hover:bg-white/10">
        <span class="material-icons-outlined text-[17px]" style="color:#F87171">priority_high</span>
        <span class="whitespace-nowrap">Needs Attention</span>
        ${
          newFailures.length
            ? `<span class="ml-auto whitespace-nowrap text-[10px] bg-red-400/20 text-red-300 px-1.5 rounded">${newFailures.length} new</span>`
            : ""
        }
      </a>
${
        compareEnv
          ? `<a href="#xenv" class="nav-link flex items-center gap-2.5 px-5 py-2 border-l-[3px] border-transparent text-slate-400 hover:text-white hover:bg-white/10">
        <span class="material-icons-outlined text-[17px]" style="color:#FB923C">compare_arrows</span>
        <span>${esc(HERE)} vs ${esc(THERE)}</span>
      </a>`
          : ""
      }
      <a href="#history" class="nav-link flex items-center gap-2.5 px-5 py-2 border-l-[3px] border-transparent text-slate-400 hover:text-white hover:bg-white/10">
        <span class="material-icons-outlined text-[17px]" style="color:#93C5FD">history</span>
        <span>Run History</span>
      </a>
      <a href="#trend" class="nav-link flex items-center gap-2.5 px-5 py-2 border-l-[3px] border-transparent text-slate-400 hover:text-white hover:bg-white/10">
        <span class="material-icons-outlined text-[17px]" style="color:#A78BFA">trending_up</span>
        <span>Trend</span>
      </a>
      <div class="px-5 pt-4 pb-1 text-[9px] font-semibold uppercase tracking-wider text-slate-500">Analysis</div>
      <a href="#causes" class="nav-link flex items-center gap-2.5 px-5 py-2 border-l-[3px] border-transparent text-slate-400 hover:text-white hover:bg-white/10">
        <span class="material-icons-outlined text-[17px]" style="color:#F87171">bug_report</span>
        <span>Failures by Cause</span>
      </a>
      <a href="#flaky" class="nav-link flex items-center gap-2.5 px-5 py-2 border-l-[3px] border-transparent text-slate-400 hover:text-white hover:bg-white/10">
        <span class="material-icons-outlined text-[17px]" style="color:#FBBF24">published_with_changes</span>
        <span>Flaky Frequency</span>
      </a>
      <a href="#modules" class="nav-link flex items-center gap-2.5 px-5 py-2 border-l-[3px] border-transparent text-slate-400 hover:text-white hover:bg-white/10">
        <span class="material-icons-outlined text-[17px]" style="color:#34D399">folder</span>
        <span>By Module</span>
      </a>
      <a href="#tags" class="nav-link flex items-center gap-2.5 px-5 py-2 border-l-[3px] border-transparent text-slate-400 hover:text-white hover:bg-white/10">
        <span class="material-icons-outlined text-[17px]" style="color:#FB923C">sell</span>
        <span>By Tag</span>
      </a>
      <div class="px-5 pt-4 pb-1 text-[9px] font-semibold uppercase tracking-wider text-slate-500">Tests</div>
      <a href="#tests" class="nav-link flex items-center gap-2.5 px-5 py-2 border-l-[3px] border-transparent text-slate-400 hover:text-white hover:bg-white/10">
        <span class="material-icons-outlined text-[17px]" style="color:#93C5FD">list</span>
        <span>Test Details</span>
      </a>
      <a href="#quarantine" class="nav-link flex items-center gap-2.5 px-5 py-2 border-l-[3px] border-transparent text-slate-400 hover:text-white hover:bg-white/10">
        <span class="material-icons-outlined text-[17px]" style="color:#FBBF24">block</span>
        <span>Quarantined</span>
        <span class="ml-auto text-[10px] bg-amber-400/20 text-amber-300 px-1.5 rounded">${quarantined}</span>
      </a>
    </nav>
    <button onclick="toggleTheme()"
            class="nav-link flex items-center gap-2.5 px-5 py-2.5 border-t border-white/10 text-slate-400 hover:text-white hover:bg-white/10 text-left text-[13px]">
      <span class="material-icons-outlined text-[18px]" id="theme-icon" style="color:#A78BFA">dark_mode</span>
      <span id="theme-label">Dark Mode</span>
    </button>
    <div class="px-5 py-4 border-t border-white/10 text-[10px] text-slate-500">
      ENV: ${esc(envName.toUpperCase())}
    </div>
  </aside>

  <main class="main-content-area flex-1 p-6">
    <div class="flex items-center gap-3 mb-4">
      <div class="w-9 h-9 rounded-lg bg-green-500/10 flex items-center justify-center">
        <span class="material-icons-outlined text-[20px]" style="color:#10B981">fact_check</span>
      </div>
      <div>
        <div class="text-[17px] font-semibold text-gray-800">E2E QA Automation</div>
        <div class="text-[11px] text-gray-500">Playwright suite results for the ${esc(envName)} environment</div>
      </div>
      <div class="ml-auto flex items-center gap-2">
        <button onclick="downloadReport()"
                class="flex items-center gap-1.5 text-[11px] px-2.5 py-1.5 rounded-md border border-gray-300 bg-white text-gray-600 hover:bg-gray-100">
          <span class="material-icons-outlined text-[15px]">download</span> Download
        </button>
      </div>
      <div id="dl-note" class="hidden fixed top-4 right-4 z-50 text-[11px] bg-gray-900 text-white px-3 py-2 rounded-md shadow-lg max-w-xs"></div>
    </div>
    ${runContextHtml}
    ${headerHtml}
    ${xenvStripHtml}
    <div id="results">${resultsHtml}</div>
    <div id="duration">${durationHtml}</div>
    <div id="attention">${attentionHtml}</div>
    <div id="xenv">${crossEnvHtml}</div>
    <div id="causes">${causesHtml}</div>
    <div id="modules">${moduleHtml}</div>
    <div id="tags">${tagHtml}</div>
    <div id="flaky">${flakeHtml}</div>
    <div id="tests">${testHtml}</div>
    <div id="quarantine">${quarantineHtml}</div>
    <div id="history">${runHistoryHtml}</div>
    <div id="trend">${trendHtml}</div>
  </main>
</div>
<script>
var RUN_DATA = ${runDataJson};
var REPORT = { env: ${JSON.stringify(envName)} };
var XLABELS = ${JSON.stringify({
  xboth: "failing on both " + HERE + " and " + THERE,
  xhere: "failing only on " + HERE,
  xthere: "failing only on " + THERE,
  xnothere: "failing here, not run on " + THERE,
})};

function toast(msg) {
  var el = document.getElementById('dl-note');
  el.textContent = msg;
  el.classList.remove('hidden');
  setTimeout(function () { el.classList.add('hidden'); }, 6000);
}

async function downloadReport() {
  var name = 'e2e-qa-report-' + REPORT.env + '-' + new Date().toISOString().slice(0, 10) + '.html';
  // Serialises the page as it stands, so whatever filter or theme is active
  // is what the saved copy opens with.
  var html = '<!DOCTYPE html>' + String.fromCharCode(10) + document.documentElement.outerHTML;
  var blob = new Blob([html], { type: 'text/html' });

  // Inside the Claude artifact viewer a page cannot start its own download;
  // the downloads capability shows the viewer a save prompt instead. Absent
  // that (opened from disk, or hosted), the ordinary blob download works.
  if (window.claude && typeof window.claude.use === 'function') {
    try {
      var dl = await window.claude.use('downloads');
      if (dl) {
        await dl.save({ filename: name, data: blob });
        toast('Report saved.');
        return;
      }
    } catch (err) {
      toast(err && err.code === 'declined' ? 'Download cancelled.' : 'Could not save the report in this viewer.');
      return;
    }
  }

  try {
    var url = URL.createObjectURL(blob);
    var a = document.createElement('a');
    a.href = url;
    a.download = name;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
    toast('Download started.');
  } catch (e) {
    toast('This viewer blocks downloads. Open the .html file directly to save a copy.');
  }
}

function applyTheme(dark) {
  document.body.classList.toggle('dark', dark);
  document.getElementById('theme-icon').textContent = dark ? 'light_mode' : 'dark_mode';
  document.getElementById('theme-label').textContent = dark ? 'Light Mode' : 'Dark Mode';
}

function toggleTheme() {
  var dark = !document.body.classList.contains('dark');
  applyTheme(dark);
  // Wrapped: the report is opened straight off disk and from sandboxed
  // viewers, where touching storage can throw outright.
  try { localStorage.setItem('nb-report-theme', dark ? 'dark' : 'light'); } catch (e) {}
}

(function () {
  // Light by default regardless of the OS setting — dark is opt-in via the
  // toggle, and the choice is remembered per viewer.
  var saved = null;
  try { saved = localStorage.getItem('nb-report-theme'); } catch (e) {}
  applyTheme(saved === 'dark');
})();

function showTrend(which) {
  ['bars', 'stacked', 'line'].forEach(function (id) {
    document.getElementById('trend-' + id).classList.toggle('hidden', id !== which);
    var btn = document.getElementById('trend-btn-' + id);
    btn.classList.toggle('bg-gray-100', id === which);
    btn.classList.toggle('font-semibold', id === which);
  });
}

// History views, built from RUN_DATA on demand: one character per test per
// run (P passed, F failed, A API failure, K flaky, S skipped, - not run).
var CODES = {
  P: ['passed', '#10B981'], F: ['failed', '#EF4444'], A: ['API failure', '#DC2626'],
  K: ['flaky', '#F59E0B'], S: ['skipped', '#9CA3AF'], '-': ['not run', '#D1D5DB']
};
var RUNS = RUN_DATA.runs;
var CUR = RUNS.length - 1;

function isBadCode(c) { return c === 'F' || c === 'A'; }
function isGreenCode(c) { return c === 'P' || c === 'K'; }

function hEsc(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
  });
}

function fmtWhen(iso) {
  return new Date(iso).toLocaleString('en-IN', {
    timeZone: 'Asia/Kolkata', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', hour12: false
  });
}

function runName(r) { return fmtWhen(r.when) + (r.run ? ' (run #' + r.run + ')' : ''); }

function pill(c) {
  return '<span class="text-[10px] font-semibold" style="color:' + CODES[c][1] + '">' + CODES[c][0].toUpperCase() + '</span>';
}

// One sentence answering "has this test been like this for long?".
function testStory(k) {
  var ran = [];
  for (var i = CUR; i >= 0; i--) {
    var c = RUNS[i].o.charAt(k);
    if (c && c !== '-') ran.push({ i: i, c: c });
  }
  if (!ran.length) return 'No recorded runs for this test yet.';
  var n = ran.length;
  var cur = RUNS[CUR].o.charAt(k);
  var bads = ran.filter(function (x) { return isBadCode(x.c); }).length;
  var flakes = ran.filter(function (x) { return x.c === 'K'; }).length;
  var tail = flakes ? ' Flaky in ' + flakes + ' of them.' : '';
  if (!bads) {
    return (flakes ? 'Never failed in the ' + n + ' recorded run' + (n > 1 ? 's' : '') + '.'
      : 'Passed in all ' + n + ' recorded run' + (n > 1 ? 's' : '') + '.') + tail;
  }
  if (isBadCode(cur)) {
    var streak = 0;
    while (streak < ran.length && isBadCode(ran[streak].c)) streak++;
    var since = runName(RUNS[ran[streak - 1].i]);
    var lastGood = ran.slice(streak).filter(function (x) { return isGreenCode(x.c); })[0];
    var lead = streak === 1 ? 'Broke in this run. ' : 'Failing for ' + streak + ' runs in a row, since ' + since + '. ';
    return lead +
      (lastGood ? 'Last passed: ' + runName(RUNS[lastGood.i]) + '.' : 'Has not passed in any of the ' + n + ' recorded runs.');
  }
  if (ran.length > 1 && isBadCode(ran[1].c)) {
    var before = 1;
    while (before < ran.length && isBadCode(ran[before].c)) before++;
    return 'Fixed in this run, after failing in the ' + (before - 1) + ' run' + (before > 2 ? 's' : '') + ' before it.' + tail;
  }
  var lastBad = ran.filter(function (x) { return isBadCode(x.c); })[0];
  return 'Passing now. Failed in ' + bads + ' of the ' + n + ' recorded runs, most recently ' + runName(RUNS[lastBad.i]) + '.' + tail;
}

// The same test in the other environment's latest full run, when the page
// was given one to compare against.
function otherEnvLine(k, nowErr) {
  var ot = RUN_DATA.other;
  if (!ot) return '';
  var c = ot.o.charAt(k) || '-';
  var err = ot.e[k] || '';
  var runRef = ot.run ? (ot.url ? '<a href="' + hEsc(ot.url) + '" target="_blank" rel="noopener" class="text-blue-500 hover:underline">run #' + hEsc(ot.run) + '</a>' : 'run #' + hEsc(ot.run)) : '';
  var tail = '';
  if (isBadCode(c) && err) tail = err === nowErr ? ' — same error as here' : ' — ' + hEsc(err);
  if (c === '-') tail = ' — the spec may not be on that branch yet';
  return '<div class="text-[11px] text-gray-700 mb-1">On ' + hEsc(ot.env) + ' (' + hEsc(fmtWhen(ot.when)) + (runRef ? ', ' + runRef : '') + '): ' +
    pill(c) + '<span class="text-gray-500">' + tail + '</span></div>';
}

function renderTestHistory(slot) {
  if (slot.dataset.done === '1' || slot.dataset.k === '') return;
  slot.dataset.done = '1';
  var k = Number(slot.dataset.k);
  var nowErr = RUNS[CUR].e[k];
  var rows = [];
  for (var i = CUR; i >= 0; i--) {
    var r = RUNS[i];
    var c = r.o.charAt(k) || '-';
    var d = r.d && r.d[k] != null ? r.d[k] + 's' : '';
    var err = r.e[k] || '';
    var same = i !== CUR && err && nowErr && err === nowErr;
    rows.push(
      '<div class="grid grid-cols-[1.1fr_0.5fr_0.7fr_0.4fr_2.6fr] gap-2 px-2 py-1 text-[10px] border-b border-gray-50 last:border-0' +
        (i === CUR ? ' bg-blue-50/40' : '') + '">' +
        '<span class="text-gray-600">' + hEsc(fmtWhen(r.when)) + (i === CUR ? ' <b class="text-blue-600">now</b>' : '') + '</span>' +
        '<span class="text-gray-500">' + (r.url ? '<a href="' + hEsc(r.url) + '" target="_blank" rel="noopener" class="text-blue-500 hover:underline">#' + hEsc(r.run) + '</a>' : hEsc(r.run ? '#' + r.run : '')) + '</span>' +
        '<span>' + pill(c) + '</span>' +
        '<span class="text-gray-500">' + d + '</span>' +
        '<span class="text-gray-500 truncate" title="' + hEsc(err) + '">' +
          (same ? '<span class="text-[9px] text-gray-400 bg-gray-100 rounded px-1 mr-1">same as now</span>' : '') + hEsc(err) + '</span>' +
      '</div>'
    );
  }
  slot.innerHTML =
    '<div class="text-[10px] font-semibold text-gray-400 uppercase tracking-wide mb-1">History</div>' +
    '<div class="text-[11px] text-gray-700 mb-1">' + hEsc(testStory(k)) + '</div>' + otherEnvLine(k, nowErr) +
    '<details open><summary class="text-[10px] text-blue-500 cursor-pointer hover:underline mb-1">Run by run (' + RUNS.length + ')</summary>' +
    '<div class="bg-white border border-gray-100 rounded-md overflow-hidden">' + rows.join('') + '</div></details>';
}

// A test named in a run panel opens its row below; one that is not in this
// run has no row to open.
function testRef(k, extra) {
  var row = document.querySelector('.test-row[data-k="' + k + '"]');
  var name = hEsc(RUN_DATA.names[k]);
  var link = row
    ? '<a href="#" onclick="openTest(' + row.id.slice(5) + ');return false" class="text-gray-700 hover:text-blue-600 hover:underline">' + name + '</a>'
    : '<span class="text-gray-500">' + name + ' <span class="text-[9px] text-gray-400">(not in this run)</span></span>';
  return '<div class="px-3 py-1.5 text-[11px] border-b border-gray-50 last:border-0">' + link +
    (extra ? '<div class="text-[10px] text-gray-500 mt-0.5 truncate" title="' + hEsc(extra) + '">' + hEsc(extra) + '</div>' : '') + '</div>';
}

function listBox(title, items, color) {
  if (!items.length) return '';
  return '<div class="bg-white border border-gray-100 rounded-xl shadow-sm overflow-hidden mb-2">' +
    '<div class="px-3 py-1.5 text-[10px] font-semibold uppercase tracking-wide bg-gray-50 border-b border-gray-100" style="color:' + color + '">' +
    hEsc(title) + ' <span class="normal-case font-normal text-gray-400">(' + items.length + ')</span></div>' + items.join('') + '</div>';
}

function showPanel(title, body) {
  var el = document.getElementById('run-panel');
  el.innerHTML =
    '<div class="border border-blue-200 rounded-xl p-3 bg-blue-50/30">' +
    '<div class="flex items-center mb-2"><span class="text-[12px] font-semibold text-gray-700">' + hEsc(title) + '</span>' +
    '<button onclick="document.getElementById(&quot;run-panel&quot;).innerHTML=&quot;&quot;" class="ml-auto text-[10px] px-2 py-0.5 rounded border border-gray-300 text-gray-600 hover:bg-gray-100">Close</button></div>' +
    body + '</div>';
  el.scrollIntoView({ behavior: 'smooth', block: 'start' });
}

function showRunFailures(ri) {
  var r = RUNS[ri];
  var failed = [], flaky = [];
  for (var k = 0; k < RUN_DATA.names.length; k++) {
    var c = r.o.charAt(k);
    if (isBadCode(c)) failed.push(testRef(k, (c === 'A' ? '[API] ' : '') + (r.e[k] || '')));
    else if (c === 'K') flaky.push(testRef(k));
  }
  showPanel('What failed — ' + runName(r),
    (failed.length || flaky.length)
      ? listBox('Failed', failed, '#EF4444') + listBox('Flaky', flaky, '#F59E0B')
      : '<div class="text-[11px] text-gray-500">Nothing failed and nothing was flaky in this run.</div>');
}

// Older runs on record carry no error text; say only what is known.
function errorPair(before, now) {
  if (!before) return now || '';
  if (before === now) return 'same error: ' + now;
  return 'then: ' + before + ' | now: ' + (now || '');
}

// Every test both runs ran, sorted by what changed between them. A test the
// current run did not run says nothing about now, so it is left out.
function compareRun(ri) {
  var then = RUNS[ri], now = RUNS[CUR];
  var broke = [], fixed = [], still = [], newBad = [];
  for (var k = 0; k < RUN_DATA.names.length; k++) {
    var a = then.o.charAt(k) || '-', b = now.o.charAt(k) || '-';
    if (b === '-') continue;
    if (isBadCode(b) && a === '-') newBad.push(testRef(k, now.e[k]));
    else if (isBadCode(b) && !isBadCode(a)) broke.push(testRef(k, now.e[k]));
    else if (isBadCode(b) && isBadCode(a)) still.push(testRef(k, errorPair(then.e[k], now.e[k])));
    else if (isBadCode(a) && isGreenCode(b)) fixed.push(testRef(k, then.e[k] ? 'was: ' + then.e[k] : ''));
  }
  var body = listBox('Broke since then', broke, '#EF4444') + listBox('Fixed since then', fixed, '#10B981') +
    listBox('Failing in both', still, '#6B7280') + listBox('Failing now, did not run then', newBad, '#6B7280');
  showPanel('This run compared with ' + runName(then),
    body || '<div class="text-[11px] text-gray-500">No difference in failures between these two runs.</div>');
}

function toggleDetail(i) {
  var el = document.getElementById('detail-' + i);
  if (!el) return;
  el.classList.toggle('hidden');
  var slot = el.querySelector('.hist-slot');
  if (slot && !el.classList.contains('hidden')) renderTestHistory(slot);
}

function toggleSteps(ev, i) {
  // Stops the click from bubbling to the row, which would collapse the
  // whole panel the moment the extra steps appear.
  ev.stopPropagation();
  var rows = document.querySelectorAll('.step-row-' + i);
  var btn = document.getElementById('more-' + i);
  var expanding = btn.dataset.open !== '1';
  rows.forEach(function (r) {
    if (r.dataset.extra === '1') r.classList.toggle('hidden', !expanding);
  });
  btn.dataset.open = expanding ? '1' : '0';
  btn.textContent = expanding ? 'Show fewer steps' : 'Show all ' + rows.length + ' steps';
}

// Arrows, dashes and line breaks all become one space, so "Cost -> Filters"
// finds the row that renders as "Cost › Filters".
function searchKey(s) {
  return ' ' + String(s || '').toLowerCase().replace(/[^a-z0-9@/._]+/g, ' ').trim() + ' ';
}

var ORIGINAL_ORDER = Array.prototype.slice.call(document.querySelectorAll('.test-row'));
// Built here from text the rows already show, rather than shipped as a second
// copy of every title and path.
ORIGINAL_ORDER.forEach(function (row) {
  var head = row.querySelector('[onclick^="toggleDetail"]');
  var tags = Array.prototype.map.call(row.querySelectorAll('.tag-chip'), function (el) { return el.textContent; });
  row.dataset.search = searchKey((head ? head.textContent : '') + ' ' + tags.join(' '));
});
var testPageSize = 15;
var qPageSize = 15;
var testFilter = 'all';
var testSearch = '';
var testPage = 1;
var qPage = 1;

// Shared by both tables: rows-per-page on the left, page controls on the
// right. The size selector stays visible even on a single page, otherwise
// there is no way to switch back off a large page size.
function renderPager(el, page, total, size, pageCb, sizeCb) {
  var pages = Math.max(1, Math.ceil(total / size));
  var from = total ? (page - 1) * size + 1 : 0;
  var to = Math.min(page * size, total);
  var opts = [10, 15, 25, 50, 100]
    .map(function (n) {
      return '<option value="' + n + '"' + (n === size ? ' selected' : '') + '>' + n + '</option>';
    })
    .join('') + '<option value="0"' + (size >= 100000 ? ' selected' : '') + '>All</option>';

  var btn = function (label, target, disabled) {
    return '<button ' + (disabled ? 'disabled' : 'onclick="' + pageCb + '(' + target + ')"') +
      ' class="text-[10px] px-2 py-1 rounded border ' +
      (disabled ? 'border-gray-200 text-gray-300 cursor-not-allowed'
                : 'border-gray-300 text-gray-600 hover:bg-gray-100') + '">' + label + '</button>';
  };

  el.innerHTML =
    '<span class="flex items-center gap-1.5 text-[10px] text-gray-500">' +
      'Rows per page' +
      '<select onchange="' + sizeCb + '(this.value)" class="text-[10px] border border-gray-300 rounded px-1 py-0.5 bg-white text-gray-600">' +
        opts +
      '</select>' +
      '<span class="ml-2">Showing ' + from + '-' + to + ' of ' + total + '</span>' +
    '</span>' +
    '<span class="flex items-center gap-1">' +
      btn('First', 1, page <= 1) +
      btn('Prev', page - 1, page <= 1) +
      '<span class="text-[10px] text-gray-500 px-1">Page ' + page + ' of ' + pages + '</span>' +
      btn('Next', page + 1, page >= pages) +
      btn('Last', pages, page >= pages) +
    '</span>';
}

function setTestPageSize(v) {
  testPageSize = Number(v) === 0 ? 100000 : Number(v);
  testPage = 1;
  renderTests();
}

function setQPageSize(v) {
  qPageSize = Number(v) === 0 ? 100000 : Number(v);
  qPage = 1;
  renderQuarantine();
}

function renderTests() {
  // Order first, then read the DOM: the page slice is taken from live document
  // order, so reordering afterwards would page through the previous order.
  var all = Array.prototype.slice.call(document.querySelectorAll('.test-row'));
  var parent = all.length ? all[0].parentNode : null;
  if (parent) {
    if (testFilter === 'slowest') {
      all.slice()
        .sort(function (a, b) { return Number(a.dataset.slowRank) - Number(b.dataset.slowRank); })
        .forEach(function (r) { parent.appendChild(r); });
    } else {
      ORIGINAL_ORDER.forEach(function (r) { parent.appendChild(r); });
    }
  }

  var rows = document.querySelectorAll('.test-row');
  var matching = [];
  rows.forEach(function (row) {
    var match = (testFilter === 'all' ||
      (' ' + row.dataset.cats + ' ').indexOf(' ' + testFilter + ' ') !== -1) &&
      (!testSearch || row.dataset.search.indexOf(testSearch) !== -1);
    if (match) matching.push(row);
    row.classList.add('hidden');
  });
  var pages = Math.max(1, Math.ceil(matching.length / testPageSize));
  if (testPage > pages) testPage = pages;
  matching.slice((testPage - 1) * testPageSize, testPage * testPageSize)
    .forEach(function (row) { row.classList.remove('hidden'); });

  var narrowed = testFilter !== 'all' || !!testSearch;
  var bar = document.getElementById('filter-bar');
  bar.classList.toggle('hidden', !narrowed);
  bar.classList.toggle('flex', narrowed);
  var LABELS = { apifailed: 'API failures', slowest: 'slowest 10', newfail: 'new failures',
                 still: 'still failing', unseen: 'failing, not in the last full run', repeatflaky: 'flaky again',
                 fixed: 'fixed since the last full run', slower: 'got much slower' };
  var label = testFilter === 'all' ? 'all tests'
    : testFilter.indexOf('tag-') === 0 ? '@' + testFilter.slice(4)
      : (LABELS[testFilter] || XLABELS[testFilter] || testFilter);
  document.getElementById('filter-label').textContent =
    label + (testSearch ? ' matching "' + testSearch + '"' : '');
  document.getElementById('filter-count').textContent = matching.length;
  document.getElementById('no-match').classList.toggle('hidden', matching.length > 0);
  renderPager(document.getElementById('tests-pager'), testPage, matching.length, testPageSize,
    'goTestPage', 'setTestPageSize');
}

function goTestPage(p) {
  testPage = p;
  renderTests();
  document.getElementById('tests').scrollIntoView({ behavior: 'smooth', block: 'start' });
}

function searchTests(q) {
  testSearch = searchKey(q).trim();
  testPage = 1;
  renderTests();
}

// Jumps from a summary list to the test's own row: clears whatever filter or
// search would hide it, turns to its page, opens it and scrolls it into view.
function openTest(i) {
  testFilter = 'all';
  testSearch = '';
  document.getElementById('test-search').value = '';
  var row = document.getElementById('test-' + i);
  if (!row) return;
  var pos = ORIGINAL_ORDER.indexOf(row);
  testPage = Math.floor(pos / testPageSize) + 1;
  renderTests();
  var detail = document.getElementById('detail-' + i);
  if (detail) {
    detail.classList.remove('hidden');
    var slot = detail.querySelector('.hist-slot');
    if (slot) renderTestHistory(slot);
  }
  row.scrollIntoView({ behavior: 'smooth', block: 'start' });
  row.classList.remove('flash');
  void row.offsetWidth;
  row.classList.add('flash');
}

function filterTests(cat) {
  testFilter = cat;
  if (cat === 'all') {
    testSearch = '';
    document.getElementById('test-search').value = '';
  }
  testPage = 1;
  renderTests();
  document.getElementById('tests').scrollIntoView({ behavior: 'smooth', block: 'start' });
}

function renderQuarantine() {
  var rows = document.querySelectorAll('.q-row');
  rows.forEach(function (row, n) {
    row.classList.toggle('hidden', n < (qPage - 1) * qPageSize || n >= qPage * qPageSize);
  });
  renderPager(document.getElementById('q-pager'), qPage, rows.length, qPageSize,
    'goQPage', 'setQPageSize');
}

function goQPage(p) {
  qPage = p;
  renderQuarantine();
  document.getElementById('quarantine').scrollIntoView({ behavior: 'smooth', block: 'start' });
}

// Every list and table marked data-paged gets the same pager as Test Details,
// so a nightly with fifty failures or two hundred modules stays one screen
// tall. Lists that already fit are left without one.
var LISTS = {};

function initLists() {
  document.querySelectorAll('[data-paged]').forEach(function (el, n) {
    var rows = Array.prototype.slice.call(el.querySelectorAll(':scope > .pg-row'));
    var size = Number(el.dataset.paged) || 10;
    if (rows.length <= size) return;
    var pager = document.createElement('div');
    pager.className = 'flex items-center justify-between gap-2 px-3 py-2 border-t border-gray-100 bg-gray-50';
    el.appendChild(pager);
    var id = 'pl' + n;
    LISTS[id] = { rows: rows, size: size, page: 1, pager: pager };
    renderList(id);
  });
}

function renderList(id) {
  var L = LISTS[id];
  var pages = Math.max(1, Math.ceil(L.rows.length / L.size));
  if (L.page > pages) L.page = pages;
  L.rows.forEach(function (r, i) {
    r.classList.toggle('hidden', i < (L.page - 1) * L.size || i >= L.page * L.size);
  });
  renderPager(L.pager, L.page, L.rows.length, L.size,
    "goList.bind(null,'" + id + "')", "setListSize.bind(null,'" + id + "')");
}

function goList(id, p) {
  LISTS[id].page = p;
  renderList(id);
}

function setListSize(id, v) {
  LISTS[id].size = Number(v) === 0 ? 100000 : Number(v);
  LISTS[id].page = 1;
  renderList(id);
}

renderTests();
renderQuarantine();
initLists();
showTrend('bars');
<\/script>
</body>
</html>`;

fs.writeFileSync(outFile, html);

// What the Slack post carrying this report says about it, so the channel
// shows what broke without anyone opening the file first.
fs.writeFileSync(
  path.join(path.dirname(outFile), "e2e-report-summary.json"),
  JSON.stringify(
    {
      verdict: verdict.tone,
      headline: verdict.text,
      total: stats.total,
      passRate: Number(stats.passRate.toFixed(1)),
      failed: stats.failed,
      apiFailures: stats.apiFailures,
      flaky: stats.flaky,
      newFailures: newFailures.map((t) => t.name),
      stillFailing: knownFailures.length,
      fixed: fixedNow.length,
      slower: slowerNow.length,
      compare: other
        ? {
            env: THERE,
            when: other.when,
            failingBoth: xBoth.length,
            failingOnlyHere: xHere.length,
            failingOnlyThere: xThere.length,
            notRunThere: xNotThere.length,
          }
        : null,
      partialRun: partialRun,
    },
    null,
    2
  )
);
console.log(
  `Wrote ${outFile} — ${stats.total} tests, ${stats.passed} passed, ${stats.failed + stats.broken} failed, ` +
    `${stats.flaky} flaky, ${quarantined} quarantined (${quarantinedRan.length ? quarantinedRan.length + " ran" : "excluded"}), ${trendHistory.length} run(s) in trend`
);

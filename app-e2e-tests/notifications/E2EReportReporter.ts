import type {
  Reporter,
  TestCase,
  TestResult,
  TestStep,
  FullConfig,
  FullResult,
} from "@playwright/test/reporter";
import { writeFileSync, mkdirSync } from "fs";
import path from "path";
import { PLAYWRIGHT_REPORT_DIR } from "../tests/utils/paths";

// Writes the run data that scripts/generate-e2e-report.js turns into the single
// HTML report. Playwright's built-in JSON reporter is not enough: it carries
// only authored test.step() entries, and most specs have none (270 of 1082 in
// the 2026-09-15 nightly), so the action trace the report falls back on for the
// rest is visible only from inside a reporter.
export const E2E_RESULTS_FILE = path.join(PLAYWRIGHT_REPORT_DIR, "e2e-results.json");

interface ReportAttachment {
  type: string;
  source: string;
}

interface ReportStep {
  name: string;
  category: string;
  at: string;
  status: string;
  start: number;
  stop: number;
  steps: ReportStep[];
  attachments: ReportAttachment[];
}

interface ReportAttempt {
  name: string;
  suite: string;
  line: number;
  tags: string[];
  status: string;
  retry: number;
  start: number;
  stop: number;
  statusDetails: { message: string; trace: string };
  steps: ReportStep[];
  attachments: ReportAttachment[];
}

// Error text arrives with the terminal colour codes Playwright uses for its CLI
// output; they would render as literal escape sequences in the HTML.
// eslint-disable-next-line no-control-regex
const ANSI = /\u001b\[[0-9;]*m/g;
const stripAnsi = (s: string) => s.replace(ANSI, "");

export default class E2EReportReporter implements Reporter {
  private readonly attempts: ReportAttempt[] = [];
  private rootDir = "";

  onBegin(config: FullConfig): void {
    this.rootDir = config.rootDir;
  }

  // Playwright's four statuses collapse to the three the report counts;
  // timedOut and interrupted are failures as far as a reader is concerned.
  private static status(status: string): string {
    if (status === "passed" || status === "skipped") return status;
    return "failed";
  }

  private static attachments(source: { attachments?: readonly { contentType?: string; path?: string }[] }): ReportAttachment[] {
    return (source.attachments || [])
      .filter((a) => !!a.path)
      .map((a) => ({ type: a.contentType || "", source: a.path as string }));
  }

  private static step(step: TestStep): ReportStep {
    const start = step.startTime ? step.startTime.getTime() : 0;
    return {
      name: step.title,
      // "fixture" and "hook" steps (Worker Cleanup, beforeEach) are not the
      // spec's own steps; the report needs to tell them apart.
      category: step.category,
      // Newer Playwright titles an action just "Click"; where it was called
      // from is what says which click it was.
      at: step.location ? `${path.basename(step.location.file)}:${step.location.line}` : "",
      status: step.error ? "failed" : "passed",
      start,
      stop: start + (step.duration >= 0 ? step.duration : 0),
      steps: (step.steps || []).map((s) => E2EReportReporter.step(s)),
      attachments: E2EReportReporter.attachments(step as unknown as { attachments?: readonly { contentType?: string; path?: string }[] }),
    };
  }

  onTestEnd(test: TestCase, result: TestResult): void {
    // titlePath() is ['', project, file, ...describes, title]; the describes and
    // the title are the part a reader recognises.
    const titlePath = test.titlePath().slice(3).filter(Boolean);
    const start = result.startTime ? result.startTime.getTime() : 0;
    const error = (result.errors || [])[0] || {};

    this.attempts.push({
      name: titlePath.join(" - ") || test.title,
      // Relative to testDir, not rootDir, so it reads as
      // admin/Audits/Audits.spec.ts: the report names each module after this
      // path's folders, and a leading "tests/" would prefix every one of them.
      suite: path.relative(test.parent.project()?.testDir || this.rootDir, test.location.file),
      line: test.location.line,
      // Playwright hands back "@dev"; the report's tag table labels them bare.
      tags: (test.tags || []).map((t) => t.replace(/^@/, "")),
      status: E2EReportReporter.status(result.status),
      retry: result.retry,
      start,
      stop: start + (result.duration >= 0 ? result.duration : 0),
      statusDetails: {
        message: stripAnsi(error.message || ""),
        trace: stripAnsi(error.stack || ""),
      },
      steps: (result.steps || []).map((s) => E2EReportReporter.step(s)),
      attachments: E2EReportReporter.attachments(result),
    });
  }

  onEnd(result: FullResult): void {
    // The HTML reporter empties playwright-report/ when the run starts, so this
    // has to be written at the end rather than incrementally.
    mkdirSync(PLAYWRIGHT_REPORT_DIR, { recursive: true });
    // The run's own clock, not the sum of test durations: tests run on several
    // workers at once, so adding them up overstates the wall time.
    const run = {
      start: result.startTime ? result.startTime.getTime() : 0,
      duration: result.duration >= 0 ? result.duration : 0,
      status: result.status,
    };
    writeFileSync(E2E_RESULTS_FILE, JSON.stringify({ run, attempts: this.attempts }), "utf8");
  }
}

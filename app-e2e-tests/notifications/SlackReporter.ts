import type {
  Reporter,
  TestCase,
  TestResult,
  FullResult,
} from "@playwright/test/reporter";
import axios, { type AxiosError } from "axios";
import path from "path";
import { writeFileSync, mkdirSync, readFileSync } from "fs";
import { PLAYWRIGHT_REPORT_DIR, TENANT_FILE_PATH } from "../tests/utils/paths";
export default class SlackReporter implements Reporter {
  // ── Test tracking ──────────────────────────────────────────────────────
  private readonly allTests = new Set<string>();
  private readonly passedTests = new Set<string>();
  private readonly failedTests = new Set<string>();
  private readonly skippedTests = new Set<string>();
  private readonly retriedTests = new Set<string>();
  private readonly apiFailedTests = new Set<string>();

  // Dedup guards — prevent duplicate instant alerts across retries
  private readonly failAlertSent = new Set<string>();
  private readonly skipAlertSent = new Set<string>();


  // ── Constants ──────────────────────────────────────────────────────────
  private static readonly COLORS = {
    FAIL: "#ff0000",
    SKIP: "#FFA500",

    SUMMARY: "#7ceaf9",
    SUMMARY_SKIP: "#7ceaf9",
  } as const;

  private static readonly MIN_TESTS_FOR_SUMMARY = 5;
  private static readonly MAX_ERROR_LENGTH = 1200;
  private static readonly MAX_SKIP_REASON_LENGTH = 800;
  private static readonly SLACK_TIMEOUT_MS = 10_000;
  private static readonly MAX_RETRIES = 2;

  // ── Helpers ────────────────────────────────────────────────────────────

  private getISTTime(): string {
    return new Date().toLocaleString("en-IN", {
      timeZone: "Asia/Kolkata",
      day: "numeric",
      month: "short",
      year: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: true,
    });
  }

  private formatDuration(ms: number): string {
    const totalSec = Math.max(0, Math.floor(ms / 1000));
    const m = Math.floor(totalSec / 60);
    const s = totalSec % 60;
    return `${m}m ${s}s`;
  }

  private getRelativePath(filePath: string): string {
    try {
      return path.relative(process.cwd(), filePath);
    } catch {
      return filePath;
    }
  }

  private stripAnsi(text: string): string {
    return text.replace(
      // eslint-disable-next-line no-control-regex
      /[\u001b\u009b][[()#;?]*(?:[0-9]{1,4}(?:;[0-9]{0,4})*)?[0-9A-ORZcf-nqry=><]/g,
      ""
    );
  }

  /** Truncate on a word boundary when possible. */
  private truncate(text: string, maxLen: number): string {
    if (text.length <= maxLen) return text;
    const cut = text.slice(0, maxLen);
    const lastSpace = cut.lastIndexOf(" ");
    return (lastSpace > maxLen * 0.8 ? cut.slice(0, lastSpace) : cut) + "…";
  }

  private getWebhookUrl(): string | null {
    const url = process.env.SLACK_WEBHOOK_URL?.trim();
    return url && url.startsWith("http") ? url : null;
  }

  private getTenant(): string {
    if (process.env.SWITCH_TENANT) return process.env.SWITCH_TENANT;
    try {
      return readFileSync(TENANT_FILE_PATH, "utf-8").trim() || "N/A";
    } catch {
      return "N/A";
    }
  }

  private getTestId(test: TestCase): string {
    return test.titlePath().join(" > ");
  }

  private isApiFailure(result: TestResult): boolean {
    return (
      ["failed", "timedOut", "interrupted"].includes(result.status) &&
      (result.errors?.some((e) => e?.message?.includes("[GraphQLWatcher]")) ??
        false)
    );
  }

  /**
   * Post JSON to Slack with exponential back-off retries.
   * Never throws — test execution must never be interrupted by Slack errors.
   */
  private async postToSlack(
    webhookUrl: string,
    payload: Record<string, unknown>,
    label: string
  ): Promise<void> {
    for (let attempt = 0; attempt <= SlackReporter.MAX_RETRIES; attempt++) {
      try {
        await axios.post(webhookUrl, payload, {
          timeout: SlackReporter.SLACK_TIMEOUT_MS,
          headers: { "Content-Type": "application/json" },
        });
        return; // success
      } catch (err: unknown) {
        const axiosErr = err as AxiosError;
        const status = axiosErr?.response?.status;

        // 4xx = client error (bad payload / invalid webhook) — don't retry
        if (status && status >= 400 && status < 500) {
          const body = axiosErr?.response?.data
            ? ` — ${JSON.stringify(axiosErr.response.data)}`
            : "";
          console.error(
            `[SlackReporter] ${label} — Slack returned ${status}${body}. Not retrying.`
          );
          return;
        }

        if (attempt < SlackReporter.MAX_RETRIES) {
          const delay = 1000 * 2 ** attempt; // 1s, 2s
          console.warn(
            `[SlackReporter] ${label} — attempt ${attempt + 1} failed, retrying in ${delay}ms…`
          );
          await new Promise((r) => setTimeout(r, delay));
        } else {
          const errMsg = axiosErr?.message ?? String(err);
          console.error(
            `[SlackReporter] ${label} — all attempts failed: ${errMsg}`
          );
        }
      }
    }
  }

  // ── Lifecycle: onTestEnd ───────────────────────────────────────────────

  async onTestEnd(test: TestCase, result: TestResult): Promise<void> {
    const testId = this.getTestId(test);
    this.allTests.add(testId);

    if (result.retry > 0) {
      this.retriedTests.add(testId);
    }

    const apiFailure = this.isApiFailure(result);

    switch (result.status) {
      case "passed":
        this.passedTests.add(testId);
        // Clear any previous negative tracking — the test recovered on retry
        this.failedTests.delete(testId);
        this.skippedTests.delete(testId);
        this.apiFailedTests.delete(testId);
        break;

      case "skipped":
        // Only count if test never passed or hard-failed (including API failures)
        if (
          !this.passedTests.has(testId) &&
          !this.failedTests.has(testId) &&
          !this.apiFailedTests.has(testId)
        ) {
          this.skippedTests.add(testId);
          await this.sendSkippedAlert(test, result);
        }
        break;

      case "failed":
      case "timedOut":
      case "interrupted":
        this.passedTests.delete(testId);
        this.skippedTests.delete(testId);

        if (apiFailure) {
          // Only track for summary — GraphQLNetworkWatcher already sends
          // its own detailed Slack alert with payload/error info.
          // Keep apiFailedTests and failedTests mutually exclusive so
          // counts and lists never double-count the same test.
          this.apiFailedTests.add(testId);
          this.failedTests.delete(testId);
        } else {
          this.failedTests.add(testId);
          this.apiFailedTests.delete(testId);
          await this.sendFailureAlert(test, result);
        }
        break;
    }
  }

  // ── Lifecycle: onEnd ───────────────────────────────────────────────────

  async onEnd(result: FullResult): Promise<void> {
    const total = this.allTests.size;
    const failed = this.failedTests.size;
    const skipped = this.skippedTests.size;
    const apiFailures = this.apiFailedTests.size;
    const executionTime = this.formatDuration(result.duration);
    const time = this.getISTTime();
    const ciRunUrl = this.buildCiRunUrl();

    // ── Compute retry outcomes (single pass) ──
    // A test is in retriedTests if it had retry > 0 at any point.
    // Its final outcome is determined by which terminal set it landed in.
    // Note: tests retried and then skipped are not counted in these breakdowns.
    let retriedAndPassed = 0;
    let retriedAndFailed = 0;
    for (const id of this.retriedTests) {
      if (this.passedTests.has(id)) {
        retriedAndPassed++;
      } else if (this.failedTests.has(id) || this.apiFailedTests.has(id)) {
        retriedAndFailed++;
      }
    }

    // ── Always write the summary JSON (CI step consumes it) ──
    const summaryData = {
      total,
      passed: this.passedTests.size,
      failed,
      skipped,
      retried: this.retriedTests.size,
      retriedAndPassed,
      retriedAndFailed,
      apiFailures,
      duration: executionTime,
      greeting: this.buildGreeting(failed, apiFailures, skipped),
      time,
      ciRunUrl,
      failedTests: Array.from(this.failedTests),
      apiFailedTests: Array.from(this.apiFailedTests),
      skippedTests: Array.from(this.skippedTests),
    };
    this.writeSummaryJson(summaryData);

    // ── Guard: too few tests → skip summary ──
    if (total < SlackReporter.MIN_TESTS_FOR_SUMMARY) {
      console.log(
        `[SlackReporter] Skipping Slack summary — only ${total} test(s) (min ${SlackReporter.MIN_TESTS_FOR_SUMMARY}).`
      );
      return;
    }

    const webhookUrl = this.getWebhookUrl();
    if (!webhookUrl) return;

    const color = this.getSummaryColor(failed, apiFailures, skipped);

    // ── Stats block (kept well under Slack's 3000-char block limit) ──
    const statsText = [
      `*Total Execution Time:* ${executionTime}`,
      "",
      `*Total Tests:* ${total}  |  *Passed:* ${this.passedTests.size}  |  *Failed:* ${failed}  |  *Skipped:* ${skipped}`,
      `*Retries:* ${this.retriedTests.size}  |  *Retry Passed:* ${retriedAndPassed}  |  *Retry Failed:* ${retriedAndFailed}  |  *API Failures:* ${apiFailures}`,
    ].join("\n");

    // ── Greeting + CI link block ──
    const greetingParts: string[] = [];
    if (ciRunUrl) {
      greetingParts.push(`<${ciRunUrl}|Click here to see complete CI execution>`);
    }
    greetingParts.push(this.buildGreeting(failed, apiFailures, skipped));
    const greetingText = greetingParts.join("\n\n");

    // ── Build attachment blocks — each list is its own block to avoid the
    //    3000-char per-block limit that silently causes Slack to reject the
    //    entire message when many tests fail. ──
    const attachmentBlocks: unknown[] = [
      { type: "section", text: { type: "mrkdwn", text: statsText } },
      { type: "section", text: { type: "mrkdwn", text: greetingText } },
    ];

    if (this.failedTests.size > 0) {
      attachmentBlocks.push({
        type: "section",
        text: {
          type: "mrkdwn",
          text: `*Failed Tests:*\n${this.buildTestListBlock(this.failedTests)}`,
        },
      });
    }
    if (this.apiFailedTests.size > 0) {
      attachmentBlocks.push({
        type: "section",
        text: {
          type: "mrkdwn",
          text: `*API Failed Tests:*\n${this.buildTestListBlock(this.apiFailedTests)}`,
        },
      });
    }
    if (this.skippedTests.size > 0) {
      attachmentBlocks.push({
        type: "section",
        text: {
          type: "mrkdwn",
          text: `*Skipped Tests:*\n${this.buildTestListBlock(this.skippedTests)}`,
        },
      });
    }

    await this.postToSlack(
      webhookUrl,
      {
        blocks: [
          {
            type: "section",
            text: {
              type: "mrkdwn",
              text: `*Execution Finished* (${time})`,
            },
          },
        ],
        attachments: [
          {
            color,
            blocks: attachmentBlocks,
          },
        ],
      },
      "Execution summary"
    );

    console.log("[SlackReporter] Execution summary sent.");
  }

  // ── Instant Alert: Test Failed ─────────────────────────────────────────

  private async sendFailureAlert(
    test: TestCase,
    result: TestResult
  ): Promise<void> {
    const webhookUrl = this.getWebhookUrl();
    const testId = this.getTestId(test);
    if (!webhookUrl) return;
    if (!["failed", "timedOut"].includes(result.status)) return;
    if (this.failAlertSent.has(testId)) return;
    this.failAlertSent.add(testId);

    const errorMsg = result.errors?.[0]?.message ?? "Unknown Error";
    const cleanError = this.truncate(
      this.stripAnsi(errorMsg),
      SlackReporter.MAX_ERROR_LENGTH
    );

    const filePath = test.location?.file
      ? this.getRelativePath(test.location.file)
      : "Unknown file";
    const line = test.location?.line ?? "N/A";

    // Read workflow URL attached by workflowHelper.saveNewWorkflow (if present)
    const workflowUrlAttachment = result.attachments.find(
      (a) => a.name === "workflowUrl"
    );
    const workflowUrl = workflowUrlAttachment?.body
      ? workflowUrlAttachment.body.toString()
      : undefined;

    const sidebarDetails = [
      `*File:* \`${filePath}:${line}\``,
      `*Env:* ${process.env.CLUSTER || "local"}`,
      `*Tenant:* ${this.getTenant()}`,
      ...(workflowUrl ? [`*Workflow:* <${workflowUrl}|Open Workflow>`] : []),
      "",
      `\`\`\`${cleanError}\`\`\``,
    ].join("\n");

    await this.postToSlack(
      webhookUrl,
      {
        blocks: [
          {
            type: "section",
            text: {
              type: "mrkdwn",
              text: `:rotating_light: *Test Failed Alert*`,
            },
          },
          {
            type: "section",
            text: {
              type: "mrkdwn",
              text: `*Test Case:* ${test.title}`,
            },
          },
        ],
        attachments: [
          {
            color: SlackReporter.COLORS.FAIL,
            blocks: [
              { type: "section", text: { type: "mrkdwn", text: sidebarDetails } },
            ],
          },
        ],
      },
      `Failure alert — "${test.title}"`
    );
  }

  // ── Instant Alert: Test Skipped ────────────────────────────────────────

  private async sendSkippedAlert(
    test: TestCase,
    result: TestResult
  ): Promise<void> {
    const webhookUrl = this.getWebhookUrl();
    const testId = this.getTestId(test);
    if (!webhookUrl) return;
    if (this.skipAlertSent.has(testId)) return;
    this.skipAlertSent.add(testId);

    const skipReason =
      result.errors?.[0]?.message ||
      test.annotations.find((a) => a.type === "skip")?.description ||
      "No reason provided";

    const cleanReason = this.truncate(
      this.stripAnsi(skipReason),
      SlackReporter.MAX_SKIP_REASON_LENGTH
    );

    const filePath = test.location?.file
      ? this.getRelativePath(test.location.file)
      : "Unknown file";
    const line = test.location?.line ?? "N/A";
    const titlePath = test.titlePath();
    const suite =
      titlePath.length > 1 ? titlePath.slice(0, -1).join(" > ") : "N/A";

    const details = [
      `*Test Case:* ${test.title}`,
      `*Suite:* ${suite}`,
      `*File:* \`${filePath}:${line}\``,
      `*Env:* \`${process.env.CLUSTER || "local"}\`  •  *Tenant:* \`${this.getTenant()}\``,
      `*Time:* ${this.getISTTime()}`,
      "",
      "*Skip Reason:*",
      "```",
      cleanReason,
      "```",
    ].join("\n");

    await this.postToSlack(
      webhookUrl,
      {
        blocks: [
          {
            type: "section",
            text: {
              type: "mrkdwn",
              text: `:warning: *Test Skipped Alert*`,
            },
          },
        ],
        attachments: [
          {
            color: SlackReporter.COLORS.SKIP,
            blocks: [
              { type: "section", text: { type: "mrkdwn", text: details } },
            ],
          },
        ],
      },
      `Skipped alert — "${test.title}"`
    );
  }

  // ── Summary helpers ────────────────────────────────────────────────────

  private buildCiRunUrl(): string {
    const { GITHUB_SERVER_URL, GITHUB_REPOSITORY, GITHUB_RUN_ID } =
      process.env;
    if (GITHUB_SERVER_URL && GITHUB_REPOSITORY && GITHUB_RUN_ID) {
      return `${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}`;
    }
    return "";
  }

  private buildGreeting(
    failed: number,
    apiFailures: number,
    skipped: number
  ): string {
    if (failed === 0 && apiFailures === 0 && skipped === 0) {
      return "Congratulations @qa: All test cases passed!";
    }
    if (failed > 0 || apiFailures > 0) {
      return "Attention required @qa: Some test cases are failing";
    }
    return "Heads up @qa: Some test cases were skipped";
  }

  private getSummaryColor(
    failed: number,
    apiFailures: number,
    skipped: number
  ): string {
    if (failed > 0 || apiFailures > 0) return SlackReporter.COLORS.SUMMARY;
    if (skipped > 0) return SlackReporter.COLORS.SUMMARY_SKIP;
    return SlackReporter.COLORS.SUMMARY;
  }

  /**
   * Render a test-name set as a bulleted list, capped so the block never
   * exceeds Slack's 3000-char mrkdwn limit.  Overflow is signalled with an
   * italicised "…and N more" footer line.
   */
  private buildTestListBlock(tests: Set<string>, maxLen = 2800): string {
    const lines: string[] = [];
    let len = 0;
    let i = 0;
    for (const test of tests) {
      const line = `• ${test}`;
      if (len + line.length + 1 > maxLen) {
        lines.push(`_…and ${tests.size - i} more_`);
        break;
      }
      lines.push(line);
      len += line.length + 1;
      i++;
    }
    return lines.join("\n");
  }

  /** Writes summary JSON consumed by both the CI workflow step and (optionally) other tooling. */
  private writeSummaryJson(data: Record<string, unknown>): void {
    try {
      mkdirSync(PLAYWRIGHT_REPORT_DIR, { recursive: true });
      writeFileSync(
        path.join(PLAYWRIGHT_REPORT_DIR, "slack-summary.json"),
        JSON.stringify(data, null, 2)
      );
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      console.error(`[SlackReporter] Failed to write summary JSON: ${msg}`);
    }
  }
}
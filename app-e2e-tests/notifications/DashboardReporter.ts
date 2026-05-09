import type {
  Reporter,
  FullConfig,
  Suite,
  TestCase,
  TestResult,
  FullResult,
} from "@playwright/test/reporter";
import axios from "axios";

/**
 * DashboardReporter sends test results to the E2E Dashboard API
 *
 * Configuration:
 * - DASHBOARD_API_URL: URL of the dashboard API (e.g., http://e2e-dashboard:8080)
 * - E2E_ENVIRONMENT: Environment name (test/prod)
 * - GITHUB_RUN_URL: GitHub Actions run URL for linking
 *
 * If DASHBOARD_API_URL is not set, this reporter is a no-op
 */
class DashboardReporter implements Reporter {
  private runId: string | null = null;
  private results: ResultData[] = [];
  private dashboardUrl: string;
  private startTime: Date = new Date();

  constructor() {
    this.dashboardUrl = process.env.DASHBOARD_API_URL || "";
  }

  onBegin(config: FullConfig, suite: Suite): void {
    if (!this.dashboardUrl) {
      console.log(
        "[DashboardReporter] DASHBOARD_API_URL not set, skipping dashboard reporting"
      );
      return;
    }

    this.startTime = new Date();
    console.log(`[DashboardReporter] Starting test run reporting to ${this.dashboardUrl}`);
  }

  async onTestEnd(test: TestCase, result: TestResult): Promise<void> {
    if (!this.dashboardUrl) return;

    // Extract artifact URLs from attachments
    let screenshotUrl = "";
    let videoUrl = "";
    let traceUrl = "";

    for (const attachment of result.attachments) {
      if (attachment.name === "screenshot" && attachment.path) {
        screenshotUrl = attachment.path;
      } else if (attachment.name === "video" && attachment.path) {
        videoUrl = attachment.path;
      } else if (attachment.name === "trace" && attachment.path) {
        traceUrl = attachment.path;
      }
    }

    // Get stack trace from error
    const stackTrace = result.error?.stack || "";

    this.results.push({
      test_file: test.location.file,
      test_name: test.title,
      status: result.status,
      duration_ms: result.duration,
      error_message: result.error?.message || "",
      stack_trace: stackTrace,
      screenshot_url: screenshotUrl,
      video_url: videoUrl,
      trace_url: traceUrl,
      retry_count: result.retry,
    });
  }

  async onEnd(result: FullResult): Promise<void> {
    if (!this.dashboardUrl) return;

    try {
      // Create the test run
      const environment = process.env.E2E_ENVIRONMENT || "test";
      const githubRunUrl = process.env.GITHUB_RUN_URL || "";

      console.log(`[DashboardReporter] Creating run for environment: ${environment}`);

      const createResponse = await axios.post(`${this.dashboardUrl}/api/v1/runs`, {
        environment,
        github_run_url: githubRunUrl,
      });

      this.runId = createResponse.data.id;
      console.log(`[DashboardReporter] Created run with ID: ${this.runId}`);

      // Batch insert results
      if (this.results.length > 0) {
        await axios.post(`${this.dashboardUrl}/api/v1/runs/${this.runId}/results`, {
          results: this.results,
        });
        console.log(`[DashboardReporter] Uploaded ${this.results.length} test results`);
      }

      // Calculate summary
      const passed = this.results.filter((r) => r.status === "passed").length;
      const failed = this.results.filter((r) => r.status === "failed").length;
      const skipped = this.results.filter((r) => r.status === "skipped").length;
      const durationMs = Date.now() - this.startTime.getTime();

      // Update run status
      await axios.put(`${this.dashboardUrl}/api/v1/runs/${this.runId}`, {
        status: result.status,
        total_tests: this.results.length,
        passed,
        failed,
        skipped,
        duration_ms: durationMs,
        completed_at: new Date().toISOString(),
      });

      console.log(
        `[DashboardReporter] Run completed: ${passed} passed, ${failed} failed, ${skipped} skipped`
      );
    } catch (error) {
      console.error("[DashboardReporter] Failed to report to dashboard:", error);
    }
  }
}

interface ResultData {
  test_file: string;
  test_name: string;
  status: string;
  duration_ms: number;
  error_message: string;
  stack_trace: string;
  screenshot_url: string;
  video_url: string;
  trace_url: string;
  retry_count: number;
}

export default DashboardReporter;

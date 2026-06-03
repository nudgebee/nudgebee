import { Page, Response } from "@playwright/test";
import axios from "axios";
import * as dotenv from "dotenv";
import { readFileSync } from "fs";
import { TENANT_FILE_PATH } from "./paths";

dotenv.config();

function getTenant(): string {
  if (process.env.SWITCH_TENANT) return process.env.SWITCH_TENANT;
  try {
    return readFileSync(TENANT_FILE_PATH, "utf-8").trim() || "unknown";
  } catch {
    return "unknown";
  }
}

export interface GraphQLValidationOptions {
  testName: string;
  operationNames?: string | string[]; // Optional — omit to auto-capture all GraphQL ops
  urlContains?: string;
  timeoutMs?: number;
  postCaptureWaitMs?: number; // extra listen time after all target ops are found — captures async execution results
  checkDataErrors?: boolean;  // also detect application-level { status: "FAILED", error: "..." } inside data
  instantSlackNotification?: boolean;
  ignoreErrorMessages?: string[]; // GQL errors whose message includes any of these strings are treated as success
  workflowUrl?: string; // URL of the workflow page — included as a direct link in failure alerts
  onTargetedFailure?: () => void; // called immediately when a targeted operation is captured with failure
}

interface CapturedResponse {
  operationName: string;
  query?: string;
  status: number;
  body: any;
  isFailure: boolean;
  errorMessage?: string;
}
// All keys are stored lowercase; lookup uses key.toLowerCase() so the match is
// case-insensitive and works for camelCase, PascalCase, snake_case, etc.
const SENSITIVE_KEYS = new Set([
  "password", "token", "accesstoken", "refreshtoken", "authorization",
  "cookie", "sessionid", "secret", "apikey", "api_key", "jwt",
  "email", "phone", "phonenumber", "ssn", "creditcard", "cardnumber",
  "cvv", "dob", "dateofbirth", "address", "firstname", "lastname", "username",
]);


function redactSensitiveFields(obj: any, depth = 0): any {
  if (depth > 10 || obj === null || obj === undefined) return obj;
  if (typeof obj !== "object") return obj;
  if (Array.isArray(obj)) {
    return obj.map(item => redactSensitiveFields(item, depth + 1));
  }
  const result: Record<string, any> = {};
  for (const [key, value] of Object.entries(obj)) {
    result[key] = SENSITIVE_KEYS.has(key.toLowerCase())
      ? "[REDACTED]"
      : redactSensitiveFields(value, depth + 1);
  }
  return result;
}


function sanitizeForSlack(input: string): string {
  if (typeof input !== "string") return String(input);
  return input
    // Block mass-notification injections (e.g. <!here>, <!channel>, <!everyone>)
    .replace(/<!(?:here|channel|everyone)>/gi, "[mention-blocked]")
    // Escape & first to avoid double-encoding downstream replacements
    .replace(/&/g, "&amp;")
    // Escape angle brackets — this neutralises <URL|label> link injection too
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

async function sendSlackAlert(
  options: GraphQLValidationOptions,
  failures: CapturedResponse[],
  missingOps: string[],
  totalScanned: number,
  isInstant: boolean,
  workflowUrl?: string
): Promise<void> {
  const webhookUrl = process.env.SLACK_WEBHOOK_URL;

  if (!webhookUrl) {
    console.warn("[GraphQLWatcher] SLACK_WEBHOOK_URL not configured");
    return;
  }

  const uniqueFailures = Array.from(
    new Map(failures.map(f => [f.operationName, f])).values()
  );

  const errorDetails = uniqueFailures
    .map(f => {
      // REC 3: redact PII before serialising the body for Slack
      const safeBody = redactSensitiveFields(f.body);
      const bodyStr  = JSON.stringify(safeBody, null, 2);
      const truncated =
        bodyStr.length > 1000
          ? bodyStr.substring(0, 1000) + "...[truncated]"
          : bodyStr;

      // REC 4: sanitize all untrusted string fields before embedding in markdown
      const safeOpName    = sanitizeForSlack(f.operationName);
      const safeErrMsg    = f.errorMessage ? sanitizeForSlack(f.errorMessage) : "";

      // Include the GraphQL query (truncated) when available
      const queryStr = f.query
        ? sanitizeForSlack(
            f.query.length > 500
              ? f.query.substring(0, 500) + "...[truncated]"
              : f.query
          )
        : "";

      return (
        `*${safeOpName}* (Status: ${f.status})` +
        `${queryStr ? `\n*Query:*\n\`\`\`${queryStr}\`\`\`` : ""}` +
        `${safeErrMsg ? `\nError: ${safeErrMsg}` : ""}` +
        `\n\`\`\`${truncated}\`\`\``
      );
    })
    .join("\n\n");

  // REC 4: sanitize missing op names — they come from user-provided config or the
  // network, either of which could contain injected content.
  const safeMissingOps = missingOps.map(op => sanitizeForSlack(op));
  const missingText    = safeMissingOps.length > 0 ? safeMissingOps.join(", ") : "None";

  const color = "#FFFF00";

  // #8: Slack Block Kit rejects an empty string in section.text (400 response).
  // The fallback string ensures delivery even when both errorDetails and missingOps are empty.
  // Header differs based on what triggered the instant alert:
  //   - missing ops timeout  → "List of Operations/Payload not matched after timeout"
  //   - captured failure     → "API Failure Detected in Response"
  const instantHeader = safeMissingOps.length > 0
    ? `*List of Operations/Payload not matched after timeout*\n\`${missingText}\``
    : `*API Failure Detected in Response*`;

  const instantText =
    instantHeader +
    `${errorDetails ? `\n${errorDetails}` : ""}`;

  const finalText =
    `${errorDetails}` +
    `${safeMissingOps.length > 0 ? `\n\n*Missing Operations:*\n\`${missingText}\`` : ""}` ||
    "*No GraphQL operations were captured for this test run.*";

  // REC 4: sanitize the test name — it is tester-supplied and embedded directly
  // into the top-level Slack text field.
  const safeTestName = sanitizeForSlack(options.testName);

  const workflowLinkText = workflowUrl
    ? `\n*Workflow:* <${workflowUrl}|Open Workflow>`
    : "";

  const payload = {
    text: `*:rotating_light:API Validation Failed*\n*Test:* ${safeTestName}${workflowLinkText}`,
    attachments: [
      {
        color,
        blocks: [
          {
            type: "section",
            text: {
              type: "mrkdwn",
              text: `*Env:* ${process.env.CLUSTER || "unknown"} | *Tenant:* ${getTenant()}`,
            },
          },
          {
            type: "section",
            fields: [
              { type: "mrkdwn", text: `*Scanned:* ${totalScanned}` },
              { type: "mrkdwn", text: `*Failed:* ${uniqueFailures.length}` },
              { type: "mrkdwn", text: `*Missing:* ${missingOps.length}` },
            ],
          },
          { type: "divider" },
          {
            type: "section",
            text: {
              type: "mrkdwn",
              text: isInstant ? instantText : finalText,
            },
          },
        ],
      },
    ],
  };

  try {
    await axios.post(webhookUrl, payload, { timeout: 5000 });
    console.log(
      `[GraphQLWatcher] Slack alert sent (${isInstant ? "instant" : "final"})`
    );
  } catch (err: any) {
    console.error(`[GraphQLWatcher] Slack error: ${err.message}`);
  }
}

// =============================================================================
// Helper: per-operation GQL-error ignore check
// =============================================================================
/**
 * Receives a SINGLE operation's response slice (never a batched array).
 * Returns true only when EVERY error in that response is covered by an ignore
 * pattern — a single uncovered error still causes the operation to be marked failed.
 *
 * IMPORTANT: this helper only evaluates GraphQL-layer errors.
 * HTTP-level failures (status !== 200) and JSON parse failures are NEVER suppressible
 * by ignoreErrorMessages — see the isFailure calculation in the main watcher.
 */
// Recursively finds { status: "FAILED", error: "..." } patterns inside GraphQL data
// Returns the list of error strings found so they can be reported in Slack
function extractDataFailures(obj: any, depth = 0): string[] {
  if (depth > 10 || !obj || typeof obj !== "object") return [];
  if (Array.isArray(obj)) {
    return obj.flatMap((item) => extractDataFailures(item, depth + 1));
  }
  const results: string[] = [];
  if (obj.status === "FAILED" || obj.status === "ERROR" || obj.success === false) {
    const errorMsg =
      typeof obj.error === "string" && obj.error.trim() !== ""
        ? obj.error
        : "Automation execution failed (no error detail in response)";
    results.push(errorMsg);
  }
  for (const value of Object.values(obj)) {
    if (value && typeof value === "object") {
      results.push(...extractDataFailures(value, depth + 1));
    }
  }
  return results;
}

function shouldIgnoreErrors(
  singleOpBody: any,
  ignoreErrorMessages: string[]
): boolean {
  if (!ignoreErrorMessages.length) return false;

  const errors: Array<{ message?: string }> = singleOpBody?.errors ?? [];

  if (!errors.length) return false;

  return errors.every(err =>
    ignoreErrorMessages.some(pattern => err?.message?.includes(pattern))
  );
}

// =============================================================================
// Main Watcher
// =============================================================================
export async function waitForGraphQLAndValidate(
  page: Page,
  action: () => Promise<void>,
  options: GraphQLValidationOptions
): Promise<void> {
  const {
    testName,
    operationNames,
    urlContains = "api/graphql",
    timeoutMs = 60000,
    postCaptureWaitMs = 0,
    checkDataErrors = false,
    instantSlackNotification = true,
    ignoreErrorMessages = [],
    workflowUrl: explicitWorkflowUrl,
  } = options;

  // Resolve the workflow URL once — prefer the caller-provided value, fall back to
  // the current page URL if it looks like a workflow page (contains /workflow/).
  const resolveWorkflowUrl = (): string | undefined => {
    if (explicitWorkflowUrl) return explicitWorkflowUrl;
    const currentUrl = page.url();
    return currentUrl.includes("/workflow/") ? currentUrl : undefined;
  };

  // Auto-capture mode: engaged when operationNames is absent or explicitly []
  const isAutoMode =
    !operationNames ||
    (Array.isArray(operationNames) && operationNames.length === 0);

  // Explicit string[] type prevents TypeScript widening to Array<string | string[] | undefined>
  const targetOps: string[] = isAutoMode
    ? []
    : Array.isArray(operationNames)
    ? operationNames
    : [operationNames as string];

  if (!isAutoMode && targetOps.length === 0) {
    throw new Error("[GraphQLWatcher] operationNames cannot be empty");
  }

  const captured: CapturedResponse[]  = [];
  const foundOps                       = new Set<string>();
  const processingPromises: Promise<void>[] = [];
  let instantAlertSent                 = false;
  let isPostCapturePhase               = false; // when true, capture ALL ops (filter disabled)

  const responseHandler = async (response: Response): Promise<void> => {
    if (
      !response.url().includes(urlContains) ||
      response.request().method() !== "POST"
    ) {
      return;
    }

    const processingPromise = (async () => {
      try {
        const postData = response.request().postData();
        if (!postData) return;

        const requestBody = JSON.parse(postData);
        const queries = Array.isArray(requestBody) ? requestBody : [requestBody];

        // #1: Read response body ONCE, outside the per-operation loop.
        const status = response.status();
        let fullBody: any  = null;
        let parseError: string | undefined;

        try {
          fullBody = await response.json();
        } catch (e) {
          parseError = `JSON parse failed: ${e}`;
        }

        // #2: Normalise into an array so responseBodies[i] maps 1-to-1
        // with queries[i] — prevents cross-operation error attribution.
        const responseBodies: any[] = Array.isArray(fullBody)
          ? fullBody
          : [fullBody];

        for (let i = 0; i < queries.length; i++) {
          const query = queries[i];

          if (!query?.operationName) continue;
          if (!isAutoMode && !isPostCapturePhase && !targetOps.includes(query.operationName)) continue;

          // ─── REC 1: Batch mismatch — no fallback to index 0 ───────────────
          // If the server returns fewer response items than queries, falling back
          // to responseBodies[0] incorrectly attributes the first operation's
          // result to this one.  Treat the missing slice as a hard failure instead.
          if (i >= responseBodies.length) {
            const mismatchError =
              `Batch response mismatch: server returned ${responseBodies.length} ` +
              `item(s) for ${queries.length} operation(s). ` +
              `No response slice available at index ${i}.`;

            captured.push({
              operationName: query.operationName,
              query: query.query,
              status,
              body: null,
              isFailure: true,
              errorMessage: mismatchError,
            });
            foundOps.add(query.operationName);
            console.log(
              `[GraphQLWatcher] ❌ ${query.operationName} (${status}) — ${mismatchError}`
            );
            continue;
          }

          const singleOpBody: any = responseBodies[i];

          const hasGqlErrors: boolean = singleOpBody?.errors?.length > 0;

          // ─── REC 2: Status suppression — split failure dimensions ──────────
          // isIgnored may only neutralise the hasGqlErrors dimension.
          // HTTP failures (status !== 200) and JSON parse failures are ALWAYS
          // reported as failures — they represent server-level or transport-level
          // problems that are outside the scope of GraphQL error ignore rules.
          const httpFailure  = status !== 200;
          const parseFailure = parseError !== undefined;

          // shouldIgnoreErrors is only evaluated when there are no HTTP / parse
          // failures — a 500 with matching GQL errors must still be reported.
          const isIgnored =
            !httpFailure &&
            !parseFailure &&
            hasGqlErrors &&
            shouldIgnoreErrors(singleOpBody, ignoreErrorMessages);

          // Application-level errors inside data (e.g. status:"FAILED", error:"...")
          const dataErrors = checkDataErrors && !httpFailure && !parseFailure
            ? extractDataFailures(singleOpBody?.data)
            : [];
          const hasDataError = dataErrors.length > 0;

          const isFailure = httpFailure || parseFailure || (!isIgnored && hasGqlErrors) || hasDataError;

          if (hasDataError) {
            console.log(
              `[GraphQLWatcher] ❌ ${query.operationName} (${status}) — data-level error: ${dataErrors.join("; ")}`
            );
          }

          const capturedEntry: CapturedResponse = {
            operationName: query.operationName,
            query: query.query,
            status,
            // store only this op's slice for accurate Slack output
            body: singleOpBody,
            isFailure,
            errorMessage: parseError ?? (hasDataError ? dataErrors.join("; ") : undefined),
          };

          captured.push(capturedEntry);
          foundOps.add(query.operationName);

          if (isIgnored) {
            console.log(
              `[GraphQLWatcher] ⚠️ ${query.operationName} (${status}) — GQL errors ignored by ignoreErrorMessages`
            );
          } else {
            console.log(
              `[GraphQLWatcher] ${isFailure ? "❌" : "✅"} ${query.operationName} (${status})`
            );
          }

          // Send instant Slack alert as soon as a targeted operation is captured with failure —
          // don't wait for action() to complete (e.g. a 90s toast timeout).
          if (isFailure && !isIgnored && !isAutoMode) {
            if (instantSlackNotification && !instantAlertSent) {
              instantAlertSent = true;
              sendSlackAlert(
                options,
                [capturedEntry],
                [],
                captured.length,
                true,
                resolveWorkflowUrl()
              ).catch(err =>
                console.error(`[GraphQLWatcher] Instant failure alert failed: ${err.message}`)
              );
            }
            // Signal the action() to abort early (e.g. stop waiting for a toast)
            if (options.onTargetedFailure) {
              options.onTargetedFailure();
            }
          }
        }
      } catch (e) {
        console.error(`[GraphQLWatcher] Handler error: ${e}`);
      }
    })();

    processingPromises.push(processingPromise);
  };

  page.on("response", responseHandler);

  try {
    try {
      await action();
    } catch (actionError: any) {
      // If we already sent an instant Slack alert for a captured GraphQL failure,
      // re-throw with [GraphQLWatcher] prefix so SlackReporter's isApiFailure()
      // detects it and suppresses the duplicate "Test Failed Alert".
      if (instantAlertSent) {
        const msg = actionError?.message ?? String(actionError);
        throw new Error(`[GraphQLWatcher] ${testName} - ${msg}`);
      }
      throw actionError;
    }

    if (isAutoMode) {
      // Cap auto-capture wait to 5 s — callers who need longer should provide
      // explicit operationNames so the targeted-mode polling is used instead.
      const autoWait = Math.min(timeoutMs, 5000);
      if (timeoutMs > 5000) {
        console.warn(
          `[GraphQLWatcher] Auto mode: timeoutMs (${timeoutMs}ms) exceeds the ` +
            `5 s auto-capture window. Only waiting ${autoWait}ms. ` +
            `Provide operationNames to wait the full timeout for specific operations.`
        );
      }
      await page.waitForTimeout(autoWait);
    } else {
      // #6: startTime scoped inside targeted-mode branch only.
      const startTime = Date.now();

      while (targetOps.some(op => !foundOps.has(op))) {
        const elapsed = Date.now() - startTime;

        if (elapsed > timeoutMs) {
          const missingOps = targetOps.filter(op => !foundOps.has(op));
          console.warn(
            `[GraphQLWatcher] Timeout (${timeoutMs}ms). Missing: ${missingOps.join(", ")}`
          );

          if (instantSlackNotification && !instantAlertSent && missingOps.length > 0) {
            instantAlertSent = true;
            const uniqueScanned  = new Set(captured.map(c => c.operationName));
            const currentFailures = captured.filter(c => c.isFailure);

            sendSlackAlert(
              options,
              currentFailures,
              missingOps,
              uniqueScanned.size,
              true,
              resolveWorkflowUrl()
            ).catch(err =>
              console.error(`[GraphQLWatcher] Instant alert failed: ${err.message}`)
            );
          }

          break;
        }

        await page.waitForTimeout(100);
      }
    }

    // Keep listener alive after target ops fire — switch to capture-all mode so
    // async execution-result operations are no longer filtered out
    if (postCaptureWaitMs > 0) {
      isPostCapturePhase = true;
      console.log(`[GraphQLWatcher] Waiting ${postCaptureWaitMs}ms for async execution results...`);
      await page.waitForTimeout(postCaptureWaitMs);
    }

    // #7: snapshot processingPromises before settling to avoid
    // silently dropping promises pushed by concurrent late-arriving responses.
    await Promise.allSettled([...processingPromises]);
    await page.waitForTimeout(50);
  } finally {
    page.off("response", responseHandler);
  }

  // #3: zero-capture guard BEFORE success evaluation — ensures a Slack
  // alert is always sent and the guard is reachable in both modes.
  if (captured.length === 0) {
    const detail   = isAutoMode ? "any GraphQL POST" : targetOps.join(", ");
    const errorMsg = `[GraphQLWatcher] No operations captured. Expected: ${detail}`;
    console.warn(errorMsg);

    if (!instantAlertSent) {
      await sendSlackAlert(options, [], isAutoMode ? [] : targetOps, 0, false, resolveWorkflowUrl());
    }

    throw new Error(`[GraphQLWatcher] ${testName} - ${errorMsg}`);
  }

  const missingOperations = isAutoMode
    ? []
    : targetOps.filter(op => !foundOps.has(op));
  const failures = captured.filter(c => c.isFailure);
  const success  = missingOperations.length === 0 && failures.length === 0;

  console.log(
    `[GraphQLWatcher] Result: ${captured.length} captured, ${failures.length} failed, ${missingOperations.length} missing`
  );

  if (!success) {
    const uniqueScanned = new Set(captured.map(c => c.operationName));

    // Send alert only ONCE per test failure — deduplicated against the instant alert.
    if (!instantAlertSent) {
      await sendSlackAlert(
        options,
        failures,
        missingOperations,
        uniqueScanned.size,
        false,
        resolveWorkflowUrl()
      );
    }

    const errorParts: string[] = [];
    if (missingOperations.length > 0) {
      errorParts.push(`Missing: ${missingOperations.join(", ")}`);
    }
    if (failures.length > 0) {
      errorParts.push(
        `Failed: ${failures.map(f => `${f.operationName}(${f.status})`).join(", ")}`
      );
    }

    throw new Error(`[GraphQLWatcher] ${testName} - ${errorParts.join(" | ")}`);
  }

  console.log(
    `[GraphQLWatcher] ✅ Success - All ${captured.length} operations validated`
  );
}

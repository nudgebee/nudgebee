import { existsSync, readFileSync, writeFileSync, mkdirSync, statSync } from "fs";
import path from "path";
import axios from "axios";
import { PLAYWRIGHT_REPORT_DIR } from "./paths";

export type CloudProvider = "aws" | "azure" | "gcp";

/**
 * Optional presentation overrides for named (non-cloud) integrations — e.g.
 * Confluence config-presence checks. Cloud call sites omit these and get the
 * legacy "<PROVIDER> Integration Not Active" wording.
 */
export interface IntegrationAlertOptions {
  /** Human name in the Slack alert (e.g. "Confluence"). Default: derived from key. */
  displayName?: string;
  /** Status phrase in the alert (e.g. "Not Available"). Default: "Not Active". */
  statusLabel?: string;
}

interface CacheEntry {
  active: boolean;
  notified: boolean;
}

// Cache is valid for 30 min — covers a full test run without re-checking
const CACHE_TTL_MS = 30 * 60 * 1000;

function getCacheFile(provider: string): string {
  return path.join(PLAYWRIGHT_REPORT_DIR, `integration-status-${provider}.json`);
}

function readCache(provider: string): CacheEntry | null {
  const file = getCacheFile(provider);
  try {
    if (!existsSync(file)) return null;
    const stat = statSync(file);
    if (Date.now() - stat.mtimeMs > CACHE_TTL_MS) return null; // stale
    return JSON.parse(readFileSync(file, "utf-8")) as CacheEntry;
  } catch {
    return null;
  }
}

function writeCache(provider: string, entry: CacheEntry): void {
  try {
    mkdirSync(PLAYWRIGHT_REPORT_DIR, { recursive: true });
    writeFileSync(getCacheFile(provider), JSON.stringify(entry, null, 2));
  } catch {
    // non-fatal — cache miss just means the next worker re-checks
  }
}

/**
 * Runs the integration check once per provider per run (30-min TTL cache).
 * If not active, sends ONE Slack alert tagging @qa — subsequent workers/tests
 * skip silently because the cache already has notified=true.
 *
 * @param provider  Cloud provider key
 * @param doCheck   Async fn that performs the actual UI check and returns boolean
 */
export async function checkIntegrationWithCache(
  provider: CloudProvider,
  doCheck: () => Promise<boolean>
): Promise<boolean> {
  const cached = readCache(provider);
  if (cached !== null) {
    const label = provider.toUpperCase();
    console.log(
      `[IntegrationCache] Using cached ${label} status: ${cached.active ? "Active ✅" : "Not Active ❌"}`
    );
    return cached.active;
  }

  const active = await doCheck();

  let notified = false;
  if (!active) {
    notified = await sendIntegrationAlert(provider);
  }

  writeCache(provider, { active, notified });
  return active;
}

// Alert dedup for named integrations — in-memory, one @qa alert per key per
// worker process. Kept separate from the file cache above on purpose: named
// integrations (e.g. Confluence) are created/deleted DURING a run, so their
// presence must be re-checked every time and never cached — only the alert is
// deduped so Disable + Enable don't each fire a message.
const missingAlertSent = new Set<string>();

/**
 * Sends ONE `@qa` Slack alert that `key`'s integration is missing, then
 * suppresses further alerts for the same key for the rest of the worker
 * process. Does NOT cache presence — callers must run their own live check
 * every time and pass the result. Safe to call from multiple tests (Disable,
 * Enable, …): only the first missing check for a given key posts to Slack.
 */
export async function notifyIntegrationMissingOnce(
  key: string,
  opts: IntegrationAlertOptions = {}
): Promise<void> {
  if (missingAlertSent.has(key)) return;
  missingAlertSent.add(key);
  await sendIntegrationAlert(key, opts);
}

async function sendIntegrationAlert(
  provider: string,
  opts: IntegrationAlertOptions = {}
): Promise<boolean> {
  const webhookUrl = process.env.SLACK_WEBHOOK_URL?.trim();
  if (!webhookUrl?.startsWith("http")) return false;

  const providerName =
    opts.displayName ?? (provider === "gcp" ? "GCP" : provider.toUpperCase());
  const statusLabel = opts.statusLabel ?? "Not Active";
  const env = process.env.CLUSTER || process.env.E2E_ENVIRONMENT || "unknown";
  const baseUrl = process.env.BASE_URL || "";

  try {
    await axios.post(
      webhookUrl,
      {
        blocks: [
          {
            type: "section",
            text: {
              type: "mrkdwn",
              text: `:warning: *${providerName} Integration ${statusLabel} — All ${providerName} Tests Skipped*`,
            },
          },
          {
            type: "section",
            text: {
              type: "mrkdwn",
              text: [
                `@qa — Please integrate *${providerName}* first, then re-run the tests.`,
                `*Env:* \`${env}\`${baseUrl ? `  •  <${baseUrl}|Open App>` : ""}`,
              ].join("\n"),
            },
          },
        ],
      },
      { timeout: 10000, headers: { "Content-Type": "application/json" } }
    );
    console.log(`[IntegrationCache] Slack alert sent for ${providerName} ${statusLabel}`);
    return true;
  } catch (err) {
    console.warn(`[IntegrationCache] Failed to send Slack alert: ${err}`);
    return false;
  }
}

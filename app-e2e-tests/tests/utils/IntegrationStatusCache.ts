import { existsSync, readFileSync, writeFileSync, mkdirSync, statSync } from "fs";
import path from "path";
import axios from "axios";
import { PLAYWRIGHT_REPORT_DIR } from "./paths";

export type CloudProvider = "aws" | "azure" | "gcp";

interface CacheEntry {
  active: boolean;
  notified: boolean;
}

// Cache is valid for 30 min — covers a full test run without re-checking
const CACHE_TTL_MS = 30 * 60 * 1000;

function getCacheFile(provider: CloudProvider): string {
  return path.join(PLAYWRIGHT_REPORT_DIR, `integration-status-${provider}.json`);
}

function readCache(provider: CloudProvider): CacheEntry | null {
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

function writeCache(provider: CloudProvider, entry: CacheEntry): void {
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

async function sendIntegrationAlert(provider: CloudProvider): Promise<boolean> {
  const webhookUrl = process.env.SLACK_WEBHOOK_URL?.trim();
  if (!webhookUrl?.startsWith("http")) return false;

  const providerName = provider === "gcp" ? "GCP" : provider.toUpperCase();
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
              text: `:warning: *${providerName} Integration Not Active — All ${providerName} Tests Skipped*`,
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
    console.log(`[IntegrationCache] Slack alert sent for ${providerName} not active`);
    return true;
  } catch (err) {
    console.warn(`[IntegrationCache] Failed to send Slack alert: ${err}`);
    return false;
  }
}

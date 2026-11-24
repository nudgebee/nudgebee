import axios from "axios";
import path from "path";
import { appendFileSync, closeSync, existsSync, mkdirSync, openSync, readFileSync, rmSync, statSync, writeFileSync } from "fs";
import { PLAYWRIGHT_REPORT_DIR } from "../tests/utils/paths";

// One run used to post one Slack message per failed or skipped test, plus a separate message for
// every API failure and missing integration, which buried the channel. With a bot token the alerts
// now go into three threads per run instead — failed tests, skipped tests, and everything else —
// each opened by a short parent message the first time that kind of alert appears, so a green run
// posts no threads at all.
//
// The alerts come from different processes (the reporter runs in the main process, the API-failure
// and integration alerts run inside the test workers), so the parent message ids are shared through
// files in the report folder, and the first process to need a parent creates it while the others
// wait for it. Without a bot token, or if Slack refuses, every caller falls back to the incoming
// webhook exactly as before, so nothing is lost — it just is not threaded.
export type AlertKind = "failed" | "skipped" | "other";

const THREADS_DIR = path.join(PLAYWRIGHT_REPORT_DIR, "slack-threads");
const WAIT_FOR_PARENT_MS = 20_000;
const SLACK_TIMEOUT_MS = 10_000;

const THREAD_TITLES: Record<AlertKind, string> = {
  failed: ":rotating_light: Failed tests",
  skipped: ":warning: Skipped tests",
  other: ":information_source: Other alerts",
};

const KINDS = Object.keys(THREAD_TITLES) as AlertKind[];

function bot(): { token: string; channel: string } | null {
  const token = process.env.SLACK_BOT_TOKEN?.trim();
  const channel = process.env.SLACK_REPORT_CHANNEL_ID?.trim();
  return token && channel ? { token, channel } : null;
}

// Overridable only so the threading can be exercised against a local stand-in for Slack.
function slackUrl(method: string): string {
  return `${process.env.SLACK_API_URL?.trim() || "https://slack.com/api"}/${method}`;
}

type SlackReply = { ok: boolean; ts?: string; permalink?: string; error?: string };

// Slack allows about one post a second per channel, so a run with many failures can be told to slow
// down. Falling back to the webhook at that point would put the per-test messages straight back, so a
// "ratelimited" answer is waited out (for as long as Slack asks, up to 30s) and tried again first.
const RATE_LIMIT_RETRIES = 3;

async function slackCall(method: string, token: string, body: Record<string, unknown>): Promise<SlackReply> {
  for (let attempt = 0; ; attempt++) {
    let waitSeconds = 0;
    try {
      const res = await axios.post(slackUrl(method), body, {
        timeout: SLACK_TIMEOUT_MS,
        headers: { "Content-Type": "application/json; charset=utf-8", Authorization: `Bearer ${token}` },
      });
      const reply: SlackReply = res.data ?? { ok: false, error: "empty response" };
      if (reply.error !== "ratelimited") return reply;
      waitSeconds = Number(res.headers?.["retry-after"]) || 2;
    } catch (err: unknown) {
      const response = (err as { response?: { status?: number; headers?: Record<string, string> } })?.response;
      if (response?.status !== 429) return { ok: false, error: (err as Error)?.message ?? String(err) };
      waitSeconds = Number(response.headers?.["retry-after"]) || 2;
    }
    if (attempt >= RATE_LIMIT_RETRIES) return { ok: false, error: "ratelimited" };
    await new Promise((r) => setTimeout(r, Math.min(waitSeconds, 30) * 1000));
  }
}

const file = (kind: AlertKind, ext: "json" | "lock" | "count") => path.join(THREADS_DIR, `${kind}.${ext}`);

function readParent(kind: AlertKind): { ts: string | null; at: string } | undefined {
  try {
    return JSON.parse(readFileSync(file(kind, "json"), "utf-8")) as { ts: string | null; at: string };
  } catch {
    return undefined; // not written yet
  }
}

// Clears the previous run's thread ids. Called once, from the reporter, before any worker starts.
export function resetAlertThreads(): void {
  try {
    rmSync(THREADS_DIR, { recursive: true, force: true });
  } catch {
    // A leftover file only risks replying into an old thread; nothing here may stop the run.
  }
}

// Says, in plain words, what this run is for. A PR that is pushed to again cancels its running job and
// starts a new one, and each run opens its own threads, so three parents reading only "Failed tests"
// look like one run duplicating itself. The PR's or merge's own title tells people which change it is.
function eventPayload(): any {
  const eventPath = process.env.GITHUB_EVENT_PATH;
  if (!eventPath) return {};
  try {
    return JSON.parse(readFileSync(eventPath, "utf-8"));
  } catch {
    return {};
  }
}

// Titles are typed by people and land in a Slack message, so the characters Slack treats as markup are escaped.
function plain(text: unknown, max = 70): string {
  const line = String(text ?? "").split(/\r?\n/)[0].trim().replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return line.length > max ? `${line.slice(0, max - 1)}…` : line;
}

// A merge commit's first line is the generic "Merge pull request #N from branch"; the PR's own title is the line below it.
function mergeTitle(message: unknown): string {
  const lines = String(message ?? "").split(/\r?\n/).map((l) => l.trim()).filter(Boolean);
  return /^Merge pull request/i.test(lines[0] ?? "") && lines[1] ? lines[1] : (lines[0] ?? "");
}

function runLabel(): string {
  const { GITHUB_EVENT_NAME, GITHUB_ACTOR } = process.env;
  if (!GITHUB_EVENT_NAME) return "Local run";
  const event = eventPayload();
  const by = GITHUB_ACTOR ? `  ·  ${plain(GITHUB_ACTOR, 40)}` : "";
  switch (GITHUB_EVENT_NAME) {
    case "pull_request":
      return `PR check  ·  ${plain(event.pull_request?.title, 60) || "untitled"}${by}`;
    case "push":
      return `After merge  ·  ${plain(mergeTitle(event.head_commit?.message), 60) || "main updated"}`;
    case "schedule":
      return "Nightly run";
    case "workflow_dispatch":
      return `Manual run${by}`;
    default:
      return plain(GITHUB_EVENT_NAME);
  }
}

// Dev, test and OSS runs post into the same channel, so the line says which one this is. Only in CI, where the
// dev workflow sets E2E_ENVIRONMENT=dev, the OSS workflow sets oss and the test workflow does not set it.
function environmentName(): string {
  if (!process.env.GITHUB_EVENT_NAME) return "";
  const env = process.env.E2E_ENVIRONMENT;
  return `${env === "dev" ? "Dev" : env === "oss" ? "OSS" : "Test"}  ·  `;
}

function startedAt(): string {
  return new Date().toLocaleString("en-IN", { timeZone: "Asia/Kolkata", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hour12: true });
}

// `at` is the time the thread was opened, so the closing count does not rewrite it to the end time.
function parentText(kind: AlertKind, at?: string, count?: number): string {
  const { GITHUB_SERVER_URL, GITHUB_REPOSITORY, GITHUB_RUN_ID } = process.env;
  const ci = GITHUB_SERVER_URL && GITHUB_REPOSITORY && GITHUB_RUN_ID ? `  ·  <${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}|CI run>` : "";
  // Two lines: what this thread holds, in bold, then which run it belongs to, so a column of them scans cleanly.
  const title = `${THREAD_TITLES[kind]}${count === undefined ? "" : ` (${count})`}`;
  const timeLabel = at ? `  ·  ${at}` : "";
  return `*${title}*\n${environmentName()}${runLabel()}${timeLabel}${ci}`;
}

// The parent message id for this kind, creating the parent if this process is the first to need it.
// Returns null when there is no usable thread, so the caller falls back to the webhook.
async function getParentTs(kind: AlertKind, cfg: { token: string; channel: string }): Promise<string | null> {
  const known = readParent(kind);
  if (known !== undefined) return known.ts;

  mkdirSync(THREADS_DIR, { recursive: true });
  let owner = false;
  try {
    closeSync(openSync(file(kind, "lock"), "wx")); // fails if another process already took it
    owner = true;
  } catch {
    owner = false;
  }

  if (owner) {
    const at = startedAt();
    const res = await slackCall("chat.postMessage", cfg.token, { channel: cfg.channel, link_names: true, text: parentText(kind, at) });
    const ts = res.ok && res.ts ? res.ts : null;
    if (!ts) console.warn(`[SlackThreads] Could not open the ${kind} thread (${res.error ?? "unknown"}) — falling back to the webhook.`);
    // Written even on failure, so the processes waiting on it stop waiting and fall back too.
    writeFileSync(file(kind, "json"), JSON.stringify({ ts, at }), "utf-8");
    return ts;
  }

  const deadline = Date.now() + WAIT_FOR_PARENT_MS;
  while (Date.now() < deadline) {
    const parent = readParent(kind);
    if (parent !== undefined) return parent.ts;
    await new Promise((r) => setTimeout(r, 200));
  }
  return null;
}

// Posts the alert as a reply in this kind's thread. Returns true when it was posted there; false
// means the caller must send it the way it always did. Never throws.
export async function postAlertInThread(kind: AlertKind, payload: Record<string, unknown>): Promise<boolean> {
  const cfg = bot();
  if (!cfg) return false;
  try {
    const ts = await getParentTs(kind, cfg);
    if (!ts) return false;
    const res = await slackCall("chat.postMessage", cfg.token, { channel: cfg.channel, thread_ts: ts, link_names: true, ...payload });
    if (!res.ok) {
      console.warn(`[SlackThreads] ${kind} reply rejected (${res.error ?? "unknown"}) — falling back to the webhook.`);
      return false;
    }
    appendFileSync(file(kind, "count"), "x");
    return true;
  } catch (err: unknown) {
    console.warn(`[SlackThreads] ${kind} reply failed (${(err as Error)?.message ?? err}) — falling back to the webhook.`);
    return false;
  }
}

// One line for the run summary that links to each thread this run opened, so the summary is the single
// place to start from. Empty when no thread was opened (a green run) or no bot is configured. Never throws.
export async function threadLinksText(): Promise<string> {
  const cfg = bot();
  if (!cfg) return "";
  // The three lookups are independent, so they run together instead of one after another.
  const found = await Promise.all(
    KINDS.map(async (kind) => {
      try {
        const parent = readParent(kind);
        if (!parent?.ts || !existsSync(file(kind, "count"))) return null;
        const res = await slackCall("chat.getPermalink", cfg.token, { channel: cfg.channel, message_ts: parent.ts });
        if (!res.ok || !res.permalink) return null;
        return `<${res.permalink}|${THREAD_TITLES[kind].replace(/^:[a-z_]+: /, "")} (${statSync(file(kind, "count")).size})>`;
      } catch {
        return null; // A missing link only leaves the summary without that shortcut.
      }
    })
  );
  const links = found.filter((link): link is string => link !== null);
  return links.length ? `*Threads:*  ${links.join("  ·  ")}` : "";
}

// Puts the final number in each thread's parent message, once the run is over. Never throws.
export async function finalizeAlertThreads(): Promise<void> {
  const cfg = bot();
  if (!cfg) return;
  // Independent updates, so they run together instead of one after another.
  await Promise.all(
    KINDS.map(async (kind) => {
      try {
        const parent = readParent(kind);
        if (!parent?.ts || !existsSync(file(kind, "count"))) return;
        await slackCall("chat.update", cfg.token, { channel: cfg.channel, ts: parent.ts, text: parentText(kind, parent.at, statSync(file(kind, "count")).size) });
      } catch {
        // The parent already reads correctly without the count.
      }
    })
  );
}

import { Page } from "@playwright/test";
import { NubiLocators } from "../nubi/nubiLocators";

// Statuses that mean the conversation stopped producing output. Mirrors
// TERMINAL_FOLLOWUP_STATUSES in the app's KubernetesLLMResponseGeneratorV2.
const TERMINAL_STATUSES = ["COMPLETED", "TERMINATED", "KILLED", "FAILED"];

export interface NubiAnswer {
  /** Assistant text of the last answered message; "" if Nubi produced none. */
  answer: string;
  /** Terminal conversation status, or "TIMEOUT" if none arrived in time. */
  status: string;
  conversationId: string;
  /** Tools Nubi actually invoked — evidence it queried the system, not the prompt. */
  toolNames: string[];
  /** Agents that ran for this conversation. */
  agentNames: string[];
}

/** Phrases that mean "I could not answer" — a COMPLETED refusal is still a failure. */
const REFUSAL_PATTERN =
  /\b(i (?:don'?t|do not|cannot|can'?t) (?:have|find|access|see)|no (?:relevant )?(?:information|data|results)|unable to (?:find|retrieve|access))\b/i;

/** True when Nubi answered with a refusal / empty-knowledge response. */
export function isRefusal(answer: string): boolean {
  return REFUSAL_PATTERN.test(answer);
}

/** Case-insensitive "the answer mentions this literal value" check, safe for regex metacharacters. */
export function answerMentions(answer: string, value: string): boolean {
  return answer.toLowerCase().includes(value.toLowerCase());
}

export interface AskNubiOptions {
  /** Start a fresh conversation before asking. Default true. */
  newChat?: boolean;
  /** How long to wait for a terminal conversation status. Default 300000. */
  timeoutMs?: number;
}

/** Pulls the answer text + status out of one ai_get_conversation_v3 payload. */
function readConversation(payload: any): NubiAnswer | null {
  const data = payload?.data?.ai_get_conversation_v3;
  if (!data) return null;

  const messages: any[] = Array.isArray(data.messages) ? data.messages : [];
  // The assistant's text lives in `response`; the user's question in `message`.
  // Last non-empty `response` wins — followups append further messages.
  const answered = [...messages].reverse().find((m) => typeof m?.response === "string" && m.response.trim());

  // Fall back to the agents' own output: a conversation answered entirely by a
  // sub-agent can carry its text there rather than on the message row.
  const agents: any[] = Array.isArray(data.agents) ? data.agents : [];
  const agentText = agents
    .map((a) => a?.response_summary || a?.response)
    .filter((t) => typeof t === "string" && t.trim())
    .join("\n");

  // Which tools actually ran. This is the strongest available evidence that Nubi
  // went and looked something up rather than answering from the prompt alone —
  // and unlike the prose, it is a deterministic value a test can assert on.
  const toolCalls: any[] = Array.isArray(data.tool_calls) ? data.tool_calls : [];

  return {
    answer: (answered?.response ?? agentText ?? "").trim(),
    status: data.conversation?.status ?? "",
    conversationId: data.conversation?.id ?? "",
    toolNames: [...new Set(toolCalls.map((t) => t?.tool_name).filter((n): n is string => typeof n === "string"))],
    agentNames: [...new Set(agents.map((a) => a?.agent_name).filter((n): n is string => typeof n === "string"))],
  };
}

/**
 * Asks Nubi a question and waits for the answer, returning the assistant text so
 * the caller can assert on it.
 *
 * The answer is read from the AiGetConversationV3 poll responses rather than the
 * DOM: the chat panel has no stable test hooks on rendered messages, and the
 * payload also carries the conversation status, which is the only reliable
 * signal that Nubi has finished rather than merely paused mid-stream.
 *
 * Assumes the caller is already logged in — it never logs in itself, so it is
 * safe to chain after other steps inside a single-login journey.
 */
export async function askNubi(page: Page, question: string, options: AskNubiOptions = {}): Promise<NubiAnswer> {
  const { newChat = true, timeoutMs = 300000 } = options;
  const locators = new NubiLocators(page);

  // Go straight to the full Nubi page instead of clicking the floating "Ask nubi"
  // button. What that button does depends on where you already are: from /home it
  // navigates here, but from elsewhere (e.g. Admin → Integrations, where a journey
  // leaves you) it opens the popup panel, which renders the same chat in a
  // different mode. Navigating makes the destination the same every time.
  if (!page.url().includes("/ask-nudgebee")) {
    await page.goto("/ask-nudgebee");
  }

  const ready = await locators.chatTextbox
    .waitFor({ state: "visible", timeout: 60000 })
    .then(() => true)
    .catch(() => false);
  // Fall back to the panel: /ask-nudgebee could be gated off in some environment.
  if (!ready) await locators.openPanel();

  if (newChat) {
    const newChatVisible = await locators.newChatBtn.isVisible().catch(() => false);
    if (newChatVisible) await locators.newChatBtn.click();
  }

  let latest: NubiAnswer = { answer: "", status: "", conversationId: "", toolNames: [], agentNames: [] };
  // The answer as it stood when the conversation reached a terminal status —
  // i.e. Nubi's final word, not a partial poll. Preferred over `latest.answer`.
  let terminalAnswer: string | null = null;
  let resolveTerminal!: () => void;
  const terminal = new Promise<void>((resolve) => {
    resolveTerminal = resolve;
  });

  const onResponse = (response: import("@playwright/test").Response) => {
    if (!response.url().includes("/api/graphql")) return;
    if (!response.request().postData()?.includes("AiGetConversationV3")) return;

    void response
      .json()
      .then((body) => {
        // The gateway batches queries, so a body may be an array of results.
        for (const payload of Array.isArray(body) ? body : [body]) {
          const parsed = readConversation(payload);
          if (!parsed) continue;
          // Tools/agents accumulate across polls; a later poll that reports none
          // must not erase what an earlier one already showed.
          latest = {
            ...latest,
            ...parsed,
            answer: parsed.answer || latest.answer,
            toolNames: [...new Set([...latest.toolNames, ...parsed.toolNames])],
            agentNames: [...new Set([...latest.agentNames, ...parsed.agentNames])],
          };
          if (TERMINAL_STATUSES.includes(parsed.status)) {
            // Snapshot the answer as it stands at the terminal poll. Polls before
            // this one carry partial text — a streaming answer mid-write, or an
            // intermediate agent's output — and asserting on those would test
            // whatever happened to be on screen when a poll landed rather than
            // what Nubi finally said.
            terminalAnswer = parsed.answer || latest.answer;
            resolveTerminal();
          }
        }
      })
      .catch(() => {
        /* non-JSON or already-consumed body — the next poll carries the same state */
      });
  };

  page.on("response", onResponse);
  try {
    await locators.chatTextbox.waitFor({ state: "visible", timeout: 60000 });
    await locators.chatTextbox.fill(question);
    await locators.submitBtn.click();

    const finished = await Promise.race([
      terminal.then(() => true),
      // The loser of this race stays pending, and page.waitForTimeout rejects
      // once the page closes at end of test — swallow that so it cannot surface
      // as an unhandled rejection after the step has already passed.
      page
        .waitForTimeout(timeoutMs)
        .then(() => false)
        .catch(() => false),
    ]);
    if (!finished) {
      console.warn(`askNubi: no terminal status within ${timeoutMs / 1000}s (last seen: "${latest.status}")`);
      return { ...latest, status: latest.status || "TIMEOUT" };
    }

    // One more poll can land between the terminal status and the listener being
    // removed, carrying the completed answer for a conversation whose earlier
    // polls were still mid-stream. Give it a moment, then prefer whichever text
    // is longer — a final answer is never shorter than the partial it replaces.
    await page.waitForTimeout(1500).catch(() => {});
    const finalAnswer = [terminalAnswer ?? "", latest.answer].sort((a, b) => b.length - a.length)[0];
    return { ...latest, answer: finalAnswer };
  } finally {
    page.off("response", onResponse);
  }
}

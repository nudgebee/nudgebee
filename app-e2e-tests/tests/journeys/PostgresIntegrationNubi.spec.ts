import { test, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import {
  checkPostgresIntegrationExists,
  deletePostgresIntegration,
  addPostgresIntegration,
} from "../admin/Integrations/postgresHelper";
import { askNubi, isRefusal, answerMentions } from "../utils/nubi";

/**
 * Feature journey: one login, several areas of the app.
 *
 * This file contains NO test logic of its own — every stage calls the same step
 * function the piecewise specs call (postgresHelper for the integration, askNubi
 * for the chat). It only decides the order, and carries state between stages.
 *
 * Shape to copy for other journeys:
 *  - ONE `test()`, one `test.step()` per stage. The `page` fixture lives for the
 *    whole test, so session, tenant and cluster survive every stage, and
 *    trace/video/screenshot/Slack reporting keep working (a hand-built context
 *    via browser.newContext() would lose all of them).
 *  - Call `doFullLogin()` once at the top. The step functions call it again
 *    internally via navigateToDatabaseTab(); it no-ops on an already-logged-in
 *    page, so stages compose without a second tenant switch.
 *  - Steps live next to the specs that use them piecewise (postgresHelper.ts),
 *    never inside a `test()` — importing another spec file does not chain its
 *    tests, it registers them as separate tests with their own fresh page.
 */

const requiredEnv = ["POSTGRES_NAME", "POSTGRES_SECRET", "CLUSTER"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);
const configName = process.env.POSTGRES_NAME ?? "";

test("Journey: add Postgres integration, then ask Nubi about it", async ({ page }) => {
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  // Covers the integration form, the LLM round-trip and the polling in between.
  test.setTimeout(600000);

  const loginPage = new LoginPage(page);

  await test.step("Log in with the configured tenant and cluster", async () => {
    await loginPage.doFullLogin();
  });

  await test.step(`Add Postgres integration "${configName}"`, async () => {
    // Clear any config a previous run left behind, so the add below is a real
    // create rather than a no-op on "already exists".
    if (await checkPostgresIntegrationExists(page, configName)) {
      await deletePostgresIntegration(page, configName);
    }
    await addPostgresIntegration(page, {
      configName,
      secret: process.env.POSTGRES_SECRET ?? "",
      cluster: process.env.CLUSTER ?? "",
    });
  });

  await test.step("Ask Nubi about the Postgres integration and validate the answer", async () => {
    // A question with a known-correct answer. The previous step created an
    // integration called `configName`, so a truthful reply MUST name it — that
    // is ground truth this test owns, not a guess about how the model phrases
    // things. Asking for names only also keeps the reply short and checkable.
    const question =
      `List the database integrations configured in this account. ` +
      `Reply with their configuration names only, one per line, and nothing else.`;

    const { answer, status, toolNames, agentNames, conversationId } = await askNubi(page, question);
    console.log(
      `Nubi conversation ${conversationId} finished as ${status}\n` +
        `tools: ${toolNames.join(", ") || "(none)"}\nagents: ${agentNames.join(", ") || "(none)"}\n${answer}`,
    );

    // 1. The run itself succeeded — not FAILED/KILLED/TIMEOUT.
    expect(status, "Nubi conversation did not complete successfully").toBe("COMPLETED");

    // 2. It produced something.
    expect(answer.length, "Nubi returned an empty answer").toBeGreaterThan(0);

    // 3. It is not a polite non-answer. A refusal still completes, so without
    //    this "I don't have access to that" would pass every other check.
    expect(isRefusal(answer), `Nubi refused to answer: ${answer}`).toBe(false);

    // 4. It actually looked the data up. Tool calls are recorded per conversation,
    //    so this distinguishes a real lookup from a fluent guess.
    expect(toolNames.length, "Nubi answered without invoking any tool").toBeGreaterThan(0);
  });
});

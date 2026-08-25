import { test, expect } from "@playwright/test";
import { openTaskRunner, selectRunnerAccount, selectTask, setTaskField, runTask, expectRunSucceeded, readRunOutput, readRunOutputValue } from "./taskRunnerHelper";

// AI/LLM category. Answers are not deterministic, so these assert the model was
// reached and the `data` field came back non-empty — never what it says.

// A summary task needs something to summarise. A bare greeting gets a short
// canned reply whose length differs per environment, which is not a summary.
const SUMMARY_MESSAGE =
  "Checkout pods began crash-looping at 14:05 right after the v2.3 rollout. " +
  "Readiness probes timed out, the rollback completed at 14:22, and error rates returned to baseline.";

test("Task Runner - select account, select LLM Summary task, enter message, run task, verify the summary payload", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(240000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "llm.summary", "summary");

  await setTaskField(locators, "Message", SUMMARY_MESSAGE);

  await runTask(page, locators, "Task Runner LLM Summary", { timeoutMs: 180000 });
  await expectRunSucceeded(locators);

  expect(await readRunOutput(locators)).toContain("conversation_id");
  // No size threshold: a valid reply can be short. What is checkable is that a
  // conversation was created and the answer field came back with something.
  expect((await readRunOutputValue(locators, "conversation_id")).trim()).not.toEqual("");
  const answer = await readRunOutputValue(locators, "data");
  expect(answer.trim()).not.toEqual("");
  console.log(`llm.summary returned a summary (${answer.length} characters)`);
});

test("Task Runner - select account, select LLM Nubi task, enter question, run task, verify the answer payload", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(240000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "llm.nubi", "nubi");

  await setTaskField(locators, "Message", "List the namespaces in this cluster.");

  await runTask(page, locators, "Task Runner LLM Nubi", { timeoutMs: 180000 });
  await expectRunSucceeded(locators);

  expect(await readRunOutput(locators)).toContain("session_id");
  expect((await readRunOutputValue(locators, "session_id")).trim()).not.toEqual("");
  const answer = await readRunOutputValue(locators, "data");
  expect(answer.trim()).not.toEqual("");
  console.log(`llm.nubi returned an answer for the selected account (${answer.length} characters)`);
});

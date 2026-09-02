import { test, expect } from "@playwright/test";
import { openTaskRunner, selectRunnerAccount, selectTask, setTaskField, runTask, expectRunSucceeded, readRunOutput } from "./taskRunnerHelper";

// Core category. Only core.print runs standalone; the rest are in
// TASKS_WITHOUT_INDIVIDUAL_RUN and are covered by Validation.spec.ts.

test("Task Runner - select account, select Core Print task, enter message, run task, verify the message is echoed back", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@smoke", "@functional"] }, async ({ page }) => {
  test.setTimeout(150000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "core.print", "print");

  const message = `Task Runner e2e ${Date.now()}`;
  await setTaskField(locators, "Message", message);
  await runTask(page, locators, "Task Runner Core Print");
  await expectRunSucceeded(locators);

  expect(await readRunOutput(locators)).toContain(message);
  console.log("core.print echoed the message back");
});

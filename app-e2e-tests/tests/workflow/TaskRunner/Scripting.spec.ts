import { test, expect } from "@playwright/test";
import { openTaskRunner, selectRunnerAccount, selectTask, setTaskField, runTask, expectRunSucceeded, readRunOutput } from "./taskRunnerHelper";

// Scripting category.

test("Task Runner - select account, select Run Script task, type script in the code editor, run task, verify stdout", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@smoke", "@functional"] }, async ({ page }) => {
  test.setTimeout(180000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "scripting.run_script", "script");

  // Executor type, OS and language are left at their account defaults —
  // exercising the same path a user gets when they only type a script.
  const marker = `task-runner-e2e-${Date.now()}`;
  await setTaskField(locators, "Script", `echo "${marker}"`);

  await runTask(page, locators, "Task Runner Run Script");
  await expectRunSucceeded(locators);

  expect(await readRunOutput(locators)).toContain(marker);
  console.log("scripting.run_script returned the script's stdout");
});

import { test, expect } from "@playwright/test";
import {
  openTaskRunner,
  selectRunnerAccount,
  selectTask,
  setTaskField,
  setDropdownField,
  runTask,
  expectRunSucceeded,
  readRunOutput,
} from "./taskRunnerHelper";

// Integrations category. integrations.ssh needs a configured SSH integration on
// the tenant, so it is not covered here.
const HTTP_TASK_URL = process.env.HTTP_TASK_URL || "https://api.github.com/zen";

test("Task Runner - select account, select HTTP task, enter url, pick GET method, run task, verify 200 status_code", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@smoke", "@functional"] }, async ({ page }) => {
  test.setTimeout(180000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "integrations.http", "http");

  await setTaskField(locators, "Url", HTTP_TASK_URL);
  await setDropdownField(page, locators, "Method", "GET");

  await runTask(page, locators, "Task Runner HTTP request");
  await expectRunSucceeded(locators);

  const output = await readRunOutput(locators);
  expect(output).toContain("status_code");
  expect(output).toContain("200");
  console.log(`integrations.http returned 200 for ${HTTP_TASK_URL}`);
});

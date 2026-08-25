import { test, expect } from "@playwright/test";
import { openTaskRunner, selectRunnerAccount, selectTask, setTaskField, runTask, expectRunSucceeded, readRunOutput } from "./taskRunnerHelper";

// Data category. Pure in-process JSONata evaluation, so the outputs are exact.

test("Task Runner - select account, select Data Transform task, enter json input and jsonata expression, run task, verify the transformed output", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({
  page,
}) => {
  test.setTimeout(150000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "data.transform", "transform");

  await setTaskField(locators, "Input", '{"pods":[{"name":"checkout"},{"name":"payments"}]}');
  await setTaskField(locators, "Expression", "pods.name");
  await runTask(page, locators, "Task Runner Data Transform");
  await expectRunSucceeded(locators);

  const output = await readRunOutput(locators);
  expect(output).toContain("checkout");
  expect(output).toContain("payments");
  console.log("data.transform projected the names out of the input");
});

test("Task Runner - select account, select Data Filter task, enter list and condition, run task, verify only matching items are kept", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(150000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "data.filter", "filter");

  // Plain strings, not objects: the result tree collapses nested objects, so
  // object items would never appear in the read-back whatever the filter did.
  await setTaskField(locators, "List", '["alpha","beta"]');
  await setTaskField(locators, "Condition", '$ = "beta"');
  await runTask(page, locators, "Task Runner Data Filter");
  await expectRunSucceeded(locators);

  // The dropped item must be absent from the result — the assertion that proves
  // the filter ran rather than echoing the list back.
  const output = await readRunOutput(locators);
  expect(output).toContain("beta");
  expect(output).not.toContain("alpha");
  console.log("data.filter kept only the matching item");
});

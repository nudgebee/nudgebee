import { test, expect } from "@playwright/test";
import {
  openTaskRunner,
  selectRunnerAccount,
  selectTask,
  setTaskField,
  readTaskField,
  runTask,
  runTaskAndReadResult,
  readRunOutputValue,
  expectRunFailed,
  RUNNER_ACCOUNT,
} from "./taskRunnerHelper";

const KUBECTL_NAMESPACE = process.env.KUBECTL_NAMESPACE!;
const MISSING_NAMESPACE = "nudgebee-no-such-namespace-e2e";

test("Task Runner - skip account selection, select task(k8s.cli), enter command, run task, verify client-side block with no API call", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@negative", "@validation"] }, async ({ page }) => {
  test.setTimeout(150000);

  const locators = await openTaskRunner(page);

  // Not a disabled test: a tenant with exactly one writable account has it
  // pre-selected, so on those tenants there is no empty state left to exercise.
  await expect(locators.accountSelect).toBeVisible({ timeout: 15000 });
  const selected = (await locators.accountSelect.innerText()).trim();
  test.skip(selected !== "" && selected !== "Select an account", `Account "${selected}" is pre-selected — no empty state to test`);

  await selectTask(page, locators, "k8s.cli", "kubectl");
  await setTaskField(locators, "Command", `kubectl get pods -n ${KUBECTL_NAMESPACE}`);

  await runTask(page, locators, "Task Runner missing account", { expectNetworkCall: false });
  await expectRunFailed(locators, /Select an account to run this task against/i);
  console.log("Missing account surfaced a client-side error instead of an API call");
});

test("Task Runner - select account, select task(k8s.cli), leave required command empty, run task, verify the backend validation error", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@negative", "@validation"] }, async ({ page }) => {
  test.setTimeout(180000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "k8s.cli", "kubectl");

  expect(await readTaskField(locators, "Command")).toEqual("");
  await locators.runTaskBtn.click();

  await expect(locators.runResultHeading).toBeVisible({ timeout: 60000 });
  await expectRunFailed(locators, /command|required|missing/i);
  console.log("Empty required parameter surfaced the task's validation error");
});

test("Task Runner - select account, select a task (core.switch) without individual-run support, verify Run Task is disabled with the reason", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@validation"] }, async ({ page }) => {
  test.setTimeout(150000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "core.switch", "switch");

  await expect(locators.runTaskBtn).toBeVisible();
  await expect(locators.runTaskBtn).toBeDisabled();
  await expect(locators.runnerBox).toContainText("Individual task execution is not supported for this task type");
  console.log("core.switch correctly refuses standalone execution");
});

test("Task Runner - select account, select task, enter k8s command for a missing namespace, run task, verify a readable failure", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@negative"] }, async ({ page }) => {
  test.setTimeout(180000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  expect(RUNNER_ACCOUNT).not.toEqual("");

  await selectTask(page, locators, "k8s.cli", "kubectl");
  await setTaskField(locators, "Command", `kubectl get pods -n ${MISSING_NAMESPACE}`);

  // This run is meant to fail, so it skips the GraphQL watcher — the watcher
  // treats a data-level failure as a test failure and fires a Slack alert.
  const result = await runTaskAndReadResult(locators);
  // kubectl exits 0 on an unknown namespace and reports through stderr, so
  // COMPLETED|FAILED would have accepted either state and asserted nothing.
  expect(result.status).toEqual("COMPLETED");
  expect(result.output).toMatch(/No resources found/i);
  expect(result.output).toContain(MISSING_NAMESPACE);
  // Empty stdout is what proves no pods leaked in from a namespace that exists.
  expect(await readRunOutputValue(locators, "data")).toEqual("");
  console.log("Missing namespace returned empty stdout with a readable stderr message");
});

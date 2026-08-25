import { test, expect } from "@playwright/test";
import { openTaskRunner, selectRunnerAccount, selectTask, setTaskField, runTask, expectRunSucceeded, readRunOutput } from "./taskRunnerHelper";

// Kubernetes category. Only k8s.cli with read-only kubectl; the mutating tasks
// would change a shared dev cluster and are excluded on purpose.

const KUBECTL_NAMESPACE = process.env.KUBECTL_NAMESPACE!;

test("Task Runner - select account, select Kubernetes CLI task, enter kubectl command, run task, validate triggerTask API, verify output", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@smoke", "@functional"] }, async ({
  page,
}) => {
  test.setTimeout(180000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "k8s.cli", "kubectl");

  await setTaskField(locators, "Command", `kubectl get pods -n ${KUBECTL_NAMESPACE}`);
  await runTask(page, locators, "Task Runner Kubernetes CLI");
  await expectRunSucceeded(locators);

  const output = await readRunOutput(locators);
  expect(output).toMatch(/NAME|No resources found/i);
  console.log("kubectl output rendered in the Run Task Result section");
});

test("Task Runner - run task, edit command, re-run task, verify the second result replaces the first", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(180000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "k8s.cli", "kubectl");

  await setTaskField(locators, "Command", `kubectl get pods -n ${KUBECTL_NAMESPACE}`);
  await runTask(page, locators, "Task Runner rerun - first run");
  await expectRunSucceeded(locators);

  // A second run with a different command must produce a fresh result rather
  // than leaving the first one on screen.
  await setTaskField(locators, "Command", `kubectl get pods -n ${KUBECTL_NAMESPACE} -o wide`);
  await runTask(page, locators, "Task Runner rerun - second run");
  await expectRunSucceeded(locators);

  const output = await readRunOutput(locators);
  expect(output).toMatch(/NAME|No resources found/i);
  await expect(locators.runResultHeading).toHaveCount(1);
  console.log("Second run replaced the first result");
});

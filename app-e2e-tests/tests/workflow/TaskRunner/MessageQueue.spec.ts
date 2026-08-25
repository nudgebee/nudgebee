import { test, expect } from "@playwright/test";
import {
  openTaskRunner,
  selectRunnerAccount,
  selectTask,
  setTaskField,
  listDropdownOptions,
  selectDropdownOptionAt,
  runTaskAndReadResult,
  readRunOutputValue,
} from "./taskRunnerHelper";

// Message Queue category. Command runs as a shell script, so it needs the binary
// name — bare arguments complete with a shell error in `data` instead of failing.
const RABBITMQ_COMMAND = "rabbitmqadmin list queues";

test("Task Runner - select account, select RabbitMQ admin task, pick integration, enter command, run task, verify queue output", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(300000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "mq.rabbitmqadmin.cli", "rabbitmq");

  // Some entries the picker offers are not resolvable at execution time
  // ("integration not found"), so each is tried until one actually runs.
  const integrations = await listDropdownOptions(page, locators, "Integration Id");
  expect(integrations.length).toBeGreaterThan(0);
  console.log(`Integration Id offers ${integrations.length}: ${integrations.join(", ")}`);

  const failures: string[] = [];
  let ranOn = "";

  for (let index = 0; index < integrations.length; index++) {
    const picked = await selectDropdownOptionAt(page, locators, "Integration Id", index);
    await setTaskField(locators, "Command", RABBITMQ_COMMAND);

    const result = await runTaskAndReadResult(locators);
    if (result.status === "COMPLETED" || result.status === "SUCCESS") {
      const listing = await readRunOutputValue(locators, "data");
      expect(listing).toMatch(/\|\s*name\s*\|\s*messages\s*\|/);
      expect(listing).not.toMatch(/not found|command not found/i);
      ranOn = picked;
      break;
    }

    const reason = result.output.split("\n").find((line: string) => /not found|error|fail|denied|unreachable/i.test(line)) ?? result.status;
    failures.push(`${picked} → ${reason.trim()}`);
    console.log(`Integration "${picked}" did not run the task (${reason.trim()}) — trying the next one`);
  }

  if (!ranOn) {
    throw new Error(`No RabbitMQ integration could run the task. Tried ${integrations.length}: ${failures.join(" | ")}`);
  }
  console.log(`mq.rabbitmqadmin.cli listed queues through integration "${ranOn}"`);
});

import { test, expect } from "@playwright/test";
import { openTaskRunner, selectRunnerAccount, selectTask, setTaskField, runTask, expectRunSucceeded, readRunOutput } from "./taskRunnerHelper";

// Networking category — all seven tasks, read-only probes against a public host.
// NETWORK_PROBE_HOST overrides the default when a runner cannot reach it.
const PROBE_HOST = process.env.NETWORK_PROBE_HOST || "google.com";

test("Task Runner - select account, select Network DNS task, enter domain, run task, verify the resolved A record", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(150000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "network.dns", "dns");

  await setTaskField(locators, "Domain", PROBE_HOST);
  await runTask(page, locators, "Task Runner Network DNS");
  await expectRunSucceeded(locators);

  const output = await readRunOutput(locators);
  expect(output).toContain(PROBE_HOST);
  expect(output).toMatch(/\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}/);
  console.log(`network.dns resolved ${PROBE_HOST} to an A record`);
});

test("Task Runner - select account, select Network TCP task, enter host and port, run task, verify the port is reachable", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(150000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "network.tcp", "tcp");

  await setTaskField(locators, "Host", PROBE_HOST);
  await setTaskField(locators, "Port", "443");
  await runTask(page, locators, "Task Runner Network TCP");
  await expectRunSucceeded(locators);

  expect(await readRunOutput(locators)).toMatch(/reachable|true|connect/i);
  console.log(`network.tcp reached ${PROBE_HOST}:443`);
});

test("Task Runner - select account, select Network SSL task, enter host, run task, verify the certificate details", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(150000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "network.ssl", "ssl");

  await setTaskField(locators, "Host", PROBE_HOST);
  await runTask(page, locators, "Task Runner Network SSL");
  await expectRunSucceeded(locators);

  const output = await readRunOutput(locators);
  expect(output).toContain("issuer");
  expect(output).toContain("not_after");
  console.log("network.ssl returned the certificate issuer and expiry");
});

test("Task Runner - select account, select Network Whois task, enter domain, run task, verify the registry record", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(150000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "network.whois", "whois");

  await setTaskField(locators, "Domain", PROBE_HOST);
  await runTask(page, locators, "Task Runner Network Whois");
  await expectRunSucceeded(locators);

  expect(await readRunOutput(locators)).toMatch(new RegExp(PROBE_HOST, "i"));
  console.log(`network.whois returned the registry record for ${PROBE_HOST}`);
});

test("Task Runner - select account, select Network NTP task, run task with default server, verify the clock offset", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(150000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "network.ntp", "ntp");

  // Every parameter is optional (host defaults to pool.ntp.org), so this also
  // covers running a task with nothing filled in at all.
  await runTask(page, locators, "Task Runner Network NTP");
  await expectRunSucceeded(locators);

  expect(await readRunOutput(locators)).toMatch(/server|offset|time/i);
  console.log("network.ntp returned a clock reading from the default server");
});

test("Task Runner - select account, select Network Ping task, enter host, run task, verify the reachability report", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(150000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "network.ping", "ping");

  await setTaskField(locators, "Host", PROBE_HOST);
  await runTask(page, locators, "Task Runner Network Ping");
  await expectRunSucceeded(locators);

  // The task returns raw ping stdout rather than the parsed schema fields.
  expect(await readRunOutput(locators)).toMatch(/packets transmitted|bytes from|rtt/i);
  console.log(`network.ping reported reachability for ${PROBE_HOST}`);
});

test("Task Runner - select account, select Network Traceroute task, enter host, run task, verify the hop list", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(180000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "network.traceroute", "traceroute");

  await setTaskField(locators, "Host", PROBE_HOST);
  await runTask(page, locators, "Task Runner Network Traceroute", { timeoutMs: 150000 });
  await expectRunSucceeded(locators);

  expect(await readRunOutput(locators)).toMatch(/hops|raw/i);
  console.log(`network.traceroute returned a hop list for ${PROBE_HOST}`);
});

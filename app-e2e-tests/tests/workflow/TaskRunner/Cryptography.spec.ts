import { test, expect } from "@playwright/test";
import {
  openTaskRunner,
  selectRunnerAccount,
  selectTask,
  setTaskField,
  setDropdownField,
  runTask,
  runTaskAndReadResult,
  expectRunSucceeded,
  readRunOutputValue,
} from "./taskRunnerHelper";
import {
  inputs,
  expectedHash,
  expectedEncode,
  expectedCiphertextByteLength,
  keyFor,
  knownCiphertext,
  assertInputsAreUsable,
} from "./fixtures/cryptoExpected";

// Cryptography category. Inputs come from fixtures/cryptoInputs.json and every
// expected value is computed from them in fixtures/cryptoExpected.ts.

const { negative, statusPatterns } = inputs;
const FAILED_STATUS = new RegExp(statusPatterns.failed);

// The three ways crypto.encrypt accepts a key: hashed from text, or handed over
// pre-decoded as 32 raw bytes in either encoding.
const KEY_ENCODINGS: ReadonlyArray<string | undefined> = [undefined, ...inputs.encodeAlgorithms];

test.beforeAll(() => {
  assertInputsAreUsable();
});

for (const algorithm of inputs.hashAlgorithms) {
  test(`Task Runner - select account, select Crypto Hash task, enter data, pick ${algorithm} algorithm, run task, verify the digest`, { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
    test.setTimeout(150000);

    const locators = await openTaskRunner(page);
    await selectRunnerAccount(page, locators);
    await selectTask(page, locators, "crypto.hash", "hash");

    await setTaskField(locators, "Data", inputs.plaintext);
    await setDropdownField(page, locators, "Algorithm", algorithm);
    await runTask(page, locators, `Task Runner Crypto Hash ${algorithm}`);
    await expectRunSucceeded(locators);

    expect(await readRunOutputValue(locators, "data")).toEqual(expectedHash(algorithm));
    console.log(`crypto.hash returned the expected ${algorithm} digest`);
  });
}

for (const algorithm of inputs.encodeAlgorithms) {
  test(`Task Runner - select account, select Crypto Encode task, enter data, pick ${algorithm} algorithm, run task, verify the encoded value`, { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
    test.setTimeout(150000);

    const locators = await openTaskRunner(page);
    await selectRunnerAccount(page, locators);
    await selectTask(page, locators, "crypto.encode", "encode");

    await setTaskField(locators, "Data", inputs.plaintext);
    await setDropdownField(page, locators, "Algorithm", algorithm);
    await runTask(page, locators, `Task Runner Crypto Encode ${algorithm}`);
    await expectRunSucceeded(locators);

    expect(await readRunOutputValue(locators, "data")).toEqual(expectedEncode(algorithm));
    console.log(`crypto.encode returned the expected ${algorithm} value`);
  });

  test(`Task Runner - select account, select Crypto Decode task, enter ${algorithm} data, pick ${algorithm} algorithm, run task, verify the decoded value`, { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
    test.setTimeout(150000);

    const locators = await openTaskRunner(page);
    await selectRunnerAccount(page, locators);
    await selectTask(page, locators, "crypto.decode", "decode");

    await setTaskField(locators, "Data", expectedEncode(algorithm));
    await setDropdownField(page, locators, "Algorithm", algorithm);
    await runTask(page, locators, `Task Runner Crypto Decode ${algorithm}`);
    await expectRunSucceeded(locators);

    expect(await readRunOutputValue(locators, "data")).toEqual(inputs.plaintext);
    console.log(`crypto.decode returned the original value from ${algorithm}`);
  });
}

test("Task Runner - select account, run Crypto Encode on a unicode string, feed the output to Crypto Decode, verify the original string comes back", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(240000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);

  await selectTask(page, locators, "crypto.encode", "encode");
  await setTaskField(locators, "Data", inputs.unicodePlaintext);
  await setDropdownField(page, locators, "Algorithm", "base64");
  await runTask(page, locators, "Task Runner Crypto Encode unicode");
  await expectRunSucceeded(locators);

  const encoded = await readRunOutputValue(locators, "data");
  expect(encoded).toEqual(expectedEncode("base64", inputs.unicodePlaintext));

  await selectTask(page, locators, "crypto.decode", "decode");
  await setTaskField(locators, "Data", encoded);
  await setDropdownField(page, locators, "Algorithm", "base64");
  await runTask(page, locators, "Task Runner Crypto Decode unicode");
  await expectRunSucceeded(locators);

  expect(await readRunOutputValue(locators, "data")).toEqual(inputs.unicodePlaintext);
  console.log("crypto.encode -> crypto.decode round-tripped a unicode payload unchanged");
});

test("Task Runner - select account, select Crypto Decode task, enter data that is not valid base64, run task, verify the decode error", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@negative"] }, async ({ page }) => {
  test.setTimeout(150000);

  const testCase = negative.decodeMalformedBase64;
  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "crypto.decode", "decode");

  await setTaskField(locators, "Data", testCase.data!);
  await setDropdownField(page, locators, "Algorithm", testCase.algorithm!);

  // Meant to fail, so it skips the GraphQL watcher — the watcher treats a
  // data-level failure as a test failure and fires a Slack alert.
  const result = await runTaskAndReadResult(locators);
  expect(result.status).toMatch(FAILED_STATUS);
  expect(result.output).toMatch(new RegExp(testCase.errorPattern, "i"));
  console.log("crypto.decode rejected malformed base64 with a readable error");
});

test("Task Runner - select account, select Crypto Encrypt task, enter data and key, run task, verify a ciphertext is produced", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@smoke", "@functional"] }, async ({ page }) => {
  test.setTimeout(150000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "crypto.encrypt", "encrypt");

  await setTaskField(locators, "Data", inputs.plaintext);
  await setTaskField(locators, "Key", keyFor());
  await runTask(page, locators, "Task Runner Crypto Encrypt");
  await expectRunSucceeded(locators);

  // Checked on the emitted `data` field, not the panel: the scaffolding alone
  // clears any length bar, so a panel check would pass on an empty ciphertext.
  const ciphertext = await readRunOutputValue(locators, "data");
  expect(ciphertext).not.toContain(inputs.plaintext);
  expect(Buffer.from(ciphertext, "base64").length).toEqual(expectedCiphertextByteLength());
  console.log("crypto.encrypt produced a ciphertext of the expected length");
});

for (const encoding of KEY_ENCODINGS) {
  const label = encoding ?? "text";

  test(`Task Runner - select account, run Crypto Encrypt with a ${label} key, feed the ciphertext to Crypto Decrypt, verify the plaintext comes back`, { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
    test.setTimeout(240000);

    const key = keyFor(encoding);
    const locators = await openTaskRunner(page);
    await selectRunnerAccount(page, locators);

    await selectTask(page, locators, "crypto.encrypt", "encrypt");
    await setTaskField(locators, "Data", inputs.plaintext);
    await setTaskField(locators, "Key", key);
    if (encoding) {
      await setDropdownField(page, locators, "Key encoding", encoding);
    }
    await runTask(page, locators, `Task Runner Crypto Encrypt ${label} key`);
    await expectRunSucceeded(locators);

    const ciphertext = await readRunOutputValue(locators, "data");
    expect(ciphertext).not.toEqual(inputs.plaintext);

    await selectTask(page, locators, "crypto.decrypt", "decrypt");
    await setTaskField(locators, "Data", ciphertext);
    await setTaskField(locators, "Key", key);
    if (encoding) {
      await setDropdownField(page, locators, "Key encoding", encoding);
    }
    await runTask(page, locators, `Task Runner Crypto Decrypt ${label} key`);
    await expectRunSucceeded(locators);

    expect(await readRunOutputValue(locators, "data")).toEqual(inputs.plaintext);
    console.log(`crypto.encrypt -> crypto.decrypt round-tripped the plaintext with a ${label} key`);
  });
}

test("Task Runner - select account, select Crypto Decrypt task, enter a ciphertext produced outside the product, run task, verify the plaintext", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(150000);

  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "crypto.decrypt", "decrypt");

  await setTaskField(locators, "Data", knownCiphertext());
  await setTaskField(locators, "Key", keyFor());
  await runTask(page, locators, "Task Runner Crypto Decrypt known vector");
  await expectRunSucceeded(locators);

  expect(await readRunOutputValue(locators, "data")).toEqual(inputs.plaintext);
  console.log("crypto.decrypt matched a ciphertext produced by an independent implementation");
});

test("Task Runner - select account, select Crypto Decrypt task, enter a valid ciphertext with the wrong key, run task, verify the run fails", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@negative"] }, async ({ page }) => {
  test.setTimeout(150000);

  const testCase = negative.decryptWrongKey;
  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "crypto.decrypt", "decrypt");

  await setTaskField(locators, "Data", knownCiphertext());
  await setTaskField(locators, "Key", testCase.key!);

  const result = await runTaskAndReadResult(locators);
  expect(result.status).toMatch(FAILED_STATUS);
  expect(result.output).toMatch(new RegExp(testCase.errorPattern, "i"));
  expect(result.output).not.toContain(inputs.plaintext);
  console.log("crypto.decrypt refused the wrong key instead of returning garbage");
});

test("Task Runner - select account, select Crypto Decrypt task, enter a ciphertext shorter than the nonce, run task, verify the run fails", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@negative"] }, async ({ page }) => {
  test.setTimeout(150000);

  const testCase = negative.decryptTruncatedCiphertext;
  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "crypto.decrypt", "decrypt");

  await setTaskField(locators, "Data", testCase.data!);
  await setTaskField(locators, "Key", keyFor());

  const result = await runTaskAndReadResult(locators);
  expect(result.status).toMatch(FAILED_STATUS);
  expect(result.output).toMatch(new RegExp(testCase.errorPattern, "i"));
  console.log("crypto.decrypt rejected a truncated ciphertext with a readable error");
});

test("Task Runner - select account, select Crypto Encrypt task, enter a base64 key of the wrong length, run task, verify the key length error", { tag: ["@dev", "@test", "@oss", "@task-runner", "@automation", "@regression", "@negative", "@validation"] }, async ({ page }) => {
  test.setTimeout(150000);

  const testCase = negative.encryptUndersizedKey;
  const locators = await openTaskRunner(page);
  await selectRunnerAccount(page, locators);
  await selectTask(page, locators, "crypto.encrypt", "encrypt");

  await setTaskField(locators, "Data", inputs.plaintext);
  // Decodes to 3 bytes; only the text encoding hashes a key up to 32 bytes.
  await setTaskField(locators, "Key", testCase.key!);
  await setDropdownField(page, locators, "Key encoding", testCase.keyEncoding!);

  const result = await runTaskAndReadResult(locators);
  expect(result.status).toMatch(FAILED_STATUS);
  expect(result.output).toMatch(new RegExp(testCase.errorPattern, "i"));
  console.log("crypto.encrypt rejected an undersized key instead of encrypting weakly");
});

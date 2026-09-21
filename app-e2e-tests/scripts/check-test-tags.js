#!/usr/bin/env node
// Fails if this diff adds a new test() with no category tag + env tag.
// Deliberately does NOT check pre-existing untagged tests — see TAGGING.md
// for the tracked, batched retrofit of those. This only gates what's new.

const { execSync } = require("child_process");

const CATEGORY_TAGS = ["@smoke", "@sanity", "@functional", "@regression", "@negative"];
const ENV_TAGS = ["@dev", "@test", "@oss"];

// quiet: true pipes stderr to nothing instead of inheriting it — used for
// calls where a non-zero exit is an expected, handled case (e.g. "file did
// not exist at this ref"), so the underlying git error never reaches the
// terminal. A shell redirect (`2>/dev/null`) would do this too but only on
// POSIX; this way works the same on the Linux CI runner and on a
// contributor's Windows machine running this locally.
function sh(cmd, { quiet = false } = {}) {
  return execSync(cmd, {
    encoding: "utf8",
    maxBuffer: 1024 * 1024 * 100,
    stdio: ["pipe", "pipe", quiet ? "ignore" : "pipe"],
  }).trim();
}

// Mirrors the base-ref resolution already used by app-e2e-tests-dev.yaml's
// "Detect Affected Tests" step, so this agrees with what CI considers "changed".
function resolveBase() {
  // CI already resolves this once in the "Detect Affected Tests" step
  // (same base this run treats as "changed") — reuse it instead of
  // re-deriving a possibly-different answer.
  if (process.env.E2E_BASE) return process.env.E2E_BASE;

  const eventName = process.env.GITHUB_EVENT_NAME || "";
  let base = "";
  if (eventName === "pull_request") {
    base = process.env.PR_BASE_SHA || "";
  } else if (eventName === "push") {
    base = process.env.PUSH_BEFORE_SHA || "";
  }

  // Null SHA on branch creation; after a force push the base may be
  // unreachable even though it's a well-formed, non-empty SHA — so this
  // must actually probe the ref with git, not just eyeball its shape.
  const looksEmpty = !base || base === "0".repeat(40);
  // No `^{commit}` peel — a SHA from a GitHub webhook payload is already a
  // commit by construction, and `^` is cmd.exe's own escape character, which
  // silently breaks this exact call when run locally on Windows.
  const isReachable = !looksEmpty && (() => {
    try {
      sh(`git cat-file -e ${base}`, { quiet: true });
      return true;
    } catch {
      return false;
    }
  })();

  if (!isReachable) {
    try {
      base = sh("git rev-parse HEAD~1");
    } catch {
      base = "";
    }
  }
  return base;
}

// Extracts { title -> tagsArrayContent|null } for every top-level test(...)
// call in a file's source (test.describe blocks are transparent — this scans
// every line regardless of nesting, since only individual test() calls are
// what actually need tags).
function extractTests(content) {
  const lines = content.split("\n");
  const tests = new Map();
  for (let i = 0; i < lines.length; i++) {
    // A test() / test.only() / test.skip() call — NOT test.describe(), which
    // is a container, not a test itself, and never takes a tag directly.
    const opener = lines[i].match(/^\s*test(\.(only|skip))?\(\s*(.*)$/);
    if (!opener) continue;

    // A real declaration's first argument is always the title string. This
    // codebase also uses test.skip(condition, "reason") *inline*, mid-test,
    // to conditionally bail out (e.g. `test.skip(!onLoki, "...")`) — that
    // call has the identical `test.skip(` prefix but a boolean expression
    // first, never a string, so it must not be mistaken for a declaration.
    const restOfLine = opener[3].trimStart();
    const firstArgIsString = /^["'`]/.test(restOfLine);
    if (!firstArgIsString) {
      if (restOfLine !== "") continue; // non-quote content right after "(" — not a title
      const nextLine = (lines[i + 1] || "").trim();
      if (!/^["'`]/.test(nextLine)) continue; // next line isn't a title either
    }

    // Title and options object can be on the same line or spread across the
    // next several — this repo's own style uses both, so scan a window.
    // Capped at the next test declaration so a tagless test immediately
    // followed by a tagged one can't absorb that next test's tags.
    const nextTestIndex = lines.slice(i + 1, i + 10).findIndex((line) => /^\s*test(\.(only|skip))?\(/.test(line));
    const limit = nextTestIndex !== -1 ? nextTestIndex + 1 : 10;
    const window = lines.slice(i, i + limit).join("\n");
    // Backreference to the SAME quote char on both ends — a plain character
    // class matches any quote at either end, so a title with a straight quote
    // of a different kind inside it (e.g. an apostrophe in a "double-quoted"
    // title) truncated the match at that inner quote instead of the real end.
    const titleMatch = window.match(/(["'`])(.{5,300}?)\1/);
    if (!titleMatch) continue; // not a real test call (e.g. a comment)
    const title = titleMatch[2];

    const tagMatch = window.match(/tag:\s*\[([^\]]*)\]/);
    tests.set(title, tagMatch ? tagMatch[1] : null);
  }
  return tests;
}

function hasRequiredTags(tagsArrayContent) {
  if (!tagsArrayContent) return false;
  const hasCategory = CATEGORY_TAGS.some((t) => tagsArrayContent.includes(t));
  const hasEnv = ENV_TAGS.some((t) => tagsArrayContent.includes(t));
  return hasCategory && hasEnv;
}

function main() {
  const base = resolveBase();
  if (!base) {
    console.log("check-test-tags: could not resolve a diff base — skipping (nothing to compare against).");
    return;
  }

  // Filtering to spec files is done here in JS, not via a git pathspec glob
  // (`-- "**/*.spec.ts"`) — execSync's default shell is cmd.exe on Windows
  // and /bin/sh in CI, and the two expand ** differently, so the same
  // pathspec silently matched nothing on Windows. Plain string filtering
  // behaves identically on every platform.
  let changedFiles;
  try {
    changedFiles = sh(`git diff --name-only --diff-filter=ACM "${base}...HEAD"`)
      .split("\n")
      .filter((f) => f.startsWith("app-e2e-tests/tests/") && f.endsWith(".spec.ts"));
  } catch (e) {
    console.log("check-test-tags: could not diff against base (" + e.message + ") — skipping.");
    return;
  }

  if (changedFiles.length === 0) {
    console.log("check-test-tags: no changed spec files.");
    return;
  }

  const violations = [];

  for (const file of changedFiles) {
    let baseContent = "";
    try {
      baseContent = sh(`git show ${base}:${file}`, { quiet: true });
    } catch {
      baseContent = ""; // file is new — every test in it is "new"
    }
    let currentContent = "";
    try {
      currentContent = sh(`git show HEAD:${file}`, { quiet: true });
    } catch {
      continue; // file was deleted — nothing to check
    }

    const baseTests = extractTests(baseContent);
    const currentTests = extractTests(currentContent);

    for (const [title, tagsArrayContent] of currentTests) {
      const isNew = !baseTests.has(title);
      if (isNew && !hasRequiredTags(tagsArrayContent)) {
        violations.push(`${file} :: "${title}"`);
      }
    }
  }

  if (violations.length > 0) {
    console.error(`\n${violations.length} new test(s) missing a tag (need at least one of ${CATEGORY_TAGS.join("/")} AND one of ${ENV_TAGS.join("/")}):\n`);
    violations.forEach((v) => console.error("  - " + v));
    console.error("\nSee app-e2e-tests/TAGGING.md for the convention and examples.");
    process.exit(1);
  }

  console.log("check-test-tags: all new tests are tagged.");
}

main();

#!/usr/bin/env node
/**
 * e2e-impact — which Playwright tests a change to `app/` puts at risk.
 *
 * The suite binds to this app through selector strings that live in `app/`
 * source, not in this repo, and nothing in `app/`'s own validation
 * (`lint2` / `type-check` / `npm test`) can see that contract. This closes the
 * mechanical half of the gap.
 *
 * How it works, in one line: whatever string the tests need, the app has to
 * still provide. So it reads every selector literal the suite depends on, reads
 * the strings each changed `app/` file provided BEFORE and AFTER the diff, and
 * reports the intersection of "the tests need this" and "this diff removed it".
 *
 * Two things keep it quiet. Comparing against the pre-diff version means a
 * selector the app builds at runtime (`toKebabCase(field.display_name)`) never
 * appears as a literal in either version, so it is never flagged. And a token is
 * only reported once it is gone from ALL of `app/` — a file dropping the string
 * `'duration'` says nothing when the `#duration` the tests want is rendered by a
 * different component entirely.
 *
 * What it CANNOT see, and why the rule around it has a judgement step: a
 * condition placed around an element that still exists. `{n > 8 && <Search/>}`
 * removes nothing — every string is still right there in the file — and it
 * breaks the suite just as hard. See "Judgement pass" in app/CLAUDE.md.
 *
 *   node scripts/e2e-impact.js [baseRef]     # default base: enterprise/main
 *
 * Exit 0 = nothing to do. Exit 1 = at least one required selector was dropped.
 */
'use strict';

const { execSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const REPO = execSync('git rev-parse --show-toplevel', { encoding: 'utf8' }).trim();
const E2E = path.join(REPO, 'app-e2e-tests');
// `enterprise/main` is the live main; local `main` tracks the OSS mirror and is
// tens of thousands of commits behind, which would diff the whole repo.
const BASE = process.argv[2] || process.env.E2E_IMPACT_BASE || 'enterprise/main';

/**
 * Where a change lands, and which specs cover it. `app/src/components/common/ds`
 * is deliberately mapped to everything: a DS primitive is rendered by every page,
 * so a change there is never local to one area.
 */
const ALL = '*';
const AREA_MAP = [
  ['app/src/components/common/ds', [ALL]],
  ['app/src/components/accounts', ['tests/admin/Integrations']],
  ['app/src/components/integrations', ['tests/admin/Integrations']],
  ['app/src/components/user-management', ['tests/admin']],
  ['app/src/components/notifications', ['tests/admin/Notifications']],
  ['app/src/components/cloudaccount', ['tests/CloudAccount']],
  ['app/src/components/k8s', ['tests/ClusterDetails']],
  ['app/src/components/optimise-new', ['tests/Optimize', 'tests/ClusterDetails/Optimize']],
  ['app/src/components/recommendations', ['tests/Optimize', 'tests/ClusterDetails/Optimize']],
  ['app/src/components/troubleshoot', ['tests/Troubleshoot', 'tests/ClusterDetails/Troubleshoot']],
  ['app/src/components/events', ['tests/Troubleshoot']],
  ['app/src/components/knowledge-graph', ['tests/Troubleshoot']],
  ['app/src/components/workflow', ['tests/workflow']],
  ['app/src/components/llm', ['tests/nubi']],
  ['app/src/components/helpbee', ['tests/nubi']],
  ['app/src/components/auth', ['tests/loginPage']],
  ['app/src/components/common/navigation', [ALL]],
  ['app/src/components/common/layout', [ALL]],
];

// ---------------------------------------------------------------- e2e contract

/**
 * Every selector string the suite needs, mapped to the places that need it.
 *
 * Only static literals are collected. A locator built from a regex or a template
 * literal cannot be matched against source text, so those are counted and
 * reported as a coverage gap rather than guessed at.
 */
function readE2EContract() {
  const required = new Map(); // token -> [{file, line}]
  let dynamic = 0;

  const add = (token, file, line) => {
    const key = token.trim();
    if (!key || key.length < 2) return;
    if (!required.has(key)) required.set(key, []);
    required.get(key).push({ file, line });
  };

  // `(?:(?!\1)[^\\\n]|\\.)*` rather than a lazy `.*?`: a lazy match stops at the
  // first quote even when it is escaped, so `getByText('Don\'t click')` would
  // yield the token `Don\`. Same shape as QUOTED in providedTokens, so the two
  // sides of the comparison tokenise identically.
  const STR = "(['\"])((?:(?!\\1)[^\\\\\\n]|\\\\.)*)\\1";
  const PATTERNS = [
    // Playwright's by-* locators.
    new RegExp(`\\bgetBy(?:TestId|Placeholder|Text|AltText|Title|Label)\\(\\s*${STR}`, 'g'),
    // getByRole('button', { name: 'Save' }) — the accessible name is the contract.
    new RegExp(`\\bname:\\s*${STR}`, 'g'),
    // .filter({ hasText: 'Save' })
    new RegExp(`\\bhasText:\\s*${STR}`, 'g'),
  ];
  // CSS selectors: pull the id / data-testid out rather than keeping the whole string.
  const CSS = new RegExp(`\\blocator\\(\\s*${STR}`, 'g');
  const CSS_ID = /#([A-Za-z][\w-]*)/g;
  const CSS_TESTID = /\[data-testid=(['"]?)([^\]'"]+)\1\]/g;
  // A locator whose selector is interpolated or a regex can't be matched statically.
  const DYNAMIC = /\b(?:locator|getBy\w+)\(\s*(?:`[^`]*\$\{|\/)/g;

  for (const file of walk(path.join(E2E, 'tests')).concat(walk(path.join(E2E, 'pages')))) {
    if (!file.endsWith('.ts')) continue;
    const rel = path.relative(REPO, file);
    const lines = fs.readFileSync(file, 'utf8').split('\n');

    lines.forEach((text, i) => {
      const lineNo = i + 1;
      for (const re of PATTERNS) {
        re.lastIndex = 0;
        let m;
        while ((m = re.exec(text))) add(m[2], rel, lineNo);
      }
      CSS.lastIndex = 0;
      let c;
      while ((c = CSS.exec(text))) {
        const sel = c[2];
        let id;
        CSS_ID.lastIndex = 0;
        while ((id = CSS_ID.exec(sel))) add(id[1], rel, lineNo);
        let tid;
        CSS_TESTID.lastIndex = 0;
        while ((tid = CSS_TESTID.exec(sel))) add(tid[2], rel, lineNo);
      }
      DYNAMIC.lastIndex = 0;
      dynamic += (text.match(DYNAMIC) || []).length;
    });
  }
  return { required, dynamic };
}

function walk(dir) {
  if (!fs.existsSync(dir)) return [];
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(dir, entry.name);
    if (entry.name === 'node_modules') return [];
    return entry.isDirectory() ? walk(full) : [full];
  });
}

// ------------------------------------------------------------ what app provides

/**
 * Every string a source file hands to the DOM: quoted literals (ids, testids,
 * placeholders, aria-labels, alts, titles, labels) plus JSX text nodes, which are
 * what `getByText` and an accessible name actually match on.
 */
function providedTokens(source) {
  const out = new Set();
  if (!source) return out;

  const QUOTED = /(['"])((?:(?!\1)[^\\\n]|\\.)*)\1/g;
  let m;
  while ((m = QUOTED.exec(source))) {
    const value = m[2].trim();
    if (value.length >= 2) out.add(value);
  }
  // JSX text: >Save< . Skip anything holding an expression or a tag.
  const JSX_TEXT = />([^<>{}]+)</g;
  while ((m = JSX_TEXT.exec(source))) {
    const value = m[1].replace(/\s+/g, ' ').trim();
    if (value.length >= 2 && /[A-Za-z]/.test(value)) out.add(value);
  }
  return out;
}

function showAt(ref, file) {
  try {
    return execSync(`git show ${ref}:${file}`, { cwd: REPO, encoding: 'utf8', stdio: ['pipe', 'pipe', 'ignore'] });
  } catch {
    return ''; // added file — nothing existed to remove
  }
}

// ------------------------------------------------------------------------ main

function main() {
  let range;
  try {
    const mergeBase = execSync(`git merge-base ${BASE} HEAD`, { cwd: REPO, encoding: 'utf8' }).trim();
    range = mergeBase;
  } catch {
    console.error(`e2e-impact: cannot resolve base ref "${BASE}". Pass one: node scripts/e2e-impact.js <ref>`);
    process.exit(2);
  }

  // Against the working tree, not HEAD: this runs BEFORE you commit, so uncommitted
  // edits are exactly the ones worth checking. It also matches how `stillProvided`
  // below reads `app/` — both sides see the same state of the repo.
  const changed = execSync(`git diff --name-only ${range} -- app/src app/pages`, { cwd: REPO, encoding: 'utf8' })
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean);

  if (!changed.length) {
    console.log('e2e-impact: no changes under app/ — nothing to check.');
    return;
  }

  const { required, dynamic } = readE2EContract();
  const findings = [];

  // What all of `app/` still provides. A token the diff removed from one file but
  // that another component still renders is not a broken contract — without this
  // every generic literal ('duration', 'Save') reads as a false break.
  const stillProvided = new Set();
  for (const file of walk(path.join(REPO, 'app', 'src')).concat(walk(path.join(REPO, 'app', 'pages')))) {
    if (!/\.(tsx?|jsx?)$/.test(file)) continue;
    for (const token of providedTokens(fs.readFileSync(file, 'utf8'))) stillProvided.add(token);
  }

  for (const file of changed) {
    const before = providedTokens(showAt(range, file));
    const onDisk = path.join(REPO, file);
    const after = providedTokens(fs.existsSync(onDisk) ? fs.readFileSync(onDisk, 'utf8') : '');
    for (const token of before) {
      if (after.has(token) || stillProvided.has(token)) continue;
      const users = required.get(token);
      if (users) findings.push({ token, file, users });
    }
  }

  console.log(`e2e-impact: ${changed.length} changed file(s) under app/, base ${BASE}`);
  console.log(`            ${required.size} static selector(s) required by the suite; ${dynamic} dynamic locator(s) not statically checkable\n`);

  const areas = new Set();
  for (const file of changed) {
    for (const [prefix, specs] of AREA_MAP) {
      if (file.startsWith(prefix)) specs.forEach((s) => areas.add(s));
    }
  }
  if (areas.has(ALL)) {
    console.log('This diff touches a shared DS primitive or the global nav — every page renders it,');
    console.log('so the blast radius is the whole suite, not one area:');
    console.log('  cd app-e2e-tests && npm run test:dev');
    areas.delete(ALL);
  }
  if (areas.size) {
    console.log('Specs covering the changed areas — run these:');
    for (const area of [...areas].sort()) console.log(`  cd app-e2e-tests && npm run test:dev -- ${area}`);
    console.log('');
  } else if (!changed.some((f) => AREA_MAP.some(([p]) => f.startsWith(p)))) {
    console.log('No spec area maps to these files. Say so in the PR body rather than leaving it implied.\n');
  }

  if (!findings.length) {
    console.log('OK — this diff removed no selector the suite depends on.');
    console.log('Still do the judgement pass (app/CLAUDE.md): a condition added around an element');
    console.log('that still exists breaks the suite without removing a single string.');
    return;
  }

  console.log(`BROKEN — ${findings.length} selector(s) the suite needs were removed or reworded:\n`);
  for (const { token, file, users } of findings) {
    console.log(`  "${token}"`);
    console.log(`     dropped by: ${file}`);
    for (const u of users) console.log(`     needed by:  ${u.file}:${u.line}`);
    console.log('');
  }
  console.log('Fix the locators in the SAME PR, then re-run this script.');
  process.exit(1);
}

main();

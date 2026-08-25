#!/usr/bin/env node
/**
 * Use-before-declaration (TDZ) gate.
 *
 * Catches the class of bug where a `const` is read before its declaration is
 * evaluated — most commonly a `useEffect` dependency array (evaluated during
 * render, unlike the effect body) listing a value declared further down the
 * component. That throws `ReferenceError: Cannot access 'x' before
 * initialization` on the first render and takes the page down. See #35255.
 *
 * Why TypeScript rather than a lint rule:
 *   - oxlint 1.59 lists `no-use-before-define` but does not implement it; it
 *     reports nothing even on `export const y = z; const z = 2;`
 *   - ESLint's `no-use-before-define` does detect it, but it is purely lexical,
 *     so it also flags the many safe references inside callbacks — 579 hits
 *     across 176 files in this repo.
 *   - tsc's block-scoped check is control-flow aware: it flags render-time
 *     reads and ignores deferred ones. Zero false positives here.
 *
 * The full `checkJs` pass emits ~3k unrelated errors on untyped .jsx, so this
 * reads only the three TDZ codes out of the output and drops the rest.
 */

const { execFileSync } = require('child_process');
const path = require('path');

// TS2448 block-scoped variable used before declaration
// TS2454 variable used before being assigned
// TS2729 property used before its initialization
const TDZ_CODES = /error TS(2448|2454|2729):/;

const appDir = path.resolve(__dirname, '..');
// Resolve through the package rather than assuming a hoisted node_modules
// layout, so this survives pnpm/yarn symlink trees and being run from a
// different cwd. Anchored on the always-exported main entry, so a future
// tightening of typescript's `exports` map cannot break it.
const tsc = path.join(path.dirname(require.resolve('typescript')), '../bin/tsc');

let output = '';
try {
  // Captured on success too: tsc exits 0 only with no diagnostics, so this is
  // normally empty, but reading it either way means a narrower config later
  // cannot silently skip the scan.
  output = execFileSync(process.execPath, [tsc, '-p', 'tsconfig.tdz.json', '--noEmit', '--pretty', 'false'], {
    cwd: appDir,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
  });
} catch (err) {
  // tsc exits non-zero whenever it reports any diagnostic, which it always
  // will here (checkJs on untyped .jsx). Only a missing stdout means it failed
  // to run at all, which must not be mistaken for a clean pass.
  output = err.stdout || '';
  if (!output) {
    console.error('check-tdz: tsc did not run —', err.message);
    process.exit(1);
  }
}

const hits = output.split('\n').filter((line) => TDZ_CODES.test(line));

if (hits.length > 0) {
  console.error(`check-tdz: ${hits.length} use-before-declaration error(s):\n`);
  hits.forEach((h) => console.error(`  ${h.trim()}`));
  console.error('\nA value is read before its declaration is evaluated. If this is a hook');
  console.error('dependency array, move the hook below the declarations it depends on.');
  process.exit(1);
}

console.log('check-tdz: no use-before-declaration errors.');

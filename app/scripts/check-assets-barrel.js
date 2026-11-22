#!/usr/bin/env node
/**
 * `@assets` barrel gate.
 *
 * `src/assets/images/index.js` is imported by ~200 modules, including the app
 * shell, so whatever it cannot shed ships in every page's first-load bundle.
 * It only tree-shakes while two things hold:
 *
 *   1. every line is a pure re-export — `export * as X from '…'` or
 *      `export { default as X } from '…'`. The file used to be 570 lines of
 *      `export const X = require('…')`; `require()` is opaque to the bundler,
 *      which kept all ~640 icons and images (a 390 KB chunk) on every page.
 *   2. the folder's package.json says `"sideEffects": false`, which is what
 *      lets Turbopack follow a re-export straight to the one asset a page
 *      uses instead of evaluating the whole barrel.
 *
 * Runs in `npm run lint2` because that is the blocking CI step; the jest step
 * is `continue-on-error`.
 */

const fs = require('fs');
const path = require('path');

const dir = path.join(__dirname, '..', 'src', 'assets', 'images');
// Quote style and spacing are Prettier's job (it runs later in lint2), so accept either here.
const RE_EXPORT = /^export (\* as \w+|\{\s*default as \w+\s*\}) from (['"])[^'"]+\2;?$/;
const problems = [];

fs.readFileSync(path.join(dir, 'index.js'), 'utf8')
  .split('\n')
  .forEach((raw, i) => {
    const line = raw.trim();
    if (!line || /^(\/\/|\/\*|\*)/.test(line) || RE_EXPORT.test(line)) return;
    problems.push(`src/assets/images/index.js:${i + 1}  ${line}`);
  });

let sideEffects;
try {
  sideEffects = JSON.parse(fs.readFileSync(path.join(dir, 'package.json'), 'utf8')).sideEffects;
} catch {
  sideEffects = undefined;
}
if (sideEffects !== false) {
  problems.push('src/assets/images/package.json must contain "sideEffects": false');
}

if (problems.length) {
  console.error('check-assets-barrel: the @assets barrel must stay tree-shakable.\n');
  problems.forEach((p) => console.error(`  ${p}`));
  console.error("\nWrite each export as  export * as Name from '@assets/file.svg';  (a `{ default }` module, as require() returned)");
  console.error("or  export { default as Name } from '@assets/file.icon.svg';  (the SVGR component itself).");
  process.exit(1);
}
console.log('check-assets-barrel: barrel is re-exports only and side-effect free.');

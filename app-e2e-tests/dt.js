#!/usr/bin/env node
'use strict';

const { spawnSync } = require('child_process');
const path = require('path');

const args = process.argv.slice(2);

// dt dev [args]  → dev env  → loads .env.dev
// dt test [args] → test env → loads .env
// dt [args]      → dev env  (default)
let envName = 'dev';
let playwrightArgs = args;

if (args[0] === 'dev' || args[0] === 'test') {
  envName = args[0];
  playwrightArgs = args.slice(1);
}

const env = { ...process.env, E2E_ENVIRONMENT: envName };

console.log(`[dt] env=${envName}  args=${playwrightArgs.join(' ') || '(full suite)'}`);

// Call playwright CLI directly via node — avoids npx wrapper issues on Windows
const playwrightCli = path.join(__dirname, 'node_modules', '@playwright', 'test', 'cli.js');

const result = spawnSync(
  process.execPath,
  [playwrightCli, 'test', '--project=chromium', ...playwrightArgs],
  { stdio: 'inherit', cwd: __dirname, env }
);

process.exit(result.status ?? 1);

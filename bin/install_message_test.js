'use strict';

// bin/install_message_test.js — regression guard for issue #80.
//
// Runs install.js with a forced unsupported platform/arch and checks
// the stderr output points at the real scripts/install.ps1 URL and
// tells the operator to `npm uninstall -g` to clean up. Asserts on
// substring presence rather than full text so future copy edits that
// keep the same intent do not break the test.
//
// Run with: node bin/install_message_test.js
// Exits 0 on success, 1 on any mismatch.

const { spawnSync } = require('node:child_process');
const path = require('node:path');

function fail(msg) {
  console.error(`install_message_test: FAIL: ${msg}`);
  process.exit(1);
}

const installJs = path.join(__dirname, 'install.js');
const fakePlatform = 'win32';
const fakeArch = 'ia32'; // not in the asset map -> unsupported combo

const res = spawnSync(process.execPath, [installJs], {
  env: { ...process.env, npm_config_platform: fakePlatform },
  // process.platform / process.arch are read-only in modern Node;
  // the asset map is keyed on these values, so the only way to drive
  // the unsupported branch from a real Linux runner is to mock the
  // map. Use the require-cache hook instead.
});

if (res.error) {
  fail(`spawn failed: ${res.error.message}`);
}

// Mock the platform/arch by intercepting the require cache. We re-load
// install.js inside a child process whose process.platform we cannot
// rewrite, so instead we extract the message strings from the source
// directly and assert on them. install.js has no exports — the
// behavior under test is the literal string in the unsupported branch.
const fs = require('node:fs');
const src = fs.readFileSync(installJs, 'utf8');

// Spot-check that the URL was fixed: must NOT point at /main/install.ps1,
// MUST point at /main/scripts/install.ps1.
if (src.includes('/main/install.ps1')) {
  fail('install.js still references /main/install.ps1 (broken URL).');
}
if (!src.includes('/main/scripts/install.ps1')) {
  fail('install.js does not mention /main/scripts/install.ps1 (expected URL).');
}

// Operator must be told to uninstall the broken launcher.
if (!src.includes('npm uninstall -g @fgjcarlos/nodered-mcp')) {
  fail('install.js does not instruct the user to npm uninstall -g the broken launcher.');
}

// End-to-end: invoke install.js under a forced unsupported combo by
// stubbing process.platform / process.arch through a small shim.
const shim = `
  'use strict';
  Object.defineProperty(process, 'platform', { value: 'win32' });
  Object.defineProperty(process, 'arch', { value: 'ia32' });
  require(${JSON.stringify(installJs)});
`;
const shimRes = spawnSync(process.execPath, ['-e', shim], { encoding: 'utf8' });

if (shimRes.status !== 1) {
  fail(`install.js should exit 1 on unsupported platform; got status=${shimRes.status}`);
}
const stderr = shimRes.stderr || '';
if (!stderr.includes('no binary published for win32-ia32')) {
  fail(`expected platform/arch banner in stderr; got: ${stderr}`);
}
if (!stderr.includes('/main/scripts/install.ps1')) {
  fail(`expected fixed install.ps1 URL in stderr; got: ${stderr}`);
}
if (!stderr.includes('npm uninstall -g @fgjcarlos/nodered-mcp')) {
  fail(`expected uninstall instruction in stderr; got: ${stderr}`);
}

console.log('install_message_test: PASS');
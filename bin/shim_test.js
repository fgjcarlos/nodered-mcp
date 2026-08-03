'use strict';

// bin/shim_test.js — regression guard for issue #80.
//
// Verifies that bin/nodered-mcp.js, when run from a Windows checkout
// without the postinstall-downloaded binary present, emits the
// correct platform-aware help message instead of the cryptic
// `Cannot find module '.../bin/nodered-mcp.js'` error reported in
// the field. Linux/macOS users still get the existing
// `npm install -g --force` hint.
//
// Run with: node bin/shim_test.js
// Exits 0 on success, 1 on any mismatch.

const { spawnSync } = require('node:child_process');
const path = require('node:path');
const fs = require('node:fs');
const os = require('node:os');

function fail(msg) {
  console.error(`shim_test: FAIL: ${msg}`);
  process.exit(1);
}

const shim = path.join(__dirname, 'nodered-mcp.js');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'shim-test-'));

try {
  // The shim looks for `nodered-mcp` next to itself; copy it into a
  // fresh temp dir without that binary so the missing-binary branch
  // fires.
  const copy = path.join(tmp, 'nodered-mcp.js');
  fs.copyFileSync(shim, copy);

  const res = spawnSync(process.execPath, [copy], { encoding: 'utf8' });

  if (res.status !== 1) {
    fail(`shim should exit 1 when binary missing; got status=${res.status}`);
  }
  const stderr = res.stderr || '';

  if (!stderr.includes('binary not found')) {
    fail(`expected 'binary not found' in stderr; got: ${stderr}`);
  }
  if (!stderr.includes('npm install -g --force @fgjcarlos/nodered-mcp')) {
    fail(`expected generic reinstall hint in stderr; got: ${stderr}`);
  }

  // On non-Windows runners the Windows-specific hint must be absent.
  if (process.platform !== 'win32' && stderr.includes('scripts/install.ps1')) {
    fail(`non-Windows shim should not mention scripts/install.ps1; got: ${stderr}`);
  }

  // Spot-check the source for the Windows branch.
  const src = fs.readFileSync(shim, 'utf8');
  if (!src.includes('/main/scripts/install.ps1')) {
    fail('shim does not reference the fixed install.ps1 URL.');
  }
  if (process.platform !== 'win32' && !src.includes('process.platform === \'win32\'')) {
    fail('shim does not gate Windows hint on platform check.');
  }

  console.log('shim_test: PASS');
} finally {
  fs.rmSync(tmp, { recursive: true, force: true });
}
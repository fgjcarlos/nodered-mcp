'use strict';

// bin/shim_test.js — regression guard for issues #80, #182, and #192.
//
// Verifies that bin/nodered-mcp.js, when run without the
// postinstall-downloaded binary present, emits the generic
// `npm install -g --force` hint instead of a Node.js stack trace.
// The shim applies the `.exe` suffix on Windows (#192) so the
// missing-binary branch fires on the platform-correct path.
// The old Windows-only message referencing install.ps1 was removed
// in #182 and stayed gone in #192 — there is no platform-specific
// hint to test.
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
  // The shim looks for `nodered-mcp[.exe]` next to itself; copy it
  // into a fresh temp dir without that binary so the missing-binary
  // branch fires.
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

  // Defensive: no install.ps1 hint should ever appear in the shim
  // output. The script is retired in #193.
  if (stderr.includes('install.ps1')) {
    fail(`shim output references install.ps1 (retired in #193); got: ${stderr}`);
  }

  console.log('shim_test: PASS');
} finally {
  fs.rmSync(tmp, { recursive: true, force: true });
}

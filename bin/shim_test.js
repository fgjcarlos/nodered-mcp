'use strict';

// bin/shim_test.js — regression guard for issues #80 and #182.
//
// Verifies that bin/nodered-mcp.js, when run without the
// postinstall-downloaded binary present, emits the generic
// `npm install -g --force` hint instead of a Node.js stack trace.
// The Windows-specific hint was removed in #182 — the package.json
// `os` field now makes npm abort on Windows before the shim runs,
// so the shim is a Linux/macOS-only path by construction.
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

const repoRoot = path.join(__dirname, '..');
const pkg = JSON.parse(fs.readFileSync(path.join(repoRoot, 'package.json'), 'utf8'));
if (!Array.isArray(pkg.os) || !pkg.os.includes('linux') || !pkg.os.includes('darwin')) {
  fail(`package.json must declare os: ["linux", "darwin"]; got ${JSON.stringify(pkg.os)}`);
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

  console.log('shim_test: PASS');
} finally {
  fs.rmSync(tmp, { recursive: true, force: true });
}

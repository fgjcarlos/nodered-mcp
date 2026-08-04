'use strict';

// bin/install_message_test.js — regression guard for issues #80 and #182.
//
// After #182 npm aborts Windows installs with EBADPLATFORM before
// bin/install.js runs, so install.js no longer needs the
// unsupported-platform error branch or any install.sh/install.ps1
// URLs. The guard that keeps the orphan-shim bug from regressing is
// the package.json `os` field — that's what this test now asserts.
//
// Run with: node bin/install_message_test.js
// Exits 0 on success, 1 on any mismatch.

const fs = require('node:fs');
const path = require('node:path');

function fail(msg) {
  console.error(`install_message_test: FAIL: ${msg}`);
  process.exit(1);
}

const repoRoot = path.join(__dirname, '..');
const pkg = JSON.parse(fs.readFileSync(path.join(repoRoot, 'package.json'), 'utf8'));

if (!Array.isArray(pkg.os) || !pkg.os.includes('linux') || !pkg.os.includes('darwin')) {
  fail(`package.json must declare os: ["linux", "darwin"]; got ${JSON.stringify(pkg.os)}`);
}

console.log('install_message_test: PASS');

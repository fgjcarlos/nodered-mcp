'use strict';

// bin/install_message_test.js — regression guard for issues #80,
// #182, and #192.
//
// Asserts the source of truth that this repo encodes by hand:
// the asset map in bin/install.js must reference every goreleaser
// .tar.gz asset name, and there must be no surviving references to
// the retired install.sh / install.ps1 scripts.
//
// Run with: node bin/install_message_test.js
// Exits 0 on success, 1 on any mismatch.

const fs = require('node:fs');
const path = require('node:path');

function fail(msg) {
  console.error(`install_message_test: FAIL: ${msg}`);
  process.exit(1);
}

const installSrc = fs.readFileSync(path.join(__dirname, 'install.js'), 'utf8');

// install.sh / install.ps1 are retired in #193 — kill any stragglers.
if (/(?:^|\W)install\.sh(?:$|\W)/.test(installSrc)) {
  fail('install.js still references install.sh (retired in #193).');
}
if (/(?:^|\W)install\.ps1(?:$|\W)/.test(installSrc)) {
  fail('install.js still references install.ps1 (retired in #193).');
}

// Goreleaser publishes six .tar.gz assets (#192). The asset map
// must reference every one of them.
const expected = [
  'nodered-mcp_linux_amd64.tar.gz',
  'nodered-mcp_linux_arm64.tar.gz',
  'nodered-mcp_darwin_amd64.tar.gz',
  'nodered-mcp_darwin_arm64.tar.gz',
  'nodered-mcp_windows_amd64.tar.gz',
  'nodered-mcp_windows_arm64.tar.gz',
];
for (const asset of expected) {
  if (!installSrc.includes(asset)) {
    fail(`install.js asset map is missing ${asset} (out of sync with goreleaser).`);
  }
}

console.log('install_message_test: PASS');

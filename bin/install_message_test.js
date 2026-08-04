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

// The list above is only half the contract: it says what install.js
// asks for, not what goreleaser actually uploads. Those drifted apart
// once already -- an archives format_overrides block gave Windows a
// .zip while install.js kept requesting a .tar.gz, so `npm i -g` on
// Windows 404'd on every release. Assert the archive config still
// produces the extension this map assumes.
const releaserPath = path.join(__dirname, '..', '.goreleaser.yaml');
const releaserSrc = fs.readFileSync(releaserPath, 'utf8');

if (!/^\s*formats:\s*\[tar\.gz\]\s*$/m.test(releaserSrc)) {
  fail('.goreleaser.yaml no longer declares `formats: [tar.gz]`; the .tar.gz names above are wrong.');
}
if (/^\s*format_overrides:/m.test(releaserSrc)) {
  fail('.goreleaser.yaml has a format_overrides block; some platform now gets a different extension than the asset map expects.');
}
if (!/^\s*name_template:\s*"\{\{ \.ProjectName \}\}_\{\{ \.Os \}\}_\{\{ \.Arch \}\}"\s*$/m.test(releaserSrc)) {
  fail('.goreleaser.yaml archive name_template changed; the asset map above no longer matches the published names.');
}

console.log('install_message_test: PASS');

'use strict';

// bin/install_message_test.js — regression guard for issues #80,
// #182, and #192.
//
// Asserts the source of truth that this repo encodes by hand:
// the asset map in bin/install-impl.js must reference every
// goreleaser .tar.gz asset name, and there must be no surviving
// references to the retired install.sh / install.ps1 scripts.
//
// (After the #242 retirement, bin/install.js is gone; the
// authoritative install code path is bin/install-impl.js, which
// reads its version from package.json and is goreleaser-
// independent. The asset map is what this test pins.)
//
// Run with: node bin/install_message_test.js
// Exits 0 on success, 1 on any mismatch.

const fs = require('node:fs');
const path = require('node:path');

function fail(msg) {
  console.error(`install_message_test: FAIL: ${msg}`);
  process.exit(1);
}

const installSrc = fs.readFileSync(path.join(__dirname, 'install-impl.js'), 'utf8');

// install.sh / install.ps1 are retired in #193. Neither the runtime
// asset map nor an unsupported-platform message may point users back
// to either retired installation path.
const bannedInstallerReference = /install\.(?:sh|ps1)/;
if (bannedInstallerReference.test(installSrc)) {
  fail('install-impl.js references a retired install.sh/install.ps1 path.');
}

// Goreleaser publishes six .tar.gz assets and the npm wrapper installs
// every one of them. The list below pins the complete asset contract.
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
    fail(`install-impl.js asset map is missing ${asset} (out of sync with goreleaser).`);
  }
}

// The list above is only half the contract: it says what install-impl
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

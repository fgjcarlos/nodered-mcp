'use strict';

// bin/npm_release_gate_test.js — regression guard for #226.
//
// The npm publish workflow now runs after release.yml, and refuses to
// publish if the GitHub Release for the tag is missing required
// assets or has a malformed checksums.txt. This file exercises
// scripts/release-gate.js's verifyReleaseAssets() against fixtures
// for every blocked path, so the gate cannot silently regress to a
// pass-through.
//
// Run with: node bin/npm_release_gate_test.js
// Exits 0 on success, 1 on any failure. Picked up automatically by
// ci.yml's `npm-wrapper` job (`for t in bin/*_test.js; do node "$t";
// done`).

const assert = require('node:assert/strict');
const {
  verifyReleaseAssets,
  EXPECTED_BINARY_COUNT,
} = require('../scripts/release-gate.js');

let passed = 0;
let failed = 0;
function test(name, fn) {
  try {
    fn();
    console.log(`  ok ${name}`);
    passed++;
  } catch (err) {
    console.error(`  FAIL ${name}: ${err.message}`);
    failed++;
  }
}

// Synthetic asset name set matching what goreleaser actually uploads
// for one full build (6 binaries + 6 SBOMs + checksums.txt). The
// asset names are pinned in .goreleaser.yaml's `name_template` and
// in install_message_test.js.
const fullAssets = [
  'nodered-mcp_linux_amd64.tar.gz',
  'nodered-mcp_linux_arm64.tar.gz',
  'nodered-mcp_darwin_amd64.tar.gz',
  'nodered-mcp_darwin_arm64.tar.gz',
  'nodered-mcp_windows_amd64.tar.gz',
  'nodered-mcp_windows_arm64.tar.gz',
  'nodered-mcp_0.6.1_linux_amd64.sbom.json',
  'nodered-mcp_0.6.1_linux_arm64.sbom.json',
  'nodered-mcp_0.6.1_darwin_amd64.sbom.json',
  'nodered-mcp_0.6.1_darwin_arm64.sbom.json',
  'nodered-mcp_0.6.1_windows_amd64.sbom.json',
  'nodered-mcp_0.6.1_windows_arm64.sbom.json',
  'checksums.txt',
];

const validChecksums = (() => {
  // 64 hex chars of zeros; we don't care about the actual digest
  // values here, only the line format.
  const hex = '0'.repeat(64);
  return fullAssets
    .filter((n) => !/\.sbom\.json$/.test(n))
    .map((n) => `${hex}  ${n}`)
    .join('\n') + '\n';
})();

function release(assets, opts) {
  return {
    isDraft: !!(opts && opts.isDraft),
    isPrerelease: false,
    assets: assets.map((name) => ({ name })),
  };
}

test('passes for a fully populated release', () => {
  const r = verifyReleaseAssets({
    release: release(fullAssets),
    checksumsContent: validChecksums,
    assetNames: fullAssets,
  });
  assert.equal(r.ok, true, JSON.stringify(r.errors));
});

test('fails when release is null (tag has no release)', () => {
  const r = verifyReleaseAssets({
    release: null,
    checksumsContent: null,
    assetNames: [],
  });
  assert.equal(r.ok, false);
  assert.ok(r.errors.length > 0);
});

test('fails for a draft release', () => {
  const r = verifyReleaseAssets({
    release: release(fullAssets, { isDraft: true }),
    checksumsContent: validChecksums,
    assetNames: fullAssets,
  });
  assert.equal(r.ok, false);
  assert.ok(r.errors.some((e) => /draft/i.test(e)), JSON.stringify(r.errors));
});

test('fails when checksums.txt is missing', () => {
  const assets = fullAssets.filter((n) => n !== 'checksums.txt');
  const r = verifyReleaseAssets({
    release: release(assets),
    checksumsContent: null,
    assetNames: assets,
  });
  assert.equal(r.ok, false);
  assert.ok(
    r.errors.some((e) => /checksums\.txt/.test(e)),
    JSON.stringify(r.errors),
  );
});

test('fails when no tarball is present', () => {
  const assets = fullAssets.filter((n) => !/\.tar\.gz$/.test(n));
  const r = verifyReleaseAssets({
    release: release(assets),
    checksumsContent: null,
    assetNames: assets,
  });
  assert.equal(r.ok, false);
  assert.ok(r.errors.some((e) => /tarball/i.test(e)), JSON.stringify(r.errors));
});

test('fails when no SBOM is present', () => {
  const assets = fullAssets.filter((n) => !/\.sbom\.json$/.test(n));
  const r = verifyReleaseAssets({
    release: release(assets),
    checksumsContent: validChecksums,
    assetNames: assets,
  });
  assert.equal(r.ok, false);
  assert.ok(r.errors.some((e) => /SBOM/i.test(e)), JSON.stringify(r.errors));
});

test('fails when checksums.txt has too few entries', () => {
  const short = `${'0'.repeat(64)}  nodered-mcp_linux_amd64.tar.gz\n`;
  const r = verifyReleaseAssets({
    release: release(fullAssets),
    checksumsContent: short,
    assetNames: fullAssets,
  });
  assert.equal(r.ok, false);
  assert.ok(
    r.errors.some((e) => /expected >=/.test(e)),
    JSON.stringify(r.errors),
  );
});

test('fails when checksums.txt contains a malformed line', () => {
  const broken = validChecksums + 'not-a-checksum  nope.tar.gz\n';
  const r = verifyReleaseAssets({
    release: release(fullAssets),
    checksumsContent: broken,
    assetNames: fullAssets,
  });
  assert.equal(r.ok, false);
  assert.ok(
    r.errors.some((e) => /malformed/i.test(e)),
    JSON.stringify(r.errors),
  );
});

test('fails when checksums.txt references a file not in the release', () => {
  const ghost = `${'0'.repeat(64)}  nodered-mcp_plan9_amd64.tar.gz\n${validChecksums}`;
  const r = verifyReleaseAssets({
    release: release(fullAssets),
    checksumsContent: ghost,
    assetNames: fullAssets,
  });
  assert.equal(r.ok, false);
  assert.ok(
    r.errors.some((e) => /not in the release/.test(e)),
    JSON.stringify(r.errors),
  );
});

test('fails when checksums.txt lists the same file twice', () => {
  const firstLine = validChecksums.split('\n')[0];
  const dup = `${firstLine}\n${validChecksums}`;
  const r = verifyReleaseAssets({
    release: release(fullAssets),
    checksumsContent: dup,
    assetNames: fullAssets,
  });
  assert.equal(r.ok, false);
  assert.ok(
    r.errors.some((e) => /more than once/.test(e)),
    JSON.stringify(r.errors),
  );
});

test('EXPECTED_BINARY_COUNT matches the goreleaser matrix (3x2)', () => {
  assert.equal(EXPECTED_BINARY_COUNT, 6);
});

if (failed > 0) {
  console.error(`npm_release_gate_test: ${failed} failed, ${passed} passed`);
  process.exit(1);
}
console.log(`npm_release_gate_test: PASS (${passed} tests)`);
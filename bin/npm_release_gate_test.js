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
  REQUIRED_NPM_ASSETS,
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
// for one full build. Required tarballs come directly from the
// installer contract; the SBOM names mirror .goreleaser.yaml.
const fullAssets = [
  ...REQUIRED_NPM_ASSETS,
  'nodered-mcp_0.6.1_linux_amd64.sbom.json',
  'nodered-mcp_0.6.1_linux_arm64.sbom.json',
  'nodered-mcp_0.6.1_darwin_amd64.sbom.json',
  'nodered-mcp_0.6.1_darwin_arm64.sbom.json',
  'nodered-mcp_0.6.1_windows_amd64.sbom.json',
  'nodered-mcp_0.6.1_windows_arm64.sbom.json',
  'checksums.txt',
];

function checksumsFor(assetNames) {
  // 64 hex chars of zeros; we don't care about the actual digest
  // values here, only the line format.
  const hex = '0'.repeat(64);
  return assetNames
    .filter((n) => /\.tar\.gz$/.test(n))
    .map((n) => `${hex}  ${n}`)
    .join('\n') + '\n';
}

const validChecksums = checksumsFor(fullAssets);

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

for (const requiredAsset of REQUIRED_NPM_ASSETS) {
  test(`fails when required npm tarball is missing: ${requiredAsset}`, () => {
    const assets = fullAssets.filter((n) => n !== requiredAsset);
    const r = verifyReleaseAssets({
      release: release(assets),
      checksumsContent: checksumsFor(assets),
      assetNames: assets,
    });
    assert.equal(r.ok, false);
    assert.ok(
      r.errors.some((e) => e.includes(requiredAsset) && /missing/i.test(e)),
      JSON.stringify(r.errors),
    );
  });
}

for (const requiredAsset of REQUIRED_NPM_ASSETS) {
  test(`fails when required npm checksum is missing: ${requiredAsset}`, () => {
    const checksums = validChecksums
      .split('\n')
      .filter((line) => !line.endsWith(`  ${requiredAsset}`))
      .join('\n');
    const r = verifyReleaseAssets({
      release: release(fullAssets),
      checksumsContent: checksums,
      assetNames: fullAssets,
    });
    assert.equal(r.ok, false);
    assert.ok(
      r.errors.some((e) => e.includes(requiredAsset) && /no entry/i.test(e)),
      JSON.stringify(r.errors),
    );
  });
}

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

test('required npm assets are derived from the installer contract', () => {
  assert.ok(REQUIRED_NPM_ASSETS.length > 0);
  assert.equal(new Set(REQUIRED_NPM_ASSETS).size, REQUIRED_NPM_ASSETS.length);
});

if (failed > 0) {
  console.error(`npm_release_gate_test: ${failed} failed, ${passed} passed`);
  process.exit(1);
}
console.log(`npm_release_gate_test: PASS (${passed} tests)`);
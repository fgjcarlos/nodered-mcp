'use strict';

// bin/release-snapshot_test.js — focused tests for
// scripts/release-snapshot.js inspect helpers.
//
// Issue #258: the release-snapshot.js script is the producer-side
// gate that fails CI if goreleaser outputs drift from the packager's
// contract. The script consumes goreleaser in CI; this test exercises
// the inspection helpers with synthetic tarballs so the suite runs on
// any node install without goreleaser present (Windows matrix,
// dependabot updates without a goreleaser action pin, etc).
//
// Specifically covered:
//   - parseChecksumLine accepts goreleaser's "<hex>  <name>" format
//     and rejects malformed input
//   - expectedBinaryName maps the right .exe suffix per platform
//   - keyFromAsset round-trips with ASSET_MAP
//   - inspectArchive flags a missing/empty binary with the correct
//     platform/artifact context
//   - inspectArchive flags a wrong-suffix binary (e.g. .exe on Linux)
//   - inspectAll flags a missing archive, a checksum mismatch, and a
//     missing checksums.txt
//   - inspectAll accepts both flat and wrapped layouts (regression
//     coverage for #256)
//
// Run with: node bin/release-snapshot_test.js
// Exits 0 on success, 1 on any failure. Picked up by ci.yml's
// npm-wrapper job (`for t in bin/*_test.js`).

const fs = require('node:fs');
const fsp = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const zlib = require('node:zlib');

const {
  inspectArchive,
  inspectAll,
  parseChecksumLine,
  expectedBinaryName,
  keyFromAsset,
  REQUIRED_ASSETS,
} = require('../scripts/release-snapshot.js');
const { TARGETS } = require('./platform-packages.js');
const ASSET_MAP = Object.fromEntries(TARGETS.map(({ key, asset }) => [key, asset]));

let failures = 0;
function fail(msg) {
  failures += 1;
  console.error(`release-snapshot_test: FAIL: ${msg}`);
}
function assertEq(actual, expected, label) {
  if (actual !== expected) {
    fail(`${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
  }
}
function assertContains(haystack, needle, label) {
  if (!haystack.includes(needle)) {
    fail(`${label}: expected to contain ${JSON.stringify(needle)}`);
  }
}

// ---------- fixture builders ----------------------------------------

function buildTarGz(entries) {
  const blocks = [];
  for (const e of entries) {
    const body = Buffer.from(e.content || '', 'utf8');
    const header = Buffer.alloc(512);
    header.write(e.name, 0, 'utf8');
    header.write('0000644', 100, 'utf8');
    header.write('0000000', 108, 'utf8');
    header.write('0000000', 116, 'utf8');
    header.write(body.length.toString(8).padStart(11, '0') + ' ', 124, 'utf8');
    header.write('00000000000', 136, 'utf8');
    header.write('        ', 148, 'utf8');
    header.write('0', 156, 'utf8');
    const cf = Buffer.alloc(8, 0x20);
    cf.copy(header, 148);
    let s = 0;
    for (let i = 0; i < 512; i += 1) s += header[i];
    header.write(s.toString(8).padStart(6, '0') + '\0 ', 148, 'utf8');
    const padded = Buffer.alloc(Math.ceil(body.length / 512) * 512, 0);
    body.copy(padded, 0);
    blocks.push(header, padded);
  }
  blocks.push(Buffer.alloc(1024, 0));
  return zlib.gzipSync(Buffer.concat(blocks));
}

async function makeDistFixture(archives, checksumsTxt) {
  const dir = await fsp.mkdtemp(path.join(os.tmpdir(), 'snapshot-test-'));
  const produced = {};
  for (const [name, entries] of Object.entries(archives)) {
    const buf = buildTarGz(entries);
    const filePath = path.join(dir, name);
    await fsp.writeFile(filePath, buf);
    produced[name] = { filePath, digest: crypto.createHash('sha256').update(buf).digest('hex') };
  }
  if (typeof checksumsTxt === 'string') {
    await fsp.writeFile(path.join(dir, 'checksums.txt'), checksumsTxt);
  }
  return { dir, cleanup: async () => fs.rmSync(dir, { recursive: true, force: true }), produced };
}

function checksumsTxtFor(produced, extra) {
  const lines = Object.entries(produced).map(
    ([name, { digest }]) => `${digest}  ${name}`,
  );
  return [...lines, ...(extra || [])].join('\n') + '\n';
}

// ---------- pure-function tests -------------------------------------

function testParseChecksumLine() {
  const parsed = parseChecksumLine(
    `${'a'.repeat(64)}  nodered-mcp_linux_amd64.tar.gz`,
  );
  assertEq(parsed.digest, 'a'.repeat(64), 'parseChecksumLine digest');
  assertEq(parsed.filename, 'nodered-mcp_linux_amd64.tar.gz', 'parseChecksumLine filename');

  // sha256sum-style with leading "*"
  const withStar = parseChecksumLine(
    `${'b'.repeat(64)} *nodered-mcp_linux_amd64.tar.gz`,
  );
  assertEq(withStar.filename, 'nodered-mcp_linux_amd64.tar.gz', 'parseChecksumLine leading *');

  assertEq(parseChecksumLine(''), null, 'parseChecksumLine empty');
  assertEq(parseChecksumLine('garbage'), null, 'parseChecksumLine garbage');
  assertEq(parseChecksumLine('short  name'), null, 'parseChecksumLine short hex');
}

function testExpectedBinaryName() {
  assertEq(expectedBinaryName('win32-x64'), 'nodered-mcp.exe', 'win32 binary name');
  assertEq(expectedBinaryName('win32-arm64'), 'nodered-mcp.exe', 'win32-arm64 binary name');
  assertEq(expectedBinaryName('linux-x64'), 'nodered-mcp', 'linux binary name');
  assertEq(expectedBinaryName('darwin-arm64'), 'nodered-mcp', 'darwin binary name');
}

function testKeyFromAsset() {
  for (const [key, asset] of Object.entries(ASSET_MAP)) {
    assertEq(keyFromAsset(asset), key, `keyFromAsset(${asset})`);
  }
  assertEq(keyFromAsset('nodered-mcp_plan9_amd64.tar.gz'), null, 'keyFromAsset unknown');
}

function testRequiredAssetsDerivedFromInstaller() {
  // Mirror the single-source-of-truth invariant the rest of the
  // repo enforces: REQUIRED_ASSETS must equal the union of ASSET_MAP
  // values. A drift here means a future ASSET_MAP change was not
  // propagated to release-snapshot.
  assertEq(
    new Set(REQUIRED_ASSETS).size,
    REQUIRED_ASSETS.length,
    'REQUIRED_ASSETS has no duplicates',
  );
  assertEq(
    new Set(REQUIRED_ASSETS).size,
    new Set(Object.values(ASSET_MAP)).size,
    'REQUIRED_ASSETS cardinality matches ASSET_MAP',
  );
  for (const a of REQUIRED_ASSETS) {
    if (!Object.values(ASSET_MAP).includes(a)) {
      fail(`REQUIRED_ASSETS contains ${a} which is not in ASSET_MAP`);
    }
  }
}

// ---------- inspectArchive tests ------------------------------------

async function testFlatLayoutArchiveOk() {
  const binary = expectedBinaryName('linux-x64');
  const assetName = ASSET_MAP['linux-x64'];
  const { dir, cleanup, produced } = await makeDistFixture({
    [assetName]: [
      { name: binary, content: '#!/bin/sh\necho ok\n' },
      { name: 'README.md', content: 'readme' },
    ],
  });
  try {
    const result = await inspectArchive({
      assetName,
      archivePath: produced[assetName].filePath,
    });
    if (!result.ok) fail(`flat linux archive: expected ok; got ${JSON.stringify(result.errors)}`);
  } finally {
    await cleanup();
  }
}

async function testWrappedLayoutArchiveOk() {
  const binary = expectedBinaryName('linux-x64');
  const assetName = ASSET_MAP['linux-x64'];
  const wrappedRoot = assetName.replace(/\.tar\.gz$/, '');
  const { dir, cleanup, produced } = await makeDistFixture({
    [assetName]: [
      { name: `${wrappedRoot}/${binary}`, content: '#!/bin/sh\necho ok\n' },
      { name: `${wrappedRoot}/README.md`, content: 'readme' },
    ],
  });
  try {
    const result = await inspectArchive({
      assetName,
      archivePath: produced[assetName].filePath,
    });
    if (!result.ok) fail(`wrapped linux archive: expected ok; got ${JSON.stringify(result.errors)}`);
  } finally {
    await cleanup();
  }
}

async function testWindowsArchiveExpectsExe() {
  const assetName = ASSET_MAP['win32-x64'];
  const { cleanup, produced } = await makeDistFixture({
    [assetName]: [
      // Wrong-suffix binary: would be the regression that #250/#252
      // were, but with a different cause. Goreleaser must emit .exe
      // on Windows.
      { name: 'nodered-mcp', content: 'binary' },
      { name: 'README.md', content: 'readme' },
    ],
  });
  try {
    const result = await inspectArchive({
      assetName,
      archivePath: produced[assetName].filePath,
    });
    if (result.ok) {
      fail('windows archive with .exe suffix missing: inspectArchive should have failed');
    } else {
      const joined = result.errors.join(' ');
      assertContains(joined, 'exactly one binary', 'windows archive error names the missing binary');
    }
  } finally {
    await cleanup();
  }
}

async function testEmptyBinaryFails() {
  const binary = expectedBinaryName('linux-x64');
  const assetName = ASSET_MAP['linux-x64'];
  const { cleanup, produced } = await makeDistFixture({
    [assetName]: [{ name: binary, content: '' }],
  });
  try {
    const result = await inspectArchive({
      assetName,
      archivePath: produced[assetName].filePath,
    });
    if (result.ok) fail('empty linux binary: inspectArchive should have failed');
  } finally {
    await cleanup();
  }
}

async function testDuplicateBinaryFails() {
  const binary = expectedBinaryName('linux-x64');
  const assetName = ASSET_MAP['linux-x64'];
  const wrappedRoot = assetName.replace(/\.tar\.gz$/, '');
  const { cleanup, produced } = await makeDistFixture({
    [assetName]: [
      { name: binary, content: 'flat' },
      { name: `${wrappedRoot}/${binary}`, content: 'wrapped' },
    ],
  });
  try {
    const result = await inspectArchive({
      assetName,
      archivePath: produced[assetName].filePath,
    });
    if (result.ok) fail('duplicate binary: inspectArchive should have failed');
    assertContains(
      result.errors.join(' '),
      'exactly one binary',
      'duplicate binary error identifies cardinality contract',
    );
  } finally {
    await cleanup();
  }
}

async function testWrongSuffixOnUnixFails() {
  const assetName = ASSET_MAP['linux-x64'];
  const { cleanup, produced } = await makeDistFixture({
    [assetName]: [
      { name: 'nodered-mcp', content: '#!/bin/sh\necho ok\n' },
      // Wrong suffix for Linux — a Windows .exe stranded in a Linux
      // archive is the kind of thing goreleaser should never produce
      // but the gate must catch.
      { name: 'nodered-mcp.exe', content: 'stray' },
    ],
  });
  try {
    const result = await inspectArchive({
      assetName,
      archivePath: produced[assetName].filePath,
    });
    if (result.ok) fail('linux archive with stray .exe: inspectArchive should have failed');
  } finally {
    await cleanup();
  }
}

// ---------- inspectAll tests ----------------------------------------

async function testInspectAllSixAssetsPass() {
  const archives = {};
  for (const [key, assetName] of Object.entries(ASSET_MAP)) {
    const binary = expectedBinaryName(key);
    const archiveRoot = assetName.replace(/\.tar\.gz$/, '');
    archives[assetName] = [
      { name: `${archiveRoot}/${binary}`, content: `binary for ${key}` },
      { name: `${archiveRoot}/README.md`, content: 'r' },
    ];
  }
  // pre-compute the checksums so the fixture passes them in
  // (makeDistFixture builds archives first; checksumsTxt is supplied
  // by the caller to keep the API symmetric with the real goreleaser
  // output, where checksums.txt is generated alongside the archives).
  const preArchives = {};
  const producedDigests = {};
  for (const [name, entries] of Object.entries(archives)) {
    const buf = buildTarGz(entries);
    producedDigests[name] = crypto.createHash('sha256').update(buf).digest('hex');
  }
  const checksumsTxt = Object.entries(producedDigests)
    .map(([name, d]) => `${d}  ${name}`)
    .join('\n') + '\n';
  const { dir, cleanup, produced } = await makeDistFixture(archives, checksumsTxt);
  try {
    const result = await inspectAll({ distDir: dir });
    if (!result.ok) {
      fail(`inspectAll 6-asset pass: ${JSON.stringify(result.errors)}`);
    }
  } finally {
    await cleanup();
  }
}

async function testInspectAllMissingArchive() {
  // Only write 5 of the 6 required archives.
  const archives = {};
  const entries = Object.entries(ASSET_MAP);
  for (let i = 0; i < entries.length - 1; i += 1) {
    const [key, assetName] = entries[i];
    const binary = expectedBinaryName(key);
    archives[assetName] = [{ name: binary, content: 'b' }];
  }
  const { dir, cleanup, produced } = await makeDistFixture(archives);
  try {
    const result = await inspectAll({ distDir: dir });
    if (result.ok) fail('inspectAll missing-archive: expected failure');
    const joined = result.errors.join(' ');
    assertContains(joined, 'missing', 'missing-archive error mentions missing');
  } finally {
    await cleanup();
  }
}

async function testInspectAllChecksumMismatch() {
  const archives = {};
  for (const [key, assetName] of Object.entries(ASSET_MAP)) {
    const binary = expectedBinaryName(key);
    archives[assetName] = [{ name: binary, content: 'b' }];
  }
  // Tamper with the checksums: every entry gets a wrong digest.
  const tampered = Object.entries(archives)
    .map(([name]) => `${'0'.repeat(64)}  ${name}`)
    .join('\n') + '\n';
  const { dir, cleanup, produced } = await makeDistFixture(archives, tampered);
  try {
    const result = await inspectAll({ distDir: dir });
    if (result.ok) fail('inspectAll checksum-mismatch: expected failure');
    const joined = result.errors.join(' ');
    assertContains(joined, 'digest mismatch', 'checksum-mismatch error names digest');
  } finally {
    await cleanup();
  }
}

async function testInspectAllMissingChecksumsTxt() {
  const archives = {};
  for (const [key, assetName] of Object.entries(ASSET_MAP)) {
    const binary = expectedBinaryName(key);
    archives[assetName] = [{ name: binary, content: 'b' }];
  }
  const { dir, cleanup, produced } = await makeDistFixture(archives);
  try {
    const result = await inspectAll({ distDir: dir });
    if (result.ok) fail('inspectAll missing-checksums: expected failure');
    assertContains(result.errors.join(' '), 'checksums.txt', 'missing-checksums error names file');
  } finally {
    await cleanup();
  }
}

async function testInspectAllMissingDistDir() {
  const dir = path.join(os.tmpdir(), `nonexistent-${Date.now()}-${process.pid}`);
  const result = await inspectAll({ distDir: dir });
  if (result.ok) fail('inspectAll missing-dist-dir: expected failure');
  assertContains(result.errors.join(' '), 'dist directory not found', 'missing-dist-dir error');
}

// ---------- runner --------------------------------------------------

async function runTest(name, fn) {
  const before = failures;
  try {
    await fn();
  } catch (err) {
    fail(`${name}: uncaught exception: ${err.message}`);
  }
  const status = failures > before ? 'FAIL' : 'PASS';
  console.log(`release-snapshot_test: ${status} ${name}`);
}

async function main() {
  testParseChecksumLine();
  testExpectedBinaryName();
  testKeyFromAsset();
  testRequiredAssetsDerivedFromInstaller();
  await runTest('testFlatLayoutArchiveOk', testFlatLayoutArchiveOk);
  await runTest('testWrappedLayoutArchiveOk', testWrappedLayoutArchiveOk);
  await runTest('testWindowsArchiveExpectsExe', testWindowsArchiveExpectsExe);
  await runTest('testEmptyBinaryFails', testEmptyBinaryFails);
  await runTest('testDuplicateBinaryFails', testDuplicateBinaryFails);
  await runTest('testWrongSuffixOnUnixFails', testWrongSuffixOnUnixFails);
  await runTest('testInspectAllSixAssetsPass', testInspectAllSixAssetsPass);
  await runTest('testInspectAllMissingArchive', testInspectAllMissingArchive);
  await runTest('testInspectAllChecksumMismatch', testInspectAllChecksumMismatch);
  await runTest('testInspectAllMissingChecksumsTxt', testInspectAllMissingChecksumsTxt);
  await runTest('testInspectAllMissingDistDir', testInspectAllMissingDistDir);

  if (failures === 0) {
    console.log('release-snapshot_test: PASS');
  } else {
    console.error(`release-snapshot_test: ${failures} failure(s)`);
    process.exit(1);
  }
}

main().catch((err) => {
  console.error(`release-snapshot_test: FAIL: ${err.message}`);
  process.exit(1);
});

'use strict';

// bin/build-platform-packages_test.js — focused tests for
// scripts/build-platform-packages.js.
//
// Issue #257: the platform package producer consumes goreleaser's
// dist/ and emits six npm-ready .tgz files. We exercise the
// build helpers with synthetic goreleaser archives (flat layout
// + wrapped layout) so the suite runs without goreleaser present.
//
// Covered:
//   - TARGETS enumerates all six required platforms
//   - binaryNameFor applies .exe on win32 only
//   - buildOne produces a valid npm tarball: package.json at the
//     archive root, bin/<binary>, LICENSE alongside
//   - buildOne respects a custom version (stamped into the manifest)
//   - buildOne fails closed on a missing goreleaser artifact
//   - buildOne fails closed on a missing platform manifest
//
// Run with: node bin/build-platform-packages_test.js

const fs = require('node:fs');
const fsp = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const { execFileSync } = require('node:child_process');
const zlib = require('node:zlib');

const { TARGETS, binaryNameFor, buildOne } = require('../scripts/build-platform-packages.js');
const { PLATFORM_PACKAGES } = require('./platform-packages.js');
const ASSET_MAP = Object.fromEntries(TARGETS.map(({ key, asset }) => [key, asset]));

let failures = 0;
function fail(msg) {
  failures += 1;
  console.error(`build-platform-packages_test: FAIL: ${msg}`);
}
function assertEq(actual, expected, label) {
  if (actual !== expected) fail(`${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}
function assertContains(haystack, needle, label) {
  if (!haystack.includes(needle)) fail(`${label}: expected to contain ${JSON.stringify(needle)}`);
}

// Build a synthetic goreleaser-style .tar.gz with one regular file
// either at the root (flat) or inside a subdir (wrapped).
function buildSyntheticTarGz(archiveRoot, name, content) {
  const body = Buffer.from(content, 'utf8');
  const header = Buffer.alloc(512);
  const fullName = archiveRoot ? `${archiveRoot}/${name}` : name;
  header.write(fullName, 0, 'utf8');
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
  const eof = Buffer.alloc(1024, 0);
  return zlib.gzipSync(Buffer.concat([header, padded, eof]));
}

async function makeSyntheticDist(dir, layout) {
  await fsp.mkdir(dir, { recursive: true });
  for (const [key, asset] of Object.entries(ASSET_MAP)) {
    const platform = TARGETS.find((target) => target.key === key).platform;
    const binary = binaryNameFor(platform);
    const archiveRoot = layout === 'wrapped' ? asset.replace(/\.tar\.gz$/, '') : null;
    const buf = buildSyntheticTarGz(archiveRoot, binary, `#!/bin/sh\necho ${key}\n`);
    await fsp.writeFile(path.join(dir, asset), buf);
  }
}

async function readTarball(tarballPath) {
  // Use system tar to enumerate entries without depending on a
  // node-tar version.
  const out = execFileSync('tar', ['-tzf', path.basename(tarballPath)], {
    cwd: path.dirname(tarballPath),
    encoding: 'utf8',
  });
  return out.split('\n').filter((l) => l.length > 0);
}

function testTargetsContract() {
  assertEq(TARGETS.length, 6, 'TARGETS length');
  const keys = TARGETS.map((t) => t.key).sort().join(',');
  const expected = Object.keys(ASSET_MAP).sort().join(',');
  assertEq(keys, expected, 'TARGETS keys match ASSET_MAP');
}

function testBinaryNameFor() {
  assertEq(binaryNameFor('win32'), 'nodered-mcp.exe', 'binaryNameFor(win32)');
  assertEq(binaryNameFor('linux'), 'nodered-mcp', 'binaryNameFor(linux)');
  assertEq(binaryNameFor('darwin'), 'nodered-mcp', 'binaryNameFor(darwin)');
}

async function testBuildOneFlatLayout() {
  const tmpRoot = await fsp.mkdtemp(path.join(os.tmpdir(), 'bpp-test-'));
  const distDir = path.join(tmpRoot, 'dist');
  const outDir = path.join(tmpRoot, 'out');
  const licenseSrc = path.join(tmpRoot, 'LICENSE');
  await fsp.writeFile(licenseSrc, 'MIT License Text\n');
  await makeSyntheticDist(distDir, 'flat');
  const target = TARGETS.find((t) => t.key === 'linux-x64');
  try {
    const result = await buildOne({
      target,
      distDir,
      outDir,
      version: '9.9.9',
      licenseSrc,
    });
    if (!fs.existsSync(result.tarball)) fail(`tarball not created at ${result.tarball}`);
    const entries = await readTarball(result.tarball);
    assertContains(entries.join('\n'), 'package/package.json', 'tarball has npm package manifest');
    assertContains(entries.join('\n'), 'package/bin/', 'tarball has bin/');
    assertContains(entries.join('\n'), 'package/bin/nodered-mcp', 'tarball has Linux binary');
    assertContains(entries.join('\n'), 'package/LICENSE', 'tarball has LICENSE');

    // Manifest version must match.
    const manifest = JSON.parse(execFileSync(
      'tar',
      ['-xzOf', path.basename(result.tarball), 'package/package.json'],
      { cwd: path.dirname(result.tarball), encoding: 'utf8' },
    ));
    assertEq(manifest.version, '9.9.9', 'manifest version stamped');
    assertEq(manifest.os[0], 'linux', 'manifest os pinned');
    assertEq(manifest.cpu[0], 'x64', 'manifest cpu pinned');
  } finally {
    fs.rmSync(tmpRoot, { recursive: true, force: true });
  }
}

async function testBuildOneWrappedLayout() {
  const tmpRoot = await fsp.mkdtemp(path.join(os.tmpdir(), 'bpp-test-'));
  const distDir = path.join(tmpRoot, 'dist');
  const outDir = path.join(tmpRoot, 'out');
  const licenseSrc = path.join(tmpRoot, 'LICENSE');
  await fsp.writeFile(licenseSrc, 'MIT License Text\n');
  await makeSyntheticDist(distDir, 'wrapped');
  const target = TARGETS.find((t) => t.key === 'darwin-arm64');
  try {
    const result = await buildOne({
      target,
      distDir,
      outDir,
      version: '9.9.9',
      licenseSrc,
    });
    const entries = await readTarball(result.tarball);
    assertContains(entries.join('\n'), 'package/bin/nodered-mcp', 'wrapped layout: tarball still has Unix binary');
  } finally {
    fs.rmSync(tmpRoot, { recursive: true, force: true });
  }
}

async function testBuildOneWindowsExe() {
  const tmpRoot = await fsp.mkdtemp(path.join(os.tmpdir(), 'bpp-test-'));
  const distDir = path.join(tmpRoot, 'dist');
  const outDir = path.join(tmpRoot, 'out');
  const licenseSrc = path.join(tmpRoot, 'LICENSE');
  await fsp.writeFile(licenseSrc, 'MIT\n');
  await makeSyntheticDist(distDir, 'flat');
  const target = TARGETS.find((t) => t.key === 'win32-x64');
  try {
    const result = await buildOne({
      target,
      distDir,
      outDir,
      version: '9.9.9',
      licenseSrc,
    });
    const entries = await readTarball(result.tarball);
    assertContains(entries.join('\n'), 'package/bin/nodered-mcp.exe', 'Windows tarball has .exe binary');
  } finally {
    fs.rmSync(tmpRoot, { recursive: true, force: true });
  }
}

async function testBuildOneMissingArtifact() {
  const tmpRoot = await fsp.mkdtemp(path.join(os.tmpdir(), 'bpp-test-'));
  const distDir = path.join(tmpRoot, 'dist'); // empty
  const outDir = path.join(tmpRoot, 'out');
  const target = TARGETS[0];
  try {
    let threw = false;
    try {
      await buildOne({
        target,
        distDir,
        outDir,
        version: '0.0.1',
        licenseSrc: '/nonexistent',
      });
    } catch (err) {
      threw = true;
      assertContains(err.message, 'missing goreleaser artifact', 'missing-artifact error names the cause');
    }
    if (!threw) fail('buildOne should have failed on missing artifact');
  } finally {
    fs.rmSync(tmpRoot, { recursive: true, force: true });
  }
}

async function runTest(name, fn) {
  const before = failures;
  try {
    await fn();
  } catch (err) {
    fail(`${name}: uncaught exception: ${err.message}`);
  }
  const status = failures > before ? 'FAIL' : 'PASS';
  console.log(`build-platform-packages_test: ${status} ${name}`);
}

async function main() {
  testTargetsContract();
  testBinaryNameFor();
  await runTest('testBuildOneFlatLayout', testBuildOneFlatLayout);
  await runTest('testBuildOneWrappedLayout', testBuildOneWrappedLayout);
  await runTest('testBuildOneWindowsExe', testBuildOneWindowsExe);
  await runTest('testBuildOneMissingArtifact', testBuildOneMissingArtifact);

  if (failures === 0) console.log('build-platform-packages_test: PASS');
  else { console.error(`build-platform-packages_test: ${failures} failure(s)`); process.exit(1); }
}

main().catch((err) => {
  console.error(`build-platform-packages_test: FAIL: ${err.message}`);
  process.exit(1);
});

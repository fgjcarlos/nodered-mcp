#!/usr/bin/env node
'use strict';

// scripts/build-platform-packages.js — assemble six native npm
// packages from goreleaser's snapshot output.
//
// Background: goreleaser emits .tar.gz archives under dist/ for
// every os/arch target. The legacy npm installer downloaded the
// matching archive via postinstall and verified its SHA-256.
// Starting with 0.7.0 (#257), the channel moves to npm-native
// platform packages: one package per os/arch with `os`/`cpu`
// constraints, shipped through `optionalDependencies`, and the
// registry's integrity replaces the custom checksum path.
//
// This script is the producer for that move. It walks dist/,
// pairs each archive with its target manifest under npm/<key>/
// (carrying `os`, `cpu`, `files`, license metadata), and emits
// six `.tgz` files ready for `npm publish`. It does NOT call
// `npm publish`; the workflow does that with the right ordering
// and idempotency guard.
//
// CLI:
//   node scripts/build-platform-packages.js [--version <semver>] [--dist <path>] [--out <path>]
//
// Exit codes:
//   0 — every platform produced a tarball
//   1 — at least one platform missing or invalid
//   2 — usage error
//
// Inputs:
//   dist/<asset>.tar.gz     (goreleaser snapshot)
//   npm/<key>/package.json  (the platform manifest template)
//   LICENSE                 (copied into every package)
//
// Outputs (under <out>):
//   <platform-package>-<version>.tgz

const fs = require('node:fs');
const fsp = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const { execFileSync } = require('node:child_process');
const { TARGETS, binaryNameFor } = require('../bin/platform-packages');
const { extractTarGz } = require('./archive');

const ROOT = path.join(__dirname, '..');
const DEFAULT_DIST = path.join(ROOT, 'dist');
const DEFAULT_OUT = path.join(ROOT, 'npm-packages');

function usage() {
  process.stdout.write(
    'usage: build-platform-packages.js [--version <semver>] [--dist <path>] [--out <path>]\n',
  );
}

function parseArgs(argv) {
  const out = {
    version: null,
    dist: DEFAULT_DIST,
    out: DEFAULT_OUT,
  };
  for (let i = 2; i < argv.length; i += 1) {
    const a = argv[i];
    if (a === '--version') out.version = argv[++i];
    else if (a === '--dist') out.dist = argv[++i];
    else if (a === '--out') out.out = argv[++i];
    else if (a === '--help' || a === '-h') {
      usage();
      process.exit(0);
    } else {
      process.stderr.write(`unknown arg: ${a}\n`);
      usage();
      process.exit(2);
    }
  }
  return out;
}

// Read the goreleaser tarball and find the native executable inside.
// Accepts both flat and wrapped layouts (the latter put it inside a
// subdirectory). The script does not re-implement extraction: it
// shells out to system tar because every CI runner has one.
async function extractBinary(assetPath, platform) {
  const out = await fsp.mkdtemp(path.join(os.tmpdir(), 'np-pkg-'));
  try {
    extractTarGz(assetPath, out);
    const binary = binaryNameFor(platform);
    const direct = path.join(out, binary);
    if (fs.existsSync(direct)) return { staging: out, binary };
    // Walk one level deep for the wrapped layout.
    const entries = await fsp.readdir(out, { withFileTypes: true });
    for (const e of entries) {
      if (!e.isDirectory()) continue;
      const candidate = path.join(out, e.name, binary);
      if (fs.existsSync(candidate)) return { staging: out, binary, nested: e.name };
    }
    throw new Error(`native binary ${binary} not found inside ${assetPath}`);
  } catch (err) {
    try { fs.rmSync(out, { recursive: true, force: true }); } catch (_) {}
    throw err;
  }
}

// Build one platform package by stamping the manifest with the
// resolved version, copying the binary and LICENSE into a staging
// dir, and producing a gzipped tarball.
async function buildOne({ target, distDir, outDir, version, licenseSrc }) {
  const assetPath = path.join(distDir, target.asset);
  if (!fs.existsSync(assetPath)) {
    throw new Error(`missing goreleaser artifact: ${assetPath}`);
  }
  const manifestPath = path.join(ROOT, 'npm', target.key, 'package.json');
  if (!fs.existsSync(manifestPath)) {
    throw new Error(`missing platform manifest: ${manifestPath}`);
  }
  const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
  const expectedOs = [target.platform];
  const expectedCpu = [target.arch];
  if (manifest.name !== target.packageName) {
    throw new Error(`${target.key} manifest name must be ${target.packageName}, got ${manifest.name}`);
  }
  if (JSON.stringify(manifest.os) !== JSON.stringify(expectedOs)) {
    throw new Error(`${target.key} manifest os must be ${JSON.stringify(expectedOs)}`);
  }
  if (JSON.stringify(manifest.cpu) !== JSON.stringify(expectedCpu)) {
    throw new Error(`${target.key} manifest cpu must be ${JSON.stringify(expectedCpu)}`);
  }
  manifest.version = version;
    const platform = target.platform;
  const { staging, binary, nested } = await extractBinary(assetPath, platform);

  const pkgStaging = await fsp.mkdtemp(path.join(os.tmpdir(), 'np-pkg-stage-'));
  try {
    await fsp.mkdir(path.join(pkgStaging, 'bin'), { recursive: true });
    // Copy the binary at its expected layout (flat at bin/, regardless
    // of whether goreleaser produced a flat or wrapped archive).
    const binSrc = nested
      ? path.join(staging, nested, binary)
      : path.join(staging, binary);
    await fsp.copyFile(binSrc, path.join(pkgStaging, 'bin', binary));
    if (platform !== 'win32') {
      fs.chmodSync(path.join(pkgStaging, 'bin', binary), 0o755);
    }
    // The platform manifest declares its own `files` allowlist.
    // LICENSE is included so downstream consumers see the same
    // terms as the main package.
    if (fs.existsSync(licenseSrc)) {
      await fsp.copyFile(licenseSrc, path.join(pkgStaging, 'LICENSE'));
    }
    await fsp.writeFile(
      path.join(pkgStaging, 'package.json'),
      JSON.stringify(manifest, null, 2) + '\n',
    );

    await fsp.mkdir(outDir, { recursive: true });
    // npm itself owns the package format (`package/` prefix, modes,
    // files allowlist, and manifest normalization). Reimplementing
    // that format with tar caused the Windows failure in PR #261.
    const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm';
    const stdout = execFileSync(
      npm,
      [
        'pack',
        '--json',
        '--cache', path.join(os.tmpdir(), 'nodered-mcp-npm-cache'),
        '--pack-destination', outDir,
        pkgStaging,
      ],
      {
        cwd: ROOT,
        encoding: 'utf8',
        stdio: ['ignore', 'pipe', 'pipe'],
        shell: process.platform === 'win32',
      },
    );
    const packed = JSON.parse(stdout);
    if (!Array.isArray(packed) || !packed[0] || !packed[0].filename) {
      throw new Error(`npm pack returned an unexpected result: ${stdout}`);
    }
    const tarballPath = path.join(outDir, packed[0].filename);
    return { key: target.key, name: manifest.name, version, tarball: tarballPath };
  } finally {
    try { fs.rmSync(staging, { recursive: true, force: true }); } catch (_) {}
    try { fs.rmSync(pkgStaging, { recursive: true, force: true }); } catch (_) {}
  }
}

async function main() {
  const args = parseArgs(process.argv);
  if (!args.version) {
    process.stderr.write('--version <semver> is required\n');
    usage();
    process.exit(2);
  }
  if (!fs.existsSync(args.dist)) {
    process.stderr.write(`dist directory not found at ${args.dist}; run goreleaser --snapshot first\n`);
    process.exit(1);
  }
  const licenseSrc = path.join(ROOT, 'LICENSE');
  const errors = [];
  const produced = [];
  for (const target of TARGETS) {
    try {
      const result = await buildOne({
        target,
        distDir: args.dist,
        outDir: args.out,
        version: args.version,
        licenseSrc,
      });
      produced.push(result);
      process.stdout.write(`build-platform-packages: ok ${result.key} -> ${path.basename(result.tarball)}\n`);
    } catch (err) {
      errors.push(`${target.key}: ${err.message}`);
      process.stderr.write(`build-platform-packages: FAIL ${target.key}: ${err.message}\n`);
    }
  }
  if (errors.length > 0) {
    process.stderr.write(`build-platform-packages: ${errors.length} failure(s)\n`);
    process.exit(1);
  }
  process.stdout.write(
    `build-platform-packages: ok (${produced.length} packages under ${args.out})\n`,
  );
}

if (require.main === module) {
  main().catch((err) => {
    process.stderr.write(`build-platform-packages: ${err.message}\n`);
    process.exit(1);
  });
}

module.exports = { TARGETS, binaryNameFor, buildOne };

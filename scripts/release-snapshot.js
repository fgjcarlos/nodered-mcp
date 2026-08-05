#!/usr/bin/env node
'use strict';

// scripts/release-snapshot.js — run goreleaser in --snapshot mode and
// inspect every generated archive against the native-package contract.
//
// Background: the 0.6.2 release shipped because the unit tests only
// verified synthetic tarballs the test harness had constructed
// itself. Both producer (goreleaser) and consumer (the npm packager)
// can agree on a contract that does not match what the release flow
// actually emits. Issue #258 mandates that CI consume the artifacts
// goreleaser produces — flat layout, correct .exe naming, real
// checksums — and fail if any of them diverges.
//
// This script is the producer-side gate. It runs `goreleaser release
// --snapshot --clean --skip=publish`, then walks `dist/` and asserts
// the per-platform artifact contract for every required npm tarball.
// CLI:
//   node scripts/release-snapshot.js [--skip-build] [--dist-dir <path>]
//
// Exit codes:
//   0 — all archives pass inspection; (optionally) serving on <port>
//   1 — at least one archive failed inspection; details on stderr
//   2 — usage error (missing goreleaser or invalid args)

const { execFileSync } = require('node:child_process');
const crypto = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const { TARGETS, binaryNameFor } = require('../bin/platform-packages');
const { extractTarGz } = require('./archive');

const DIST = path.join(__dirname, '..', 'dist');
const CHECKSUM_FILE = 'checksums.txt';

const REQUIRED_ASSETS = TARGETS.map((target) => target.asset);

// Map process-style platform/arch to the goreleaser asset key.
function keyFromAsset(assetName) {
  return TARGETS.find((target) => target.asset === assetName)?.key || null;
}

// `tar -tzf` reads tar.gz listings with the system tar available on
// every GitHub-hosted runner. Passing a basename from the archive's
// directory avoids GNU tar treating a Windows drive letter as a host.
async function listArchive(archivePath) {
  const out = execFileSync('tar', ['-tzf', path.basename(archivePath)], {
    cwd: path.dirname(archivePath),
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  return out.split('\n').filter((line) => line.length > 0);
}

// Read the SHA-256 of an asset file.
function sha256(filePath) {
  const buf = fs.readFileSync(filePath);
  return crypto.createHash('sha256').update(buf).digest('hex');
}

// Parse one line of goreleaser checksums.txt:
//   "<64-hex>  "  (two spaces, leniency for one or more)
function parseChecksumLine(line) {
  const m = /^([a-f0-9]{64})\s+\*?(.+)$/.exec(line.trim());
  return m ? { digest: m[1], filename: m[2] } : null;
}

async function readChecksumsTxt(distDir) {
  const cp = path.join(distDir, CHECKSUM_FILE);
  if (!fs.existsSync(cp)) return null;
  return fs.readFileSync(cp, 'utf8');
}

// Inspect one archive: list entries, derive the expected binary name,
// assert the binary is present (with the correct .exe suffix on
// Windows) and non-empty.
function expectedBinaryName(key) {
  const [platform] = key.split('-');
  return binaryNameFor(platform);
}

async function inspectArchive({ assetName, archivePath }) {
  const errors = [];
  const st = fs.statSync(archivePath);
  if (!st.isFile() || st.size === 0) {
    errors.push(`archive is not a non-empty file: ${archivePath} (size=${st.size})`);
    return { ok: false, errors };
  }
  const entries = await listArchive(archivePath);
  const key = keyFromAsset(assetName);
  if (!key) {
    errors.push(`asset name ${assetName} does not match any ASSET_MAP entry`);
    return { ok: false, errors };
  }
  const binary = expectedBinaryName(key);
  // Path semantics: goreleaser v2 with wrap_in_directory:false puts
  // the binary at the archive root. Legacy configs with
  // wrap_in_directory:true put it at <archiveRoot>/<binary>. Accept
  // both layouts so existing and current GoReleaser output can be
  // packaged. Reject if the binary is missing from BOTH layouts.
  const expectedRooted = binary;
  const expectedWrapped =
    `${assetName.replace(/\.tar\.gz$/, '')}/${binary}`;
  const binaryEntries = entries.filter((entry) => {
    const normalized = entry.replace(/^\.\//, '');
    return normalized === expectedRooted || normalized === expectedWrapped;
  });
  if (binaryEntries.length !== 1) {
    errors.push(
      `${assetName}: expected exactly one binary at ${expectedRooted} or ${expectedWrapped}; found ${binaryEntries.length}; entries=${JSON.stringify(entries)}`,
    );
  } else {
    // Extract the real archive to confirm the selected binary is a
    // non-empty regular file.
    const tmp = fs.mkdtempSync(path.join(require('node:os').tmpdir(), 'snapshot-inspect-'));
    try {
      extractTarGz(archivePath, tmp);
      const wrappedDir = path.join(tmp, assetName.replace(/\.tar\.gz$/, ''));
      const stagedDir = fs.existsSync(wrappedDir) ? wrappedDir : tmp;
      const binPath = path.join(stagedDir, binary);
      if (!fs.existsSync(binPath)) {
        errors.push(`${assetName}: ${binary} not found after extraction at ${binPath}`);
      } else {
        const bst = fs.statSync(binPath);
        if (!bst.isFile() || bst.size === 0) {
          errors.push(`${assetName}: ${binary} is empty or not a regular file (size=${bst.size})`);
        }
      }
    } finally {
      try { fs.rmSync(tmp, { recursive: true, force: true }); } catch (_) {}
    }
  }

  // Cross-suffix guard: a Windows archive must not contain a
  // non-.exe binary and vice versa. This is the regression that
  // would have caught #250/#252 had #258 already shipped.
  const wrongSuffix = entries.find((e) => {
    const base = path.basename(e);
    const platform = key.split('-')[0];
    if (platform === 'win32') return base === 'nodered-mcp';
    return base === 'nodered-mcp.exe';
  });
  if (wrongSuffix) {
    errors.push(`${assetName}: found wrong-suffix binary ${wrongSuffix} for ${key}`);
  }

  return { ok: errors.length === 0, errors, entries };
}

async function inspectAll({ distDir }) {
  const errors = [];
  const reports = [];
  if (!fs.existsSync(distDir)) {
    errors.push(`dist directory not found at ${distDir}; did goreleaser --snapshot run?`);
    return { ok: false, errors, reports };
  }
  for (const asset of REQUIRED_ASSETS) {
    const archivePath = path.join(distDir, asset);
    if (!fs.existsSync(archivePath)) {
      errors.push(`required archive missing: ${asset} (looked at ${archivePath})`);
      reports.push({ assetName: asset, ok: false, errors: [`file not found`] });
      continue;
    }
    const report = await inspectArchive({ assetName: asset, archivePath });
    reports.push({ assetName: asset, ...report });
    if (!report.ok) errors.push(...report.errors);
  }

  // Verify checksums.txt: every required asset must have a line and
  // the listed digest must match the file on disk.
  const checksumsTxt = await readChecksumsTxt(distDir);
  if (checksumsTxt === null) {
    errors.push(`${CHECKSUM_FILE} is missing from dist`);
  } else {
    const lines = checksumsTxt
      .split(/\r?\n/)
      .map((l) => l.trim())
      .filter((l) => l.length > 0);
    const byName = new Map();
    for (const line of lines) {
      const parsed = parseChecksumLine(line);
      if (!parsed) {
        errors.push(`${CHECKSUM_FILE}: malformed line ${JSON.stringify(line)}`);
        continue;
      }
      byName.set(parsed.filename, parsed.digest);
    }
    for (const asset of REQUIRED_ASSETS) {
      const expected = byName.get(asset);
      if (!expected) {
        errors.push(`${CHECKSUM_FILE}: no entry for ${asset}`);
        continue;
      }
      const assetPath = path.join(distDir, asset);
      if (!fs.existsSync(assetPath)) continue;
      const actual = sha256(assetPath);
      if (actual !== expected) {
        errors.push(
          `${CHECKSUM_FILE}: digest mismatch for ${asset} (expected ${expected}, got ${actual})`,
        );
      }
    }
  }

  return { ok: errors.length === 0, errors, reports };
}

async function runGoreleaserSnapshot() {
  // goreleaser writes artifacts to ./dist/. --snapshot skips the
  // publish step so no GitHub token is required and no release is
  // drafted. --clean wipes ./dist/ first so a partial run does not
  // poison the next one.
  const args = ['release', '--snapshot', '--clean', '--skip=publish'];
  execFileSync('goreleaser', args, {
    stdio: 'inherit',
    cwd: path.join(__dirname, '..'),
  });
}

function parseArgs(argv) {
  const out = { skipBuild: false, distDir: DIST };
  for (let i = 2; i < argv.length; i += 1) {
    const a = argv[i];
    if (a === '--skip-build') out.skipBuild = true;
    else if (a === '--dist-dir') { out.distDir = argv[++i]; }
    else if (a === '--help' || a === '-h') {
      process.stdout.write(
        'usage: release-snapshot.js [--skip-build] [--dist-dir <path>]\n',
      );
      process.exit(0);
    } else {
      process.stderr.write(`unknown arg: ${a}\n`);
      process.exit(2);
    }
  }
  return out;
}

async function main() {
  const args = parseArgs(process.argv);
  if (!args.skipBuild) {
    try {
      await runGoreleaserSnapshot();
    } catch (err) {
      process.stderr.write(`release-snapshot: goreleaser failed: ${err.message}\n`);
      process.exit(1);
    }
  }
  const result = await inspectAll({ distDir: args.distDir });
  for (const r of result.reports) {
    const status = r.ok ? 'ok' : 'fail';
    process.stdout.write(`release-snapshot: ${status} ${r.assetName}\n`);
    if (!r.ok) {
      for (const e of r.errors) process.stderr.write(`  ${e}\n`);
    }
  }
  if (!result.ok) {
    process.stderr.write(
      `release-snapshot: ${result.errors.length} contract violation(s)\n`,
    );
    process.exit(1);
  }
  process.stdout.write(`release-snapshot: ok (${result.reports.length} archives)\n`);
}

if (require.main === module) {
  main().catch((err) => {
    process.stderr.write(`release-snapshot: ${err.message}\n`);
    process.exit(1);
  });
}

module.exports = {
  inspectArchive,
  inspectAll,
  parseChecksumLine,
  expectedBinaryName,
  keyFromAsset,
  REQUIRED_ASSETS,
};

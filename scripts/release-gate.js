#!/usr/bin/env node
'use strict';

// scripts/release-gate.js — verify a tag's GitHub Release assets are
// present and structurally consistent before npm publishes.
//
// This script is the consumer of release.yml's output. It is called
// from .github/workflows/npm.yml after release.yml reports success;
// if any check below fails, the script exits non-zero and npm publish
// is skipped. The npm publisher consumes these exact tarballs to build
// native platform packages, so a missing or malformed release asset
// must block publication.
//
// The same verifyReleaseAssets() function is unit-tested by
// bin/npm_release_gate_test.js with synthetic fixtures, so the
// success and blocked paths have coverage without a live release.
//
// CLI usage (from the workflow):
//   node scripts/release-gate.js <tag> <owner/repo>
// Auth comes from GITHUB_TOKEN (or GH_TOKEN) in the environment.
//
// Exit codes:
//   0 — all checks passed; npm publish may proceed
//   1 — at least one check failed; details printed to stderr
//   2 — usage error (missing args or token)

const { execFileSync } = require('node:child_process');
const https = require('node:https');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const { TARGETS } = require('../bin/platform-packages');

// Issue #257: bin/install-impl.js is retired. The goreleaser asset
// names that the GitHub Release must carry (and that the platform
// packages under npm/<key>/package.json wrap) are derived from
// the same goreleaser `name_template` that lives in .goreleaser.yaml:
//   "{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}"
//
// The list is small and stable; pin it here in lockstep with
// .goreleaser.yaml. scripts/release-snapshot.js (issue #258) carries
// the same list and verifies the on-disk bytes. If a future change
// to goreleaser drifts, the snapshot job fails the release before
// npm publish sees it.
//
// Asset names mapped to the npm package name they feed into; both
// halves of this object are the single source of truth for the
// release gate.
const REQUIRED_ASSETS = TARGETS.map((target) => target.asset);
const REQUIRED_NPM_ASSETS = [...new Set(REQUIRED_ASSETS)];

// Pattern for one line of checksums.txt. Goreleaser emits
// sha256sum-style lines: `<hex64>  <filename>`. Both leading and
// inline `*` are accepted to mirror `sha256sum --strict`'s syntax.
const CHECKSUM_LINE_RE = /^([0-9a-fA-F]{64})\s+\*?(.+)$/;

function verifyReleaseAssets({ release, checksumsContent, assetNames }) {
  const errors = [];

  if (!release) {
    return { ok: false, errors: ['Release not found for tag'] };
  }

  if (release.isDraft === true) {
    errors.push('Release is a draft');
  }

  // Asset set must include checksums, every tarball the npm platform
  // package producer consumes, and at least one SBOM.
  const hasChecksums = assetNames.includes('checksums.txt');
  if (!hasChecksums) {
    errors.push('checksums.txt is missing from the release');
  }

  for (const asset of REQUIRED_NPM_ASSETS) {
    if (!assetNames.includes(asset)) {
      errors.push(`Required npm tarball is missing from the release: ${asset}`);
    }
  }

  const sboms = assetNames.filter((n) => /\.sbom\.json$/.test(n));
  if (sboms.length < 1) {
    errors.push('No SBOM assets found (*.sbom.json)');
  }

  // checksums.txt content check. Only run if the file is present;
  // a missing file already produced an error above.
  if (hasChecksums && typeof checksumsContent === 'string') {
    const lines = checksumsContent
      .split(/\r?\n/)
      .map((l) => l.trim())
      .filter((l) => l.length > 0);

    const malformed = [];
    const unknown = [];
    const seen = new Set();
    const checksumCounts = new Map();
    for (const line of lines) {
      const m = CHECKSUM_LINE_RE.exec(line);
      if (!m) {
        malformed.push(line);
        continue;
      }
      const filename = m[2];
      if (seen.has(filename)) {
        errors.push(`checksums.txt lists ${filename} more than once`);
      }
      seen.add(filename);
      checksumCounts.set(filename, (checksumCounts.get(filename) || 0) + 1);
      if (!assetNames.includes(filename)) {
        unknown.push(filename);
      }
    }
    if (malformed.length > 0) {
      errors.push(`checksums.txt has malformed lines: ${malformed.join('; ')}`);
    }
    if (unknown.length > 0) {
      errors.push(
        `checksums.txt references files not in the release: ${unknown.join('; ')}`,
      );
    }
    for (const asset of REQUIRED_NPM_ASSETS) {
      if ((checksumCounts.get(asset) || 0) === 0) {
        errors.push(`checksums.txt has no entry for required npm tarball: ${asset}`);
      }
    }
  }

  return { ok: errors.length === 0, errors };
}

function downloadToFile(url, dest, token) {
  return new Promise((resolve, reject) => {
    const headers = { 'User-Agent': 'nodered-mcp-release-gate' };
    if (token) headers.Authorization = `Bearer ${token}`;

    const follow = (target, redirectsLeft) => {
      const req = https.get(target, { headers }, (res) => {
        if (
          (res.statusCode === 301 || res.statusCode === 302) &&
          res.headers.location &&
          redirectsLeft > 0
        ) {
          res.resume();
          follow(res.headers.location, redirectsLeft - 1);
          return;
        }
        if (res.statusCode !== 200) {
          res.resume();
          reject(new Error(`HTTP ${res.statusCode} for ${target}`));
          return;
        }
        const out = fs.createWriteStream(dest);
        res.pipe(out);
        out.on('finish', () => out.close(resolve));
        out.on('error', reject);
      });
      req.on('error', reject);
    };
    follow(url, 3);
  });
}

async function main() {
  const [tag, repo] = process.argv.slice(2);
  if (!tag || !repo) {
    process.stderr.write('usage: release-gate.js <tag> <owner/repo>\n');
    process.exit(2);
  }
  const token = process.env.GITHUB_TOKEN || process.env.GH_TOKEN || '';
  if (!token) {
    process.stderr.write('GITHUB_TOKEN (or GH_TOKEN) must be set\n');
    process.exit(2);
  }

  // Fetch release metadata. gh release view with --json avoids
  // interactive output and gives us a structured payload.
  let release;
  try {
    const out = execFileSync(
      'gh',
      [
        'release',
        'view',
        tag,
        '--repo',
        repo,
        '--json',
        'isDraft,isPrerelease,assets',
      ],
      { env: { ...process.env, GH_TOKEN: token }, encoding: 'utf8' },
    );
    release = JSON.parse(out);
  } catch (err) {
    process.stderr.write(`failed to fetch release ${tag}: ${err.message}\n`);
    process.exit(1);
  }
  const assetNames = (release.assets || []).map((a) => a.name);

  // Download checksums.txt (best-effort; missing file surfaces as a
  // verification error rather than a download error).
  let checksumsContent = null;
  if (assetNames.includes('checksums.txt')) {
    const url = `https://github.com/${repo}/releases/download/${tag}/checksums.txt`;
    const tmp = path.join(os.tmpdir(), `checksums-${tag.replace(/[^A-Za-z0-9._-]/g, '_')}-${process.pid}.txt`);
    try {
      await downloadToFile(url, tmp, token);
      checksumsContent = fs.readFileSync(tmp, 'utf8');
    } finally {
      try { fs.unlinkSync(tmp); } catch (_) { /* best-effort cleanup */ }
    }
  }

  const result = verifyReleaseAssets({ release, checksumsContent, assetNames });
  if (!result.ok) {
    for (const e of result.errors) {
      process.stderr.write(`release-gate: ${e}\n`);
    }
    process.exit(1);
  }
  process.stdout.write(
    `release-gate: ${tag} ok (${assetNames.length} assets, ${assetNames.filter((n) => /\.tar\.gz$/.test(n)).length} tarballs)\n`,
  );
}

module.exports = {
  verifyReleaseAssets,
  REQUIRED_NPM_ASSETS,
  CHECKSUM_LINE_RE,
};

if (require.main === module) {
  main().catch((err) => {
    process.stderr.write(`release-gate: ${err.message}\n`);
    process.exit(1);
  });
}

#!/usr/bin/env node
'use strict';

// scripts/release-latest-guard.js — decide whether a publication may
// move the moving `latest` channel.
//
// Issue #349: the three publication channels (GitHub Release, GHCR,
// npm) each decided on their own whether to move `latest`, so a
// prerelease or a re-run of an older tag could take `latest` away
// from a newer stable release. This script holds the single rule the
// three channels share:
//
//   a publication moves `latest` only when its tag is a stable
//   version and is the newest stable version in the repository
//
// Everything else is published either way: the exact-version
// artefacts (GitHub Release assets, the versioned image tag, the npm
// version under `next`) are unaffected. Only the floating channel is
// skipped.
//
// The decision is a pure function of the tag being published and the
// list of tags in the repository, so it is unit-tested without a
// live registry (bin/release_latest_guard_test.js).
//
// CLI usage (from the workflows):
//   node scripts/release-latest-guard.js --tag v1.2.3 [--tags a,b] [--tags-file path]
//
// The tag list defaults to `git tag --list` in the current
// repository. The decision is printed to stdout and, when
// GITHUB_OUTPUT is set, appended to the step outputs.
//
// Exit codes:
//   0 — decision made (promote is either true or false)
//   1 — usage error, or a tag that is not a version (fail loudly:
//       the release itself would be malformed)

const { execFileSync } = require('node:child_process');
const fs = require('node:fs');

// vMAJOR.MINOR.PATCH, an optional -prerelease and an optional
// +build metadata suffix. The leading "v" is the repository's tag
// convention and is optional.
const VERSION_RE = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$/;

function parseVersion(tag) {
  const m = VERSION_RE.exec(String(tag).trim());
  if (!m) return null;
  return {
    major: Number(m[1]),
    minor: Number(m[2]),
    patch: Number(m[3]),
    prerelease: m[4] || null,
    version: `${Number(m[1])}.${Number(m[2])}.${Number(m[3])}`,
  };
}

// Numeric comparison of the release triple. Prerelease ordering
// (semver's own precedence rules) is not needed: prereleases never
// take part in the "newest stable" decision.
function compareVersions(a, b) {
  for (const key of ['major', 'minor', 'patch']) {
    if (a[key] !== b[key]) return a[key] < b[key] ? -1 : 1;
  }
  return 0;
}

function decideLatest({ tag, tags }) {
  const current = parseVersion(tag);
  if (!current) {
    return { promote: false, error: true, reason: `tag ${tag} is not a version` };
  }
  if (current.prerelease) {
    return { promote: false, reason: `${tag} is a prerelease` };
  }

  const stable = (tags || [])
    .map(parseVersion)
    .filter((v) => v && !v.prerelease);
  if (stable.length === 0) {
    return { promote: true, reason: `${tag} is the only stable tag` };
  }

  let newest = stable[0];
  for (const v of stable) {
    if (compareVersions(v, newest) > 0) newest = v;
  }

  const order = compareVersions(current, newest);
  if (order === 0) {
    return { promote: true, reason: `${tag} is the newest stable tag` };
  }
  if (order > 0) {
    return { promote: true, reason: `${tag} is newer than every known stable tag` };
  }
  return {
    promote: false,
    reason: `${tag} is older than the newest stable tag ${newest.version}`,
  };
}

function readTagList(args) {
  const fileAt = args.indexOf('--tags-file');
  if (fileAt !== -1) {
    const file = args[fileAt + 1];
    if (!file) throw new Error('--tags-file needs a path');
    return fs
      .readFileSync(file, 'utf8')
      .split(/\r?\n/)
      .map((l) => l.trim())
      .filter((l) => l.length > 0);
  }

  const inlineAt = args.indexOf('--tags');
  if (inlineAt !== -1) {
    const list = args[inlineAt + 1];
    if (!list) throw new Error('--tags needs a comma-separated list');
    return list
      .split(',')
      .map((t) => t.trim())
      .filter((t) => t.length > 0);
  }

  return execFileSync('git', ['tag', '--list'], { encoding: 'utf8' })
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter((l) => l.length > 0);
}

function main() {
  const args = process.argv.slice(2);
  const tagAt = args.indexOf('--tag');
  if (tagAt === -1 || !args[tagAt + 1]) {
    process.stderr.write('usage: release-latest-guard.js --tag <vX.Y.Z> [--tags a,b] [--tags-file path]\n');
    process.exit(1);
  }
  const tag = args[tagAt + 1];

  let tags;
  try {
    tags = readTagList(args);
  } catch (err) {
    process.stderr.write(`release-latest-guard: ${err.message}\n`);
    process.exit(1);
  }

  const decision = decideLatest({ tag, tags });
  if (decision.error) {
    process.stderr.write(`release-latest-guard: ${decision.reason}\n`);
    process.exit(1);
  }

  const lines = [`promote=${decision.promote}`, `reason=${decision.reason}`];
  process.stdout.write(`${lines.join('\n')}\n`);
  if (process.env.GITHUB_OUTPUT) {
    fs.appendFileSync(process.env.GITHUB_OUTPUT, `${lines.join('\n')}\n`);
  }
}

module.exports = {
  parseVersion,
  compareVersions,
  decideLatest,
};

if (require.main === module) {
  main();
}

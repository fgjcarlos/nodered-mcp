'use strict';

// bin/release_latest_guard_test.js — regression guard for #349.
//
// The three publication channels (GitHub Release, GHCR, npm) each
// decided on their own whether to move the moving `latest` channel,
// so a prerelease or a re-run of an older tag could take `latest`
// away from a newer stable release. scripts/release-latest-guard.js
// holds the single shared rule:
//
//   a publication moves `latest` only when its tag is a stable
//   version and is the newest stable version in the repository
//
// This file exercises the decision function and the CLI contract, so
// the rule cannot silently regress to "always promote".
//
// Run with: node bin/release_latest_guard_test.js
// Exits 0 on success, 1 on any failure. Picked up automatically by
// ci.yml's `npm-wrapper` job (`for t in bin/*_test.js; do node "$t";
// done`).

const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

const {
  parseVersion,
  compareVersions,
  decideLatest,
} = require('../scripts/release-latest-guard.js');

const SCRIPT = path.resolve(__dirname, '..', 'scripts', 'release-latest-guard.js');

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

test('parses a stable version', () => {
  const v = parseVersion('v1.2.3');
  assert.equal(v.major, 1);
  assert.equal(v.minor, 2);
  assert.equal(v.patch, 3);
  assert.equal(v.prerelease, null);
});

test('parses a prerelease version', () => {
  const v = parseVersion('v1.2.3-rc.1');
  assert.equal(v.prerelease, 'rc.1');
});

test('rejects a non-version tag', () => {
  assert.equal(parseVersion('latest'), null);
  assert.equal(parseVersion('v1.2'), null);
});

test('compares versions numerically, not lexicographically', () => {
  assert.equal(compareVersions(parseVersion('v0.7.10'), parseVersion('v0.7.9')), 1);
  assert.equal(compareVersions(parseVersion('v0.7.9'), parseVersion('v0.7.10')), -1);
  assert.equal(compareVersions(parseVersion('v1.0.0'), parseVersion('v1.0.0')), 0);
});

test('promotes the newest stable tag', () => {
  const d = decideLatest({ tag: 'v1.2.0', tags: ['v1.0.0', 'v1.1.0', 'v1.2.0'] });
  assert.equal(d.promote, true);
  assert.match(d.reason, /newest stable tag/);
});

test('does not promote an older stable tag', () => {
  const d = decideLatest({ tag: 'v1.1.0', tags: ['v1.0.0', 'v1.1.0', 'v1.2.0'] });
  assert.equal(d.promote, false);
  assert.match(d.reason, /older than the newest stable tag/);
});

test('does not promote a prerelease', () => {
  const d = decideLatest({ tag: 'v1.3.0-rc.1', tags: ['v1.2.0', 'v1.3.0-rc.1'] });
  assert.equal(d.promote, false);
  assert.match(d.reason, /prerelease/);
});

test('promotes a stable tag that is newer than every known tag', () => {
  const d = decideLatest({ tag: 'v2.0.0', tags: ['v1.0.0', 'v1.9.9'] });
  assert.equal(d.promote, true);
});

test('promotes the only stable tag in the repository', () => {
  const d = decideLatest({ tag: 'v0.1.0', tags: ['v0.1.0'] });
  assert.equal(d.promote, true);
});

test('ignores prerelease tags when picking the newest stable one', () => {
  const d = decideLatest({ tag: 'v1.2.0', tags: ['v1.2.0', 'v1.3.0-rc.1'] });
  assert.equal(d.promote, true);
});

test('fails closed on an unparsable tag', () => {
  const d = decideLatest({ tag: 'not-a-version', tags: ['v1.0.0'] });
  assert.equal(d.promote, false);
  assert.equal(d.error, true);
});

test('CLI prints promote and reason', () => {
  const out = execFileSync(process.execPath, [SCRIPT, '--tag', 'v1.0.0', '--tags', 'v1.0.0,v0.9.0'], {
    encoding: 'utf8',
  });
  assert.match(out, /^promote=true$/m);
  assert.match(out, /^reason=/m);
});

test('CLI reads the tag list from a file', () => {
  const file = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'latest-guard-')), 'tags.txt');
  fs.writeFileSync(file, 'v2.0.0\nv1.0.0\n');
  const out = execFileSync(process.execPath, [SCRIPT, '--tag', 'v1.0.0', '--tags-file', file], {
    encoding: 'utf8',
  });
  assert.match(out, /^promote=false$/m);
});

test('CLI writes the decision to GITHUB_OUTPUT', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'latest-guard-'));
  const outFile = path.join(dir, 'github-output');
  fs.writeFileSync(outFile, '');
  execFileSync(process.execPath, [SCRIPT, '--tag', 'v1.0.0-rc.1', '--tags', 'v1.0.0'], {
    encoding: 'utf8',
    env: { ...process.env, GITHUB_OUTPUT: outFile },
  });
  const written = fs.readFileSync(outFile, 'utf8');
  assert.match(written, /^promote=false$/m);
  assert.match(written, /^reason=/m);
});

test('CLI fails on an unparsable tag', () => {
  let status = 0;
  try {
    execFileSync(process.execPath, [SCRIPT, '--tag', 'nope', '--tags', 'v1.0.0'], {
      encoding: 'utf8',
      stdio: 'pipe',
    });
  } catch (err) {
    status = err.status;
  }
  assert.equal(status, 1);
});

if (failed > 0) {
  console.error(`release_latest_guard_test: ${failed} failed, ${passed} passed`);
  process.exit(1);
}
console.log(`release_latest_guard_test: PASS (${passed} tests)`);

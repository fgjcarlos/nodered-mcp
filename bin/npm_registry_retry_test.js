'use strict';

// bin/npm_registry_retry_test.js — regression guard for #298.
//
// The npm release workflow treats the registry as eventually consistent:
// a successful `npm publish` may be followed by an `npm view` returning
// E404 or an empty value for several seconds. The v0.7.2 release hit this
// and required two manual reruns before all reads resolved. The fix
// moved every post-write read into scripts/wait-for-registry.sh, a tiny
// bash helper that retries with linear backoff. This test exercises the
// helper against synthetic npm-shaped readers so the retry behaviour
// cannot silently regress (helper dropped, backoff removed, exit code
// changed, etc.).
//
// Run with: node bin/npm_registry_retry_test.js
// Picked up automatically by ci.yml's `for t in bin/*_test.js` loop.

const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const helper = path.resolve(__dirname, '..', 'scripts', 'wait-for-registry.sh');
assert.ok(fs.existsSync(helper), `missing helper at ${helper}`);

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

// Build a one-shot "fake npm view" script whose stdout mirrors what
// npmjs would emit for the requested field, with a configurable
// failure schedule so each test can express "empty for the first N
// calls, then return VALUE forever". The script reads its schedule
// from $RETRY_SCRIPT_SCHEDULE = "fail:<n>" | "value:<v>" lines, and
// exits 0 even on the empty path so the helper's own exit code is
// the only signal (matching the real npm view behaviour: exit 0
// with empty stdout when the field is absent, E404 with stderr when
// the package itself is absent — the helper masks both into "empty
// stdout" for retry purposes, see the helper's comment).
//
// A step of the form "err:<message>" makes the fake exit non-zero
// with that message on stderr, the way a real `npm view` reports a
// registry error (#344).
function makeFakeNpm(schedule) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'wait-for-registry-test-'));
  const script = path.join(dir, 'npm');
  const lines = schedule.map((step, i) => `step${i}='${step}'`).join('\n');
  const failStep = (step, index) =>
    step.startsWith('err:')
      ? `  ${index}) printf '%s' "${step.slice(4)}" >&2; exit 1;;`
      : `  ${index}) printf '%s' "$step${index - 1}";;`;
  const last = schedule[schedule.length - 1];
  const fallback = last.startsWith('err:')
    ? `  *) printf '%s' "${last.slice(4)}" >&2; exit 1;;`
    : `  *) printf '%s' "$step${schedule.length - 1}";;`;
  fs.writeFileSync(
    script,
    `#!/usr/bin/env bash
# Synthetic npm view: emit the empty string for the first N calls,
# then emit the configured value on every subsequent call. Tracks
# the call count under $STATE_DIR/calls.
set -u
state_dir="$STATE_DIR"
mkdir -p "$state_dir"
calls=$(( $(cat "$state_dir/calls" 2>/dev/null || echo 0) + 1 ))
echo "$calls" > "$state_dir/calls"
${lines}
case "$calls" in
${schedule.map((step, i) => failStep(step, i + 1)).join('\n')}
${fallback}
esac
`,
  );
  fs.chmodSync(script, 0o755);
  return { dir, script };
}

function runHelper(args, env) {
  return spawnSync('bash', [helper, ...args], {
    env: { ...process.env, ...(env || {}) },
    encoding: 'utf8',
    timeout: 60_000,
  });
}

function fakeEnv(scriptPath) {
  return {
    PATH: `${path.dirname(scriptPath)}:${process.env.PATH || ''}`,
    STATE_DIR: path.dirname(scriptPath) + '/state',
  };
}

test('passes on the first non-empty read (no sleep)', () => {
  const { script } = makeFakeNpm(['sha512-abc']);
  const r = runHelper(['--', script, 'dist.integrity'], fakeEnv(script));
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}\nstderr: ${r.stderr}`);
  assert.equal(r.stdout, 'sha512-abc');
});

test('retries through stale empty reads and resolves once the value appears', () => {
  // Four empty reads, then the integrity value on the 5th attempt.
  const { dir, script } = makeFakeNpm(['', '', '', '', 'sha512-late']);
  const r = runHelper(
    ['--attempts', '5', '--base-sleep', '1', '--', script, 'dist.integrity'],
    fakeEnv(script),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}\nstderr: ${r.stderr}`);
  assert.equal(r.stdout, 'sha512-late');
  // Backoff sleeps 1+2+3+4 = 10s of bash `sleep` calls; this test
  // runs fast in CI but is bounded so it cannot hang forever.
  assert.match(r.stderr, /attempt 1\/5 empty/);
  assert.match(r.stderr, /attempt 4\/5 empty/);
});

test('fails closed after the retry budget with no value', () => {
  const { dir, script } = makeFakeNpm(['', '', '', '', '', '']);
  const r = runHelper(
    ['--attempts', '3', '--base-sleep', '1', '--', script, 'dist-tags.latest'],
    fakeEnv(script),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}\nstderr: ${r.stderr}`);
  assert.match(r.stderr, /gave up after 3 attempts/);
  assert.equal(r.stdout, '');
});

// #344: after `npm dist-tag add`, the registry can answer with the
// previous version of the tag. That answer is non-empty, so the
// "first non-empty value wins" rule accepted it and the caller's own
// comparison failed without a retry. `--expect` moves the comparison
// inside the loop: a stale value is retried like an empty one.
test('retries a stale value until the expected value appears', () => {
  const { dir, script } = makeFakeNpm(['0.7.2', '0.7.3']);
  const r = runHelper(
    ['--expect', '0.7.3', '--attempts', '5', '--base-sleep', '1', '--', script, 'dist-tags.latest'],
    fakeEnv(script),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}\nstderr: ${r.stderr}`);
  assert.equal(r.stdout, '0.7.3');
  assert.match(r.stderr, /stale value '0\.7\.2'/);
});

test('fails closed when the value never reaches the expectation', () => {
  const { dir, script } = makeFakeNpm(['0.7.2']);
  const r = runHelper(
    ['--expect', '0.7.3', '--attempts', '3', '--base-sleep', '1', '--', script, 'dist-tags.latest'],
    fakeEnv(script),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}\nstderr: ${r.stderr}`);
  assert.match(r.stderr, /gave up after 3 attempts/);
  assert.match(r.stderr, /expected '0\.7\.3', last value '0\.7\.2'/);
  assert.equal(r.stdout, '');
});

test('an empty read still reaches the expected value', () => {
  const { dir, script } = makeFakeNpm(['', '', '0.7.3']);
  const r = runHelper(
    ['--expect', '0.7.3', '--attempts', '5', '--base-sleep', '1', '--', script, 'dist-tags.latest'],
    fakeEnv(script),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}\nstderr: ${r.stderr}`);
  assert.equal(r.stdout, '0.7.3');
});

test('reports the real command error instead of swallowing it', () => {
  const { dir, script } = makeFakeNpm(['err:npm ERR! code E404']);
  const r = runHelper(
    ['--attempts', '2', '--base-sleep', '1', '--', script, 'dist-tags.latest'],
    fakeEnv(script),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}\nstderr: ${r.stderr}`);
  assert.match(r.stderr, /npm ERR! code E404/);
});

test('rejects unknown flags with exit 2', () => {
  const r = runHelper(['--bogus']);
  assert.equal(r.status, 2, `expected exit 2, got ${r.status}\nstderr: ${r.stderr}`);
  assert.match(r.stderr, /unknown argument: --bogus/);
});

test('rejects a call with no command after --', () => {
  const r = runHelper(['--']);
  assert.equal(r.status, 2, `expected exit 2, got ${r.status}\nstderr: ${r.stderr}`);
  assert.match(r.stderr, /missing command after --/);
});

test('rejects a call with bare positional arguments (no --)', () => {
  // A typo or a future copy-paste that forgets the `--` separator is
  // a programming error, not a registry problem; the helper must
  // refuse it loudly instead of silently sleeping and retrying the
  // wrong command.
  const r = runHelper(['npm', 'view', 'foo']);
  assert.equal(r.status, 2, `expected exit 2, got ${r.status}\nstderr: ${r.stderr}`);
  assert.match(r.stderr, /unknown argument: npm/);
});

test('rejects non-numeric --attempts', () => {
  const r = runHelper(['--attempts', 'oops', '--', 'true']);
  assert.equal(r.status, 2, `expected exit 2, got ${r.status}\nstderr: ${r.stderr}`);
  assert.match(r.stderr, /--attempts must be a positive integer/);
});

if (failed > 0) {
  console.error(`npm_registry_retry_test: ${failed} failed, ${passed} passed`);
  process.exit(1);
}
console.log(`npm_registry_retry_test: PASS (${passed} tests)`);
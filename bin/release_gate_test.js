'use strict';

// bin/release_gate_test.js — regression guard for #349.
//
// The publication channels used to run on a tag push without any
// dependency on the test suite of the tagged commit: ci.yml never ran
// for a tag (its push trigger was limited to `main`), release.yml
// published the GitHub Release from its own snapshot/smoke chain, and
// docker.yml pushed to GHCR from a single unguarded job.
//
// This file parses the four workflow files as text (the same approach
// bin/npm_workflow_order_test.js uses for npm.yml) and asserts:
//
//   1. ci.yml is callable as a reusable workflow and also runs for
//      tags, so a tagged commit has a visible CI run;
//   2. release.yml and docker.yml have a `gate` job that calls
//      ci.yml, and every job that publishes hangs from that gate;
//   3. with a failing gate, no publication job is reachable — the
//      simulated functional-test failure of the issue's acceptance
//      criteria;
//   4. npm.yml publishes only when the gated release run succeeded;
//   5. the moving `latest` channel follows the shared guard rule.
//
// Run with: node bin/release_gate_test.js
// Exits 0 on success, 1 on any failure. Picked up automatically by
// ci.yml's `npm-wrapper` job (`for t in bin/*_test.js; do node "$t";
// done`).

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const ROOT = path.resolve(__dirname, '..');
const WORKFLOWS = path.join(ROOT, '.github', 'workflows');

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

function readWorkflow(name) {
  return fs.readFileSync(path.join(WORKFLOWS, name), 'utf8');
}

// --- a deliberately small YAML reader -------------------------------------
//
// Only what these workflow files use: a `jobs:` map whose jobs carry
// `needs`, `if` and `uses`. Anything else is ignored.

function parseJobs(text) {
  const lines = text.split(/\r?\n/);
  const jobs = {};
  let inJobs = false;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (!inJobs) {
      if (/^jobs:\s*$/.test(line)) inJobs = true;
      continue;
    }
    if (/^\S/.test(line)) break; // left the jobs block
    const m = /^ {2}([A-Za-z0-9_-]+):\s*$/.exec(line);
    if (!m) continue;

    const body = [];
    for (let j = i + 1; j < lines.length; j++) {
      const l = lines[j];
      if (l.trim() === '') {
        body.push(l);
        continue;
      }
      if (!/^ {4}/.test(l)) break;
      body.push(l);
    }
    jobs[m[1]] = {
      name: m[1],
      body,
      needs: jobNeeds(body),
      if: jobIf(body),
      uses: jobUses(body),
    };
    i = body.length > 0 ? i + body.length : i;
  }
  return jobs;
}

function jobNeeds(body) {
  const needs = [];
  for (let k = 0; k < body.length; k++) {
    const m = /^ {4}needs:\s*(.*)$/.exec(body[k]);
    if (!m) continue;
    const value = m[1].trim();
    if (value === '') {
      for (let n = k + 1; n < body.length; n++) {
        const item = /^ {6}-\s*([A-Za-z0-9_-]+)\s*$/.exec(body[n]);
        if (!item) break;
        needs.push(item[1]);
      }
    } else if (value.startsWith('[')) {
      for (const part of value.replace(/^\[|\]$/g, '').split(',')) {
        const t = part.trim();
        if (t) needs.push(t);
      }
    } else {
      needs.push(value);
    }
  }
  return needs;
}

function jobIf(body) {
  for (let k = 0; k < body.length; k++) {
    const m = /^ {4}if:\s*(.*)$/.exec(body[k]);
    if (!m) continue;
    const value = m[1].trim();
    if (value === '>-' || value === '>' || value === '|') {
      const parts = [];
      for (let n = k + 1; n < body.length; n++) {
        if (!/^ {6}\S/.test(body[n])) break;
        parts.push(body[n].trim());
      }
      return parts.join(' ');
    }
    return value;
  }
  return null;
}

function jobUses(body) {
  for (const line of body) {
    const m = /^ {4}uses:\s*(.*)$/.exec(line);
    if (m) return m[1].trim();
  }
  return null;
}

// Transitive closure of the `needs` edges of a job.
function needsClosure(jobs, name) {
  const seen = new Set();
  const stack = [...(jobs[name] ? jobs[name].needs : [])];
  while (stack.length) {
    const n = stack.pop();
    if (seen.has(n)) continue;
    seen.add(n);
    if (jobs[n]) stack.push(...jobs[n].needs);
  }
  return seen;
}

// Evaluate the conditions these workflows use: the workflow_run
// conclusion and `needs.<job>.result == '<status>'`.
function conditionHolds(ifText, state, context) {
  if (!ifText) return true;
  const conclusion = /github\.event\.workflow_run\.conclusion\s*==\s*'([^']+)'/.exec(ifText);
  if (conclusion && context && context.conclusion && context.conclusion !== conclusion[1]) {
    return false;
  }
  const required = [...ifText.matchAll(/needs\.([A-Za-z0-9_-]+)\.result\s*==\s*'([^']+)'/g)];
  for (const r of required) {
    if ((state[r[1]] || 'skipped') !== r[2]) return false;
  }
  return true;
}

// Which jobs would run, given the outcome of the jobs that already
// ran. Mirrors the GitHub semantics these files rely on: a job whose
// need failed or was skipped does not run, unless its condition says
// always().
function simulate(jobs, initial, context) {
  const state = { ...initial };
  const reached = new Set();
  const names = Object.keys(jobs);
  for (let pass = 0; pass <= names.length; pass++) {
    let progressed = false;
    for (const name of names) {
      if (state[name]) continue;
      const job = jobs[name];
      if (!conditionHolds(job.if, state, context)) {
        state[name] = 'skipped';
        progressed = true;
        continue;
      }
      const needs = job.needs || [];
      if (needs.some((n) => !state[n])) continue; // still waiting
      const blocked = needs.some((n) => state[n] !== 'success');
      const always = /\balways\(\)/.test(job.if || '');
      if (blocked && !always) {
        state[name] = 'skipped';
      } else {
        state[name] = 'success';
        reached.add(name);
      }
      progressed = true;
    }
    if (!progressed) break;
  }
  return reached;
}

// --- the assertions --------------------------------------------------------

const ci = readWorkflow('ci.yml');
const release = readWorkflow('release.yml');
const docker = readWorkflow('docker.yml');
const npm = readWorkflow('npm.yml');
const goreleaser = fs.readFileSync(path.join(ROOT, '.goreleaser.yaml'), 'utf8');

const releaseJobs = parseJobs(release);
const dockerJobs = parseJobs(docker);
const npmJobs = parseJobs(npm);

test('ci.yml is callable as a reusable workflow', () => {
  assert.match(ci, /^ {2}workflow_call:\s*$/m);
});

test('ci.yml runs for tags, so a tagged commit has a CI run', () => {
  assert.match(ci, /^ {4}tags:\s*\n {6}- "v\*"\s*$/m);
});

test('release.yml gates publication on ci.yml', () => {
  const gate = releaseJobs.gate;
  assert.ok(gate, 'release.yml has no gate job');
  assert.match(gate.uses, /\.github\/workflows\/ci\.yml$/);
});

test('every release.yml job hangs from the gate', () => {
  for (const name of Object.keys(releaseJobs)) {
    if (name === 'gate') continue;
    assert.ok(
      needsClosure(releaseJobs, name).has('gate'),
      `${name} does not depend on the gate`,
    );
  }
});

test('docker.yml gates publication on ci.yml', () => {
  const gate = dockerJobs.gate;
  assert.ok(gate, 'docker.yml has no gate job');
  assert.match(gate.uses, /\.github\/workflows\/ci\.yml$/);
});

test('every docker.yml job hangs from the gate', () => {
  for (const name of Object.keys(dockerJobs)) {
    if (name === 'gate') continue;
    assert.ok(
      needsClosure(dockerJobs, name).has('gate'),
      `${name} does not depend on the gate`,
    );
  }
});

test('a failing gate publishes no release channel', () => {
  const reached = simulate(releaseJobs, { gate: 'failure' });
  assert.deepEqual([...reached].filter((n) => n !== 'gate'), []);
});

test('a failing gate publishes no docker channel', () => {
  const reached = simulate(dockerJobs, { gate: 'failure' });
  assert.deepEqual([...reached].filter((n) => n !== 'gate'), []);
});

test('npm.yml publishes only when the gated release run succeeded', () => {
  for (const name of ['publish-platforms', 'publish-main']) {
    const job = npmJobs[name];
    assert.ok(job, `npm.yml has no ${name} job`);
    assert.match(
      job.if || '',
      /github\.event\.workflow_run\.conclusion\s*==\s*'success'/,
      `${name} is not conditioned on the release conclusion`,
    );
  }
});

test('a failed release run publishes no npm channel', () => {
  const reached = simulate(npmJobs, {}, { conclusion: 'failure' });
  assert.deepEqual([...reached], []);
});

test('a failed platform publish does not promote to latest', () => {
  const reached = simulate(
    npmJobs,
    { 'publish-platforms': 'failure', 'publish-main': 'success' },
    { conclusion: 'success' },
  );
  assert.ok(!reached.has('verify-and-promote'));
});

test('npm.yml publishes the SHA of the gated release run', () => {
  assert.match(npm, /ref:\s*\$\{\{\s*github\.event\.workflow_run\.head_sha\s*\}\}/);
});

test('goreleaser never marks a release as latest on its own', () => {
  assert.match(goreleaser, /^\s*make_latest:\s*false\s*$/m);
});

test('the docker latest tag follows the shared guard', () => {
  assert.match(docker, /^\s*flavor:\s*\|?\s*$/m);
  assert.match(docker, /^\s*latest=false\s*$/m);
  assert.match(docker, /type=raw,value=latest,enable=.*promote/);
});

test('the npm latest promotion follows the shared guard', () => {
  assert.match(npm, /promote platform packages to latest/);
  const step = /- name: promote platform packages to latest\n(?: {8}.*\n)*/.exec(npm);
  assert.ok(step, 'npm.yml has no platform promotion step');
  assert.match(step[0], /if:\s*steps\.\w+\.outputs\.promote == 'true'/);
});

test('the release artifact name does not clash with the gate upload', () => {
  // The gate runs ci.yml inside the release run, and ci.yml uploads
  // its own snapshot artifact. Two artifacts cannot share a name in
  // the same run, so the release copy needs its own name.
  const ciArtifact = /name:\s*(release-[a-z-]*dist)/.exec(ci);
  const releaseArtifact = /name:\s*(release-[a-z-]*dist)/.exec(release);
  assert.ok(ciArtifact, 'no snapshot artifact name found in ci.yml');
  assert.ok(releaseArtifact, 'no snapshot artifact name found in release.yml');
  assert.notEqual(ciArtifact[1], releaseArtifact[1]);
});

if (failed > 0) {
  console.error(`release_gate_test: ${failed} failed, ${passed} passed`);
  process.exit(1);
}
console.log(`release_gate_test: PASS (${passed} tests)`);

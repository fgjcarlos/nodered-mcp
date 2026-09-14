'use strict';

// Guard the npm release DAG: exact versions may publish in parallel, but no
// `latest` tag may move until both publisher jobs succeed. The main package
// must remain the final promotion because it is the public wrapper.
//
// Also guard (#298): every post-write `npm view` read must go through
// scripts/wait-for-registry.sh so a stale registry read after a
// successful publish cannot fail the workflow. The four call sites
// are the exact-version integrity checks in publish-platforms and
// publish-main, the seven-package verifier in verify-and-promote, and
// the final `dist-tags.latest` verifier. Without this guard, a future
// refactor that drops `wait-for-registry.sh` from any one of them
// reintroduces the v0.7.2 failure mode.
//
// Run with: node bin/npm_workflow_order_test.js
// Picked up automatically by ci.yml's `for t in bin/*_test.js` loop.

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const workflow = fs.readFileSync(
  path.resolve(__dirname, '..', '.github', 'workflows', 'npm.yml'),
  'utf8',
);

const finalJobMarker = '\n  verify-and-promote:\n';
const finalJobStart = workflow.indexOf(finalJobMarker);
assert.notEqual(finalJobStart, -1, 'npm workflow must define verify-and-promote');

const publisherJobs = workflow.slice(0, finalJobStart);
const finalJob = workflow.slice(finalJobStart);
assert.ok(
  finalJob.includes('needs: [publish-platforms, publish-main]'),
  'verify-and-promote must wait for both publisher jobs',
);
assert.ok(
  finalJob.includes("needs.publish-platforms.result == 'success'") &&
    finalJob.includes("needs.publish-main.result == 'success'"),
  'verify-and-promote must require both publisher jobs to succeed',
);
assert.ok(
  !publisherJobs.includes('npm dist-tag add'),
  'publisher jobs must not move npm dist-tags',
);

const orderedMarkers = [
  '- name: verify all seven packages are live',
  '- name: install and launch canary from npm registry',
  '- name: promote platform packages to latest',
  '- name: promote main package to latest',
  '- name: verify all latest tags resolve to the release',
];
let previous = -1;
for (const marker of orderedMarkers) {
  const current = finalJob.indexOf(marker);
  assert.ok(current > previous, `${marker} is missing or out of order`);
  previous = current;
}

// Regression guard for #298: every post-write `npm view` read must
// route through scripts/wait-for-registry.sh so npmjs propagation
// delay cannot fail a successful publish. We slice each step's run
// block by its `- name:` marker and assert the helper is called
// inside. A naive string match on the whole file would be enough to
// catch a global drop, but slicing per step catches the
// step-specific regression (one step stops using the helper while
// the others keep it).
function stepRunBlock(workflowText, stepName) {
  const idx = workflowText.indexOf(`- name: ${stepName}`);
  assert.notEqual(idx, -1, `npm workflow must define step: ${stepName}`);
  // The next `- name:` marker at the same indent starts the next
  // step; if there is none, the slice runs to the end of the file.
  const tail = workflowText.slice(idx);
  const nextStep = tail.indexOf('\n      - name:');
  return nextStep === -1 ? tail : tail.slice(0, nextStep);
}

const retrySteps = [
  'verify all platform packages are live',
  'verify main package integrity',
  'verify all seven packages are live',
  'verify all latest tags resolve to the release',
];
for (const step of retrySteps) {
  const block = stepRunBlock(workflow, step);
  assert.ok(
    block.includes('scripts/wait-for-registry.sh'),
    `${step} must route its npm view through scripts/wait-for-registry.sh (issue #298)`,
  );
}

console.log('npm_workflow_order_test: PASS');

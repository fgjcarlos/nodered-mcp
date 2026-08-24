'use strict';

// Guard the npm release DAG: exact versions may publish in parallel, but no
// `latest` tag may move until both publisher jobs succeed. The main package
// must remain the final promotion because it is the public wrapper.
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

console.log('npm_workflow_order_test: PASS');

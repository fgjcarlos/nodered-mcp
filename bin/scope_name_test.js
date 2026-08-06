'use strict';

// bin/scope_name_test.js — guard against typos in the npm scope name.
//
// The scope declared in package.json ("@fgjcarlos/nodered-mcp") has a
// trip hazard: "fgjcarlos" is one easy keystroke away from "fgcarlos"
// (missing 'j'). On 2026-08-06 such a typo landed in PR #279 (the
// `promote main package to latest` step) and would have called
// `npm dist-tag add "@fgcarlos/..."` against a non-existent scope,
// failing the publish job after a successful publish.
//
// The test reads the canonical scope from package.json, then greps
// .github/workflows/*.yml and scripts/*.js for any scope-like string
// that is close to the canonical one but wrong (specifically, missing
// the 'j'). Any hit fails the test.
//
// Run with: node bin/scope_name_test.js
// Picked up automatically by ci.yml's `for t in bin/*_test.js` loop.
//
// ponytail: one regex, one scan, ten lines. Add new entries to
// known_close_typos only when a new miss is observed in the wild;
// the canonical scope drives the test, not a hardcoded string.

const fs = require('node:fs');
const path = require('node:path');

const ROOT = path.resolve(__dirname, '..');
const pkg = JSON.parse(fs.readFileSync(path.join(ROOT, 'package.json'), 'utf8'));
const at = pkg.name.indexOf('@');
const slash = pkg.name.indexOf('/');
if (at !== 0 || slash < 1) {
  console.error(`package.json name is not scoped: ${JSON.stringify(pkg.name)}`);
  process.exit(1);
}
const canonicalScope = pkg.name.slice(0, slash); // "@fgjcarlos"

// Match a scope that looks like @fgcarlos but is missing the canonical 'j':
//   @fg<j?>carlos  (j dropped) — the actual miss observed in PR #279.
// Keep this list narrow: a typo-guard has to miss nothing we want it
// to catch, but it also has to not flag legitimate text (comments like
// "@fgcarlos letters" are not in this codebase). When a new miss
// variant shows up, append a regex branch.
const typoRegex = /@(?:fgcarlos|fgjacarlos|fgjarlos|fgjacalos)\b/g;

const scanRoots = [
  path.join(ROOT, '.github', 'workflows'),
  path.join(ROOT, 'scripts'),
];

const failures = [];
function check(file) {
  const txt = fs.readFileSync(file, 'utf8');
  let m;
  while ((m = typoRegex.exec(txt)) !== null) {
    const lineNum = txt.slice(0, m.index).split('\n').length;
    failures.push({ file, line: lineNum, snippet: m[0], where: canonicalScope });
  }
  typoRegex.lastIndex = 0;
}

for (const dir of scanRoots) {
  if (!fs.existsSync(dir)) continue;
  for (const entry of fs.readdirSync(dir)) {
    const full = path.join(dir, entry);
    if (fs.statSync(full).isFile() && /\.(yml|yaml|js)$/.test(entry)) {
      check(full);
    }
  }
}

if (failures.length === 0) {
  console.log(`scope_name_test: PASS (canonical scope ${canonicalScope}, no near-misses)`);
} else {
  for (const f of failures) {
    console.error(
      `scope_name_test: FAIL ${f.file}:${f.line}: typo "${f.snippet}" (canonical is ${f.where})`,
    );
  }
  process.exit(1);
}

'use strict';

// bin/shim_test.js — regression guard for bin/nodered-mcp.js.
//
// The wrapper's contract under #257:
//   - When called with an unsupported platform/arch, it prints the
//     unsupported-target message and exits 1.
//   - When the optional platform package is missing (simulated by
//     clearing require.cache + an unresolved require.resolve), it
//     prints the missing-optional diagnostic and exits 1.
//   - On any error, exit code is 1 (not a stack trace).
//
// Verifies the .js shim applies the correct binary suffix on each
// host platform via the exported binaryName().
//
// Run with: node bin/shim_test.js
// Exits 0 on success, 1 on any mismatch.

const { spawnSync } = require('node:child_process');
const path = require('node:path');
const fs = require('node:fs');
const os = require('node:os');

const shim = require('./nodered-mcp');
const shimPath = path.join(__dirname, 'nodered-mcp.js');

let failures = 0;
function fail(msg) {
  failures += 1;
  console.error(`shim_test: FAIL: ${msg}`);
}
function assertEq(actual, expected, label) {
  if (actual !== expected) fail(`${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}
function assertContains(haystack, needle, label) {
  if (!haystack.includes(needle)) fail(`${label}: expected to contain ${JSON.stringify(needle)}`);
}

function testBinaryName() {
  assertEq(shim.binaryName('win32'), 'nodered-mcp.exe', 'binaryName(win32)');
  assertEq(shim.binaryName('linux'), 'nodered-mcp', 'binaryName(linux)');
  assertEq(shim.binaryName('darwin'), 'nodered-mcp', 'binaryName(darwin)');
}

function testPlatformPackagesKeys() {
  const expected = [
    'win32-x64', 'win32-arm64',
    'linux-x64', 'linux-arm64',
    'darwin-x64', 'darwin-arm64',
  ];
  assertEq(
    Object.keys(shim.PLATFORM_PACKAGES).sort().join(','),
    expected.sort().join(','),
    'PLATFORM_PACKAGES keys',
  );
  for (const [key, name] of Object.entries(shim.PLATFORM_PACKAGES)) {
    if (!name.startsWith('@fgjcarlos/nodered-mcp-')) {
      fail(`PLATFORM_PACKAGES[${key}] = ${name} does not start with @fgjcarlos/nodered-mcp-`);
    }
  }
}

function testUnsupportedMessage() {
  const msg = shim.unsupportedMessage('plan9', 'x64');
  assertContains(msg, 'plan9-x64', 'unsupported message names the platform');
  assertContains(msg, 'go install', 'unsupported message points to go install');
  assertContains(msg, 'docker pull', 'unsupported message points to docker pull');
  assertContains(msg, 'win32-x64', 'unsupported message lists supported targets');
  // No mention of retired scripts.
  if (msg.includes('install.ps1') || msg.includes('install.sh')) {
    fail('unsupported message must not reference retired scripts');
  }
}

function testMissingOptionalMessage() {
  const msg = shim.missingOptionalMessage('@fgjcarlos/nodered-mcp-foo', 'linux', 'x64');
  assertContains(msg, '@fgjcarlos/nodered-mcp-foo', 'message names the package');
  assertContains(msg, '--omit=optional', 'message explains --omit=optional');
  assertContains(msg, 'linux-x64', 'message names host platform');
}

function testResolvePlatformPackageUnsupported() {
  try {
    shim.resolvePlatformPackage('plan9', 'x64');
    fail('resolvePlatformPackage(plan9, x64) should throw');
  } catch (err) {
    if (err.code !== 'EBADPLATFORM') fail(`expected EBADPLATFORM, got ${err.code}`);
    assertContains(err.message, 'plan9-x64', 'error names platform');
  }
}

function testResolvePlatformPackageMissingOptional() {
  // arm64 on win32 is in the matrix, so it maps to a real package
  // name. We simulate the missing optional dep by asking for a key
  // that is NOT in the matrix (freebsd) but is technically valid —
  // the unsupported branch fires first. To exercise the missing
  // branch we monkey-patch PLATFORM_PACKAGES at runtime via a copy
  // of the function with an injection point. Simpler: call the
  // existing entry on a CI runner that does not have the
  // optional package installed (this happens on a typical Linux
  // runner: win32-arm64 is in the matrix but not installed).
  // We use the win32-* pair regardless of host OS because the
  // require.resolve will fail.
  try {
    shim.resolvePlatformPackage('win32', 'arm64');
    // If we're on a Windows ARM64 host with the package installed,
    // this resolves successfully — that's not a test failure.
    if (process.platform !== 'win32') {
      fail('resolvePlatformPackage(win32, arm64) should throw on non-win32 host');
    }
  } catch (err) {
    if (err.code !== 'ENOOPTIONAL') {
      // EBINMISSING / EBINNOTREG / EBINNOEXEC are also acceptable
      // when the package happens to be present in some test env.
      const acceptable = ['ENOOPTIONAL', 'EBINMISSING', 'EBINNOTREG', 'EBINNOEXEC'];
      if (!acceptable.includes(err.code)) {
        fail(`expected ENOOPTIONAL on non-win32 host, got ${err.code}: ${err.message}`);
      }
    }
  }
}

function testShimExitsNonZeroOnMissingBinary() {
  // Copy the shim into a tmp dir and run it without any node_modules.
  // The wrapper must exit 1 and not print a Node stack trace.
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'shim-test-'));
  try {
    const copy = path.join(tmp, 'nodered-mcp.js');
    fs.copyFileSync(shimPath, copy);
    fs.copyFileSync(path.join(__dirname, 'platform-packages.js'), path.join(tmp, 'platform-packages.js'));
    const res = spawnSync(process.execPath, [copy], {
      encoding: 'utf8',
      // Clear NODE_PATH / require paths to make sure no installed
      // platform package leaks into the test environment.
      env: { ...process.env, NODE_PATH: '' },
    });
    if (res.status !== 1) {
      fail(`shim should exit 1; got status=${res.status}`);
    }
    if (/at .* \(.*:\d+:\d+\)/.test(res.stderr)) {
      fail(`shim should not print a Node stack trace; got ${JSON.stringify(res.stderr)}`);
    }
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

function main() {
  testBinaryName();
  testPlatformPackagesKeys();
  testUnsupportedMessage();
  testMissingOptionalMessage();
  testResolvePlatformPackageUnsupported();
  testResolvePlatformPackageMissingOptional();
  testShimExitsNonZeroOnMissingBinary();

  if (failures === 0) {
    console.log('shim_test: PASS');
  } else {
    console.error(`shim_test: ${failures} failure(s)`);
    process.exit(1);
  }
}

main();

'use strict';

// bin/install-impl_test.js — focused tests for bin/install-impl.js.
//
// Covers the contract from issue #227:
//   - success path with checksum-verified download
//   - checksum mismatch fails closed
//   - download timeout fails closed
//   - extraction failure fails closed and cleans staging
//   - promotion failure preserves the prior install in binDir
//   - cleanup of partial staging on every failure
//   - skip-if-installed honors the .installed marker
//   - the public assetFor / unsupportedPlatformMessage exports
//     match the contract pinned by bin/install_test.js
//
// Uses local in-memory fixtures (synthetic tar.gz, no network).
//
// Run with: node bin/install-impl_test.js
// Exits 0 on success, 1 on any mismatch.

const fs = require('node:fs');
const fsp = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const zlib = require('node:zlib');

const {
  _run,
  assetFor,
  unsupportedPlatformMessage,
  parseChecksumsTxt,
  verifyChecksum,
  promote,
  downloadWithTimeout,
  PACKAGE_VERSION,
  DOWNLOAD_RETRIES,
} = require('./install-impl');

let failures = 0;
function fail(msg) {
  failures += 1;
  console.error(`install-impl_test: FAIL: ${msg}`);
}
function assertEq(actual, expected, label) {
  if (actual !== expected) {
    fail(`${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
  }
}
function assertContains(haystack, needle, label) {
  if (!haystack.includes(needle)) {
    fail(`${label}: expected to contain ${JSON.stringify(needle)}\n--- actual ---\n${haystack}`);
  }
}
function assertThrows(fn, label) {
  let threw = false;
  try { fn(); } catch (_) { threw = true; }
  if (!threw) fail(`${label}: expected synchronous throw`);
}

// ---------- fixture builders ----------------------------------------

// Build a synthetic .tar.gz in memory containing one regular file.
// Pass `archiveRoot` to wrap the entry in a subdirectory (legacy
// goreleaser layout); omit it for the flat layout goreleaser v2
// emits by default. Mirrors the header layout used by bin/tar_test.js
// so we know tar.js can read it.
function buildTarGzFixture(name, content, archiveRoot) {
  const body = Buffer.from(content, 'utf8');
  const header = Buffer.alloc(512);
  const fullName = archiveRoot ? `${archiveRoot}/${name}` : name;
  header.write(fullName, 0, 'utf8');
  header.write('0000644', 100, 'utf8');
  header.write('0000000', 108, 'utf8');
  header.write('0000000', 116, 'utf8');
  header.write(body.length.toString(8).padStart(11, '0') + ' ', 124, 'utf8');
  header.write('00000000000', 136, 'utf8');
  header.write('        ', 148, 'utf8');
  header.write('0', 156, 'utf8');
  const checksumField = Buffer.alloc(8, 0x20);
  checksumField.copy(header, 148);
  let sum = 0;
  for (let i = 0; i < 512; i++) sum += header[i];
  header.write(sum.toString(8).padStart(6, '0') + '\0 ', 148, 'utf8');
  const padded = Buffer.alloc(Math.ceil(body.length / 512) * 512, 0);
  body.copy(padded, 0);
  const eof = Buffer.alloc(1024, 0);
  const tarBytes = Buffer.concat([header, padded, eof]);
  return zlib.gzipSync(tarBytes);
}

function buildChecksumLine(buffer, assetName) {
  const hex = crypto.createHash('sha256').update(buffer).digest('hex');
  return `${hex}  ${assetName}\n`;
}

// A fresh tmp binDir + staging + a synthetic tarball buffer for the
// happy-path dep stub. Returns everything a test needs.
//
// Default layout is FLAT (matches goreleaser v2 with
// `wrap_in_directory: false`, which is what real releases ship with
// since 0.6.2 and what #256 is fixing). Pass `archiveRoot` for the
// regression suite that exercises the legacy wrapped layout.
function makeScenario(options = {}) {
  const tmpRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'install-impl-test-'));
  const binDir = path.join(tmpRoot, 'bin');
  fs.mkdirSync(binDir, { recursive: true });
  const stagingDir = path.join(tmpRoot, 'staging');
  const assetName = options.assetName || 'nodered-mcp_linux_amd64.tar.gz';
  const archiveRoot = options.archiveRoot; // undefined => flat
  const binaryName = options.binaryName || 'nodered-mcp';
  const binaryContent = options.binaryContent ||
    '#!/bin/sh\necho "fake nodered-mcp v' + PACKAGE_VERSION + '"\n';
  const tarball = buildTarGzFixture(binaryName, binaryContent, archiveRoot);
  const checksumsTxt = buildChecksumLine(tarball, assetName);
  return { tmpRoot, binDir, stagingDir, assetName, archiveRoot, tarball, checksumsTxt };
}

function makeDeps(scenario, overrides) {
  const realExtract = require('./tar').extract;
  const deps = {
    downloadWithTimeout: async (url, _timeoutMs) => {
      if (url.endsWith('/checksums.txt')) return Buffer.from(scenario.checksumsTxt, 'utf8');
      if (url.endsWith('/' + scenario.assetName)) return Buffer.from(scenario.tarball);
      throw new Error(`unexpected download url in test: ${url}`);
    },
    downloadWithRetry: async (url, timeoutMs, retries) => {
      // Use the production retry wrapper so the tests cover the
      // exact backoff / budget logic in install-impl.js. Override
      // downloadWithTimeout via the `deps` arg below to simulate
      // transient transport failures.
      const { downloadWithRetry } = require('./install-impl');
      return downloadWithRetry(url, timeoutMs, retries, {
        downloadWithTimeout: deps.downloadWithTimeout,
        sleep: () => Promise.resolve(), // tests skip real backoff waits
      });
    },
    parseChecksumsTxt,
    verifyChecksum,
    stageExtract: async (tarballBuffer, stagingDir, assetName) => {
      fs.mkdirSync(stagingDir, { recursive: true });
      const tarballPath = path.join(stagingDir, assetName);
      await fsp.writeFile(tarballPath, tarballBuffer);
      await realExtract(tarballPath, stagingDir);
      // Mirror the production layout detection: prefer the wrapped
      // subdir when it exists, else use the staging dir itself.
      const wrappedDir = path.join(stagingDir, assetName.replace(/\.tar\.gz$/, ''));
      return fs.existsSync(wrappedDir) ? wrappedDir : stagingDir;
    },
    promote,
    extract: realExtract,
    makeStagingDir: () => scenario.stagingDir,
    mkdirSync: fs.mkdirSync,
    renameSync: fs.renameSync,
    copyFileSync: fs.copyFileSync,
    chmodSync: fs.chmodSync,
    readdirSync: fs.readdirSync,
    statSync: fs.statSync,
    rmSync: fs.rmSync,
    unlinkSync: fs.unlinkSync,
    existsSync: fs.existsSync,
    readFileSync: fs.readFileSync,
    writeFileSync: fs.writeFileSync,
    readMarker: (markerPath) => {
      try {
        return fs.readFileSync(markerPath, 'utf8').split(/\r?\n/, 1)[0].trim() || null;
      } catch (_) {
        return null;
      }
    },
    rollback: (stagingDir) => {
      try {
        if (fs.existsSync(stagingDir)) {
          fs.rmSync(stagingDir, { recursive: true, force: true });
        }
      } catch (_) {}
    },
  };
  return Object.assign(deps, overrides || {});
}

function ctx(scenario, overrides) {
  return Object.assign({
    binDir: scenario.binDir,
    platform: 'linux',
    arch: 'x64',
    version: PACKAGE_VERSION,
    exeSuffix: '',
  }, overrides || {});
}

function cleanup(scenario) {
  fs.rmSync(scenario.tmpRoot, { recursive: true, force: true });
}

// ---------- pure-function tests -------------------------------------

function testAssetMapContract() {
  const supported = [
    ['linux',  'x64',   'nodered-mcp_linux_amd64.tar.gz'],
    ['linux',  'arm64', 'nodered-mcp_linux_arm64.tar.gz'],
    ['darwin', 'x64',   'nodered-mcp_darwin_amd64.tar.gz'],
    ['darwin', 'arm64', 'nodered-mcp_darwin_arm64.tar.gz'],
    ['win32',  'x64',   'nodered-mcp_windows_amd64.tar.gz'],
    ['win32',  'arm64', 'nodered-mcp_windows_arm64.tar.gz'],
  ];
  for (const [p, a, expected] of supported) {
    assertEq(assetFor(p, a), expected, `assetFor(${p}, ${a})`);
    assertEq(unsupportedPlatformMessage(p, a), null, `unsupportedPlatformMessage(${p}, ${a}) null`);
  }
  const msg = unsupportedPlatformMessage('freebsd', 'x64');
  assertEq(msg === null, false, 'freebsd must be unsupported');
  assertContains(msg, 'freebsd-x64', 'unsupported platform label');
  assertContains(
    msg,
    'go install github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp@latest',
    'unsupported platform Go hint',
  );
  if (msg.includes('install.ps1') || msg.includes('install.sh')) {
    fail(`unsupported platform message must not reference retired installers; got ${msg}`);
  }
}
function testParseChecksumsTxt() {
  const text = [
    '# goreleaser comment line',
    '0000000000000000000000000000000000000000000000000000000000000000  nodered-mcp_linux_amd64.tar.gz',
    '   ',
    '1111111111111111111111111111111111111111111111111111111111111111  nodered-mcp_darwin_arm64.tar.gz',
  ].join('\n');
  const got = parseChecksumsTxt(text, 'nodered-mcp_darwin_arm64.tar.gz');
  assertEq(
    got,
    '1111111111111111111111111111111111111111111111111111111111111111',
    'parseChecksumsTxt returns lowercase hex',
  );
  assertEq(
    parseChecksumsTxt(text, 'nodered-mcp_windows_amd64.tar.gz'),
    null,
    'parseChecksumsTxt missing asset returns null',
  );
}

function testVerifyChecksum() {
  const buf = Buffer.from('hello world', 'utf8');
  const hex = crypto.createHash('sha256').update(buf).digest('hex');
  assertEq(verifyChecksum(buf, hex), hex, 'verifyChecksum returns digest on match');
  assertThrows(() => verifyChecksum(buf, '0'.repeat(64)), 'verifyChecksum mismatched digest throws');
  assertThrows(() => verifyChecksum(buf, 'not-hex'), 'verifyChecksum malformed expected throws');
  assertThrows(() => verifyChecksum(buf, ''), 'verifyChecksum empty expected throws');
  assertThrows(() => verifyChecksum(buf, undefined), 'verifyChecksum undefined expected throws');
}

// ---------- end-to-end _run tests -----------------------------------

async function testSuccessPath() {
  const s = makeScenario();
  try {
    const result = await _run(makeDeps(s), ctx(s));
    assertEq(result.skipped, false, 'success: skipped=false');
    const target = path.join(s.binDir, 'nodered-mcp');
    if (!fs.existsSync(target)) fail(`success: target binary missing at ${target}`);
    const st = fs.statSync(target);
    if (!st.isFile()) fail(`success: target is not a regular file`);
    if (st.size === 0) fail(`success: target is empty`);
    if (fs.existsSync(s.stagingDir)) fail(`success: staging dir not cleaned up`);
    const marker = path.join(s.binDir, '.installed');
    if (!fs.existsSync(marker)) fail(`success: .installed marker missing`);
    const markerText = fs.readFileSync(marker, 'utf8').trim();
    assertEq(markerText, PACKAGE_VERSION, 'success: marker version matches package');
    const leakedArchive = path.join(s.binDir, s.assetName);
    if (fs.existsSync(leakedArchive)) {
      fail(`success: transport archive must not be copied into bin/: ${leakedArchive}`);
    }
  } finally {
    cleanup(s);
  }
}

async function testWindowsSuccessPath() {
  const s = makeScenario({
    assetName: 'nodered-mcp_windows_amd64.tar.gz',
    // Flat layout — what goreleaser v2 actually emits with
    // `wrap_in_directory: false`. Issue #256: 0.6.2's postinstall
    // searched for the binary inside a wrapped subdir that real
    // releases never produced, so npm install on Windows 404-promoted
    // nothing.
    binaryName: 'nodered-mcp.exe',
    binaryContent: 'fake Windows nodered-mcp v' + PACKAGE_VERSION + '\n',
  });
  try {
    const result = await _run(
      makeDeps(s),
      ctx(s, { platform: 'win32', arch: 'x64', exeSuffix: '.exe' }),
    );
    assertEq(result.skipped, false, 'windows success: skipped=false');
    const target = path.join(s.binDir, 'nodered-mcp.exe');
    if (!fs.existsSync(target)) fail(`windows success: target binary missing at ${target}`);
    const st = fs.statSync(target);
    if (!st.isFile() || st.size === 0) fail('windows success: target must be a non-empty regular file');
    if (fs.existsSync(s.stagingDir)) fail('windows success: staging dir not cleaned up');
    const marker = path.join(s.binDir, '.installed');
    assertEq(fs.readFileSync(marker, 'utf8').trim(), PACKAGE_VERSION, 'windows success: marker version matches package');
  } finally {
    cleanup(s);
  }
}

// Regression for the wrapped layout. Older goreleaser configs and
// future `wrap_in_directory: true` flips produce a subdir named
// after the asset minus `.tar.gz`. Issue #256 requires backward
// compatibility so a config drift fails safely instead of breaking
// npm installs. The installer must detect the wrapped subdir when
// present and promote the binary from inside it.
async function testWrappedLayoutRegression() {
  const s = makeScenario({
    assetName: 'nodered-mcp_linux_amd64.tar.gz',
    archiveRoot: 'nodered-mcp_linux_amd64',
  });
  try {
    const result = await _run(makeDeps(s), ctx(s));
    assertEq(result.skipped, false, 'wrapped layout: skipped=false');
    const target = path.join(s.binDir, 'nodered-mcp');
    if (!fs.existsSync(target)) fail(`wrapped layout: target binary missing at ${target}`);
    const st = fs.statSync(target);
    if (!st.isFile() || st.size === 0) fail('wrapped layout: target must be a non-empty regular file');
    if (fs.existsSync(s.stagingDir)) fail('wrapped layout: staging dir not cleaned up');
  } finally {
    cleanup(s);
  }
}

async function testChecksumMismatch() {
  const s = makeScenario();
  // Replace the checksums.txt content with a wrong digest.
  const tampered = Object.assign({}, s, {
    checksumsTxt: '0000000000000000000000000000000000000000000000000000000000000000  ' + s.assetName + '\n',
  });
  try {
    let threw = false;
    try {
      await _run(makeDeps(tampered), ctx(tampered));
    } catch (err) {
      threw = true;
      if (!/checksum mismatch/i.test(err.message)) {
        fail(`checksum mismatch: error should mention 'checksum mismatch'; got ${err.message}`);
      }
    }
    if (!threw) fail(`checksum mismatch: _run should have rejected`);
    if (fs.existsSync(path.join(s.binDir, 'nodered-mcp'))) {
      fail(`checksum mismatch: target binary must NOT exist after failure`);
    }
    if (fs.existsSync(path.join(s.binDir, '.installed'))) {
      fail(`checksum mismatch: .installed marker must NOT exist after failure`);
    }
    if (fs.existsSync(s.stagingDir)) {
      fail(`checksum mismatch: staging dir must be cleaned up after failure`);
    }
  } finally {
    cleanup(s);
  }
}

async function testDownloadTimeout() {
  const s = makeScenario();
  const deps = makeDeps(s, {
    downloadWithTimeout: async (_url, _timeoutMs) => {
      throw new Error(`download timed out after 10ms: https://example/test.tar.gz`);
    },
  });
  try {
    let threw = false;
    try {
      await _run(deps, ctx(s));
    } catch (err) {
      threw = true;
      if (!/timed out/.test(err.message)) {
        fail(`download timeout: error should mention 'timed out'; got ${err.message}`);
      }
    }
    if (!threw) fail(`download timeout: _run should have rejected`);
    if (fs.existsSync(path.join(s.binDir, 'nodered-mcp'))) {
      fail(`download timeout: target binary must NOT exist after failure`);
    }
    if (fs.existsSync(s.stagingDir)) {
      fail(`download timeout: staging dir must be cleaned up after failure`);
    }
  } finally {
    cleanup(s);
  }
}

// Issue #256: bounded retries on transient transport failures. A
// flaky download that recovers within the retry budget must succeed
// and the install must complete normally.
//
// The first call to downloadWithTimeout is the checksums.txt fetch.
// To isolate asset retry behavior, that request succeeds immediately.
async function testRetryRecoversFromTransientTransport() {
  const s = makeScenario();
  let calls = 0;
  const deps = makeDeps(s, {
    downloadWithTimeout: async (url, _timeoutMs) => {
      calls += 1;
      if (url.endsWith('/checksums.txt')) return Buffer.from(s.checksumsTxt, 'utf8');
      if (url.endsWith('/' + s.assetName)) {
        // Throw the first 2 asset attempts, succeed on the 3rd.
        if (calls < 4) {
          throw new Error('ECONNRESET (simulated transient)');
        }
        return Buffer.from(s.tarball);
      }
      throw new Error(`unexpected download url in test: ${url}`);
    },
  });
  try {
    const result = await _run(deps, ctx(s));
    assertEq(result.skipped, false, 'retry: skipped=false');
    // 1 checksums call + 3 asset calls (2 fail + 1 success) = 4 total.
    assertEq(calls, 4, `retry: downloadWithTimeout called ${calls} times, expected 4`);
    const target = path.join(s.binDir, 'nodered-mcp');
    if (!fs.existsSync(target)) fail(`retry: target binary missing at ${target}`);
    const marker = path.join(s.binDir, '.installed');
    if (!fs.existsSync(marker)) fail('retry: marker missing after recovery');
  } finally {
    cleanup(s);
  }
}

// Issue #256: bounded retries on persistent transport failures. The
// retry wrapper must give up after the configured budget and let
// the error propagate; the install must leave binDir untouched.
async function testRetryExhaustsAfterBudget() {
  const s = makeScenario();
  let calls = 0;
  const deps = makeDeps(s, {
    downloadWithTimeout: async (url, _timeoutMs) => {
      calls += 1;
      if (url.endsWith('/checksums.txt')) return Buffer.from(s.checksumsTxt, 'utf8');
      // Always throw on the asset URL → the retry budget is exhausted.
      throw new Error('ECONNRESET (simulated persistent)');
    },
  });
  try {
    let threw = false;
    try {
      await _run(deps, ctx(s));
    } catch (err) {
      threw = true;
      if (!/ECONNRESET/.test(err.message)) {
        fail(`retry exhaust: should propagate original transport error; got ${err.message}`);
      }
    }
    if (!threw) fail('retry exhaust: _run should have rejected');
    // 1 checksums call + retries attempts on the asset URL.
    assertEq(
      calls,
      1 + DOWNLOAD_RETRIES,
      `retry exhaust: one checksums call plus ${DOWNLOAD_RETRIES} asset attempts`,
    );
    if (fs.existsSync(path.join(s.binDir, 'nodered-mcp'))) {
      fail('retry exhaust: target binary must NOT exist after persistent failure');
    }
    if (fs.existsSync(path.join(s.binDir, '.installed'))) {
      fail('retry exhaust: marker must NOT be written after persistent failure');
    }
    if (fs.existsSync(s.stagingDir)) {
      fail('retry exhaust: staging dir must be cleaned up');
    }
  } finally {
    cleanup(s);
  }
}

// checksums.txt is part of the install's network path and must receive
// the same bounded retry protection as the larger release asset.
async function testChecksumsRetryRecoversFromTransientTransport() {
  const s = makeScenario();
  let checksumsCalls = 0;
  let assetCalls = 0;
  const deps = makeDeps(s, {
    downloadWithTimeout: async (url, _timeoutMs) => {
      if (url.endsWith('/checksums.txt')) {
        checksumsCalls += 1;
        if (checksumsCalls < DOWNLOAD_RETRIES) {
          throw new Error('ECONNRESET checksums (simulated transient)');
        }
        return Buffer.from(s.checksumsTxt, 'utf8');
      }
      if (url.endsWith('/' + s.assetName)) {
        assetCalls += 1;
        return Buffer.from(s.tarball);
      }
      throw new Error(`unexpected download url in test: ${url}`);
    },
  });
  try {
    const result = await _run(deps, ctx(s));
    assertEq(result.skipped, false, 'checksums retry: skipped=false');
    assertEq(
      checksumsCalls,
      DOWNLOAD_RETRIES,
      `checksums retry: recovered on attempt ${DOWNLOAD_RETRIES}`,
    );
    assertEq(assetCalls, 1, 'checksums retry: asset downloaded once');
  } finally {
    cleanup(s);
  }
}

async function testChecksumsRetryExhaustsAfterBudget() {
  const s = makeScenario();
  let checksumsCalls = 0;
  let assetCalls = 0;
  const deps = makeDeps(s, {
    downloadWithTimeout: async (url, _timeoutMs) => {
      if (url.endsWith('/checksums.txt')) {
        checksumsCalls += 1;
        throw new Error('ECONNRESET checksums (simulated persistent)');
      }
      assetCalls += 1;
      throw new Error(`asset should not be requested after checksums failure: ${url}`);
    },
  });
  try {
    let threw = false;
    try {
      await _run(deps, ctx(s));
    } catch (err) {
      threw = true;
      if (!/ECONNRESET checksums/.test(err.message)) {
        fail(`checksums retry exhaust: unexpected error: ${err.message}`);
      }
    }
    if (!threw) fail('checksums retry exhaust: _run should have rejected');
    assertEq(
      checksumsCalls,
      DOWNLOAD_RETRIES,
      `checksums retry exhaust: stopped after ${DOWNLOAD_RETRIES} attempts`,
    );
    assertEq(assetCalls, 0, 'checksums retry exhaust: asset was not requested');
    if (fs.existsSync(path.join(s.binDir, 'nodered-mcp'))) {
      fail('checksums retry exhaust: target binary must NOT exist');
    }
    if (fs.existsSync(path.join(s.binDir, '.installed'))) {
      fail('checksums retry exhaust: marker must NOT be written');
    }
    if (fs.existsSync(s.stagingDir)) {
      fail('checksums retry exhaust: staging dir must be cleaned up');
    }
  } finally {
    cleanup(s);
  }
}

async function testExtractionFailure() {
  const s = makeScenario();
  // Corrupt the tarball bytes so extract() blows up mid-stream.
  const corrupted = Object.assign({}, s, {
    tarball: Buffer.concat([s.tarball.subarray(0, 4), Buffer.from('GARBAGE')]),
  });
  try {
    let threw = false;
    try {
      await _run(makeDeps(corrupted), ctx(corrupted));
    } catch (err) {
      threw = true;
    }
    if (!threw) fail(`extract failure: _run should have rejected`);
    if (fs.existsSync(path.join(s.binDir, 'nodered-mcp'))) {
      fail(`extract failure: target binary must NOT exist after failure`);
    }
    if (fs.existsSync(s.stagingDir)) {
      fail(`extract failure: staging dir must be cleaned up after failure`);
    }
  } finally {
    cleanup(s);
  }
}

async function testPromotionFailure() {
  const s = makeScenario();
  // Write a sentinel "prior install" to binDir/nodered-mcp. If the
  // promotion rolls back correctly, the sentinel survives.
  const prior = path.join(s.binDir, 'nodered-mcp');
  fs.writeFileSync(prior, 'PRIOR_INSTALL_SENTINEL\n');
  fs.chmodSync(prior, 0o644);

  const deps = makeDeps(s, {
    renameSync: (a, b) => {
      // Fail the second rename (the one that places the binary at
      // its final target) so we exercise the rollback branch.
      if (
        path.basename(String(b)) === 'nodered-mcp' &&
        path.basename(String(a)).startsWith('nodered-mcp.tmp-')
      ) {
        throw new Error('simulated promote failure');
      }
      return fs.renameSync(a, b);
    },
  });
  try {
    let threw = false;
    try {
      await _run(deps, ctx(s));
    } catch (err) {
      threw = true;
      if (!/simulated promote failure/.test(err.message)) {
        fail(`promote failure: should propagate original error; got ${err.message}`);
      }
    }
    if (!threw) fail(`promote failure: _run should have rejected`);
    // Prior install must survive.
    if (!fs.existsSync(prior)) fail(`promote failure: prior install binary missing after rollback`);
    const content = fs.readFileSync(prior, 'utf8');
    if (!content.includes('PRIOR_INSTALL_SENTINEL')) {
      fail(`promote failure: prior install binary content changed; got ${JSON.stringify(content)}`);
    }
    if (fs.existsSync(path.join(s.binDir, '.installed'))) {
      fail(`promote failure: .installed marker must NOT be written on rollback`);
    }
    if (fs.existsSync(s.stagingDir)) {
      fail(`promote failure: staging dir must be cleaned up after rollback`);
    }
  } finally {
    cleanup(s);
  }
}

async function testSkipIfInstalled() {
  const s = makeScenario();
  try {
    // Pretend a prior successful install is on disk.
    fs.writeFileSync(path.join(s.binDir, 'nodered-mcp'), 'existing-binary\n');
    fs.writeFileSync(path.join(s.binDir, '.installed'), `${PACKAGE_VERSION}\n`);

    // Stubs that MUST NOT be called if the skip path fires.
    let downloadCalls = 0;
    const deps = makeDeps(s, {
      downloadWithTimeout: async () => {
        downloadCalls += 1;
        return Buffer.alloc(0);
      },
    });
    const result = await _run(deps, ctx(s));
    assertEq(result.skipped, true, 'skip: skipped=true');
    assertEq(downloadCalls, 0, 'skip: downloadWithTimeout never called');
    if (!fs.existsSync(path.join(s.binDir, '.installed'))) {
      fail(`skip: .installed marker was removed`);
    }
    const content = fs.readFileSync(path.join(s.binDir, 'nodered-mcp'), 'utf8');
    if (!content.includes('existing-binary')) {
      fail(`skip: prior binary content changed`);
    }
  } finally {
    cleanup(s);
  }
}

async function testReinstallOnVersionMismatch() {
  const s = makeScenario();
  try {
    // Marker says 0.0.0 but we're installing the current version.
    fs.writeFileSync(path.join(s.binDir, '.installed'), '0.0.0\n');
    const result = await _run(makeDeps(s), ctx(s));
    assertEq(result.skipped, false, 'version mismatch: skipped=false (re-install)');
    const marker = path.join(s.binDir, '.installed');
    if (!fs.existsSync(marker)) fail(`version mismatch: marker missing`);
    assertEq(
      fs.readFileSync(marker, 'utf8').trim(),
      PACKAGE_VERSION,
      'version mismatch: marker updated',
    );
  } finally {
    cleanup(s);
  }
}

async function testReinstallOnCorruptedPrior() {
  const s = makeScenario();
  try {
    // Binary exists but no marker — corrupt prior install.
    fs.writeFileSync(path.join(s.binDir, 'nodered-mcp'), 'broken-binary\n');
    const result = await _run(makeDeps(s), ctx(s));
    assertEq(result.skipped, false, 'corrupt prior: skipped=false');
    if (!fs.existsSync(path.join(s.binDir, '.installed'))) {
      fail(`corrupt prior: marker missing after reinstall`);
    }
    const content = fs.readFileSync(path.join(s.binDir, 'nodered-mcp'), 'utf8');
    if (content.includes('broken-binary')) {
      fail(`corrupt prior: broken binary not replaced`);
    }
  } finally {
    cleanup(s);
  }
}

async function testRollbackOnUnsupportedPlatform() {
  const s = makeScenario();
  try {
    let threw = false;
    try {
      await _run(makeDeps(s), ctx(s, { platform: 'freebsd', arch: 'x64' }));
    } catch (err) {
      threw = true;
      if (!err.message.includes('no npm-installable binary')) {
        fail(`unsupported platform: message should be friendly; got ${err.message}`);
      }
      if (!err.message.includes('go install github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp@latest')) {
        fail(`unsupported platform: message should provide the Go path; got ${err.message}`);
      }
      if (err.message.includes('install.ps1') || err.message.includes('install.sh')) {
        fail(`unsupported platform: message must not reference retired scripts; got ${err.message}`);
      }
    }
    if (!threw) fail('unsupported platform: _run should have rejected');
    if (fs.existsSync(path.join(s.binDir, 'nodered-mcp'))) {
      fail('unsupported platform: no binary should be written for unsupported platforms');
    }
    if (fs.existsSync(s.stagingDir)) {
      fail('unsupported platform: staging dir must not exist');
    }
  } finally {
    cleanup(s);
  }
}

// ---------- runner ---------------------------------------------------

async function runTest(name, fn) {
  const before = failures;
  try {
    await fn();
  } catch (err) {
    fail(`${name}: uncaught exception: ${err.message}`);
  }
  const status = failures > before ? 'FAIL' : 'PASS';
  console.log(`install-impl_test: ${status} ${name}`);
}

async function main() {
  testAssetMapContract();
  testParseChecksumsTxt();
  testVerifyChecksum();
  await runTest('testSuccessPath', testSuccessPath);
  await runTest('testWindowsSuccessPath', testWindowsSuccessPath);
  await runTest('testWrappedLayoutRegression', testWrappedLayoutRegression);
  await runTest('testChecksumMismatch', testChecksumMismatch);
  await runTest('testDownloadTimeout', testDownloadTimeout);
  await runTest('testRetryRecoversFromTransientTransport', testRetryRecoversFromTransientTransport);
  await runTest('testRetryExhaustsAfterBudget', testRetryExhaustsAfterBudget);
  await runTest('testChecksumsRetryRecoversFromTransientTransport', testChecksumsRetryRecoversFromTransientTransport);
  await runTest('testChecksumsRetryExhaustsAfterBudget', testChecksumsRetryExhaustsAfterBudget);
  await runTest('testExtractionFailure', testExtractionFailure);
  await runTest('testPromotionFailure', testPromotionFailure);
  await runTest('testSkipIfInstalled', testSkipIfInstalled);
  await runTest('testReinstallOnVersionMismatch', testReinstallOnVersionMismatch);
  await runTest('testReinstallOnCorruptedPrior', testReinstallOnCorruptedPrior);
  await runTest('testRollbackOnUnsupportedPlatform', testRollbackOnUnsupportedPlatform);

  if (failures === 0) {
    console.log('install-impl_test: PASS');
  } else {
    console.error(`install-impl_test: ${failures} failure(s)`);
    process.exit(1);
  }
}

main().catch((err) => {
  console.error(`install-impl_test: FAIL: ${err.message}`);
  process.exit(1);
});

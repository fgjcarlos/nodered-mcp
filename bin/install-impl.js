'use strict';

// bin/install-impl.js — checksum-verified, atomic-promotion install
// for the @fgjcarlos/nodered-mcp postinstall hook.
//
// This is the authoritative code path for npm-install installs.
// VERSION is read from package.json at runtime (not hardcoded) so
// the goreleaser before-hook does not need to rewrite us. A
// previous bin/install.js sibling (hand-bumped to match the
// goreleaser tag, with the npm publish workflow gating against
// its VERSION constant) was retired in #242 because the
// goreleaser before-hook never actually rewrote it — the
// exclusion list was a fiction. Today, package.json#version is
// the single source of truth and this file consumes it directly.
//
// Flow:
//   1. Skip if a complete previous install is on disk
//      (target binary + .installed marker whose version matches).
//   2. Fetch checksums.txt from the same release tag with bounded retry.
//   3. Fetch the tarball with a bounded timeout and retry.
//   4. Verify SHA-256 of the tarball against checksums.txt.
//   5. Stage the tarball in os.tmpdir() and extract into a per-run
//      staging directory.
//   6. Verify the extracted binary exists and is non-empty.
//   7. Promote the binary into binDir via a temp file + atomic
//      rename. The prior binary, if any, is replaced atomically;
//      nothing in binDir is half-updated.
//   8. Copy the non-binary extras (README, LICENSE, examples).
//   9. Write the .installed marker only after successful promotion.
//
// On any failure before step 9, the staging directory is removed and
// binDir is left untouched. The prior install (if any) keeps working.

const https = require('node:https');
const fs = require('node:fs');
const fsp = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const { extract } = require('./tar');

const REPO = 'fgjcarlos/nodered-mcp';

// Defaults tolerate a multi-megabyte binary on a slow but progressing
// connection. They are conservative on purpose: a too-aggressive
// timeout strands otherwise-valid installs, and the issue history is
// full of 30s timeouts failing on mobile or proxied links (#256).
// Operators can override per-run via env vars without recompiling.
const DEFAULT_DOWNLOAD_TIMEOUT_MS = 120000;
const DEFAULT_CHECKSUMS_TIMEOUT_MS = 30000;
const DEFAULT_DOWNLOAD_RETRIES = 3;

// Env vars are the override surface. Empty / non-positive-integer
// values fall back to the documented default so a typo cannot
// silently disable the timeout or retry ceiling.
function readPositiveIntEnv(name, fallback) {
  const raw = process.env[name];
  if (raw === undefined || raw === null || raw === '') return fallback;
  const n = Number(raw);
  if (!Number.isInteger(n) || n <= 0) return fallback;
  return n;
}

const DOWNLOAD_TIMEOUT_MS = readPositiveIntEnv(
  'NODERED_MCP_DOWNLOAD_TIMEOUT_MS',
  DEFAULT_DOWNLOAD_TIMEOUT_MS,
);
const CHECKSUMS_TIMEOUT_MS = readPositiveIntEnv(
  'NODERED_MCP_CHECKSUMS_TIMEOUT_MS',
  DEFAULT_CHECKSUMS_TIMEOUT_MS,
);
const DOWNLOAD_RETRIES = readPositiveIntEnv(
  'NODERED_MCP_DOWNLOAD_RETRIES',
  DEFAULT_DOWNLOAD_RETRIES,
);

const PACKAGE_VERSION = require('../package.json').version;

// Test-only release origin override used by the real-artifact CI gate.
// It is deliberately restricted to loopback and CI so normal installs
// can only download from the canonical GitHub Release.
function releaseBaseUrl(version, env = process.env) {
  const canonical =
    `https://github.com/${REPO}/releases/download/v${version}`;
  const override = env.NODERED_MCP_TEST_RELEASE_BASE_URL;
  if (!override) return canonical;
  if (env.CI !== 'true') {
    throw new Error(
      'NODERED_MCP_TEST_RELEASE_BASE_URL is restricted to CI',
    );
  }
  let parsed;
  try {
    parsed = new URL(override);
  } catch (_) {
    throw new Error('NODERED_MCP_TEST_RELEASE_BASE_URL must be a valid URL');
  }
  const loopback = new Set(['127.0.0.1', 'localhost', '::1']);
  if (!['http:', 'https:'].includes(parsed.protocol) ||
      !loopback.has(parsed.hostname)) {
    throw new Error(
      'NODERED_MCP_TEST_RELEASE_BASE_URL must use HTTP(S) loopback',
    );
  }
  return parsed.toString().replace(/\/$/, '');
}

// Goreleaser publishes a .tar.gz for every os/arch combo we support.
// The unknown-combo branch is a defense-in-depth assert: a future
// goreleaser build without a matching key here fails fast during
// install instead of shipping a 404 URL.
const ASSET_MAP = {
  'linux-x64':    'nodered-mcp_linux_amd64.tar.gz',
  'linux-arm64':  'nodered-mcp_linux_arm64.tar.gz',
  'darwin-x64':   'nodered-mcp_darwin_amd64.tar.gz',
  'darwin-arm64': 'nodered-mcp_darwin_arm64.tar.gz',
  'win32-x64':    'nodered-mcp_windows_amd64.tar.gz',
  'win32-arm64':  'nodered-mcp_windows_arm64.tar.gz',
};

function assetFor(platform, arch) {
  return ASSET_MAP[`${platform}-${arch}`] || null;
}

// Every GoReleaser archive has a matching npm entry. Unsupported
// platforms fail before any download and point to the remaining
// supported installation channels without resurrecting retired scripts.
function unsupportedPlatformMessage(platform, arch) {
  if (assetFor(platform, arch)) return null;
  const key = `${platform}-${arch}`;
  return [
    `@fgjcarlos/nodered-mcp: no npm-installable binary for ${key}.`,
    'Use Docker, or install from source with Go:',
    `  go install github.com/${REPO}/cmd/nodered-mcp@latest`,
  ].join('\n');
}

// HTTPS GET that resolves with the full response body as a Buffer, or
// rejects on a wall-clock timeout, non-2xx status, or transport error.
// Follows exactly one redirect (GitHub releases redirect 302 to S3).
function downloadWithTimeout(url, timeoutMs) {
  return new Promise((resolve, reject) => {
    let settled = false;
    let activeRes = null;

    const timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      if (activeRes) activeRes.destroy();
      reject(new Error(`download timed out after ${timeoutMs}ms: ${url}`));
    }, timeoutMs);

    const req = https.get(
      url,
      { headers: { 'User-Agent': 'nodered-mcp-install' } },
      (res) => {
        activeRes = res;
        if (settled) return;

        if (res.statusCode === 301 || res.statusCode === 302) {
          const next = res.headers.location;
          clearTimeout(timer);
          res.resume();
          if (!next || !/^https?:\/\//i.test(next)) {
            settled = true;
            return reject(
              new Error(`redirect from ${url} is not an absolute URL: ${next}`),
            );
          }
          downloadWithTimeout(next, timeoutMs).then(resolve, reject);
          return;
        }

        if (res.statusCode !== 200) {
          settled = true;
          clearTimeout(timer);
          res.resume();
          return reject(new Error(`download ${url}: HTTP ${res.statusCode}`));
        }

        const chunks = [];
        res.on('data', (chunk) => {
          if (!settled) chunks.push(chunk);
        });
        res.on('end', () => {
          if (settled) return;
          settled = true;
          clearTimeout(timer);
          resolve(Buffer.concat(chunks));
        });
        res.on('error', (err) => {
          if (settled) return;
          settled = true;
          clearTimeout(timer);
          reject(err);
        });
      },
    );

    req.on('error', (err) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      reject(err);
    });
  });
}

// Retry a transport call with bounded attempts and exponential backoff.
// Used for downloads that can fail on a transient network blip
// (proxy reset, mobile handoff, GitHub redirect flapping). Checksum
// mismatch is NOT a transport error — it lives behind the download,
// after the bytes are on disk — so this wrapper must NOT be used to
// wrap the verifyChecksum step. Doing so would mask a corrupted
// release asset as a retryable transient and never fail closed.
//
// `deps` is the test seam: production callers pass a default dep
// that includes the real `downloadWithTimeout` and `setTimeout`-based
// `sleep`. Tests pass a stubbed `sleep` to skip real backoff waits
// so the suite finishes in milliseconds.
function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function defaultRetryDeps() {
  return { downloadWithTimeout, sleep };
}

async function downloadWithRetry(url, timeoutMs, retries, deps) {
  const d = deps || defaultRetryDeps();
  const attempt = async (n) => {
    try {
      return await d.downloadWithTimeout(url, timeoutMs);
    } catch (err) {
      if (n >= retries) throw err;
      const backoff = Math.min(2000 * Math.pow(2, n), 30000);
      await d.sleep(backoff);
      return attempt(n + 1);
    }
  };
  return attempt(1);
}

// Goreleaser's checksums.txt is one line per asset:
//   "<64-hex-digest>  <filename>"
// with two spaces. We accept one-or-more spaces to be lenient. Lines
// that do not match the schema (e.g. comments) are skipped. Returns
// the lowercase hex digest for the requested asset, or null if the
// asset is not present.
function parseChecksumsTxt(text, assetName) {
  const lines = String(text).split(/\r?\n/);
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const m = /^([a-fA-F0-9]{64})\s+(\S+)$/.exec(trimmed);
    if (!m) continue;
    if (m[2] === assetName) return m[1].toLowerCase();
  }
  return null;
}

// Verify a buffer's SHA-256 against an expected hex digest. Throws on
// any mismatch or malformed expected value. Returns the lowercase hex
// digest that was actually computed.
function verifyChecksum(buffer, expected) {
  if (typeof expected !== 'string' || !/^[a-f0-9]{64}$/.test(expected)) {
    throw new Error(
      `checksum must be 64 lowercase hex chars; got ${JSON.stringify(expected)}`,
    );
  }
  const actual = crypto.createHash('sha256').update(buffer).digest('hex');
  if (actual !== expected) {
    throw new Error(
      `checksum mismatch for downloaded asset: ` +
        `expected ${expected}, got ${actual}`,
    );
  }
  return actual;
}

// Make a fresh staging directory inside os.tmpdir(). The directory is
// not created on disk; callers do that explicitly.
function makeStagingDir() {
  const id = crypto.randomBytes(8).toString('hex');
  return path.join(os.tmpdir(), `nodered-mcp-install-${id}`);
}

// Layout invariant: goreleaser `name_template`
// (`{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}`) historically produced
// archives whose top-level entry was a subdirectory named after the
// asset minus the `.tar.gz` suffix — e.g. `nodered-mcp_linux_amd64/`.
// goreleaser v2 defaults `archives.wrap_in_directory` to `false`, so
// the same `name_template` now produces a flat archive where the
// binary lives directly in the archive root. Issue #256 ships the
// fix; this code accepts BOTH layouts so a future goreleaser config
// flip fails safely instead of silently breaking npm installs. If
// `.goreleaser.yaml` `name_template` ever changes, the layout
// detection below and `bin/install_message_test.js` must change in
// lockstep.

// Stage the tarball on disk and extract it into the same staging dir.
// Then locate the directory that holds the binary: prefer the wrapped
// subdir (`<staging>/<archiveName>/`) when it exists, else use the
// staging dir itself for the flat layout. Extras (README, LICENSE,
// examples) are copied from whichever directory the binary lives in.
async function stageExtract(tarballBuffer, stagingDir, assetName, deps) {
  deps.mkdirSync(stagingDir, { recursive: true });
  const tarballPath = path.join(stagingDir, assetName);
  await fsp.writeFile(tarballPath, tarballBuffer);
  await deps.extract(tarballPath, stagingDir);
  const wrappedDir = path.join(stagingDir, assetName.replace(/\.tar\.gz$/, ''));
  return deps.existsSync(wrappedDir) ? wrappedDir : stagingDir;
}

// Promote the staged archive into binDir. The binary is moved first
// via a temp filename in binDir so the prior binary is replaced
// atomically (POSIX rename(2); Windows MoveFileEx with REPLACE_EXISTING
// since Node 18). Extras (README, LICENSE, examples) are then copied
// in. A promote-failure-after-binary move restores the prior binary
// so the install either succeeds fully or leaves the previous install
// runnable.
function promote(stagingArchiveDir, binDir, assetName, exeSuffix, deps) {
  const binaryName = `nodered-mcp${exeSuffix}`;
  const stagedBinary = path.join(stagingArchiveDir, binaryName);
  if (!deps.existsSync(stagedBinary)) {
    throw new Error(
      `staged archive is missing the ${binaryName} binary: ${stagedBinary}`,
    );
  }
  const st = deps.statSync(stagedBinary);
  if (!st.isFile() || st.size === 0) {
    throw new Error(
      `staged binary is not a regular file or is empty: ${stagedBinary} (size=${st.size})`,
    );
  }
  if (process.platform !== 'win32') {
    deps.chmodSync(stagedBinary, 0o755);
  }

  deps.mkdirSync(binDir, { recursive: true });
  const finalTarget = path.join(binDir, binaryName);
  const tmpTarget = path.join(
    binDir,
    `${binaryName}.tmp-${process.pid}-${Date.now()}`,
  );

  let binaryPromoted = false;
  let restored = false;
  try {
    deps.renameSync(stagedBinary, tmpTarget);
    try {
      deps.renameSync(tmpTarget, finalTarget);
      binaryPromoted = true;
    } catch (renameErr) {
      try {
        deps.renameSync(tmpTarget, stagedBinary);
        restored = true;
      } catch (_) {
        // Best-effort: tmp file may be left behind, but the prior
        // binary in finalTarget (if any) is untouched.
      }
      throw renameErr;
    }
  } catch (err) {
    if (!binaryPromoted && !restored && deps.existsSync(tmpTarget)) {
      try { deps.unlinkSync(tmpTarget); } catch (_) {}
    }
    throw err;
  }

  // Copy non-binary extras. A failure here is non-fatal: the binary
  // is already in place and runnable, so the install is functional.
  // The staging dir cleanup still removes the staged copies.
  let entries;
  try {
    entries = deps.readdirSync(stagingArchiveDir, { withFileTypes: true });
  } catch (_) {
    return finalTarget;
  }
  for (const entry of entries) {
    // In a flat GoReleaser archive, the downloaded tarball and the
    // extracted files share stagingArchiveDir. Never promote the
    // transport archive itself into the installed package's bin/.
    if (entry.name === binaryName || entry.name === assetName) continue;
    if (!entry.isFile()) continue;
    const src = path.join(stagingArchiveDir, entry.name);
    const dst = path.join(binDir, entry.name);
    try {
      deps.copyFileSync(src, dst);
    } catch (_) {
      // Extras (README, LICENSE, examples) are documentation; the
      // install remains usable without them. Surfacing the error
      // would mask a successful binary install.
    }
  }
  return finalTarget;
}

// Remove the staging directory tree. Best-effort: never throws.
function rollback(stagingDir, deps) {
  try {
    if (deps.existsSync(stagingDir)) {
      deps.rmSync(stagingDir, { recursive: true, force: true });
    }
  } catch (_) {
    // Leave it for the OS to clean /tmp later.
  }
}

// Read the marker file's first line, trimmed. Returns null when the
// marker is missing or unreadable.
function readMarker(markerPath, deps) {
  try {
    return deps.readFileSync(markerPath, 'utf8').split(/\r?\n/, 1)[0].trim() || null;
  } catch (_) {
    return null;
  }
}

// The testable core. `main()` calls this with a real-context default;
// tests call it with a tmp-dir context and stubbed deps.
async function _run(deps, ctx) {
  const { binDir, platform, arch, version, exeSuffix } = ctx;

  const assetName = assetFor(platform, arch);
  if (!assetName) {
    throw new Error(unsupportedPlatformMessage(platform, arch));
  }

  const binaryName = `nodered-mcp${exeSuffix}`;
  const target = path.join(binDir, binaryName);
  const marker = path.join(binDir, '.installed');

  // Skip only when the prior install is complete: both the binary
  // AND the marker exist, AND the marker version matches. A missing
  // marker, missing binary, or stale version triggers a re-install.
  const markerVersion = deps.readMarker(marker);
  if (deps.existsSync(target) && markerVersion === version) {
    return { skipped: true, target };
  }

  const stagingDir = deps.makeStagingDir();
  let archiveDir = null;
  try {
    process.stdout.write(
      `@fgjcarlos/nodered-mcp: downloading ${assetName} for ${platform}-${arch} ...\n`,
    );

    const baseUrl = ctx.releaseBaseUrl || releaseBaseUrl(version);
    const checksumsUrl = `${baseUrl}/checksums.txt`;
    const checksumsTxt = await deps.downloadWithRetry(
      checksumsUrl,
      CHECKSUMS_TIMEOUT_MS,
      DOWNLOAD_RETRIES,
    );
    const expectedHash = deps.parseChecksumsTxt(checksumsTxt, assetName);
    if (!expectedHash) {
      throw new Error(
        `checksums.txt for v${version} has no entry for ${assetName}`,
      );
    }

    const assetUrl = `${baseUrl}/${assetName}`;
    // Retry transport only. Checksum mismatch is verified below
    // against the bytes-on-disk result of this call and is never
    // treated as a retryable condition (see downloadWithRetry).
    const buffer = await deps.downloadWithRetry(
      assetUrl,
      DOWNLOAD_TIMEOUT_MS,
      DOWNLOAD_RETRIES,
    );

    deps.verifyChecksum(buffer, expectedHash);

    archiveDir = await stageExtract(buffer, stagingDir, assetName, deps);

    promote(archiveDir, binDir, assetName, exeSuffix, deps);

    deps.writeFileSync(marker, `${version}\n`);

    rollback(stagingDir, deps);

    process.stdout.write(
      `@fgjcarlos/nodered-mcp: installed v${version}\n`,
    );
    return { skipped: false, target };
  } catch (err) {
    rollback(stagingDir, deps);
    throw err;
  }
}

// Default deps used by the production entry point. Tests pass their
// own deps object into _run.
function defaultDeps() {
  return {
    downloadWithTimeout,
    downloadWithRetry,
    parseChecksumsTxt,
    verifyChecksum,
    extract,
    makeStagingDir,
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
    readMarker,
  };
}

async function main() {
  return _run(defaultDeps(), {
    binDir: __dirname,
    platform: process.platform,
    arch: process.arch,
    version: PACKAGE_VERSION,
    exeSuffix: process.platform === 'win32' ? '.exe' : '',
  });
}

module.exports = {
  main,
  _run,
  defaultDeps,
  assetFor,
  unsupportedPlatformMessage,
  downloadWithTimeout,
  downloadWithRetry,
  parseChecksumsTxt,
  verifyChecksum,
  stageExtract,
  promote,
  rollback,
  readMarker,
  makeStagingDir,
  ASSET_MAP,
  PACKAGE_VERSION,
  REPO,
  releaseBaseUrl,
  DOWNLOAD_TIMEOUT_MS,
  CHECKSUMS_TIMEOUT_MS,
  DOWNLOAD_RETRIES,
  DEFAULT_DOWNLOAD_TIMEOUT_MS,
  DEFAULT_CHECKSUMS_TIMEOUT_MS,
  DEFAULT_DOWNLOAD_RETRIES,
};

if (require.main === module) {
  main().catch((err) => {
    console.error(`@fgjcarlos/nodered-mcp: install failed: ${err.message}`);
    process.exit(1);
  });
}

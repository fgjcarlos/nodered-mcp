'use strict';

// bin/install-impl.js — checksum-verified, atomic-promotion install
// for the @fgjcarlos/nodered-mcp postinstall hook.
//
// This is the authoritative code path for npm-install installs. It
// lives next to bin/install.js on purpose: that file is hand-bumped
// to match the goreleaser tag and is verified by .github/workflows/
// npm.yml before publish. Touching it from a PR is forbidden by the
// repo's exclusion list (#218 / #219). All postinstall logic lives
// here instead, so this file is goreleaser-independent.
//
// VERSION is read from package.json at runtime (not hardcoded) so the
// goreleaser before-hook does not need to rewrite us.
//
// Flow:
//   1. Skip if a complete previous install is on disk
//      (target binary + .installed marker whose version matches).
//   2. Fetch checksums.txt from the same release tag.
//   3. Fetch the tarball with a bounded timeout.
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
const DOWNLOAD_TIMEOUT_MS = 30000;
const CHECKSUMS_TIMEOUT_MS = 10000;
const PACKAGE_VERSION = require('../package.json').version;

// Goreleaser publishes a .tar.gz for every os/arch combo we support.
// The unknown-combo branch is a defense-in-depth assert: a future
// goreleaser build without a matching key here fails fast during
// install instead of shipping a 404 URL.
const ASSET_MAP = {
  'linux-x64':    'nodered-mcp_linux_amd64.tar.gz',
  'linux-arm64':  'nodered-mcp_linux_arm64.tar.gz',
  'darwin-x64':   'nodered-mcp_darwin_amd64.tar.gz',
  'darwin-arm64': 'nodered-mcp_darwin_arm64.tar.gz',
};

function assetFor(platform, arch) {
  return ASSET_MAP[`${platform}-${arch}`] || null;
}

// Windows and unsupported platforms fall through to scripts/install.ps1
// (issue #182 / #192 retired the npm wrapper for Windows). The message
// points at the corrected /main/scripts/install.ps1 URL, not the legacy
// /main/install.ps1 path that #80 had to delete, and tells the user how
// to remove the broken launcher from PATH.
function unsupportedPlatformMessage(platform, arch) {
  if (assetFor(platform, arch)) return null;
  const key = `${platform}-${arch}`;
  return [
    `@fgjcarlos/nodered-mcp: no npm-installable binary for ${key}.`,
    'On Windows, install via scripts/install.ps1:',
    `  irm https://raw.githubusercontent.com/${REPO}/main/scripts/install.ps1 | iex`,
    'If a previous install left a broken npm shim, remove it first:',
    '  npm uninstall -g @fgjcarlos/nodered-mcp',
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
// (`{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}`) produces archives whose
// top-level entry is a subdir named after the asset minus the `.tar.gz`
// suffix — e.g. `nodered-mcp_linux_amd64/`. The extraction below expects
// that subdir; if `.goreleaser.yaml` `name_template` ever changes, this
// code and `bin/install_message_test.js` (which enforces the invariant
// on the goreleaser side) must change in lockstep.

// Stage the tarball on disk and extract it into the same staging dir.
// The goreleaser archive unpacks into a single subdirectory
// (`nodered-mcp_<os>_<arch>/`); we keep that layout intact so the
// promotion step can refer to files by their archive-relative path.
async function stageExtract(tarballBuffer, stagingDir, assetName, deps) {
  deps.mkdirSync(stagingDir, { recursive: true });
  const tarballPath = path.join(stagingDir, assetName);
  await fsp.writeFile(tarballPath, tarballBuffer);
  await deps.extract(tarballPath, stagingDir);
  return path.join(stagingDir, assetName.replace(/\.tar\.gz$/, ''));
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
    if (entry.name === binaryName) continue;
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

    const checksumsUrl =
      `https://github.com/${REPO}/releases/download/v${version}/checksums.txt`;
    const checksumsTxt = await deps.downloadWithTimeout(
      checksumsUrl,
      CHECKSUMS_TIMEOUT_MS,
    );
    const expectedHash = deps.parseChecksumsTxt(checksumsTxt, assetName);
    if (!expectedHash) {
      throw new Error(
        `checksums.txt for v${version} has no entry for ${assetName}`,
      );
    }

    const assetUrl =
      `https://github.com/${REPO}/releases/download/v${version}/${assetName}`;
    const buffer = await deps.downloadWithTimeout(assetUrl, DOWNLOAD_TIMEOUT_MS);

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
};

if (require.main === module) {
  main().catch((err) => {
    console.error(`@fgjcarlos/nodered-mcp: install failed: ${err.message}`);
    process.exit(1);
  });
}

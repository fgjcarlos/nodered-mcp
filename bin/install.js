'use strict';

// bin/install.js — postinstall script for @fgjcarlos/nodered-mcp.
//
// Downloads the platform-appropriate binary tarball from the matching
// GitHub release and unpacks it next to this file. The actual nodered-mcp
// binary is then invoked by bin/nodered-mcp.js via child_process.
//
// We deliberately use only Node stdlib — no npm dependencies. The
// tar extraction lives in bin/tar.js so it can be unit-tested.

const https = require('node:https');
const fs = require('node:fs');
const path = require('node:path');
const { pipeline } = require('node:stream/promises');
const { extract } = require('./tar');

// The release tag this package version corresponds to. Bumped by hand on
// each release; the npm publish workflow runs after the goreleaser push
// so the tarball is guaranteed to exist by the time this script runs in
// a fresh `npm install`.
const VERSION = '0.6.1';
const REPO = 'fgjcarlos/nodered-mcp';

// Map node's process.platform / process.arch to the goreleaser asset
// name. Goreleaser publishes a .tar.gz for every os/arch combo we
// support:
//
//   nodered-mcp_linux_amd64.tar.gz
//   nodered-mcp_linux_arm64.tar.gz
//   nodered-mcp_darwin_amd64.tar.gz
//   nodered-mcp_darwin_arm64.tar.gz
//   nodered-mcp_windows_amd64.tar.gz
//   nodered-mcp_windows_arm64.tar.gz
//
// The unknown-combo branch is a defense-in-depth assert: a future
// `goreleaser` build without a matching asset key here fails fast
// during install instead of shipping a 404 URL.
function assetName() {
  const map = {
    'linux-x64':    'nodered-mcp_linux_amd64.tar.gz',
    'linux-arm64':  'nodered-mcp_linux_arm64.tar.gz',
    'darwin-x64':   'nodered-mcp_darwin_amd64.tar.gz',
    'darwin-arm64': 'nodered-mcp_darwin_arm64.tar.gz',
    'win32-x64':    'nodered-mcp_windows_amd64.tar.gz',
    'win32-arm64':  'nodered-mcp_windows_arm64.tar.gz',
  };
  const key = `${process.platform}-${process.arch}`;
  const name = map[key];
  if (!name) {
    throw new Error(`@fgjcarlos/nodered-mcp: no binary published for ${key}`);
  }
  return name;
}

function download(url, dest) {
  return new Promise((resolve, reject) => {
    const req = https.get(url, { headers: { 'User-Agent': 'nodered-mcp-install' } }, (res) => {
      // GitHub returns 302 to S3; follow one redirect.
      if (res.statusCode === 302 || res.statusCode === 301) {
        return download(res.headers.location, dest).then(resolve, reject);
      }
      if (res.statusCode !== 200) {
        return reject(new Error(`download ${url}: HTTP ${res.statusCode}`));
      }
      const out = fs.createWriteStream(dest);
      pipeline(res, out).then(resolve, reject);
    });
    req.on('error', reject);
  });
}

async function main() {
  const binDir = __dirname;
  const tarball = path.join(binDir, 'nodered-mcp.tar.gz');
  const url = `https://github.com/${REPO}/releases/download/v${VERSION}/${assetName()}`;

  // Skip if a previous install already left the binary in place. The
  // shim below fails fast if it is missing or not executable, which is
  // a clearer signal than re-downloading on every npm install.
  const exeSuffix = process.platform === 'win32' ? '.exe' : '';
  const target = path.join(binDir, `nodered-mcp${exeSuffix}`);
  if (fs.existsSync(target)) {
    return;
  }

  process.stdout.write(`@fgjcarlos/nodered-mcp: downloading ${path.basename(url)} ...\n`);
  await download(url, tarball);
  await extract(tarball, binDir);
  // chmod is a no-op on Windows (only the read-only bit has meaning
  // there), so we skip it to avoid the noise from `fs.chmodSync`
  // when the file was extracted without a writable bit.
  if (process.platform !== 'win32') {
    fs.chmodSync(target, 0o755);
  }
  fs.unlinkSync(tarball);
  process.stdout.write(`@fgjcarlos/nodered-mcp: installed v${VERSION}\n`);
}

main().catch((err) => {
  console.error(`@fgjcarlos/nodered-mcp: install failed: ${err.message}`);
  // Do not leave a partial tarball lying around; the next install
  // would see the marker file and skip re-downloading.
  try { fs.unlinkSync(path.join(__dirname, 'nodered-mcp.tar.gz')); } catch (_) {}
  process.exit(1);
});

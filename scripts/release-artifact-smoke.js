#!/usr/bin/env node
'use strict';

// End-to-end npm smoke for the exact artifacts produced by GoReleaser.
// The workflow downloads dist/ from the snapshot job, then this script:
//   1. packs the real npm package;
//   2. serves checksums.txt and the host archive from loopback;
//   3. installs the tarball through the production postinstall;
//   4. proves the installed native bytes match the snapshot archive;
//   5. launches the npm CLI shim; and
//   6. verifies checksum failure leaves no binary or staging directory.

const { execFile } = require('node:child_process');
const { promisify } = require('node:util');
const crypto = require('node:crypto');
const fs = require('node:fs');
const fsp = require('node:fs/promises');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');

const execFileAsync = promisify(execFile);
const { assetFor } = require('../bin/install-impl');
const { extract } = require('../bin/tar');

const REPO_ROOT = path.join(__dirname, '..');

function sha256(filePath) {
  return crypto
    .createHash('sha256')
    .update(fs.readFileSync(filePath))
    .digest('hex');
}

function stagingDirs() {
  return new Set(
    fs.readdirSync(os.tmpdir())
      .filter((name) => name.startsWith('nodered-mcp-install-')),
  );
}

function assertNoNewStaging(before, label) {
  const leaked = [...stagingDirs()].filter((name) => !before.has(name));
  if (leaked.length > 0) {
    throw new Error(`${label}: leaked staging directories: ${leaked.join(', ')}`);
  }
}

function tamperChecksum(text, assetName) {
  const lines = text.split(/\r?\n/);
  let replaced = false;
  const result = lines.map((line) => {
    const parts = /^([a-fA-F0-9]{64})(\s+)(\S+)$/.exec(line.trim());
    if (!parts || parts[3] !== assetName) return line;
    replaced = true;
    return `${'0'.repeat(64)}${parts[2]}${parts[3]}`;
  });
  if (!replaced) {
    throw new Error(`checksums.txt has no entry for ${assetName}`);
  }
  return result.join('\n');
}

async function startFixtureServer(distDir, assetName) {
  const archivePath = path.join(distDir, assetName);
  const checksumsPath = path.join(distDir, 'checksums.txt');
  if (!fs.existsSync(archivePath) || !fs.existsSync(checksumsPath)) {
    throw new Error(`fixture missing ${assetName} or checksums.txt in ${distDir}`);
  }
  const checksums = fs.readFileSync(checksumsPath, 'utf8');
  const counts = { checksums: 0, archive: 0 };
  let mode = 'happy';

  const server = http.createServer((req, res) => {
    const pathname = new URL(req.url, 'http://127.0.0.1').pathname;
    if (pathname === '/checksums.txt') {
      counts.checksums += 1;
      if (mode === 'happy' && counts.checksums === 1) {
        res.writeHead(503);
        res.end('simulated transient checksum failure');
        return;
      }
      const body = mode === 'checksum-mismatch'
        ? tamperChecksum(checksums, assetName)
        : checksums;
      setTimeout(() => {
        res.writeHead(200, { 'content-type': 'text/plain' });
        res.end(body);
      }, 50);
      return;
    }
    if (pathname === `/${assetName}`) {
      res.writeHead(302, { location: `/files/${assetName}` });
      res.end();
      return;
    }
    if (pathname === `/files/${assetName}`) {
      counts.archive += 1;
      if (mode === 'happy' && counts.archive === 1) {
        res.writeHead(503);
        res.end('simulated transient archive failure');
        return;
      }
      setTimeout(() => {
        res.writeHead(200, { 'content-type': 'application/gzip' });
        fs.createReadStream(archivePath).pipe(res);
      }, 50);
      return;
    }
    res.writeHead(404);
    res.end('not found');
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const address = server.address();
  return {
    baseUrl: `http://127.0.0.1:${address.port}`,
    counts,
    setMode(next) {
      mode = next;
      counts.checksums = 0;
      counts.archive = 0;
    },
    close: () => new Promise((resolve, reject) => {
      server.close((err) => err ? reject(err) : resolve());
    }),
  };
}

function npmCommand() {
  return process.platform === 'win32' ? 'npm.cmd' : 'npm';
}

async function packPackage(packDir) {
  const { stdout } = await execFileAsync(
    npmCommand(),
    ['pack', '--json', '--pack-destination', packDir, REPO_ROOT],
    { cwd: REPO_ROOT, maxBuffer: 10 * 1024 * 1024 },
  );
  const result = JSON.parse(stdout);
  if (!Array.isArray(result) || !result[0] || !result[0].filename) {
    throw new Error(`npm pack returned an unexpected result: ${stdout}`);
  }
  return path.join(packDir, result[0].filename);
}

async function installPacked(tarball, prefix, baseUrl) {
  return execFileAsync(
    npmCommand(),
    [
      '--prefix', prefix,
      'install', '--global',
      '--no-audit', '--no-fund', '--foreground-scripts',
      tarball,
    ],
    {
      cwd: REPO_ROOT,
      env: {
        ...process.env,
        CI: 'true',
        NODERED_MCP_TEST_RELEASE_BASE_URL: baseUrl,
        NODERED_MCP_DOWNLOAD_TIMEOUT_MS: '10000',
        NODERED_MCP_CHECKSUMS_TIMEOUT_MS: '10000',
        NODERED_MCP_DOWNLOAD_RETRIES: '3',
      },
      maxBuffer: 20 * 1024 * 1024,
    },
  );
}

async function packageRoot(prefix) {
  const { stdout } = await execFileAsync(
    npmCommand(),
    ['root', '--global', '--prefix', prefix],
    { maxBuffer: 1024 * 1024 },
  );
  return path.join(stdout.trim(), '@fgjcarlos', 'nodered-mcp');
}

async function expectedBinaryHash(distDir, assetName, binaryName, tmpRoot) {
  const extracted = path.join(tmpRoot, 'expected');
  await fsp.mkdir(extracted, { recursive: true });
  await extract(path.join(distDir, assetName), extracted);
  const wrapped = path.join(
    extracted,
    assetName.replace(/\.tar\.gz$/, ''),
  );
  const archiveRoot = fs.existsSync(wrapped) ? wrapped : extracted;
  const binaryPath = path.join(archiveRoot, binaryName);
  if (!fs.existsSync(binaryPath)) {
    throw new Error(`snapshot archive does not contain ${binaryName}`);
  }
  return sha256(binaryPath);
}

async function main() {
  const distArg = process.argv.indexOf('--dist-dir');
  const distDir = path.resolve(
    distArg >= 0 ? process.argv[distArg + 1] : path.join(REPO_ROOT, 'dist'),
  );
  const assetName = assetFor(process.platform, process.arch);
  if (!assetName) {
    throw new Error(`unsupported smoke platform ${process.platform}-${process.arch}`);
  }
  const binaryName =
    `nodered-mcp${process.platform === 'win32' ? '.exe' : ''}`;
  const tmpRoot = await fsp.mkdtemp(
    path.join(os.tmpdir(), 'nodered-mcp-artifact-smoke-'),
  );
  let fixture;
  try {
    const packDir = path.join(tmpRoot, 'pack');
    await fsp.mkdir(packDir, { recursive: true });
    const tarball = await packPackage(packDir);
    fixture = await startFixtureServer(distDir, assetName);

    const beforeSuccess = stagingDirs();
    const prefix = path.join(tmpRoot, 'prefix');
    await installPacked(tarball, prefix, fixture.baseUrl);
    assertNoNewStaging(beforeSuccess, 'successful install');
    if (fixture.counts.checksums < 2 || fixture.counts.archive < 2) {
      throw new Error(
        `retry/redirect fixture was not exercised: ${JSON.stringify(fixture.counts)}`,
      );
    }

    const root = await packageRoot(prefix);
    const installedBinary = path.join(root, 'bin', binaryName);
    if (!fs.existsSync(installedBinary)) {
      throw new Error(`installed native binary missing: ${installedBinary}`);
    }
    const expectedHash = await expectedBinaryHash(
      distDir,
      assetName,
      binaryName,
      tmpRoot,
    );
    if (sha256(installedBinary) !== expectedHash) {
      throw new Error('installed binary bytes differ from the GoReleaser archive');
    }
    if (fs.existsSync(path.join(root, 'bin', assetName))) {
      throw new Error('transport archive leaked into the installed bin directory');
    }

    const shim = path.join(root, 'bin', 'nodered-mcp.js');
    const { stdout: version } = await execFileAsync(
      process.execPath,
      [shim, 'version'],
      { env: process.env, maxBuffer: 1024 * 1024 },
    );
    if (!version.trim()) {
      throw new Error('npm CLI launched but returned an empty version');
    }

    fixture.setMode('checksum-mismatch');
    const beforeFailure = stagingDirs();
    const failedPrefix = path.join(tmpRoot, 'failed-prefix');
    let rejected = false;
    try {
      await installPacked(tarball, failedPrefix, fixture.baseUrl);
    } catch (err) {
      rejected = true;
      const output = `${err.stdout || ''}\n${err.stderr || ''}`;
      if (!/checksum mismatch/.test(output)) {
        throw new Error(`expected checksum mismatch, got: ${output}`);
      }
    }
    if (!rejected) {
      throw new Error('tampered checksum install unexpectedly succeeded');
    }
    assertNoNewStaging(beforeFailure, 'checksum mismatch');
    const failedRoot = await packageRoot(failedPrefix);
    if (fs.existsSync(path.join(failedRoot, 'bin', binaryName))) {
      throw new Error('checksum mismatch left a native binary installed');
    }

    process.stdout.write(
      `release-artifact-smoke: PASS ${process.platform}-${process.arch} ${version.trim()}\n`,
    );
  } finally {
    if (fixture) await fixture.close();
    await fsp.rm(tmpRoot, { recursive: true, force: true });
  }
}

main().catch((err) => {
  process.stderr.write(`release-artifact-smoke: FAIL: ${err.stack || err.message}\n`);
  process.exit(1);
});

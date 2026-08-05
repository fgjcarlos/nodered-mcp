#!/usr/bin/env node
'use strict';

// End-to-end smoke for the exact GoReleaser snapshot and the npm-native
// distribution introduced in 0.7.0. It builds all six platform packages,
// installs the host package with scripts disabled, compares native bytes,
// launches the real CLI, and verifies the missing-optional error path.

const { execFile } = require('node:child_process');
const { promisify } = require('node:util');
const crypto = require('node:crypto');
const fs = require('node:fs');
const fsp = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');

const execFileAsync = promisify(execFile);
const { TARGETS, targetFor } = require('../bin/platform-packages');
const { buildOne } = require('./build-platform-packages');

const REPO_ROOT = path.join(__dirname, '..');

function npmCommand() {
  return process.platform === 'win32' ? 'npm.cmd' : 'npm';
}

function run(command, args, options = {}) {
  return execFileAsync(command, args, {
    ...options,
    shell: process.platform === 'win32' && command.endsWith('.cmd'),
    maxBuffer: 20 * 1024 * 1024,
  });
}

function runNpm(args, options = {}) {
  return run(npmCommand(), args, options);
}

function sha256(filePath) {
  return crypto.createHash('sha256').update(fs.readFileSync(filePath)).digest('hex');
}

async function packMain(outDir) {
  const { stdout } = await runNpm(
    ['pack', '--json', '--pack-destination', outDir, REPO_ROOT],
    { cwd: REPO_ROOT },
  );
  const packed = JSON.parse(stdout);
  if (!Array.isArray(packed) || !packed[0]?.filename) {
    throw new Error(`npm pack returned an unexpected result: ${stdout}`);
  }
  return path.join(outDir, packed[0].filename);
}

async function expectedBinary(distDir, target, tmpRoot) {
  const out = path.join(tmpRoot, 'expected');
  await fsp.mkdir(out, { recursive: true });
  const archive = path.join(distDir, target.asset);
  await run('tar', ['-xzf', path.basename(archive), '-C', out], {
    cwd: path.dirname(archive),
  });
  const direct = path.join(out, target.binary);
  if (fs.existsSync(direct)) return direct;
  const wrapped = path.join(out, target.asset.replace(/\.tar\.gz$/, ''), target.binary);
  if (fs.existsSync(wrapped)) return wrapped;
  throw new Error(`${target.asset} does not contain ${target.binary}`);
}

async function install(consumer, tarballs) {
  await fsp.mkdir(consumer, { recursive: true });
  await fsp.writeFile(
    path.join(consumer, 'package.json'),
    JSON.stringify({ name: 'nodered-mcp-smoke-consumer', version: '1.0.0', private: true }) + '\n',
  );
  await runNpm(
    ['install', '--ignore-scripts', '--omit=optional', '--no-audit', '--no-fund', ...tarballs],
    { cwd: consumer, env: { ...process.env, CI: 'true' } },
  );
}

async function main() {
  const distIndex = process.argv.indexOf('--dist-dir');
  const distDir = path.resolve(
    distIndex >= 0 ? process.argv[distIndex + 1] : path.join(REPO_ROOT, 'dist'),
  );
  const version = JSON.parse(fs.readFileSync(path.join(REPO_ROOT, 'package.json'), 'utf8')).version;
  const host = targetFor(process.platform, process.arch);
  if (!host) throw new Error(`unsupported smoke platform ${process.platform}-${process.arch}`);

  const tmpRoot = await fsp.mkdtemp(path.join(os.tmpdir(), 'nodered-mcp-artifact-smoke-'));
  try {
    const packagesDir = path.join(tmpRoot, 'packages');
    const produced = [];
    for (const target of TARGETS) {
      produced.push(await buildOne({
        target,
        distDir,
        outDir: packagesDir,
        version,
        licenseSrc: path.join(REPO_ROOT, 'LICENSE'),
      }));
    }
    if (produced.length !== TARGETS.length) {
      throw new Error(`expected ${TARGETS.length} native packages, built ${produced.length}`);
    }
    const hostPackage = produced.find((item) => item.key === host.key);
    const mainTarball = await packMain(packagesDir);

    // --ignore-scripts is intentional: the new channel must work under
    // corporate policies that disable lifecycle scripts. The explicit
    // host tarball is a root dependency, while the main package's other
    // optional dependencies are omitted to keep this test registry-free.
    const consumer = path.join(tmpRoot, 'consumer');
    await install(consumer, [mainTarball, hostPackage.tarball]);
    const mainRoot = path.join(consumer, 'node_modules', '@fgjcarlos', 'nodered-mcp');
    const nativeRoot = path.join(consumer, 'node_modules', '@fgjcarlos', `nodered-mcp-${host.key}`);
    const installedBinary = path.join(nativeRoot, 'bin', host.binary);
    if (!fs.existsSync(installedBinary)) throw new Error(`installed binary missing: ${installedBinary}`);
    const expected = await expectedBinary(distDir, host, tmpRoot);
    if (sha256(installedBinary) !== sha256(expected)) {
      throw new Error('installed native bytes differ from the GoReleaser artifact');
    }

    const shim = path.join(mainRoot, 'bin', 'nodered-mcp.js');
    const launched = await run(process.execPath, [shim, 'version'], {
      cwd: consumer,
      env: process.env,
    });
    const versionOutput = `${launched.stdout || ''}${launched.stderr || ''}`.trim();
    if (!versionOutput) throw new Error('native CLI returned an empty version');

    const missingConsumer = path.join(tmpRoot, 'missing-optional');
    await install(missingConsumer, [mainTarball]);
    const missingShim = path.join(
      missingConsumer,
      'node_modules',
      '@fgjcarlos',
      'nodered-mcp',
      'bin',
      'nodered-mcp.js',
    );
    let missingFailed = false;
    try {
      await run(process.execPath, [missingShim, 'version'], { cwd: missingConsumer });
    } catch (err) {
      missingFailed = true;
      const output = `${err.stdout || ''}\n${err.stderr || ''}`;
      if (!output.includes(host.packageName) || !/omit=optional/.test(output)) {
        throw new Error(`missing optional dependency error is unclear: ${output}`);
      }
    }
    if (!missingFailed) throw new Error('shim unexpectedly ran without its native optional package');

    process.stdout.write(
      `release-artifact-smoke: PASS ${host.key} (${produced.length} packages) ${versionOutput}\n`,
    );
  } finally {
    await fsp.rm(tmpRoot, { recursive: true, force: true });
  }
}

main().catch((err) => {
  process.stderr.write(`release-artifact-smoke: FAIL: ${err.stack || err.message}\n`);
  process.exit(1);
});

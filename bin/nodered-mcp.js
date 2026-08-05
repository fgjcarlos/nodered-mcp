#!/usr/bin/env node
// SPDX-License-Identifier: MIT
//
// bin/nodered-mcp.js — npm-installed entry point.
//
// Resolves the platform-specific package from node_modules and
// re-execs its native binary with the user's argv, stdio, signals,
// and environment. The platform package (one of
// `@fgjcarlos/nodered-mcp-<plat>-<arch>`) is pulled in via
// optionalDependencies so npm's registry integrity replaces the
// old checksum-verified GitHub download.
//
// Error surface (each is a plain stderr message + exit 1; the
// user's first experience of the package is never a Node stack
// trace):
//
//   - unsupported OS/architecture → list the supported matrix
//     and point to go install / Docker
//   - compatible optional package missing → explain --omit=optional,
//     --no-optional, or a broken mirror, and point to the matching
//     package name the resolver looked for
//   - native executable missing or non-executable inside an
//     installed platform package → reinstall that package
//   - child-process startup failure → surface errno from spawn

'use strict';

const { spawn } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const { PLATFORM_PACKAGES, binaryNameFor } = require('./platform-packages');

function binaryName(platform) {
  return binaryNameFor(platform);
}

function unsupportedMessage(platform, arch) {
  const key = `${platform}-${arch}`;
  return [
    `@fgjcarlos/nodered-mcp: no prebuilt binary for ${key}.`,
    'Supported targets: ' + Object.keys(PLATFORM_PACKAGES).join(', ') + '.',
    'Use one of these channels instead:',
    `  go install github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp@latest`,
    `  docker pull ghcr.io/fgjcarlos/nodered-mcp:latest`,
  ].join('\n');
}

function missingOptionalMessage(packageName, platform, arch) {
  return [
    `@fgjcarlos/nodered-mcp: optional dependency ${packageName} is not installed.`,
    `This usually means npm was invoked with --omit=optional, --no-optional,`,
    `or the npm mirror is missing the platform-specific package. Re-run:`,
    `  npm install -g @fgjcarlos/nodered-mcp`,
    `without --omit=optional, or reinstall the platform package directly:`,
    `  npm install -g ${packageName}`,
    `(host: ${platform}-${arch})`,
  ].join('\n');
}

function resolvePlatformPackage(platform, arch) {
  const key = `${platform}-${arch}`;
  const packageName = PLATFORM_PACKAGES[key];
  if (!packageName) {
    const err = new Error(unsupportedMessage(platform, arch));
    err.code = 'EBADPLATFORM';
    throw err;
  }
  let pkgJsonPath;
  try {
    pkgJsonPath = require.resolve(`${packageName}/package.json`);
  } catch (_) {
    const err = new Error(missingOptionalMessage(packageName, platform, arch));
    err.code = 'ENOOPTIONAL';
    throw err;
  }
  const pkgDir = path.dirname(pkgJsonPath);
  const binPath = path.join(pkgDir, 'bin', binaryName(platform));
  try {
    const st = fs.statSync(binPath);
    if (!st.isFile()) {
      const err = new Error(
        `@fgjcarlos/nodered-mcp: native executable inside ${packageName} is not a regular file: ${binPath}`,
      );
      err.code = 'EBINNOTREG';
      throw err;
    }
    // On non-Windows hosts npm preserves the file mode baked into
    // the tarball; on Windows we skip the executable-bit check
    // because the bit is meaningless there.
    if (platform !== 'win32') {
      try {
        fs.accessSync(binPath, fs.constants.X_OK);
      } catch (_) {
        const err = new Error(
          `@fgjcarlos/nodered-mcp: native executable inside ${packageName} is not executable: ${binPath}\n` +
            `Run: chmod +x ${binPath}`,
        );
        err.code = 'EBINNOEXEC';
        throw err;
      }
    }
  } catch (err) {
    if (err.code === 'ENOENT') {
      const e = new Error(
        `@fgjcarlos/nodered-mcp: native executable missing inside ${packageName}: ${binPath}\n` +
          `Reinstall the platform package: npm install -g --force ${packageName}`,
      );
      e.code = 'EBINMISSING';
      throw e;
    }
    throw err;
  }
  return binPath;
}

function spawnChild(binPath) {
  return spawn(binPath, process.argv.slice(2), {
    stdio: 'inherit',
    // Inherit the user's environment so NODERED_URL / MCP_* /
    // proxy variables reach the Go process unchanged.
    env: process.env,
  });
}

function main() {
  let binPath;
  try {
    binPath = resolvePlatformPackage(process.platform, process.arch);
  } catch (err) {
    console.error(err.message);
    process.exit(1);
  }
  const child = spawnChild(binPath);
  child.on('exit', (code, signal) => {
    if (signal) {
      // Re-raise the same signal on the wrapper so the parent shell
      // sees the conventional exit semantics (128 + signo on POSIX,
      // terminate on Windows).
      process.kill(process.pid, signal);
      return;
    }
    process.exit(code ?? 0);
  });
  child.on('error', (err) => {
    console.error(`@fgjcarlos/nodered-mcp: failed to start ${binPath}: ${err.message}`);
    process.exit(1);
  });
}

if (require.main === module) {
  main();
}

module.exports = {
  PLATFORM_PACKAGES,
  resolvePlatformPackage,
  unsupportedMessage,
  missingOptionalMessage,
  binaryName,
};

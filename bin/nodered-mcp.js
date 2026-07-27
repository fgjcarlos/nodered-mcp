#!/usr/bin/env node
// SPDX-License-Identifier: MIT
//
// bin/nodered-mcp.js — npm-installed entry point.
//
// When the user runs `nodered-mcp ...` after `npm install -g
// @fgjcarlos/nodered-mcp`, npm resolves the `bin` entry to this file.
// This shim exists because the postinstall step downloads the real
// binary as `bin/nodered-mcp` (no extension); we cannot put a shebang
// on that one without first making sure it is executable, and we
// cannot make it executable without it existing. So this `.js` file
// re-execs the downloaded binary with the same argv.
//
// On any error we fall back to a clear message instead of a stack
// trace — the user's first experience of the package should not be a
// JavaScript exception.

'use strict';

const { spawn } = require('node:child_process');
const path = require('node:path');
const fs = require('node:fs');

const real = path.join(__dirname, 'nodered-mcp');

if (!fs.existsSync(real)) {
  console.error('nodered-mcp: binary not found.');
  console.error('The postinstall download may have failed. Try:');
  console.error('  npm install -g --force @fgjcarlos/nodered-mcp');
  process.exit(1);
}

const child = spawn(real, process.argv.slice(2), {
  stdio: 'inherit',
  // Run the binary with the same environment the user has, including
  // any NODERED_URL / MCP_* they set for `nodered-mcp` to read.
  env: process.env,
});

child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 0);
});

child.on('error', (err) => {
  console.error(`nodered-mcp: failed to start: ${err.message}`);
  process.exit(1);
});

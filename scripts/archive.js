'use strict';

const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

// GNU tar on Windows treats drive-letter paths used with either -f or
// -C as remote-host syntax. Stage the archive inside the destination
// and run tar there so every argument is a simple relative filename.
function extractTarGz(archivePath, destination) {
  fs.mkdirSync(destination, { recursive: true });
  const stagedName = `.nodered-mcp-${process.pid}-${Date.now()}.tar.gz`;
  const stagedArchive = path.join(destination, stagedName);
  fs.copyFileSync(archivePath, stagedArchive);
  try {
    execFileSync('tar', ['-xzf', stagedName], {
      cwd: destination,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
  } finally {
    try { fs.unlinkSync(stagedArchive); } catch (_) {}
  }
}

module.exports = { extractTarGz };

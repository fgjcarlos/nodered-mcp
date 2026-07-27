'use strict';

// bin/tar.js — extract a .tar.gz archive into a directory.
//
// Hand-rolled POSIX.1-1988 (ustar) parser using only Node stdlib.
// The npm install script uses this so the wrapper has zero npm
// dependencies, but it is exposed here as its own module so it can be
// unit-tested in isolation (see bin/tar_test.js).
//
// Tar header layout per POSIX.1-1988 (ustar):
//
//   offset  size  field
//   ------  ----  -----
//        0   100  name
//      100     8  mode (octal, NUL-terminated)
//      108     8  uid
//      116     8  gid
//      124    12  size (octal)
//      136    12  mtime
//      148     8  checksum
//      156     1  typeflag ('0' or '\0' = regular file)
//      157   345  linkname / padding
//
// Records are 512 bytes; file bodies are padded to a multiple of 512
// with NUL bytes. Two 512-byte NUL blocks mark end of archive.
//
// # ponytail: handles only regular files; silently skips directories.
// Goreleaser archives have at most one of each but extra defensiveness
// is cheap.

const fs = require('node:fs');
const zlib = require('node:zlib');

async function extract(tarGzPath, outDir) {
  const fh = await fs.promises.open(tarGzPath, 'r');
  try {
    const gunzip = zlib.createGunzip();
    const src = fh.createReadStream().pipe(gunzip);

    let pending = Buffer.alloc(0);

    function take(n) {
      const slice = pending.subarray(0, n);
      pending = pending.subarray(n);
      return slice;
    }

    for await (const chunk of src) {
      pending = Buffer.concat([pending, chunk]);
      // Each tar record is 512 bytes. Loop while we have a full header
      // available and the header is not the end-of-archive marker (a
      // fully-NUL 512-byte block).
      while (pending.length >= 512) {
        const header = pending.subarray(0, 512);
        if (header[0] === 0) {
          // End of archive: two NUL blocks. Drop them.
          if (pending.length < 1024) break;
          pending = pending.subarray(1024);
          continue;
        }
        const typeflag = String.fromCharCode(header[156]);
        if (typeflag !== '0' && typeflag !== '\0') {
          // Skip non-regular entries. We don't know their size from
          // typeflag alone, but goreleaser doesn't put any in here.
          const sizeStr = header.subarray(124, 136).toString('utf8').trim();
          const size = parseInt(sizeStr, 8) || 0;
          const padded = Math.ceil(size / 512) * 512;
          take(512 + padded);
          continue;
        }
        const nameEnd = header.indexOf(0, 0);
        const name = header.subarray(0, nameEnd === -1 ? 100 : nameEnd).toString('utf8');
        const sizeStr = header.subarray(124, 136).toString('utf8').trim();
        const size = parseInt(sizeStr, 8) || 0;
        const padded = Math.ceil(size / 512) * 512;

        if (pending.length < 512 + padded) break;

        take(512); // discard header

        if (size > 0) {
          const body = take(size);
          const dest = `${outDir}/${name}`;
          fs.mkdirSync(require('node:path').dirname(dest), { recursive: true });
          await fs.promises.writeFile(dest, body);
        }
        const padLen = padded - size;
        if (padLen > 0) take(padLen);
      }
    }
  } finally {
    await fh.close();
  }
}

module.exports = { extract };

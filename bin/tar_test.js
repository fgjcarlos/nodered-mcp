'use strict';

// bin/tar_test.js — minimal smoke test for the tar extractor.
//
// Run with: node bin/tar_test.js
// Exits 0 on success, 1 on any mismatch.

const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const zlib = require('node:zlib');
const { spawnSync } = require('node:child_process');

const { extract } = require('./tar');

function fail(msg) {
  console.error(`tar_test: FAIL: ${msg}`);
  process.exit(1);
}

async function main() {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'tar-test-'));
  try {
    // Build a synthetic tar.gz in memory: one regular file "hello.txt"
    // with content "world\n".
    const content = Buffer.from('world\n', 'utf8');
    const header = Buffer.alloc(512);
    header.write('hello.txt', 0, 'utf8');
    header.write('0000644', 100, 'utf8'); // mode
    header.write('0000000', 108, 'utf8'); // uid
    header.write('0000000', 116, 'utf8'); // gid
    header.write(content.length.toString(8).padStart(11, '0') + ' ', 124, 'utf8');
    header.write('00000000000', 136, 'utf8'); // mtime
    header.write('        ', 148, 'utf8'); // checksum placeholder
    header.write('0', 156, 'utf8'); // typeflag

    // Compute the checksum: sum of all header bytes, treating the
    // checksum field itself as eight ASCII spaces.
    const checksumField = Buffer.alloc(8, 0x20); // spaces
    checksumField.copy(header, 148);
    let sum = 0;
    for (let i = 0; i < 512; i++) sum += header[i];
    header.write(sum.toString(8).padStart(6, '0') + '\0 ', 148, 'utf8');

    const padded = Buffer.alloc(Math.ceil(content.length / 512) * 512, 0);
    content.copy(padded, 0);
    const eof = Buffer.alloc(1024, 0); // two NUL blocks
    const tarBytes = Buffer.concat([header, padded, eof]);
    const gzPath = path.join(tmp, 'fixture.tar.gz');
    fs.writeFileSync(gzPath, zlib.gzipSync(tarBytes));

    const outDir = path.join(tmp, 'out');
    fs.mkdirSync(outDir);
    await extract(gzPath, outDir);

    const got = fs.readFileSync(path.join(outDir, 'hello.txt'), 'utf8');
    if (got !== 'world\n') {
      fail(`content mismatch: got ${JSON.stringify(got)}`);
    }

    // Round-trip the real goreleaser tarball. The actual file from
    // release v0.5.1 has a `nodered-mcp` binary plus extras.
    if (process.env.RELEASE_TARBALL) {
      const realDir = path.join(tmp, 'real');
      fs.mkdirSync(realDir);
      await extract(process.env.RELEASE_TARBALL, realDir);
      const binary = path.join(realDir, 'nodered-mcp');
      if (!fs.existsSync(binary)) {
        fail('real tarball: nodered-mcp binary missing after extract');
      }
      const st = fs.statSync(binary);
      if (!st.isFile() || st.size < 1024) {
        fail(`real tarball: nodered-mcp looks wrong (size=${st.size})`);
      }
    }

    console.log('tar_test: PASS');
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

main().catch((err) => {
  console.error(`tar_test: FAIL: ${err.message}`);
  process.exit(1);
});

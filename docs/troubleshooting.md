# Troubleshooting

**Tools do not appear.** Confirm the binary is on the `PATH`, or use
an absolute path in `command`. On Windows, escape the backslashes:
`C:\\path\\nodered-mcp.exe`. Running `nodered-mcp init` resolves the
path for you.

**401 or 403 from Node-RED.** The token is missing or lacks the
required scope. With `adminAuth` enabled, generate a token with
write permission on flows.

**No log output.** Logs are written to stderr; stdout is reserved
for JSON-RPC frames on the stdio transport. Increase verbosity with
`--log-level debug`.

**HTTP transport does not connect.** Confirm `--transport http` is
set and that the `--http-addr` port is free.

**A write is refused with a backup error.** Backups are fail-closed
by design: if the snapshot cannot be written, the write does not
proceed. Check that `NODERED_BACKUP_DIR` exists and is writable.

**Windows: orphaned npm shims.** A failed `npm i -g
@fgjcarlos/nodered-mcp` on Windows leaves three launcher files
(`nodered-mcp`, `.cmd`, `.ps1`) in `%APPDATA%\npm\` that no
`npm uninstall` removes. They precede `%LOCALAPPDATA%\Programs\nodered-mcp\`
in the default PATH and shadow a working install made with
`scripts/install.ps1`. Run
[`scripts/cleanup-npm-shims.ps1`](../scripts/cleanup-npm-shims.ps1)
to remove them. (Tracked as #184; the script ships in a separate
PR.)
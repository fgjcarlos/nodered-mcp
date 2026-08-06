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

**Windows: orphaned npm shims (legacy).** A `npm i -g
@fgjcarlos/nodered-mcp` from a version before 0.5.16 could leave three
launcher files (`nodered-mcp`, `.cmd`, `.ps1`) in `%APPDATA%\npm\` that
no `npm uninstall` removes. After #192 the npm package ships Windows
binaries directly, so fresh installs do not reproduce this.

Users who inherited the orphaned shims from a pre-0.5.16 install
can remove them with the shipped helper:

```powershell
.\scripts\cleanup-npm-shims.ps1
```

The script is idempotent; every Remove-Item tolerates a missing
file, so re-running it is safe. No admin required, no PATH
modified.

**Platform-specific install issues**

**macOS — "cannot be opened because the developer cannot be verified."**
The npm-shipped binaries are not code-signed with an Apple Developer ID.
On first run macOS Gatekeeper blocks them. Right-click the binary in
Finder → Open → confirm once, or remove the quarantine attribute for
all subsequent invocations:

```bash
xattr -dr com.apple.quarantine "$(npm root -g)/@fgjcarlos/nodered-mcp"
```

This affects every unsigned CLI installed via `npm install -g` (e.g.
`esbuild`, `playwright`, `prisma`), not just `nodered-mcp`. Code signing
is not currently in scope.

**Windows ARM64 — `EPERM` or "not a valid Win32 application".**
Snapdragon X / Surface Pro X machines ship with an x64 emulation layer
enabled by default, which causes npm to resolve the
`@fgjcarlos/nodered-mcp-win32-x64` platform package. If you want the
native ARM64 binary, force the install with `--cpu=arm64 --os=win32`:

```bash
npm install -g @fgjcarlos/nodered-mcp --cpu=arm64 --os=win32
```

**Linux ARM64 — Raspberry Pi / AWS Graviton.**
The `@fgjcarlos/nodered-mcp-linux-arm64` package is resolved automatically
on `aarch64` Linux. If you see "Exec format error" the binary was
resolved against the wrong architecture — check `uname -m` and
`npm config get os cpu`. On Raspberry Pi OS / Debian you may also need
`libc6` ≥ 2.31 (Bullseye); Bookworm is fine.

**Linux — "GLIBC not found" / "GLIBC_2.XX not found".**
The binaries are built on Ubuntu 22.04 (glibc 2.35). On older distros
(Debian 10, CentOS 7, RHEL 7) the loader cannot find the required
symbols and the binary fails immediately with a glibc version error.
Use `go install` against a matching Go toolchain, or run the Docker
image. `npm rebuild` does not help because the failure is at runtime,
not at install time.

**Unsupported architecture (Linux PowerPC, FreeBSD, OpenBSD, etc.).**
npm only ships the six platform packages listed in the README. For
anything outside that matrix, use one of:

```bash
go install github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp@latest
# or
docker pull ghcr.io/fgjcarlos/nodered-mcp:latest
```
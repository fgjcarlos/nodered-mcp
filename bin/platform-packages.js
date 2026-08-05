'use strict';

// Authoritative contract shared by the npm runtime shim, the package
// producer, and the release gates. Keep platform, npm, and GoReleaser
// naming in one place so a new target cannot drift between channels.
const TARGETS = [
  { key: 'win32-x64', platform: 'win32', arch: 'x64', goos: 'windows', goarch: 'amd64' },
  { key: 'win32-arm64', platform: 'win32', arch: 'arm64', goos: 'windows', goarch: 'arm64' },
  { key: 'linux-x64', platform: 'linux', arch: 'x64', goos: 'linux', goarch: 'amd64' },
  { key: 'linux-arm64', platform: 'linux', arch: 'arm64', goos: 'linux', goarch: 'arm64' },
  { key: 'darwin-x64', platform: 'darwin', arch: 'x64', goos: 'darwin', goarch: 'amd64' },
  { key: 'darwin-arm64', platform: 'darwin', arch: 'arm64', goos: 'darwin', goarch: 'arm64' },
].map((target) => ({
  ...target,
  packageName: `@fgjcarlos/nodered-mcp-${target.key}`,
  asset: `nodered-mcp_${target.goos}_${target.goarch}.tar.gz`,
  binary: target.platform === 'win32' ? 'nodered-mcp.exe' : 'nodered-mcp',
}));

const PLATFORM_PACKAGES = Object.fromEntries(
  TARGETS.map(({ key, packageName }) => [key, packageName]),
);

function targetFor(platform, arch) {
  return TARGETS.find((target) => target.platform === platform && target.arch === arch) || null;
}

function binaryNameFor(platform) {
  return platform === 'win32' ? 'nodered-mcp.exe' : 'nodered-mcp';
}

module.exports = { TARGETS, PLATFORM_PACKAGES, targetFor, binaryNameFor };

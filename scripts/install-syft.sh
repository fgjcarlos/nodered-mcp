#!/usr/bin/env bash
# Install the exact Syft binary used by CI and release jobs.

set -euo pipefail

version="1.27.0"
archive="syft_${version}_linux_amd64.tar.gz"
expected_sha256="2cee2128b0a05dfadec34676def064979b3098bfa447679c38ce3bb69e9321f3"
url="https://github.com/anchore/syft/releases/download/v${version}/${archive}"
install_dir="${SYFT_INSTALL_DIR:-${HOME}/.local/bin}"
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

curl --fail --silent --show-error --location \
  --proto '=https' --tlsv1.2 \
  --output "${tmp_dir}/${archive}" \
  "$url"

printf '%s  %s\n' "$expected_sha256" "$archive" \
  | (cd "$tmp_dir" && sha256sum --check --strict -)

tar -xzf "${tmp_dir}/${archive}" -C "$tmp_dir" syft
install -d "$install_dir"
install -m 0755 "${tmp_dir}/syft" "${install_dir}/syft"

if [[ -n "${GITHUB_PATH:-}" ]]; then
  printf '%s\n' "$install_dir" >> "$GITHUB_PATH"
fi

"${install_dir}/syft" version

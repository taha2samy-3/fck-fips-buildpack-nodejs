#!/usr/bin/env bash
# Runs after `scripts/package.sh --publish`. For every image the release
# pushed (buildpacks, meta buildpack, builder) it resolves the
# digest, signs it with cosign (keyless, GitHub OIDC) and records it for the
# provenance attestation step. It also pushes the .cnb files as OCI artifacts
# and writes SHA256SUMS for the GitHub release.
#
#   scripts/release-artifacts.sh --version V [--registry R]
#
# Writes build/digests.txt ("<image> <sha256:...>" per line) and, on GitHub
# Actions, one "<name>_digest" output per image.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REGISTRY="ghcr.io/taha2samy-3/fck-fips"
VERSION=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --registry) REGISTRY="$2"; shift 2 ;;
    *) echo "unknown option: $1" >&2; exit 1 ;;
  esac
done
[[ -n "${VERSION}" ]] || { echo "--version is required" >&2; exit 1; }

OUT="${ROOT}/build"
# The stack images are signed by .github/workflows/stack.yml, which publishes them.
IMAGES=(node-engine openssl-fips pnpm pnpm-install nodejs builder-nodejs)
: > "${OUT}/digests.txt"

for name in "${IMAGES[@]}"; do
  ref="${REGISTRY}/${name}:${VERSION}"
  digest="$(crane digest "${ref}")"
  echo "==> ${ref}@${digest}"
  cosign sign --yes "${REGISTRY}/${name}@${digest}"
  echo "${REGISTRY}/${name} ${digest}" >> "${OUT}/digests.txt"
  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    echo "${name//-/_}_digest=${digest}" >> "${GITHUB_OUTPUT}"
  fi
done

# A .cnb file is an OCI image layout in a tarball, hence the media type.
cd "${OUT}/cnb"
for bp in node-engine openssl-fips pnpm pnpm-install nodejs; do
  files=()
  for f in "${bp}"-linux-*.cnb; do
    files+=("${f}:application/vnd.oci.image.layout.v1+tar")
  done
  ref="${REGISTRY}/cnb/${bp}:${VERSION}"
  echo "==> ${ref} (${#files[@]} files)"
  oras push "${ref}" \
    --artifact-type application/vnd.fckfips.buildpack.cnb.v1 \
    --annotation "org.opencontainers.image.source=https://github.com/taha2samy-3/fck-fips-buildpack-nodejs" \
    --annotation "org.opencontainers.image.version=${VERSION}" \
    "${files[@]}"
  digest="$(crane digest "${ref}")"
  cosign sign --yes "${REGISTRY}/cnb/${bp}@${digest}"
  echo "${REGISTRY}/cnb/${bp} ${digest}" >> "${OUT}/digests.txt"
  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    echo "cnb_${bp//-/_}_digest=${digest}" >> "${GITHUB_OUTPUT}"
  fi
done

sha256sum ./*.cnb > SHA256SUMS
cat SHA256SUMS

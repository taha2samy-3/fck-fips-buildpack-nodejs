#!/usr/bin/env bash
# Builds and publishes the stack images (build, run, run-distroless)
# independently of buildpack releases, so OS fixes reach apps through
# `pack rebase` without waiting for a release.
#
#   scripts/stack.sh changed [--registry R]
#       Builds each image for linux/amd64 and compares its installed package
#       list (/usr/share/fckfips/stack-packages.txt) with the published
#       :latest. Prints a diff per image; on GitHub Actions writes
#       changed=true|false to $GITHUB_OUTPUT.
#
#   scripts/stack.sh publish --tag T [--registry R]
#       Builds linux/amd64 and linux/arm64, pushes :latest and :T, signs each
#       digest with cosign (keyless) and writes <image>_digest outputs.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REGISTRY="ghcr.io/taha2samy-3/fck-fips"
IMAGES=(build run run-distroless)
MANIFEST="usr/share/fckfips/stack-packages.txt"

cmd="${1:-}"; shift || true
TAG=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --registry) REGISTRY="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    *) echo "unknown option: $1" >&2; exit 1 ;;
  esac
done

output() {
  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then echo "$1" >> "${GITHUB_OUTPUT}"; fi
}

# local_manifest <image>: the package list of a locally built image. Distroless
# images have no shell, so copy the file out of a created container.
local_manifest() {
  local id
  id="$(docker create --entrypoint /nonexistent "$1")"
  docker cp "${id}:/${MANIFEST}" - | tar -xO
  docker rm "${id}" > /dev/null
}

# published_manifest <ref>: the package list of the published linux/amd64
# image, or nothing if it is not published (or predates the manifest).
published_manifest() {
  crane export --platform linux/amd64 "$1" - 2> /dev/null | tar -xO "${MANIFEST}" 2> /dev/null || true
}

changed() {
  local any=false tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "${tmp}"' RETURN
  for img in "${IMAGES[@]}"; do
    echo "==> ${img}"
    docker buildx build "${ROOT}/stack" --file "${ROOT}/stack/${img}.Dockerfile" \
      --platform linux/amd64 --tag "fckfips-stack-check/${img}" --load --quiet > /dev/null
    local_manifest "fckfips-stack-check/${img}" > "${tmp}/new"
    published_manifest "${REGISTRY}/${img}:latest" > "${tmp}/old"
    if diff -u "${tmp}/old" "${tmp}/new" > "${tmp}/diff"; then
      echo "    unchanged"
    else
      any=true
      sed 's/^/    /' "${tmp}/diff"
    fi
  done
  echo "changed=${any}"
  output "changed=${any}"
}

publish() {
  [[ -n "${TAG}" ]] || { echo "publish needs --tag" >&2; exit 1; }
  local revision
  revision="$(git -C "${ROOT}" rev-parse HEAD)"
  for img in "${IMAGES[@]}"; do
    echo "==> ${REGISTRY}/${img}:${TAG}"
    docker buildx build "${ROOT}/stack" --file "${ROOT}/stack/${img}.Dockerfile" \
      --platform linux/amd64,linux/arm64 \
      --tag "${REGISTRY}/${img}:${TAG}" \
      --tag "${REGISTRY}/${img}:latest" \
      --label "org.opencontainers.image.version=${TAG}" \
      --label "org.opencontainers.image.revision=${revision}" \
      --provenance=false \
      --push
    local digest
    digest="$(crane digest "${REGISTRY}/${img}:${TAG}")"
    cosign sign --yes "${REGISTRY}/${img}@${digest}"
    echo "    ${digest}"
    output "${img//-/_}_digest=${digest}"
  done
  output "tag=${TAG}"
}

case "${cmd}" in
  changed) changed ;;
  publish) publish ;;
  *) sed -n '2,16p' "$0"; exit 1 ;;
esac

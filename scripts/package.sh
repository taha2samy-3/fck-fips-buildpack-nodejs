#!/usr/bin/env bash
# Builds the buildpack executables, packages every buildpack and creates the
# builder.
#
#   scripts/package.sh                      # local: host arch, .cnb files, builder in the daemon
#   scripts/package.sh --publish            # release: linux/amd64+arm64, pushed to the registry
#
# Options:
#   --version V     version to stamp (default: contents of VERSION)
#   --registry R    image prefix (default: ghcr.io/taha2samy-3/fck-fips)
#   --publish       push multi-arch images instead of building locally
#   --no-builder    stop after packaging the buildpacks
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="$(tr -d '[:space:]' < "${ROOT}/VERSION")"
REGISTRY="ghcr.io/taha2samy-3/fck-fips"
PUBLISH=false
BUILDER=true
BUILDPACKS=(node-engine openssl-fips pnpm pnpm-install)

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --registry) REGISTRY="$2"; shift 2 ;;
    --publish) PUBLISH=true; shift ;;
    --no-builder) BUILDER=false; shift ;;
    -h|--help) sed -n '2,13p' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 1 ;;
  esac
done

OUT="${ROOT}/build"
rm -rf "${OUT}"
mkdir -p "${OUT}/cnb"

if [[ "${PUBLISH}" == true ]]; then
  TARGETS=(linux/amd64 linux/arm64)
else
  TARGETS=("linux/$(go env GOARCH)")
fi

log() { printf '\n==> %s\n' "$*"; }

# binaries <buildpack> lists the commands of a buildpack. "run" doubles as
# bin/detect and bin/build; the others are exec.d helpers.
binaries() {
  case "$1" in
    node-engine) echo "run node-options" ;;
    pnpm-install) echo "run setup-symlinks" ;;
    *) echo "run" ;;
  esac
}

# stage <buildpack> builds the executables and lays the buildpack out the way
# pack expects: bin/ at the root for one target, <os>/<arch>/bin/ for several.
stage() {
  local bp="$1" dir="${OUT}/buildpacks/$1"
  mkdir -p "${dir}"
  sed "s/{{VERSION}}/${VERSION}/g" "${ROOT}/buildpacks/${bp}/buildpack.toml" > "${dir}/buildpack.toml"

  for target in "${TARGETS[@]}"; do
    local os="${target%/*}" arch="${target#*/}" bin
    if [[ ${#TARGETS[@]} -eq 1 ]]; then bin="${dir}/bin"; else bin="${dir}/${os}/${arch}/bin"; fi
    mkdir -p "${bin}"
    for cmd in $(binaries "${bp}"); do
      CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" go build -trimpath -buildvcs=false \
        -ldflags="-s -w" -o "${bin}/${cmd}" "./buildpacks/${bp}/cmd/${cmd}"
    done
    ln -sf run "${bin}/detect"
    ln -sf run "${bin}/build"
  done
}

target_flags=()
for t in "${TARGETS[@]}"; do target_flags+=(--target "$t"); done

cd "${ROOT}"
for bp in "${BUILDPACKS[@]}"; do
  log "Packaging fckfips/${bp} ${VERSION} (${TARGETS[*]})"
  stage "${bp}"
  if [[ "${PUBLISH}" == true ]]; then
    pack buildpack package "${REGISTRY}/${bp}:${VERSION}" --path "${OUT}/buildpacks/${bp}" \
      "${target_flags[@]}" --publish
    # The same buildpack as files, one per target: <name>-linux-<arch>.cnb
    pack buildpack package "${OUT}/cnb/${bp}.cnb" --path "${OUT}/buildpacks/${bp}" \
      "${target_flags[@]}" --format file
  else
    pack buildpack package "${OUT}/${bp}.cnb" --path "${OUT}/buildpacks/${bp}" --format file \
      "${target_flags[@]}"
  fi
done

log "Packaging fckfips/nodejs ${VERSION}"
meta="${OUT}/buildpacks/nodejs"
mkdir -p "${meta}"
sed "s/{{VERSION}}/${VERSION}/g" buildpacks/nodejs/buildpack.toml > "${meta}/buildpack.toml"
{
  sed '/^\[\[targets\]\]/,$d' buildpacks/nodejs/package.toml
  for bp in "${BUILDPACKS[@]}"; do
    if [[ "${PUBLISH}" == true ]]; then uri="docker://${REGISTRY}/${bp}:${VERSION}"; else uri="file://${OUT}/${bp}.cnb"; fi
    printf '[[dependencies]]\n  uri = "%s"\n\n' "${uri}"
  done
  sed -n '/^\[\[targets\]\]/,$p' buildpacks/nodejs/package.toml
} > "${meta}/package.toml"

if [[ "${PUBLISH}" == true ]]; then
  pack buildpack package "${REGISTRY}/nodejs:${VERSION}" --config "${meta}/package.toml" \
    --path "${meta}" "${target_flags[@]}" --publish
  pack buildpack package "${OUT}/cnb/nodejs.cnb" --config "${meta}/package.toml" \
    --path "${meta}" "${target_flags[@]}" --format file
  meta_uri="docker://${REGISTRY}/nodejs:${VERSION}"
else
  pack buildpack package "${OUT}/nodejs.cnb" --config "${meta}/package.toml" --format file \
    "${target_flags[@]}" --pull-policy if-not-present
  meta_uri="file://${OUT}/nodejs.cnb"
fi

[[ "${BUILDER}" == true ]] || exit 0

log "Creating builder ${REGISTRY}/builder-nodejs:${VERSION}"
sed -e "s#{{VERSION}}#${VERSION}#g" \
    -e "s#{{META_URI}}#${meta_uri}#g" \
    -e "s#{{BUILD_IMAGE}}#${REGISTRY}/build:${VERSION}#g" \
    -e "s#{{RUN_IMAGE}}#${REGISTRY}/run:${VERSION}#g" \
    builder/builder.toml > "${OUT}/builder.toml"

if [[ "${PUBLISH}" == true ]]; then
  pack builder create "${REGISTRY}/builder-nodejs:${VERSION}" --config "${OUT}/builder.toml" \
    "${target_flags[@]}" --publish
else
  sed -i '/^\[\[targets\]\]/,$d' "${OUT}/builder.toml"
  pack builder create "${REGISTRY}/builder-nodejs:${VERSION}" --config "${OUT}/builder.toml" \
    --pull-policy if-not-present
fi

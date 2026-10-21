# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/). A release is cut by pushing a tag
`vX.Y.Z`; the release workflow refuses to run unless this file has a
`## [X.Y.Z]` section, and uses that section as the release notes.

## [Unreleased]

## [2.0.0] - 2026-10-21

Rewrite of the Python buildpacks and the Paketo forks as one Go project.

### Added

- `fckfips/openssl-fips`: opt-in use (`BP_OPENSSL_FIPS=true`) of the FIPS
  140-3 validated OpenSSL FIPS Provider 3.1.2 (CMVP #4985), from the signed
  repository at taha2samy-3.github.io/node/apk. The validation covers the
  module; the images are not FIPS certified. node-engine verifies FIPS mode at build
  time and starts node with `--force-fips`; a corrupted module stops node
  from starting.
- pnpm 12 support: the native executable is installed directly.
- SBOMs (CycloneDX, SPDX) for every layer.
- Distroless run image (`run-distroless`).
- Releases as signed OCI artifacts with SLSA v1.0 Build L3 provenance:
  buildpackages, `.cnb` files, builder and stack images.

### Changed

- `fckfips/node-engine` is written in Go. Node.js comes from Wolfi with the
  full apk chain of trust (signed index, control checksum, data hash), its
  shared libraries ship in the layer, and npm is the release bundled with
  the selected Node.js, verified against the registry integrity.
- The default Node.js is 24 (was 20, now end of life).
- `fckfips/pnpm` resolves any published version from the npm registry and
  checks `packageManager` hashes; it replaces the amd64-only embedded catalog.
- `fckfips/pnpm-install` keeps the pnpm store in a cache layer, adds
  `pnpm-workspace.yaml`, `.npmrc` and the pnpm version to the cache key, and
  allows dependency build scripts only when the project configures none.
- pnpm is no longer added to the launch image by default
  (`BP_PNPM_IN_LAUNCH=true` restores it).
- Buildpack IDs moved from `paketo-buildpacks/*` to `fckfips/*`.
- The stack images no longer bake in OpenSSL FIPS; it is a buildpack now.

### Fixed

- arm64 builds of pnpm (the embedded catalog only listed amd64).
- The pnpm lifecycle buildpack, which installed Node.js instead of running
  `pnpm install`.
- Version comparison of Wolfi packages (22.9 sorted above 22.10).
- The memory exec.d helper, which lost variables after its first write.
- Node.js debugging with `BPL_DEBUG_ENABLED` (the minimal flavor had no
  inspector).

[Unreleased]: https://github.com/taha2samy-3/fck-fips-buildpack-nodejs/compare/v2.0.0...HEAD
[2.0.0]: https://github.com/taha2samy-3/fck-fips-buildpack-nodejs/releases/tag/v2.0.0

# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/). A release is cut by pushing a tag
`vX.Y.Z`; the release workflow refuses to run unless this file has a
`## [X.Y.Z]` section, and uses that section as the release notes.

## [Unreleased]

## [1.0.1] - 2026-10-24

### Security

- The buildpacks are built with Go 1.26.10 and `golang.org/x/net` 0.60.0,
  fixing 11 vulnerabilities in the standard library and `x/net` (HTTP,
  TLS and certificate handling) that `govulncheck` showed the buildpacks'
  download code could reach.

### Changed

- The stack images (`build`, `run`, `run-distroless`) are published by their
  own workflow, independently of releases: daily when Wolfi packages change,
  and whenever `stack/` changes. They are tagged `:latest` (rolling) and
  `:YYYYMMDD.N` (immutable). The builder uses `run:latest`, so `pack rebase`
  moves apps to the newest run image without a new buildpack release. Each
  image records its installed packages in `/usr/share/fckfips/stack-packages.txt`.

### Added

- `tools/ghcr-cleanup` and a weekly workflow that delete dangling GHCR
  versions without breaking tags: per-architecture manifests of multi-arch
  images, and attestations, signatures and SBOMs of kept images, are never
  deleted; neither is anything younger than 72 hours or unreadable.
- Self-hosted Renovate, authenticated as a GitHub App, with every update
  pinned by digest: one pull request for regular updates, a separate one for
  security fixes, and no major updates.
- A Usage page in the documentation, with a versions list generated from
  this changelog.

## [1.0.0] - 2026-10-21

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

[Unreleased]: https://github.com/taha2samy-3/fck-fips-buildpack-nodejs/compare/v1.0.1...HEAD
[1.0.1]: https://github.com/taha2samy-3/fck-fips-buildpack-nodejs/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/taha2samy-3/fck-fips-buildpack-nodejs/releases/tag/v1.0.0

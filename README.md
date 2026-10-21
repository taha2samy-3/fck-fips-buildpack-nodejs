<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/public/wordmark-dark.svg">
    <img alt="fck-fips · Node.js buildpacks" src="docs/public/wordmark-light.svg" width="420">
  </picture>
</p>

<p align="center">
  Cloud Native Buildpacks for Node.js on Wolfi: verified Node.js, npm, yarn and pnpm,<br>
  and an opt-in <strong>FIPS 140-3 validated</strong> OpenSSL module.
</p>

<p align="center">
  <a href="https://taha2samy-3.github.io/fck-fips-buildpack-nodejs/">Documentation</a> ·
  <a href="https://taha2samy-3.github.io/fck-fips-buildpack-nodejs/configuration/">Configuration</a> ·
  <a href="https://taha2samy-3.github.io/fck-fips-buildpack-nodejs/fips/">FIPS</a> ·
  <a href="CHANGELOG.md">Changelog</a>
</p>

---

```sh
pack build my-app --builder ghcr.io/taha2samy-3/fck-fips/builder-nodejs:1.0.0
pack build my-app --builder ghcr.io/taha2samy-3/fck-fips/builder-nodejs:1.0.0 --env BP_OPENSSL_FIPS=true
```

The builder detects pnpm, yarn or npm from the lockfile, takes the Node.js version from `engines.node`,
`.nvmrc` or `.node-version` (default 24), and starts the app from `scripts.start` or `server.js`.

> [!IMPORTANT]
> **Validated module, not a certified product.** With `BP_OPENSSL_FIPS=true`, all OpenSSL cryptography in the
> image goes through the OpenSSL FIPS Provider 3.1.2, a FIPS 140-3 validated cryptographic module
> ([CMVP #4985](https://csrc.nist.gov/projects/cryptographic-module-validation-program/certificate/4985)).
> The validation belongs to the module. This project and the images it builds are **not FIPS certified**.
> Wolfi is not one of the module's tested operational environments; see [FIPS](https://taha2samy-3.github.io/fck-fips-buildpack-nodejs/fips/).

## Buildpacks

| Buildpack | Does |
|---|---|
| `fckfips/node-engine` | Node.js from signed Wolfi packages (index signature → control checksum → data hash), its shared libraries, and the npm that ships with that Node.js release |
| `fckfips/openssl-fips` | The validated OpenSSL FIPS provider from a signed apk repository, and an OpenSSL configuration that uses only it |
| `fckfips/pnpm` | Any pnpm release from the npm registry, integrity checked, honouring `packageManager` hashes; pnpm 12 as a native executable |
| `fckfips/pnpm-install` | `pnpm install` into build and launch layers, with the store cached between builds. Derived from Paketo's yarn-install |
| `fckfips/nodejs` | The meta buildpack: pnpm, yarn, npm and plain Node.js order groups with Paketo's buildpacks |

This builder extends [Paketo's Node.js buildpack](https://github.com/paketo-buildpacks/nodejs) rather than replacing it:
npm-install, yarn, yarn-install, node-start, npm-start, node-run-script and the utility buildpacks are Paketo's, unchanged.
[What is reused, replaced and added](https://taha2samy-3.github.io/fck-fips-buildpack-nodejs/paketo/).

## Releases

Pushing a tag `vX.Y.Z` with a matching `CHANGELOG.md` entry publishes to `ghcr.io/taha2samy-3/fck-fips/`:
the builder, every buildpack (as an image and as `.cnb` files), and the stack images, for linux/amd64 and
linux/arm64. Each artifact is signed with cosign (keyless) and carries SLSA v1.0 Build L3 provenance; see
[SECURITY.md](SECURITY.md) for how to verify them.

## Development

Tools are pinned in `mise.toml`:

```sh
mise install            # Go, pack, golangci-lint, shellcheck, actionlint, oras, cosign, crane, Node.js, pnpm
mise run lint           # golangci-lint, shellcheck, actionlint
mise run test           # unit tests with the race detector
mise run integration    # build the stack images and the builder, then build and run the test apps
mise run docs           # documentation site with live reload
```

| Path | Contents |
|---|---|
| `buildpacks/` | One directory per buildpack: Go package, `buildpack.toml`, commands in `cmd/` |
| `internal/` | apk verification, npm registry client, safe tar extraction, SBOM writer, HTTP fetching |
| `stack/` | Build, run and distroless run images |
| `builder/`, `scripts/` | Builder template; packaging and release scripts |
| `integration/` | End-to-end tests with real `pack` builds |
| `docs/` | Documentation site (Astro), published to GitHub Pages; `docs/brand/` generates the logo |

## License

Apache-2.0. Derived in part from Paketo Buildpacks; see [NOTICE](NOTICE). Not affiliated with Paketo, Chainguard,
the OpenSSL project or NIST.

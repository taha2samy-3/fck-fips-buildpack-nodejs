# Security policy

## Reporting a vulnerability

Report vulnerabilities privately through
[GitHub security advisories](https://github.com/taha2samy-3/fck-fips-buildpacks-nodejs/security/advisories/new).
Please do not open a public issue. You will get an answer within 7 days.

## Supply chain

Releases are built only by `.github/workflows/release.yml` on GitHub-hosted
runners, from a version tag, and publish to `ghcr.io/taha2samy-3/fck-fips/*`:

- every image and `.cnb` artifact is signed with cosign keyless signing
  (GitHub OIDC, no long-lived keys);
- every one has SLSA v1.0 Build Level 3 provenance from
  [slsa-github-generator](https://github.com/slsa-framework/slsa-github-generator)
  and a GitHub build attestation;
- workflow actions are pinned by commit SHA (except the SLSA generator, which
  must be referenced by tag), tool versions are pinned in `mise.toml` and base
  images by digest.

Verify before you use a release:

```sh
slsa-verifier verify-image ghcr.io/taha2samy-3/fck-fips/builder-nodejs@sha256:<digest> \
  --source-uri github.com/taha2samy-3/fck-fips-buildpacks-nodejs --source-tag v<version>

gh attestation verify oci://ghcr.io/taha2samy-3/fck-fips/builder-nodejs:<version> \
  --repo taha2samy-3/fck-fips-buildpacks-nodejs

cosign verify ghcr.io/taha2samy-3/fck-fips/builder-nodejs:<version> \
  --certificate-identity-regexp 'https://github.com/taha2samy-3/fck-fips-buildpacks-nodejs/.github/workflows/release.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## What the buildpacks verify at build time

- Node.js and its libraries: the Wolfi APKINDEX signature (RSA/SHA-256 with
  the embedded Wolfi key), then each package's control checksum, package
  signature and data hash.
- The OpenSSL FIPS provider: the same chain, against the embedded key of
  https://taha2samy-3.github.io/node/apk.
- npm and pnpm: the registry's sha512 integrity, plus the `packageManager`
  hash when package.json pins one.

## FIPS 140-3 scope

The OpenSSL FIPS Provider 3.1.2 holds CMVP certificate #4985 for its tested
operational environments. Wolfi is not one of them: running the unchanged
module there is a port under FIPS 140-3 IG 2.3.A that the operator affirms.
This project makes no other claim.

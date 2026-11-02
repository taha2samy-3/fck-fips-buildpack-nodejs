# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
# Run image: glibc, CA certificates, time zones, bash and envoke (secrets-entrypoint).
# Everything Node.js needs, including its shared libraries, arrives in buildpack layers.
ARG BASE_IMAGE=cgr.dev/chainguard/wolfi-base:latest@sha256:81211e15a86ea1d5357f284482af45730c51de45eaf10ad9ce205d6bfc2df323
FROM ${BASE_IMAGE}

ARG CNB_USER_ID=1000
ARG CNB_GROUP_ID=1000
ARG TARGETARCH
ARG ENVOKE_VERSION=v1.0.1
ARG ENVOKE_SHA256_AMD64=924a81eabd1d460029baf6f624ce3c109a1aa07f0c6f9cc4633f616833a3a418
ARG ENVOKE_SHA256_ARM64=a0bffe48a6c6f824426415ac704adb316475d555ed343b37107658c41328b567

USER root
RUN apk add --no-cache bash ca-certificates-bundle tzdata \
 && addgroup -g "${CNB_GROUP_ID}" cnb \
 && adduser -D -u "${CNB_USER_ID}" -G cnb -s /bin/bash cnb

# Install envoke (secrets-entrypoint) v1.0.1
ADD https://github.com/taha2samy-3/envoke/releases/download/${ENVOKE_VERSION}/secrets-entrypoint_linux_${TARGETARCH} /usr/bin/secrets-entrypoint
RUN set -e; \
    case "${TARGETARCH}" in \
      amd64) sum="${ENVOKE_SHA256_AMD64}" ;; \
      arm64) sum="${ENVOKE_SHA256_ARM64}" ;; \
      *) echo "envoke: unsupported architecture ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    echo "${sum}  /usr/bin/secrets-entrypoint" | sha256sum -c -; \
    chmod 0755 /usr/bin/secrets-entrypoint; \
    ln -sf secrets-entrypoint /usr/bin/envoke

# The installed package list goes into the image: the stack workflow compares
# it with the published image to decide whether a rebuild changed anything.
RUN mkdir -p /usr/share/fckfips \
 && { apk info -v; echo "envoke-${ENVOKE_VERSION}"; } | sort > /usr/share/fckfips/stack-packages.txt

ENV CNB_USER_ID=${CNB_USER_ID} \
    CNB_GROUP_ID=${CNB_GROUP_ID} \
    SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

LABEL io.buildpacks.stack.id="io.fckfips.stacks.wolfi" \
      io.buildpacks.base.distro.name="wolfi" \
      io.buildpacks.base.homepage="https://github.com/taha2samy-3/fck-fips-buildpack-nodejs" \
      io.buildpacks.base.maintainer="taha2samy-3" \
      org.opencontainers.image.source="https://github.com/taha2samy-3/fck-fips-buildpack-nodejs"

USER ${CNB_USER_ID}:${CNB_GROUP_ID}

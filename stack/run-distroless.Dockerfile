# syntax=docker/dockerfile:1
# Distroless run image: glibc, CA certificates, time zones and envoke, with no
# shell and no package manager. Works for apps started by node-start (node
# runs directly); npm-start and Procfile commands need the regular run image.
# Use it with: pack build --run-image <registry>/run-distroless:latest
# The installed package list stays in the image (the apk database does not):
# the stack workflow compares it with the published image to decide whether a
# rebuild changed anything.
ARG BASE_IMAGE=cgr.dev/chainguard/wolfi-base:latest@sha256:81211e15a86ea1d5357f284482af45730c51de45eaf10ad9ce205d6bfc2df323
FROM ${BASE_IMAGE} AS rootfs

ARG CNB_USER_ID=1000
ARG CNB_GROUP_ID=1000
ARG TARGETARCH
ARG ENVOKE_VERSION=v1.0.1
ARG ENVOKE_SHA256_AMD64=924a81eabd1d460029baf6f624ce3c109a1aa07f0c6f9cc4633f616833a3a418
ARG ENVOKE_SHA256_ARM64=a0bffe48a6c6f824426415ac704adb316475d555ed343b37107658c41328b567

USER root
RUN apk add --no-cache --initdb --no-scripts --root /rootfs \
      --keys-dir /etc/apk/keys --repositories-file /etc/apk/repositories \
      ca-certificates-bundle glibc ld-linux tzdata wolfi-baselayout \
 && mkdir -p /rootfs/usr/share/fckfips \
 && { apk info -v --root /rootfs; echo "envoke-${ENVOKE_VERSION}"; } | sort > /rootfs/usr/share/fckfips/stack-packages.txt \
 && rm -rf /rootfs/lib/apk /rootfs/var/cache/apk /rootfs/etc/apk \
 && echo "cnb:x:${CNB_USER_ID}:${CNB_GROUP_ID}:cnb:/home/cnb:/sbin/nologin" >> /rootfs/etc/passwd \
 && echo "cnb:x:${CNB_GROUP_ID}:" >> /rootfs/etc/group \
 && mkdir -p /rootfs/home/cnb /rootfs/tmp /rootfs/usr/bin \
 && chown "${CNB_USER_ID}:${CNB_GROUP_ID}" /rootfs/home/cnb \
 && chmod 1777 /rootfs/tmp

# Install envoke (secrets-entrypoint) v1.0.1
ADD https://github.com/taha2samy-3/envoke/releases/download/${ENVOKE_VERSION}/secrets-entrypoint_linux_${TARGETARCH} /rootfs/usr/bin/secrets-entrypoint
RUN set -e; \
    case "${TARGETARCH}" in \
      amd64) sum="${ENVOKE_SHA256_AMD64}" ;; \
      arm64) sum="${ENVOKE_SHA256_ARM64}" ;; \
      *) echo "envoke: unsupported architecture ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    echo "${sum}  /rootfs/usr/bin/secrets-entrypoint" | sha256sum -c -; \
    chmod 0755 /rootfs/usr/bin/secrets-entrypoint; \
    ln -sf secrets-entrypoint /rootfs/usr/bin/envoke

FROM scratch
ARG CNB_USER_ID=1000
ARG CNB_GROUP_ID=1000
COPY --from=rootfs /rootfs/ /

ENV CNB_USER_ID=${CNB_USER_ID} \
    CNB_GROUP_ID=${CNB_GROUP_ID} \
    SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

LABEL io.buildpacks.stack.id="io.fckfips.stacks.wolfi" \
      io.buildpacks.base.distro.name="wolfi" \
      io.buildpacks.base.homepage="https://github.com/taha2samy-3/fck-fips-buildpack-nodejs" \
      io.buildpacks.base.maintainer="taha2samy-3" \
      org.opencontainers.image.source="https://github.com/taha2samy-3/fck-fips-buildpack-nodejs"

USER ${CNB_USER_ID}:${CNB_GROUP_ID}

# syntax=docker/dockerfile:1
# Distroless run image: glibc, CA certificates and time zones only, with no
# shell and no package manager. Works for apps started by node-start (node
# runs directly); npm-start and Procfile commands need the regular run image.
# Use it with: pack build --run-image <registry>/run-distroless:<tag>
ARG BASE_IMAGE=cgr.dev/chainguard/wolfi-base@sha256:81211e15a86ea1d5357f284482af45730c51de45eaf10ad9ce205d6bfc2df323
FROM ${BASE_IMAGE} AS rootfs

ARG CNB_USER_ID=1000
ARG CNB_GROUP_ID=1000

USER root
RUN apk add --no-cache --initdb --no-scripts --root /rootfs \
      --keys-dir /etc/apk/keys --repositories-file /etc/apk/repositories \
      ca-certificates-bundle glibc ld-linux tzdata wolfi-baselayout \
 && rm -rf /rootfs/lib/apk /rootfs/var/cache/apk /rootfs/etc/apk \
 && echo "cnb:x:${CNB_USER_ID}:${CNB_GROUP_ID}:cnb:/home/cnb:/sbin/nologin" >> /rootfs/etc/passwd \
 && echo "cnb:x:${CNB_GROUP_ID}:" >> /rootfs/etc/group \
 && mkdir -p /rootfs/home/cnb /rootfs/tmp \
 && chown "${CNB_USER_ID}:${CNB_GROUP_ID}" /rootfs/home/cnb \
 && chmod 1777 /rootfs/tmp

FROM scratch
ARG CNB_USER_ID=1000
ARG CNB_GROUP_ID=1000
COPY --from=rootfs /rootfs/ /

ENV CNB_USER_ID=${CNB_USER_ID} \
    CNB_GROUP_ID=${CNB_GROUP_ID} \
    SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

LABEL io.buildpacks.stack.id="io.fckfips.stacks.wolfi" \
      io.buildpacks.base.distro.name="wolfi" \
      io.buildpacks.base.homepage="https://github.com/taha2samy-3/fck-fips-buildpacks-nodejs" \
      io.buildpacks.base.maintainer="taha2samy-3" \
      org.opencontainers.image.source="https://github.com/taha2samy-3/fck-fips-buildpacks-nodejs"

USER ${CNB_USER_ID}:${CNB_GROUP_ID}
